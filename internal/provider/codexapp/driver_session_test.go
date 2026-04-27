package codexapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/gorilla/websocket"
)

type stubRuntimeReporter struct {
	last  contract.RuntimeReport
	calls int
	err   error
}

func (s *stubRuntimeReporter) ReportRuntime(_ context.Context, report contract.RuntimeReport) error {
	s.calls++
	s.last = report
	return s.err
}

func TestNewDriverUsesEnvServerURLAndName(t *testing.T) {
	t.Setenv("CODEX_APP_SERVER_URL", " ws://127.0.0.1:9123 ")
	got, ok := newDriver(nil, nil, nil, nil, nil, nil).(*driver)
	if !ok {
		t.Fatalf("newDriver() type = %T, want *driver", newDriver(nil, nil, nil, nil, nil, nil))
	}
	if got.logger == nil {
		t.Fatal("newDriver() logger = nil")
	}
	if got.serverURL != "ws://127.0.0.1:9123" {
		t.Fatalf("serverURL = %q, want ws://127.0.0.1:9123", got.serverURL)
	}
	if got.Name() != "codex" {
		t.Fatalf("Name() = %q, want codex", got.Name())
	}
}

func TestNewDriverFactoryCreateReturnsCodexDriver(t *testing.T) {
	t.Parallel()

	factory := NewDriverFactory(nil, nil, nil, nil, nil, nil)
	if factory.Name != "codex" {
		t.Fatalf("factory.Name = %q, want codex", factory.Name)
	}
	got, ok := factory.Create().(*driver)
	if !ok {
		t.Fatalf("factory.Create() type = %T, want *driver", factory.Create())
	}
	if got.Name() != "codex" {
		t.Fatalf("created driver Name() = %q, want codex", got.Name())
	}
}

func TestDriverReportRuntimeUsesParsedServerURLPort(t *testing.T) {
	reporter := &stubRuntimeReporter{}
	t.Setenv("CODEX_APP_SERVER_URL", " ws://127.0.0.1:9123/ws ")
	got := newDriver(nil, nil, nil, reporter, nil, nil).(*driver)
	got.reportRuntime(" agent-1 ")
	if reporter.calls != 1 {
		t.Fatalf("ReportRuntime() calls = %d, want 1", reporter.calls)
	}
	if reporter.last.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", reporter.last.AgentID)
	}
	if reporter.last.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex", reporter.last.Provider)
	}
	if reporter.last.Port != 9123 {
		t.Fatalf("Port = %d, want 9123", reporter.last.Port)
	}
}

