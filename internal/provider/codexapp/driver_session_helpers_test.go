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

	"github.com/gorilla/websocket"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
)

func mustCanonicalCodexHome(t *testing.T, home string) string {
	t.Helper()
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(codexHome)
	if err != nil {
		t.Fatalf("canonicalize test codex home: %v", err)
	}
	return wantHome
}

func setDefaultCodexHomeEnvForTest(t *testing.T) string {
	t.Helper()
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	return mustCanonicalCodexHome(t, userHome)
}

func mustCanonicalAppManagedCodexHome(t *testing.T, home string) string {
	t.Helper()
	codexHome := filepath.Join(home, ".super-dolphin", "providers", "codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("mkdir app-managed codex home: %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(codexHome)
	if err != nil {
		t.Fatalf("canonicalize app-managed codex home: %v", err)
	}
	return wantHome
}

func validStartAssemblyForTest() dto.StartAssembly {
	return dto.StartAssembly{BaseInstructions: "test base instructions"}
}

func canonicalCodexHomeResult(method, wantHome string) json.RawMessage {
	switch method {
	case "initialize":
		return mustJSON(map[string]any{"codexHome": wantHome})
	case "thread/start":
		return mustJSON(map[string]any{
			"thread": map[string]any{"id": "provider-thread-canonical", "cwd": "/repo"},
			"model":  "gpt-5.5",
		})
	default:
		return mustJSON(map[string]any{"ok": true})
	}
}

func assertCodexHomeConfigUnchanged(t *testing.T, config map[string]any) {
	t.Helper()
	if config["codexHome"] != "~/.codex" {
		t.Fatalf("StartSession mutated input config codexHome = %#v", config["codexHome"])
	}
}

func TestDriverResumeSessionRestoresApprovalPolicy(t *testing.T) {
	t.Parallel()

	serverURL := startCodexRPCServer(t, resumeApprovalPolicyResult)
	d := &driver{approvals: testApprovalManager(), pool: newSingleURLPoolForTest(t, serverURL), mirror: &recordingSkillMirrorReconciler{}}
	workDir := t.TempDir()
	got, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:           "codex",
		AgentID:            "agent-1",
		ThreadID:           "thread-1",
		ProviderThreadID:   "thread-1",
		CWD:                workDir,
		PromptSnapshot:     validResumePromptSnapshotForTest(),
		CodexHome:          t.TempDir(),
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "ResumeSession")
	defer closeCodexTestSession(t, s)
	assertResumeApprovalSession(t, s, workDir)
}

func TestDriverResumeSessionRejectsMissingProviderThreadID(t *testing.T) {
	t.Parallel()

	serverURL := startCodexRPCServer(t, resumeApprovalPolicyResult)
	d := &driver{approvals: testApprovalManager(), pool: newSingleURLPoolForTest(t, serverURL), mirror: &recordingSkillMirrorReconciler{}}
	_, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:           "codex",
		AgentID:            "agent-1",
		ThreadID:           "thread-public",
		CWD:                t.TempDir(),
		CodexHome:          t.TempDir(),
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	})
	if err == nil || !strings.Contains(err.Error(), "provider thread id is required") {
		t.Fatalf("ResumeSession() error = %v, want provider thread id required", err)
	}
}

func resumeApprovalPolicyResult(method string) json.RawMessage {
	switch method {
	case "initialize":
		return mustJSON(map[string]any{"ok": true})
	case "thread/resume":
		return mustJSON(map[string]any{"thread": map[string]any{"id": "provider-thread-1"}})
	case "thread/config/get":
		return mustJSON(map[string]any{
			"threadId":  "provider-thread-1",
			"provider":  "codex",
			"effective": map[string]any{"approvals": "never"},
		})
	default:
		return mustJSON(map[string]any{"ok": true})
	}
}

func assertResumeApprovalSession(t *testing.T, s *session, wantCWD string) {
	t.Helper()
	if s.ThreadID() != "provider-thread-1" {
		t.Fatalf("ThreadID() = %q, want provider-thread-1", s.ThreadID())
	}
	if s.approvalPolicyValue() != "never" {
		t.Fatalf("approvalPolicy = %q, want never", s.approvalPolicyValue())
	}
	assertRuntimeConfigValue(t, s, "cwd", wantCWD)
}

func noopCodexToolLister(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
	return nil, nil
}

func mustCodexSession(t *testing.T, got contract.Session, method string) *session {
	t.Helper()
	s, ok := got.(*session)
	if !ok {
		t.Fatalf("%s() type = %T, want *session", method, got)
	}
	return s
}

func assertRuntimeConfigValue(t *testing.T, s *session, key string, want any) {
	t.Helper()
	cfg := s.RuntimeConfigSnapshot()
	if cfg[key] != want {
		t.Fatalf("runtime %s = %#v, want %#v; cfg=%#v", key, cfg[key], want, cfg)
	}
}

func startCodexRPCServer(t *testing.T, resultFor func(string) json.RawMessage) string {
	t.Helper()
	return startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		return resultFor(msg.Method)
	})
}

func validCodexModelListResult() json.RawMessage {
	return mustJSON(validCodexModelListMap())
}

func validCodexModelListMap() map[string]any {
	return map[string]any{
		"models": []map[string]any{
			{"id": "gpt-5"},
		},
	}
}

func codexTestRPCResultOrDefault(method string, result json.RawMessage) json.RawMessage {
	if method != "model/list" || !isGenericOKResult(result) {
		return result
	}
	return validCodexModelListResult()
}

func isGenericOKResult(result json.RawMessage) bool {
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		return false
	}
	ok, hasOK := payload["ok"].(bool)
	return hasOK && ok && len(payload) == 1
}

func startCodexRPCServerWithHandler(t *testing.T, handle func(jsonRPCMessage) json.RawMessage) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serveCodexRPCConnectionWithHandler(t, conn, handle)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func serveCodexRPCConnectionWithHandler(t *testing.T, conn *websocket.Conn, handle func(jsonRPCMessage) json.RawMessage) {
	t.Helper()
	defer conn.Close()
	for {
		_, rawBytes, err := conn.ReadMessage()
		if err != nil {
			return
		}
		msg, ok := decodeCodexTestRPCMessage(rawBytes)
		if !ok {
			continue
		}
		result := codexTestRPCResultOrDefault(msg.Method, handle(msg))
		if !writeCodexTestRPCResponse(t, conn, msg.ID, result) {
			return
		}
	}
}

func decodeCodexTestRPCMessage(raw []byte) (jsonRPCMessage, bool) {
	var msg jsonRPCMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return jsonRPCMessage{}, false
	}
	return msg, len(msg.ID) != 0
}

func writeCodexTestRPCResponse(t *testing.T, conn *websocket.Conn, id json.RawMessage, result json.RawMessage) bool {
	t.Helper()
	resp, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(append([]byte(nil), id...)),
		"result":  json.RawMessage(append([]byte(nil), result...)),
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return conn.WriteMessage(websocket.TextMessage, resp) == nil
}

func startCodexTestServer(t *testing.T) string {
	t.Helper()

	return startCodexRPCServer(t, func(string) json.RawMessage {
		return mustJSON(map[string]any{"ok": true})
	})
}

func closeCodexTestSession(t *testing.T, s *session) {
	t.Helper()

	// s.Close 会通过 runtime.Stop() 收敛 reader、health 和 recovery goroutine。
	// 保留这个测试 helper，让调用方只关心关闭断言，不需要了解 SessionRuntime 细节。
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
