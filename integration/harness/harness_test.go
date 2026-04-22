package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aiterm/internal/tmux"
)

const interactiveHarnessEnv = "SHUTTLE_RUN_INTERACTIVE_HARNESS"

func TestMain(m *testing.M) {
	if os.Getenv(interactiveHarnessEnv) != "1" {
		fmt.Printf("interactive harness disabled; set %s=1 to run\n", interactiveHarnessEnv)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type scriptedProviderResponse struct {
	expectContains string
	structuredJSON string
}

type scriptedProviderServer struct {
	server    *httptest.Server
	mu        sync.Mutex
	responses []scriptedProviderResponse
	requests  []string
}

func newScriptedProviderServer(t *testing.T, responses []scriptedProviderResponse) *scriptedProviderServer {
	t.Helper()

	s := &scriptedProviderServer{
		responses: append([]scriptedProviderResponse(nil), responses...),
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}

			s.mu.Lock()
			defer s.mu.Unlock()
			s.requests = append(s.requests, string(body))
			if len(s.responses) == 0 {
				t.Fatalf("unexpected provider request with no scripted response left:\n%s", string(body))
			}
			next := s.responses[0]
			s.responses = s.responses[1:]
			if next.expectContains != "" && !strings.Contains(string(body), next.expectContains) {
				t.Fatalf("provider request missing expected substring %q:\n%s", next.expectContains, string(body))
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, fmt.Sprintf(`{
				"id":"resp_harness",
				"object":"response",
				"model":"gpt-5-test",
				"output":[
					{
						"type":"message",
						"content":[
							{
								"type":"output_text",
								"text":%s
							}
						]
					}
				]
			}`, mustJSONString(next.structuredJSON)))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))

	t.Cleanup(s.server.Close)
	return s
}

func (s *scriptedProviderServer) URL() string {
	return s.server.URL
}

func (s *scriptedProviderServer) dumpRequests() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.requests, "\n\n---\n\n")
}

type interactiveHarness struct {
	t            *testing.T
	repoRoot     string
	workspaceDir string
	stateDir     string
	runtimeDir   string
	artifactDir  string
	outerSocket  string
	outerSession string
	innerSocket  string
	innerSession string
	outerClient  *tmux.Client
	innerClient  *tmux.Client
	tuiPaneID    string
	topPaneID    string
	provider     *scriptedProviderServer
}