func TestNewSessionInitializesStateAndCapabilities(t *testing.T) {
	t.Parallel()

	s, err := newSession(context.Background(), nil, startCodexTestServer(t), " agent-1 ", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	defer closeCodexTestSession(t, s)

	if s.agentID != "agent-1" {
		t.Fatalf("agentID = %q, want agent-1", s.agentID)
	}
	if s.transport == nil || !s.transport.Running() {
		t.Fatal("newSession() transport is not running")
	}
	// P22 P1c: newSession must build the session ctx, cancel and runtime
	// handle, but must NOT have started the runtime yet — Start() is an
	// explicit production call site inside StartSession / ResumeSession.
	if s.ctx == nil || s.cancel == nil {
		t.Fatal("newSession() did not initialize session ctx / cancel")
	}
	if s.runtime == nil {
		t.Fatal("newSession() did not build SessionRuntime handle")
	}
	if s.runtime.Started() {
		t.Fatal("newSession() must not implicitly Start() the runtime")
	}
	for cap, want := range codexCapabilities {
		if s.caps[cap] != want {
			t.Fatalf("caps[%q] = %v, want %v", cap, s.caps[cap], want)
		}
	}
}

func TestSessionCapabilitiesReturnsClone(t *testing.T) {
	t.Parallel()

	s := &session{caps: cloneCaps(codexCapabilities)}
	got := s.Capabilities()
	got[dto.CapThreadList] = false
	if !contract.HasCapability(s.caps, dto.CapThreadList) {
		t.Fatal("Capabilities() returned aliased map")
	}
}

func TestBuildThreadStartParamsUsesStartAssemblyInstructions(t *testing.T) {
	t.Parallel()

	params := buildThreadStartParams(dto.StartSessionRequest{
		CWD:          " /repo ",
		Model:        " gpt-5.5 ",
		Instructions: "legacy instructions",
		StartAssembly: dto.StartAssembly{
			BaseInstructions:      "assembled base",
			DeveloperInstructions: "assembled dev",
		},
		Config: map[string]any{"modelProvider": "openai"},
	})
	if params.BaseInstructions != "assembled base" {
		t.Fatalf("BaseInstructions = %q, want assembled base", params.BaseInstructions)
	}
	if params.DeveloperInstructions != "assembled dev" {
		t.Fatalf("DeveloperInstructions = %q, want assembled dev", params.DeveloperInstructions)
	}
	if params.Cwd != "/repo" || params.Model != "gpt-5.5" || params.ModelProvider != "openai" {
		t.Fatalf("unexpected params = %#v", params)
	}
}

func TestBuildThreadResumeParamsUsesPromptSnapshotInstructions(t *testing.T) {
	t.Parallel()

	params := buildThreadResumeParams(dto.ResumeSessionRequest{
		CWD:    " /repo ",
		Model:  " gpt-5.5 ",
		Effort: " high ",
		PromptSnapshot: dto.PromptAssemblySnapshot{
			BaseInstructions:      "snapshot base",
			DeveloperInstructions: "snapshot dev",
		},
	})
	if params.BaseInstructions != "snapshot base" {
		t.Fatalf("BaseInstructions = %q, want snapshot base", params.BaseInstructions)
	}
	if params.DeveloperInstructions != "snapshot dev" {
		t.Fatalf("DeveloperInstructions = %q, want snapshot dev", params.DeveloperInstructions)
	}
	if params.Cwd != "/repo" || params.Model != "gpt-5.5" || params.Effort != "high" {
		t.Fatalf("unexpected params = %#v", params)
	}
}

func TestSessionRuntimeConfigSnapshotIncludesPromptInstructions(t *testing.T) {
	t.Parallel()

	s := &session{}
	s.setRuntimeConfig(map[string]any{"developer_instructions": "legacy dev"})
	s.setRuntimeConfigValue("baseInstructions", "base")
	got := s.RuntimeConfigSnapshot()
	if got["baseInstructions"] != "base" {
		t.Fatalf("baseInstructions = %#v, want base", got["baseInstructions"])
	}
	if got["developerInstructions"] != "legacy dev" {
		t.Fatalf("developerInstructions = %#v, want legacy dev", got["developerInstructions"])
	}
}

func TestDriverStartSessionInjectsInitializeCodexHome(t *testing.T) {
	home := t.TempDir()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, rawBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg jsonRPCMessage
			if err := json.Unmarshal(rawBytes, &msg); err != nil || len(msg.ID) == 0 {
				continue
			}
			var result json.RawMessage
			switch msg.Method {
			case "initialize":
				result = mustJSON(map[string]any{"codexHome": home})
			case "thread/start":
				result = mustJSON(map[string]any{
					"thread": map[string]any{"id": "provider-thread-1", "cwd": "/repo"},
					"model":  "gpt-5.5",
				})
			default:
				result = mustJSON(map[string]any{"ok": true})
			}
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  json.RawMessage(append([]byte(nil), result...)),
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	d := &driver{
		serverURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		listTools: func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
			return nil, nil
		},
	}
	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "codex",
		AgentID:  "agent-1",
		CWD:      "/repo",
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s, ok := got.(*session)
	if !ok {
		t.Fatalf("StartSession() type = %T, want *session", got)
	}
	defer closeCodexTestSession(t, s)
	if cfg := s.RuntimeConfigSnapshot(); cfg["codexHome"] != home {
		t.Fatalf("runtime codexHome = %#v, want %q; cfg=%#v", cfg["codexHome"], home, cfg)
	}
}

