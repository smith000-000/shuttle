package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"aiterm/internal/securefs"
)

type streamSemanticCollector struct {
	client       pipePaneClient
	stateDir     string
	generationID string

	mu    sync.Mutex
	panes map[string]*paneSemanticStream
}

type paneSemanticStream struct {
	mu      sync.Mutex
	paneTTY string
	path    string
	started bool
	offset  int
	reducer semanticOSCStreamReducer
}

func newStreamSemanticCollector(client pipePaneClient, stateDir string) *streamSemanticCollector {
	collector := &streamSemanticCollector{
		client:       client,
		stateDir:     strings.TrimSpace(stateDir),
		generationID: newSemanticStreamGenerationID(),
		panes:        make(map[string]*paneSemanticStream),
	}
	_ = collector.cleanupStaleGenerations()
	return collector
}

func (c *streamSemanticCollector) Collect(ctx context.Context, paneID string, paneTTY string, _ string, _ PromptContext) (semanticObservation, bool) {
	stream := c.paneStream(paneID, paneTTY)
	return stream.collect(ctx, c.client, paneID)
}

func (c *streamSemanticCollector) paneStream(paneID string, paneTTY string) *paneSemanticStream {
	key := strings.TrimSpace(paneID)
	if key == "" {
		key = strings.TrimSpace(paneTTY)
	}
	if key == "" {
		key = "unknown"
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	stream, ok := c.panes[key]
	if ok && (strings.TrimSpace(paneTTY) == "" || stream.paneTTY == strings.TrimSpace(paneTTY)) {
		return stream
	}

	stream = &paneSemanticStream{
		paneTTY: strings.TrimSpace(paneTTY),
		path:    semanticStreamPath(c.stateDir, c.generationID, paneID, paneTTY),
	}
	c.panes[key] = stream
	return stream
}

func (s *paneSemanticStream) collect(ctx context.Context, client pipePaneClient, paneID string) (semanticObservation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		if err := s.start(ctx, client, paneID); err != nil {
			return semanticObservation{}, false
		}
		s.started = true
	}

	data, _, err := securefs.ReadFileNoFollow(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.currentObservation()
		}
		return semanticObservation{}, false
	}

	if len(data) < s.offset {
		s.offset = 0
		s.reducer.Reset()
	}

	if len(data) > s.offset {
		s.reducer.Feed(data[s.offset:], time.Now())
		s.offset = len(data)
	}

	return s.currentObservation()
}

func (s *paneSemanticStream) start(ctx context.Context, client pipePaneClient, paneID string) error {
	if err := securefs.EnsurePrivateDir(filepath.Dir(s.path)); err != nil {
		return err
	}
	if err := securefs.WriteAtomicPrivate(s.path, []byte{}, 0o600); err != nil {
		return err
	}
	s.offset = 0
	s.reducer.Reset()
	return client.PipePaneOutput(ctx, paneID, "umask 077; cat > "+shellQuote(s.path))
}

func (s *paneSemanticStream) currentObservation() (semanticObservation, bool) {
	state, ok := s.reducer.State()
	if !ok {
		return semanticObservation{}, false
	}
	return semanticObservation{State: state, Source: semanticSourceStream}, true
}

func semanticStreamPath(stateDir string, generationID string, paneID string, paneTTY string) string {
	name := strings.TrimSpace(paneTTY)
	if name == "" {
		name = strings.TrimSpace(paneID)
	}
	if name == "" {
		name = "unknown"
	}
	generationID = strings.TrimSpace(generationID)
	if generationID == "" {
		generationID = "default"
	}
	return filepath.Join(stateDir, "semantic-stream", sanitizeIntegrationName(generationID), sanitizeIntegrationName(name)+".log")
}

func newSemanticStreamGenerationID() string {
	return fmt.Sprintf("session-%d-%d", os.Getpid(), time.Now().UnixNano())
}