func newInteractiveHarness(t *testing.T, workspaceDir string, provider *scriptedProviderServer) *interactiveHarness {
	t.Helper()
	if os.Getenv(interactiveHarnessEnv) != "1" {
		t.Skipf("interactive harness is disabled by default; set %s=1 to run these tests", interactiveHarnessEnv)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}

	stateDir := t.TempDir()
	runtimeDir := filepath.Join(stateDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	artifactDir, err := os.MkdirTemp("", "shuttle-harness-artifacts-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}

	outerSocket := fmt.Sprintf("shuttle-harness-ui-%d", time.Now().UnixNano())
	outerSession := fmt.Sprintf("shuttle-harness-ui-%d", time.Now().UnixNano())
	innerSocket := fmt.Sprintf("shuttle-harness-app-%d", time.Now().UnixNano())
	innerSession := fmt.Sprintf("shuttle-harness-app-%d", time.Now().UnixNano())

	outerClient, err := tmux.NewClient(outerSocket)
	if err != nil {
		t.Fatalf("NewClient(outer) error = %v", err)
	}
	innerClient, err := tmux.NewClient(innerSocket)
	if err != nil {
		t.Fatalf("NewClient(inner) error = %v", err)
	}

	h := &interactiveHarness{
		t:            t,
		repoRoot:     repoRoot,
		workspaceDir: workspaceDir,
		stateDir:     stateDir,
		runtimeDir:   runtimeDir,
		artifactDir:  artifactDir,
		outerSocket:  outerSocket,
		outerSession: outerSession,
		innerSocket:  innerSocket,
		innerSession: innerSession,
		outerClient:  outerClient,
		innerClient:  innerClient,
		provider:     provider,
	}

	t.Cleanup(func() {
		h.captureArtifacts()
		if t.Failed() {
			t.Logf("interactive harness artifacts: %s", h.artifactDir)
		}
		_ = h.outerClient.KillSession(context.Background(), h.outerSession)
		_ = h.innerClient.KillSession(context.Background(), h.innerSession)
	})

	h.start()
	return h
}

func (h *interactiveHarness) start() {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	env := map[string]string{
		"OPENAI_API_KEY": "test-openai-key",
		"GOCACHE":        filepath.Join(h.stateDir, "gocache"),
		"GOTMPDIR":       filepath.Join(h.stateDir, "gotmp"),
	}
	if err := os.MkdirAll(env["GOCACHE"], 0o755); err != nil {
		h.t.Fatalf("MkdirAll(gocache) error = %v", err)
	}
	if err := os.MkdirAll(env["GOTMPDIR"], 0o755); err != nil {
		h.t.Fatalf("MkdirAll(gotmpdir) error = %v", err)
	}

	if err := h.outerClient.NewDetachedSession(ctx, h.outerSession, h.repoRoot, env, tmux.LaunchSpec{}); err != nil {
		h.t.Fatalf("NewDetachedSession() error = %v", err)
	}

	panes, err := h.outerClient.ListPanes(ctx, h.outerSession)
	if err != nil {
		h.t.Fatalf("ListPanes() error = %v", err)
	}
	if len(panes) != 1 {
		h.t.Fatalf("expected one outer pane, got %#v", panes)
	}
	h.tuiPaneID = panes[0].ID

	command := strings.Join([]string{
		"cd " + shellQuote(h.repoRoot),
		"&&",
		"go run ./cmd/shuttle",
		"--tui",
		"--dir " + shellQuote(h.workspaceDir),
		"--socket " + shellQuote(h.innerSocket),
		"--session " + shellQuote(h.innerSession),
		"--state-dir " + shellQuote(h.stateDir),
		"--runtime-dir " + shellQuote(h.runtimeDir),
		"--trace",
		"--trace-path " + shellQuote(filepath.Join(h.stateDir, "trace.log")),
		"--provider openai",
		"--auth api_key",
		"--base-url " + shellQuote(h.provider.URL()+"/v1"),
		"--model gpt-5-test",
	}, " ")
	if err := h.outerClient.SendLiteralKeys(ctx, h.tuiPaneID, command); err != nil {
		h.t.Fatalf("SendLiteralKeys() error = %v", err)
	}
	if err := h.outerClient.SendKeys(ctx, h.tuiPaneID, "C-m", false); err != nil {
		h.t.Fatalf("SendKeys(enter) error = %v", err)
	}

	h.waitForOuterPaneContains("[F1] help", 45*time.Second)
	h.waitForInnerWorkspace(45 * time.Second)
}

func (h *interactiveHarness) submitPrompt(prompt string) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.outerClient.SendKeys(ctx, h.tuiPaneID, "BTab", false); err != nil {
		h.t.Fatalf("SendKeys(agent mode toggle) error = %v", err)
	}
	if err := h.outerClient.SendLiteralKeys(ctx, h.tuiPaneID, prompt); err != nil {
		h.t.Fatalf("SendLiteralKeys(prompt) error = %v", err)
	}
	if err := h.outerClient.SendKeys(ctx, h.tuiPaneID, "C-m", false); err != nil {
		h.t.Fatalf("SendKeys(prompt enter) error = %v", err)
	}
}

func (h *interactiveHarness) pressKey(key string) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.outerClient.SendKeys(ctx, h.tuiPaneID, key, false); err != nil {
		h.t.Fatalf("SendKeys(%s) error = %v", key, err)
	}
}