func TestDriverStartSessionCanonicalizesRuntimeCodexHome(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	t.Setenv("HOME", home)
	wantHome, err := filepath.EvalSymlinks(codexHome)
	if err != nil {
		t.Fatalf("canonicalize test codex home: %v", err)
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, rawBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg jsonRPCMessage
			if err := json.Unmarshal(rawBytes, &msg); err != nil || len(msg.ID) == 0 {
				continue
			}
			var result json.RawMessage
			switch msg.Method {
			case "initialize":
				result = mustJSON(map[string]any{"codexHome": wantHome})
			case "thread/start":
				result = mustJSON(map[string]any{
					"thread": map[string]any{"id": "provider-thread-canonical", "cwd": "/repo"},
					"model":  "gpt-5.5",
				})
			default:
				result = mustJSON(map[string]any{"ok": true})
			}
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  json.RawMessage(append([]byte(nil), result...)),
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	config := map[string]any{
		"codexHome":          "~/.codex",
		"codexInstanceKey":   "default",
		"codexModelProvider": "openai",
	}
	d := &driver{
		serverURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		listTools: func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
			return nil, nil
		},
	}
	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "codex",
		AgentID:  "agent-canonical",
		CWD:      "/repo",
		Config:   config,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s, ok := got.(*session)
	if !ok {
		t.Fatalf("StartSession() type = %T, want *session", got)
	}
	defer closeCodexTestSession(t, s)
	if gotHome := s.RuntimeConfigSnapshot()["codexHome"]; gotHome != wantHome {
		t.Fatalf("runtime codexHome = %#v, want %q", gotHome, wantHome)
	}
	if config["codexHome"] != "~/.codex" {
		t.Fatalf("StartSession mutated input config codexHome = %#v", config["codexHome"])
	}
}

func TestDriverResumeSessionRestoresApprovalPolicy(t *testing.T) {
	t.Parallel()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, rawBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			raw := string(rawBytes)
			var msg jsonRPCMessage
			if err := json.Unmarshal([]byte(raw), &msg); err != nil {
				continue
			}
			if len(msg.ID) == 0 {
				continue
			}
			var result json.RawMessage
			switch msg.Method {
			case "initialize":
				result = mustJSON(map[string]any{"ok": true})
			case "thread/resume":
				result = mustJSON(map[string]any{"thread": map[string]any{"id": "provider-thread-1"}})
			case "thread/config/get":
				result = mustJSON(map[string]any{
					"threadId": "provider-thread-1",
					"provider": "codex",
					"effective": map[string]any{
						"approvals": "never",
					},
				})
			default:
				result = mustJSON(map[string]any{"ok": true})
			}
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  json.RawMessage(append([]byte(nil), result...)),
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	d := &driver{serverURL: "ws" + strings.TrimPrefix(server.URL, "http")}
	got, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider: "codex",
		AgentID:  "agent-1",
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s, ok := got.(*session)
	if !ok {
		t.Fatalf("ResumeSession() type = %T, want *session", got)
	}
	defer closeCodexTestSession(t, s)
	if s.ThreadID() != "provider-thread-1" {
		t.Fatalf("ThreadID() = %q, want provider-thread-1", s.ThreadID())
	}
	if s.approvalPolicyValue() != "never" {
		t.Fatalf("approvalPolicy = %q, want never", s.approvalPolicyValue())
	}
}

func startCodexTestServer(t *testing.T) string {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, rawBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			raw := string(rawBytes)
			var msg jsonRPCMessage
			if err := json.Unmarshal([]byte(raw), &msg); err != nil {
				continue
			}
			if len(msg.ID) == 0 {
				continue
			}
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  mustJSON(map[string]any{"ok": true}),
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func closeCodexTestSession(t *testing.T, s *session) {
	t.Helper()

	// P22 P1c: s.Close() now drains reader/health/recovery via runtime.Stop();
	// callers no longer need to reach for a waitReadLoopStopped helper to
	// prove drain. Keep this wrapper so existing tests do not need to know
	// about SessionRuntime, but the body is just Close() + assertion.
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
