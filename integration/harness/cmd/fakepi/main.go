package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type fakePI struct {
	writer            *bufio.Writer
	sessionDir        string
	model             string
	lastAssistantText string
	sessionFile       string
	sessionID         string
}

func main() {
	var (
		mode       string
		sessionDir string
		model      string
	)

	flag.StringVar(&mode, "mode", "", "runtime mode")
	flag.StringVar(&sessionDir, "session-dir", "", "session directory")
	flag.StringVar(&model, "model", "", "model id")
	flag.String("provider", "", "provider id")
	flag.Parse()

	if strings.TrimSpace(mode) != "rpc" {
		fmt.Fprintln(os.Stderr, "fakepi requires --mode rpc")
		os.Exit(2)
	}
	if strings.TrimSpace(sessionDir) == "" {
		fmt.Fprintln(os.Stderr, "fakepi requires --session-dir")
		os.Exit(2)
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "prepare session dir: %v\n", err)
		os.Exit(1)
	}

	runtime := &fakePI{
		writer:     bufio.NewWriter(os.Stdout),
		sessionDir: sessionDir,
		model:      strings.TrimSpace(model),
	}
	if runtime.model == "" {
		runtime.model = "fake-pi-test"
	}
	if err := runtime.serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (f *fakePI) serve() error {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var request map[string]any
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			return fmt.Errorf("decode request: %w", err)
		}

		id := stringValue(request["id"])
		command := stringValue(request["type"])
		switch command {
		case "new_session":
			if err := f.newSession(); err != nil {
				return err
			}
			if err := f.respond(id, command, true, map[string]any{"sessionFile": f.sessionFile, "sessionId": f.sessionID}, ""); err != nil {
				return err
			}
		case "switch_session":
			sessionPath := stringValue(request["sessionPath"])
			if strings.TrimSpace(sessionPath) == "" {
				if err := f.respond(id, command, false, nil, "session path is required"); err != nil {
					return err
				}
				continue
			}
			if _, err := os.Stat(sessionPath); err != nil {
				if err := f.respond(id, command, false, nil, "session file not found"); err != nil {
					return err
				}
				continue
			}
			f.sessionFile = sessionPath
			f.sessionID = sessionIDFromPath(sessionPath)
			if err := f.respond(id, command, true, map[string]any{"sessionFile": f.sessionFile, "sessionId": f.sessionID}, ""); err != nil {
				return err
			}
		case "prompt":
			if err := f.ensureSession(); err != nil {
				return err
			}
			if err := f.respond(id, command, true, nil, ""); err != nil {
				return err
			}
			if err := f.streamPrompt(); err != nil {
				return err
			}
		case "get_last_assistant_text":
			if strings.TrimSpace(f.lastAssistantText) == "" {
				f.lastAssistantText = fakeStructuredAssistantText()
			}
			if err := f.respond(id, command, true, map[string]any{"text": f.lastAssistantText}, ""); err != nil {
				return err
			}
		case "get_state":
			if err := f.ensureSession(); err != nil {
				return err
			}
			if err := f.respond(id, command, true, map[string]any{
				"sessionFile": f.sessionFile,
				"sessionId":   f.sessionID,
				"model": map[string]any{
					"id": f.model,
				},
			}, ""); err != nil {
				return err
			}
		default:
			if err := f.respond(id, command, false, nil, "unsupported command"); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	return nil
}

func (f *fakePI) ensureSession() error {
	if strings.TrimSpace(f.sessionFile) != "" && strings.TrimSpace(f.sessionID) != "" {
		return nil
	}
	return f.newSession()
}

func (f *fakePI) newSession() error {
	f.sessionID = fmt.Sprintf("fake-session-%d", time.Now().UTC().UnixNano())
	f.sessionFile = filepath.Join(f.sessionDir, f.sessionID+".json")
	payload, err := json.Marshal(map[string]any{
		"id":      f.sessionID,
		"created": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("marshal session payload: %w", err)
	}
	if err := os.WriteFile(f.sessionFile, payload, 0o600); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}
	return nil
}

func (f *fakePI) streamPrompt() error {
	if err := f.emit(map[string]any{
		"type": "message_update",
		"message": map[string]any{
			"id": "msg-1",
			"content": []map[string]any{
				{"type": "text", "text": "Reviewing Shuttle context and taking over the task."},
			},
		},
	}); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)

	if err := f.emit(map[string]any{
		"type":       "tool_execution_start",
		"toolName":   "bash",
		"toolCallId": "tool-1",
		"args": map[string]any{
			"command": "printf 'fake pi runtime ran a task\\n'",
		},
	}); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)

	if err := f.emit(map[string]any{
		"type":          "tool_execution_update",
		"toolName":      "bash",
		"toolCallId":    "tool-1",
		"partialResult": map[string]any{"stdout": "fake pi runtime ran a task"},
	}); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)

	if err := f.emit(map[string]any{
		"type":       "tool_execution_end",
		"toolName":   "bash",
		"toolCallId": "tool-1",
		"result": map[string]any{
			"stdout": "fake pi runtime ran a task",
			"exit":   0,
		},
	}); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)

	f.lastAssistantText = fakeStructuredAssistantText()
	if err := f.emit(map[string]any{
		"type": "message_update",
		"message": map[string]any{
			"id": "msg-1",
			"content": []map[string]any{
				{"type": "text", "text": "Prepared the final Shuttle response."},
			},
		},
	}); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)

	return f.emit(map[string]any{"type": "agent_end"})
}

func (f *fakePI) respond(id string, command string, success bool, data map[string]any, errorText string) error {
	payload := map[string]any{
		"type":    "response",
		"id":      id,
		"command": command,
		"success": success,
	}
	if data != nil {
		payload["data"] = data
	}
	if strings.TrimSpace(errorText) != "" {
		payload["error"] = errorText
	}
	return f.emit(payload)
}

func (f *fakePI) emit(payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	if _, err := f.writer.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	if err := f.writer.Flush(); err != nil {
		return fmt.Errorf("flush response: %w", err)
	}
	return nil
}

func fakeStructuredAssistantText() string {
	return `{"message":"Fake PI completed the external task.","plan_summary":"","plan_steps":[],"proposal_kind":"","proposal_command":"","proposal_keys":"","proposal_patch":"","proposal_description":"","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":"","handoff_title":"","handoff_summary":"","handoff_reason":"","handoff_runtime":""}`
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func sessionIDFromPath(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" {
		return "fake-session"
	}
	return base
}