func (h *interactiveHarness) waitForOuterPaneContains(fragment string, timeout time.Duration) string {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		captured, err := h.outerClient.CapturePane(ctx, h.tuiPaneID, -200)
		if err == nil && strings.Contains(captured, fragment) {
			return captured
		}
		if ctx.Err() != nil {
			if err != nil {
				h.t.Fatalf("timed out waiting for outer pane fragment %q: %v", fragment, err)
			}
			h.t.Fatalf("timed out waiting for outer pane fragment %q.\nLast capture:\n%s", fragment, captured)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (h *interactiveHarness) waitForOuterPaneContainsAny(fragments []string, timeout time.Duration) string {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if len(fragments) == 0 {
		h.t.Fatal("waitForOuterPaneContainsAny requires at least one fragment")
	}

	for {
		captured, err := h.outerClient.CapturePane(ctx, h.tuiPaneID, -200)
		if err == nil {
			for _, fragment := range fragments {
				if strings.Contains(captured, fragment) {
					return captured
				}
			}
		}
		if ctx.Err() != nil {
			if err != nil {
				h.t.Fatalf("timed out waiting for any outer pane fragment %q: %v", fragments, err)
			}
			h.t.Fatalf("timed out waiting for any outer pane fragment %q.\nLast capture:\n%s", fragments, captured)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (h *interactiveHarness) waitForFile(path string, content string, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if content == "" || string(data) == content {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	h.t.Fatalf("timed out waiting for file %s with content %q, got %q", path, content, string(data))
}

func (h *interactiveHarness) waitForInnerWorkspace(timeout time.Duration) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		ok, err := h.innerClient.HasSession(ctx, h.innerSession)
		if err == nil && ok {
			panes, err := h.innerClient.ListPanes(ctx, h.innerSession)
			if err == nil && len(panes) >= 1 {
				h.topPaneID = panes[0].ID
				return
			}
		}
		if ctx.Err() != nil {
			h.t.Fatalf("timed out waiting for inner workspace session %q", h.innerSession)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (h *interactiveHarness) captureArtifacts() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if h.tuiPaneID != "" {
		if captured, err := h.outerClient.CapturePane(ctx, h.tuiPaneID, -200); err == nil {
			_ = os.WriteFile(filepath.Join(h.artifactDir, "outer-pane.txt"), []byte(captured), 0o644)
		}
	}
	if h.topPaneID != "" {
		if captured, err := h.innerClient.CapturePane(ctx, h.topPaneID, -200); err == nil {
			_ = os.WriteFile(filepath.Join(h.artifactDir, "inner-top-pane.txt"), []byte(captured), 0o644)
		}
	}
	for _, name := range []string{"trace.log", "shuttle.log"} {
		source := filepath.Join(h.stateDir, name)
		if data, err := os.ReadFile(source); err == nil {
			_ = os.WriteFile(filepath.Join(h.artifactDir, name), data, 0o644)
		}
	}
	_ = os.WriteFile(filepath.Join(h.artifactDir, "provider-requests.txt"), []byte(h.provider.dumpRequests()), 0o644)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func mustJSONString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestInteractiveHarnessAppliesPatchProposal(t *testing.T) {
	workspaceDir := t.TempDir()
	provider := newScriptedProviderServer(t, []scriptedProviderResponse{
		{
			expectContains: "Add a new file named hello.txt containing exactly one line: hello world",
			structuredJSON: `{"message":"I can add hello.txt as a patch.","plan_summary":"","plan_steps":[],"proposal_kind":"patch","proposal_command":"","proposal_keys":"","proposal_patch":"diff --git a/hello.txt b/hello.txt\nnew file mode 100644\n--- /dev/null\n+++ b/hello.txt\n@@ -0,0 +1 @@\n+hello world\n","proposal_description":"Create hello.txt with one line of content.","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`,
		},
		{
			expectContains: "previously approved or proposed patch was applied",
			structuredJSON: `{"message":"The patch was applied and the task is complete.","plan_summary":"","plan_steps":[],"proposal_kind":"","proposal_command":"","proposal_keys":"","proposal_patch":"","proposal_description":"","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`,
		},
	})

	h := newInteractiveHarness(t, workspaceDir, provider)
	h.submitPrompt("Add a new file named hello.txt containing exactly one line: hello world")
	h.waitForOuterPaneContains("Create hello.txt with one line of content.", 30*time.Second)
	h.waitForOuterPaneContains("Y apply  N reject  R ask agent", 30*time.Second)

	h.pressKey("y")
	h.waitForFile(filepath.Join(workspaceDir, "hello.txt"), "hello world\n", 30*time.Second)
	h.waitForOuterPaneContains("The patch was applied and the task is complete.", 30*time.Second)
}

func TestInteractiveHarnessRetriesFailedPatchProposal(t *testing.T) {
	workspaceDir := t.TempDir()
	provider := newScriptedProviderServer(t, []scriptedProviderResponse{
		{
			expectContains: "Add a new file named hello.txt containing exactly one line: hello world",
			structuredJSON: `{"message":"I can add hello.txt as a patch.","plan_summary":"","plan_steps":[],"proposal_kind":"patch","proposal_command":"","proposal_keys":"","proposal_patch":"diff --git a/hello.txt b/hello.txt\n--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-hello\n+hello world\n","proposal_description":"Attempt the patch once.","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`,
		},
		{
			expectContains: "previously proposed or approved patch did not apply cleanly",
			structuredJSON: `{"message":"The first patch did not apply. I can retry with one corrected patch.","plan_summary":"","plan_steps":[],"proposal_kind":"patch","proposal_command":"","proposal_keys":"","proposal_patch":"diff --git a/hello.txt b/hello.txt\nnew file mode 100644\n--- /dev/null\n+++ b/hello.txt\n@@ -0,0 +1 @@\n+hello world\n","proposal_description":"Retry with a corrected patch.","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`,
		},
		{
			expectContains: "previously approved or proposed patch was applied",
			structuredJSON: `{"message":"The corrected patch was applied and the task is complete.","plan_summary":"","plan_steps":[],"proposal_kind":"","proposal_command":"","proposal_keys":"","proposal_patch":"","proposal_description":"","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`,
		},
	})

	h := newInteractiveHarness(t, workspaceDir, provider)
	h.submitPrompt("Add a new file named hello.txt containing exactly one line: hello world")
	h.waitForOuterPaneContains("Attempt the patch once.", 30*time.Second)
	h.pressKey("y")

	h.waitForOuterPaneContains("Retry with a corrected patch.", 30*time.Second)
	h.waitForOuterPaneContains("patch apply failed", 30*time.Second)
	h.pressKey("y")

	h.waitForFile(filepath.Join(workspaceDir, "hello.txt"), "hello world\n", 30*time.Second)
	h.waitForOuterPaneContains("The corrected patch was applied and the task is complete.", 30*time.Second)
}

func TestInteractiveHarnessRunsCommandProposalAndAutoContinues(t *testing.T) {
	workspaceDir := t.TempDir()
	resultPath := filepath.Join(workspaceDir, "result.txt")
	proposalCommand := fmt.Sprintf("printf 'alpha\\n' > %s", shellQuote(resultPath))
	provider := newScriptedProviderServer(t, []scriptedProviderResponse{
		{
			expectContains: "Create result.txt with alpha, then report completion.",
			structuredJSON: fmt.Sprintf(`{"message":"I will create the file first.","plan_summary":"Create result.txt and confirm completion.","plan_steps":["Write result.txt with alpha.","Report completion."],"proposal_kind":"command","proposal_command":%s,"proposal_keys":"","proposal_patch":"","proposal_description":"Write alpha into result.txt.","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`, mustJSONString(proposalCommand)),
		},
		{
			expectContains: "previously approved or proposed command has completed",
			structuredJSON: `{"message":"The command created result.txt and the workflow is complete.","plan_summary":"","plan_steps":[],"proposal_kind":"","proposal_command":"","proposal_keys":"","proposal_patch":"","proposal_description":"","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`,
		},
	})

	h := newInteractiveHarness(t, workspaceDir, provider)
	h.submitPrompt("Create result.txt with alpha, then report completion.")
	h.waitForOuterPaneContains("Write alpha into result.txt.", 30*time.Second)
	h.waitForOuterPaneContains("Y continue", 30*time.Second)

	h.pressKey("y")
	h.waitForFile(resultPath, "alpha\n", 30*time.Second)
	h.waitForOuterPaneContains("The command created result.txt and the workflow is complete.", 30*time.Second)
}

func TestInteractiveHarnessPlanLoopAutoContinuesAcrossMultipleActions(t *testing.T) {
	workspaceDir := t.TempDir()
	loopPath := filepath.Join(workspaceDir, "loop.txt")
	writeAlpha := fmt.Sprintf("printf 'alpha\\n' | tee %s", shellQuote(loopPath))
	replaceWithBeta := fmt.Sprintf("printf 'beta\\n' > %s && cat %s", shellQuote(loopPath), shellQuote(loopPath))

	provider := newScriptedProviderServer(t, []scriptedProviderResponse{
		{
			expectContains: "Create loop.txt with alpha, confirm it, then replace it with beta and finish.",
			structuredJSON: fmt.Sprintf(`{"message":"I will start with the first step.","plan_summary":"Create loop.txt, confirm alpha, replace it with beta, and finish.","plan_steps":["Write alpha into loop.txt.","Replace loop.txt with beta.","Report completion."],"proposal_kind":"command","proposal_command":%s,"proposal_keys":"","proposal_patch":"","proposal_description":"Write alpha into loop.txt and echo it.","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`, mustJSONString(writeAlpha)),
		},
		{
			expectContains: "summary=alpha",
			structuredJSON: fmt.Sprintf(`{"message":"Alpha was written successfully, so I can advance to the next step.","plan_summary":"","plan_steps":[],"proposal_kind":"command","proposal_command":%s,"proposal_keys":"","proposal_patch":"","proposal_description":"Replace loop.txt with beta and print the new contents.","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`, mustJSONString(replaceWithBeta)),
		},
		{
			expectContains: "summary=beta",
			structuredJSON: `{"message":"The checklist is complete: loop.txt now contains beta.","plan_summary":"","plan_steps":[],"proposal_kind":"","proposal_command":"","proposal_keys":"","proposal_patch":"","proposal_description":"","approval_kind":"","approval_title":"","approval_summary":"","approval_command":"","approval_patch":"","approval_risk":""}`,
		},
	})

	h := newInteractiveHarness(t, workspaceDir, provider)
	h.submitPrompt("Create loop.txt with alpha, confirm it, then replace it with beta and finish.")
	h.waitForOuterPaneContains("Write alpha into loop.txt and echo it.", 30*time.Second)
	h.waitForOuterPaneContains("Y continue", 30*time.Second)

	h.pressKey("y")
	h.waitForFile(loopPath, "alpha\n", 30*time.Second)
	h.waitForOuterPaneContains("Replace loop.txt with beta and print the new contents.", 30*time.Second)

	h.pressKey("y")
	h.waitForFile(loopPath, "beta\n", 30*time.Second)
	h.waitForOuterPaneContains("The checklist is complete: loop.txt now contains beta.", 30*time.Second)
	h.waitForOuterPaneContains("[x] 1. Write alpha into loop.txt.", 30*time.Second)
	h.waitForOuterPaneContains("[x] 2. Replace loop.txt with beta.", 30*time.Second)
	h.waitForOuterPaneContainsAny([]string{
		"[x] 3. Report completion.",
		"... (1 more steps, Ctrl+O to inspect)",
	}, 30*time.Second)
}