func (c *streamSemanticCollector) cleanupStaleGenerations() error {
	root := filepath.Join(c.stateDir, "semantic-stream")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		generationID := strings.TrimSpace(entry.Name())
		if generationID == "" || generationID == c.generationID {
			continue
		}
		if !shouldPruneSemanticStreamGeneration(generationID) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, generationID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

var semanticStreamProcessAlive = processAlive

func shouldPruneSemanticStreamGeneration(generationID string) bool {
	pid, ok := semanticStreamGenerationPID(generationID)
	if !ok {
		return false
	}
	return !semanticStreamProcessAlive(pid)
}

func semanticStreamGenerationPID(generationID string) (int, bool) {
	const prefix = "session-"
	if !strings.HasPrefix(generationID, prefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(generationID, prefix)
	sep := strings.IndexByte(rest, '-')
	if sep <= 0 {
		return 0, false
	}
	pid, err := strconv.Atoi(rest[:sep])
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func pipePaneOutputPath(shellCommand string) string {
	shellCommand = strings.TrimSpace(shellCommand)
	const prefix = "umask 077; cat > "
	if !strings.HasPrefix(shellCommand, prefix) {
		return ""
	}
	value := strings.TrimSpace(strings.TrimPrefix(shellCommand, prefix))
	if len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
		return ""
	}
	unquoted := strings.TrimSuffix(strings.TrimPrefix(value, "'"), "'")
	return strings.ReplaceAll(unquoted, `'"'"'`, `'`)
}

func pipePaneGenerationID(streamPath string) string {
	if strings.TrimSpace(streamPath) == "" {
		return ""
	}
	return path.Base(filepath.Dir(streamPath))
}

type semanticOSCStreamReducer struct {
	parser      oscPayloadStreamParser
	state       semanticShellState
	pendingExit *int
	hasState    bool
}

func (r *semanticOSCStreamReducer) Feed(chunk []byte, observedAt time.Time) {
	for _, payload := range r.parser.Feed(chunk) {
		r.applyPayload(payload, observedAt)
	}
}

func (r *semanticOSCStreamReducer) State() (semanticShellState, bool) {
	if !r.hasState || r.state.Event == semanticEventUnknown {
		return semanticShellState{}, false
	}
	return r.state, true
}

func (r *semanticOSCStreamReducer) Reset() {
	*r = semanticOSCStreamReducer{}
}

func (r *semanticOSCStreamReducer) applyPayload(payload string, observedAt time.Time) {
	switch {
	case strings.HasPrefix(payload, "133;"):
		r.applyOSC133(strings.TrimPrefix(payload, "133;"), observedAt)
	case strings.HasPrefix(payload, "7;file://"):
		cwd := parseOSC7Directory(payload)
		if cwd == "" {
			return
		}
		r.state.Directory = cwd
		if r.state.Event != semanticEventUnknown {
			r.state.UpdatedAt = observedAt
			r.hasState = true
		}
	}
}

func (r *semanticOSCStreamReducer) applyOSC133(payload string, observedAt time.Time) {
	switch {
	case payload == "A":
		r.state.Event = semanticEventPrompt
		if r.pendingExit != nil {
			exitCode := *r.pendingExit
			r.state.ExitCode = &exitCode
			r.pendingExit = nil
		}
		r.state.UpdatedAt = observedAt
		r.hasState = true
	case payload == "B" || payload == "C":
		r.state.Event = semanticEventCommand
		r.state.ExitCode = nil
		r.pendingExit = nil
		r.state.UpdatedAt = observedAt
		r.hasState = true
	case payload == "D":
		r.pendingExit = nil
	case strings.HasPrefix(payload, "D;"):
		exitCode, ok := parseOSCExit(strings.TrimPrefix(payload, "D;"))
		if !ok {
			r.pendingExit = nil
			return
		}
		r.pendingExit = &exitCode
	}
}

type oscPayloadParserState uint8

const (
	oscParserOutside oscPayloadParserState = iota
	oscParserSawEscape
	oscParserInside
	oscParserInsideSawEscape
)

type oscPayloadStreamParser struct {
	state   oscPayloadParserState
	payload []byte
}

func (p *oscPayloadStreamParser) Feed(chunk []byte) []string {
	payloads := make([]string, 0, 4)
	for _, b := range chunk {
		switch p.state {
		case oscParserOutside:
			if b == 0x1b {
				p.state = oscParserSawEscape
			}
		case oscParserSawEscape:
			if b == ']' {
				p.payload = p.payload[:0]
				p.state = oscParserInside
				continue
			}
			if b == 0x1b {
				p.state = oscParserSawEscape
				continue
			}
			p.state = oscParserOutside
		case oscParserInside:
			switch b {
			case 0x07:
				payloads = append(payloads, string(p.payload))
				p.payload = p.payload[:0]
				p.state = oscParserOutside
			case 0x1b:
				p.state = oscParserInsideSawEscape
			default:
				p.payload = append(p.payload, b)
			}
		case oscParserInsideSawEscape:
			if b == '\\' {
				payloads = append(payloads, string(p.payload))
				p.payload = p.payload[:0]
				p.state = oscParserOutside
				continue
			}
			p.payload = append(p.payload, 0x1b)
			if b == 0x1b {
				p.state = oscParserInsideSawEscape
				continue
			}
			p.payload = append(p.payload, b)
			p.state = oscParserInside
		}
	}
	return payloads
}
