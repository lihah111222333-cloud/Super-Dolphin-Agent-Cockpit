package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	rpcpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestNewServiceInitializesDefaults(t *testing.T) {
	t.Parallel()

	got, ok := NewService(nil, nil, nil, nil, nil, nil, nil, nil).(*service)
	if !ok {
		t.Fatalf("NewService() type = %T, want *service", NewService(nil, nil, nil, nil, nil, nil, nil, nil))
	}
	if got.logger == nil {
		t.Fatal("NewService() logger = nil")
	}
	if got.threadAgents == nil {
		t.Fatal("NewService() threadAgents = nil")
	}
	if len(got.threadAgents) != 0 {
		t.Fatalf("len(threadAgents) = %d, want 0", len(got.threadAgents))
	}
}

func TestNewThreadHandlersRegistersExpectedRoutes(t *testing.T) {
	t.Parallel()

	got := NewThreadHandlers(&stubThreadService{}, nil).Handlers
	if len(got) != 35 {
		t.Fatalf("len(Handlers) = %d, want 35", len(got))
	}
	for _, method := range []string{"thread/start", "thread/stop", "thread/list", "thread/listPage", "thread/loaded/listPage", "thread/model/set", "thread/clear", "thread/realtime/start", "thread/handoff", "thread/promptHistory"} {
		if _, ok := got[method]; !ok {
			t.Fatalf("Handlers missing %q", method)
		}
	}
	if _, ok := got["ui/task/flush_and_verify"]; ok {
		t.Fatalf("Handlers unexpectedly contains removed task handoff endpoint")
	}
	removedManualTaskRoute := "ui/thread/" + strings.Join([]string{"promote", "task"}, "-")
	if _, ok := got[removedManualTaskRoute]; ok {
		t.Fatalf("Handlers unexpectedly contains removed manual task endpoint")
	}
}

func TestPromptHistoryParamsRejectUnknownFieldAndInvalidBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		params      string
		wantMessage string
	}{
		{name: "unknown field", params: `{"cwd":"/repo","limit":10,"extra":true}`, wantMessage: "invalid parameters"},
		{name: "zero limit", params: `{"cwd":"/repo","limit":0}`, wantMessage: "prompt history limit must be between 1 and 50"},
		{name: "oversized limit", params: `{"cwd":"/repo","limit":51}`, wantMessage: "prompt history limit must be between 1 and 50"},
		{name: "oversized cursor", params: `{"cwd":"/repo","limit":10,"cursor":"` + strings.Repeat("x", 2049) + `"}`, wantMessage: "prompt history cursor exceeds 2048 bytes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubThreadService{}
			server := newThreadTestServer(stub)
			_, err := server.Dispatch(context.Background(), "thread/promptHistory", json.RawMessage(tc.params))
			var rpcErr *jrpc2.Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.Code(contract.CodeInvalidParams) {
				t.Fatalf("Dispatch(thread/promptHistory) error = %v, want invalid params", err)
			}
			if !strings.Contains(rpcErr.Message, tc.wantMessage) {
				t.Fatalf("rpc message = %q, want stable fragment %q", rpcErr.Message, tc.wantMessage)
			}
			if stub.promptHistoryCalls != 0 {
				t.Fatalf("ScanPromptHistory calls = %d, want 0", stub.promptHistoryCalls)
			}
		})
	}
}

func TestPromptHistoryHandlerReturnsExactWireKeys(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	stub := newPromptHistoryWireStub(createdAt)
	got := dispatchPromptHistoryWire(t, stub)
	requirePromptHistoryExactKeys(t, got, "entries", "nextCursor", "hasMore", "nonce")
	requirePromptHistoryWireEntry(t, got, createdAt)
	requirePromptHistoryWireMetadata(t, got)
	requirePromptHistoryCapturedRequest(t, stub)
}

func newPromptHistoryWireStub(createdAt time.Time) *stubThreadService {
	return &stubThreadService{stubThreadServicePromptHistory: stubThreadServicePromptHistory{
		promptHistoryResult: threaddto.PromptHistoryResult{
			Entries: []threaddto.PromptHistoryEntry{{
				ThreadID:  "thread-1",
				MessageID: "42",
				Text:      "prompt text",
				CreatedAt: createdAt,
			}},
			NextCursor: "opaque-cursor",
			HasMore:    true,
			Nonce:      "opaque-nonce",
		},
	}}
}

func dispatchPromptHistoryWire(t *testing.T, stub *stubThreadService) map[string]any {
	t.Helper()
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/promptHistory", json.RawMessage(`{"cwd":"/repo","activeThreadId":"thread-1","cursor":"cursor-in","nonce":"nonce-in","limit":10}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/promptHistory) error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/promptHistory) error = %v", err)
	}
	return got
}

func requirePromptHistoryWireEntry(t *testing.T, got map[string]any, createdAt time.Time) {
	t.Helper()
	entries, ok := got["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %#v, want one entry", got["entries"])
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("entry = %#v, want object", entries[0])
	}
	requirePromptHistoryExactKeys(t, entry, "threadId", "messageId", "text", "createdAt")
	if entry["threadId"] != "thread-1" || entry["messageId"] != "42" || entry["text"] != "prompt text" || entry["createdAt"] != createdAt.Format(time.RFC3339) {
		t.Fatalf("entry = %#v, want exact prompt history fields", entry)
	}
}

func requirePromptHistoryWireMetadata(t *testing.T, got map[string]any) {
	t.Helper()
	if got["nextCursor"] != "opaque-cursor" || got["hasMore"] != true || got["nonce"] != "opaque-nonce" {
		t.Fatalf("response metadata = %#v", got)
	}
}

func requirePromptHistoryCapturedRequest(t *testing.T, stub *stubThreadService) {
	t.Helper()
	if stub.promptHistoryCalls != 1 || stub.promptHistoryReq.CWD != "/repo" || stub.promptHistoryReq.ActiveThreadID != "thread-1" || stub.promptHistoryReq.Cursor != "cursor-in" || stub.promptHistoryReq.Nonce != "nonce-in" || stub.promptHistoryReq.Limit != 10 {
		t.Fatalf("ScanPromptHistory request/calls = %#v/%d", stub.promptHistoryReq, stub.promptHistoryCalls)
	}
}

func TestPromptHistoryHandlerMapsTypedErrorsWithoutSourceLeakage(t *testing.T) {
	t.Parallel()
	const (
		fixtureCWD    = "/private/repository/path"
		fixturePrompt = "prompt-secret"
	)
	tests := []struct {
		name        string
		sentinel    error
		wantCode    jrpc2.Code
		wantMessage string
	}{
		{name: "active cwd", sentinel: ErrPromptHistoryActiveThreadCWD, wantCode: jrpc2.Code(contract.CodeInvalidParams), wantMessage: "active thread is outside the requested cwd"},
		{name: "invalid request", sentinel: ErrPromptHistoryInvalidRequest, wantCode: jrpc2.Code(contract.CodeInvalidParams), wantMessage: "invalid prompt history request"},
		{name: "thread cap", sentinel: ErrPromptHistoryThreadLimitExceeded, wantCode: jrpc2.Code(contract.CodeInvalidParams), wantMessage: "prompt history thread limit exceeded"},
		{name: "stale nonce", sentinel: ErrPromptHistoryStaleNonce, wantCode: jrpc2.Code(contract.CodeConflict), wantMessage: "prompt history snapshot is stale"},
		{name: "invalid cursor", sentinel: ErrPromptHistoryInvalidCursor, wantCode: jrpc2.Code(contract.CodeInvalidParams), wantMessage: "invalid prompt history cursor"},
		{name: "revision unavailable", sentinel: ErrPromptHistoryRevisionUnavailable, wantCode: jrpc2.Code(contract.CodeCapabilityGate), wantMessage: "prompt history source revision unavailable"},
		{name: "page read", sentinel: ErrPromptHistoryPageRead, wantCode: jrpc2.Code(contract.CodeInvalidState), wantMessage: "prompt history page read failed"},
		{name: "generic failure", sentinel: errors.New("unexpected prompt history failure"), wantCode: jrpc2.Code(contract.CodeInvalidState), wantMessage: "prompt history request failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubThreadService{stubThreadServicePromptHistory: stubThreadServicePromptHistory{
				promptHistoryErr: fmt.Errorf("%w: %s %s", tc.sentinel, fixtureCWD, fixturePrompt),
			}}
			server := newThreadTestServer(stub)
			_, err := server.Dispatch(context.Background(), "thread/promptHistory", json.RawMessage(`{"cwd":"`+fixtureCWD+`","limit":10}`))
			var rpcErr *jrpc2.Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != tc.wantCode {
				t.Fatalf("Dispatch(thread/promptHistory) error = %v, want code %v", err, tc.wantCode)
			}
			if rpcErr.Message != tc.wantMessage {
				t.Fatalf("rpc message = %q, want %q", rpcErr.Message, tc.wantMessage)
			}
			if strings.Contains(rpcErr.Message, fixtureCWD) || strings.Contains(rpcErr.Message, fixturePrompt) {
				t.Fatalf("rpc message exposes fixture source: %q", rpcErr.Message)
			}
		})
	}
}

func TestPromptHistoryRPCErrorPreservesContextTermination(t *testing.T) {
	for name, cause := range map[string]error{"canceled": context.Canceled, "deadline": context.DeadlineExceeded} {
		t.Run(name, func(t *testing.T) {
			wrapped := fmt.Errorf("wrapped: %w", cause)
			if got := promptHistoryRPCError(wrapped); got != wrapped {
				t.Fatalf("promptHistoryRPCError() = %v, want original %v", got, wrapped)
			}
		})
	}
}

func requirePromptHistoryExactKeys(t *testing.T, got map[string]any, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("JSON keys = %#v, want exactly %#v", got, want)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Fatalf("JSON object missing key %q: %#v", key, got)
		}
	}
}

func TestNewThreadHandlersDispatchList(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		listResult: []Ref{{ID: "thread-1", Name: "demo", AgentID: "agent-1", Status: "archived"}},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/list) error = %v", err)
	}
	var got []Ref
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/list) error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "thread-1" || got[0].Status != "archived" || stub.listCalls != 1 {
		t.Fatalf("Dispatch(thread/list) = %#v, calls=%d", got, stub.listCalls)
	}
}

func TestNewThreadHandlersDispatchListHidesCreatingButShowsFailedFork(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		listResult: []Ref{
			{ID: "thread-creating", Status: statusForkCreating},
			{ID: "thread-failed", Status: statusFailed},
			{ID: "thread-created", Status: statusCreated},
		},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/list) error = %v", err)
	}
	var got []Ref
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/list) error = %v", err)
	}
	if len(got) != 2 || got[0].ID != "thread-failed" || got[1].ID != "thread-created" {
		t.Fatalf("Dispatch(thread/list) = %#v, want failed fork visible and creating hidden", got)
	}
}

func TestNewThreadHandlersDispatchStart(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		startResult: StartResult{
			ThreadID:       "thread-7",
			AgentID:        "agent-7",
			SessionID:      "session-7",
			Status:         "running",
			Model:          "gpt-5.5",
			Provider:       "codex",
			ModelProvider:  "openai",
			CWD:            "/tmp/demo",
			ApprovalPolicy: "never",
		},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/start", json.RawMessage(`{"cwd":"/tmp/demo","name":"Hello","modelProvider":"codex","prompt_key":"main/dag_designer_zh","agent_key":"assistant","toolSurfaceMode":"chat","defer_spawn":true,"launchIntentId":"launch_018f00e0-39fc-72ac-a47a-2a858c75d111"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/start) error = %v", err)
	}
	got := decodeThreadHandlerMap(t, "thread/start", raw)
	assertThreadStartEnvelope(t, got)
	assertThreadStartEffectiveFields(t, got)
	assertThreadStartRequest(t, stub.startReq)
}

func TestNewThreadHandlersDispatchStartRejectsUnknownField(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{}
	server := newThreadTestServer(stub)
	_, err := server.Dispatch(context.Background(), "thread/start", json.RawMessage(`{"provider":"codex","cwd":"/tmp/demo","unexpectedUiOnlyField":"leak"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid parameters") {
		t.Fatalf("Dispatch(thread/start) error = %v, want unknown field rejection", err)
	}
	if stub.startReq.Provider != "" || stub.startReq.CWD != "" {
		t.Fatalf("Start() was called with %#v, want decode rejection before service call", stub.startReq)
	}
}

func TestNewThreadHandlersDispatchStartRequiresProvider(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{}
	server := newThreadTestServer(stub)
	_, err := server.Dispatch(context.Background(), "thread/start", json.RawMessage(`{"cwd":"/tmp/demo"}`))
	if err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("Dispatch(thread/start) error = %v, want provider required", err)
	}
	if startRequestWasCalled(stub.startReq) {
		t.Fatalf("Start() was called with %#v, want dispatch rejection before service call", stub.startReq)
	}
}

func TestNewThreadHandlersDispatchStartRequiresCWD(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{}
	server := newThreadTestServer(stub)
	_, err := server.Dispatch(context.Background(), "thread/start", json.RawMessage(`{"modelProvider":"codex"}`))
	if err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("Dispatch(thread/start) error = %v, want cwd required", err)
	}
	if startRequestWasCalled(stub.startReq) {
		t.Fatalf("Start() was called with %#v, want dispatch rejection before service call", stub.startReq)
	}
}

func TestNewThreadHandlersDispatchResume(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		resumeResult: ResumeResult{
			ThreadID:  "thread-9",
			SessionID: "session-9",
			Status:    "resumed",
			Model:     "gpt-5.5",
			CWD:       "/tmp/resume",
		},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/resume", json.RawMessage(`{"threadId":"thread-9","path":"/tmp/legacy","cwd":"/tmp/resume","model":"gpt-5.5"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/resume) error = %v", err)
	}
	got := decodeThreadHandlerMap(t, "thread/resume", raw)
	assertThreadResumeEnvelope(t, got)
	assertThreadResumeRequest(t, stub.resumeReq)
}

func decodeThreadHandlerMap(t *testing.T, method string, raw json.RawMessage) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", method, err)
	}
	return got
}

func assertThreadStartEnvelope(t *testing.T, got map[string]any) {
	t.Helper()
	if got["threadId"] != "thread-7" || got["sessionId"] != "session-7" || got["status"] != "running" {
		t.Fatalf("Dispatch(thread/start) = %#v", got)
	}
	thread, _ := got["thread"].(map[string]any)
	if thread["id"] != "thread-7" || thread["status"] != "running" {
		t.Fatalf("Dispatch(thread/start).thread = %#v", thread)
	}
}

func assertThreadStartEffectiveFields(t *testing.T, got map[string]any) {
	t.Helper()
	assertTopLevelStartEffectiveFields(t, got)
	effective, _ := got["effective"].(map[string]any)
	assertNestedStartEffectiveFields(t, effective)
}

func assertTopLevelStartEffectiveFields(t *testing.T, got map[string]any) {
	t.Helper()
	if got["model"] != "gpt-5.5" || got["provider"] != "codex" || got["modelProvider"] != "openai" || got["cwd"] != "/tmp/demo" || got["approvalPolicy"] != "never" {
		t.Fatalf("Dispatch(thread/start) effective fields = %#v", got)
	}
}

func assertNestedStartEffectiveFields(t *testing.T, effective map[string]any) {
	t.Helper()
	if effective["model"] != "gpt-5.5" || effective["provider"] != "codex" || effective["modelProvider"] != "openai" || effective["cwd"] != "/tmp/demo" || effective["approvalPolicy"] != "never" {
		t.Fatalf("Dispatch(thread/start).effective = %#v", effective)
	}
}

func assertThreadStartRequest(t *testing.T, req StartRequest) {
	t.Helper()
	assertThreadStartCoreRequest(t, req)
	assertThreadStartLaunchMetadata(t, req)
}

func startRequestWasCalled(req StartRequest) bool {
	return req.Provider != "" ||
		req.ModelProvider != "" ||
		req.CWD != "" ||
		req.Name != "" ||
		req.Prompt != "" ||
		req.AgentKey != "" ||
		req.PromptKey != ""
}

func assertThreadStartCoreRequest(t *testing.T, req StartRequest) {
	t.Helper()
	if req.Provider != "" || req.ModelProvider != "codex" || req.CWD != "/tmp/demo" || req.Name != "Hello" || req.Prompt != "" || req.BaseInstructions != "" || req.ToolSurfaceMode != "chat" {
		t.Fatalf("StartRequest = %#v", req)
	}
}

func assertThreadStartLaunchMetadata(t *testing.T, req StartRequest) {
	t.Helper()
	if req.PromptKey != "main/dag_designer_zh" || req.AgentKey != "assistant" || !req.DeferSpawn || req.LaunchIntentID != "launch_018f00e0-39fc-72ac-a47a-2a858c75d111" {
		t.Fatalf("StartRequest = %#v", req)
	}
}

func TestNormalizeStartRequestRejectsInvalidToolSurfaceMode(t *testing.T) {
	t.Parallel()

	_, _, err := normalizeStartRequest(StartRequest{
		Provider:        "codex",
		CWD:             "/tmp/demo",
		ToolSurfaceMode: "full",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid tool surface mode") {
		t.Fatalf("normalizeStartRequest() error = %v, want invalid tool surface mode", err)
	}
}

func TestBuildPendingSpawnRequestPreservesToolSurfaceMode(t *testing.T) {
	t.Parallel()

	row := &ThreadRecord{
		ThreadID: "thread-pending",
		Cwd:      "/tmp/demo",
		Model:    "gpt-5.5",
		Prompt:   "New chat",
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Provider:        "codex",
			ToolSurfaceMode: "chat",
		}),
	}
	req, err := buildPendingSpawnRequest(row, "thread-pending", "hello", "/tmp/demo")
	if err != nil {
		t.Fatalf("buildPendingSpawnRequest() error = %v", err)
	}
	if req.ToolSurfaceMode != "chat" {
		t.Fatalf("ToolSurfaceMode = %q, want chat", req.ToolSurfaceMode)
	}
}

func assertThreadResumeEnvelope(t *testing.T, got map[string]any) {
	t.Helper()
	if got["threadId"] != "thread-9" || got["sessionId"] != "session-9" || got["status"] != "resumed" || got["model"] != "gpt-5.5" || got["cwd"] != "/tmp/resume" {
		t.Fatalf("Dispatch(thread/resume) = %#v", got)
	}
	thread, _ := got["thread"].(map[string]any)
	if thread["id"] != "thread-9" || thread["status"] != "resumed" {
		t.Fatalf("Dispatch(thread/resume).thread = %#v", thread)
	}
}

func assertThreadResumeRequest(t *testing.T, req ResumeRequest) {
	t.Helper()
	if req.ThreadID != "thread-9" || req.Path != "/tmp/legacy" || req.CWD != "/tmp/resume" || req.Model != "gpt-5.5" {
		t.Fatalf("ResumeRequest = %#v", req)
	}
}

func TestNewThreadHandlersDispatchForkReturnsThreadEnvelope(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		forkResult: ForkResult{NewThreadID: "thread-7-fork", ForkedFrom: "thread-7"},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/fork", json.RawMessage(`{"threadId":"thread-7"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/fork) error = %v", err)
	}
	var got struct {
		Thread threadInfo `json:"thread"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/fork) error = %v", err)
	}
	if got.Thread.ID != "thread-7-fork" || got.Thread.ForkedFrom != "thread-7" {
		t.Fatalf("Dispatch(thread/fork) = %#v", got)
	}
	if stub.forkThreadID != "thread-7" {
		t.Fatalf("Fork thread id = %q, want thread-7", stub.forkThreadID)
	}
}

func TestNewThreadHandlersDispatchRecoverReturnsEnvelope(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		recoverResult: RecoverResult{ThreadID: "thread-8", Status: "recovering", Recovered: true, Mode: "relaunch_resume"},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/recover", json.RawMessage(`{"threadId":"thread-8"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/recover) error = %v", err)
	}
	var got struct {
		Thread    threadInfo `json:"thread"`
		Recovered bool       `json:"recovered"`
		Mode      string     `json:"mode"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/recover) error = %v", err)
	}
	if got.Thread.ID != "thread-8" || got.Thread.Status != "recovering" || !got.Recovered || got.Mode != "relaunch_resume" {
		t.Fatalf("Dispatch(thread/recover) = %#v", got)
	}
	if stub.recoverThreadID != "thread-8" {
		t.Fatalf("Recover thread id = %q, want thread-8", stub.recoverThreadID)
	}
}

func TestNewThreadHandlersDispatchConfigSet(t *testing.T) {
	t.Parallel()

	high := "high"
	empty := ""
	stub := &stubThreadService{
		setConfigResp: dto.ThreadConfig{
			ThreadID: "thread-1",
			Effective: dto.ThreadConfigValues{
				Model:  "gpt-5.5",
				Effort: "high",
			},
		},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/config/set", json.RawMessage(`{"threadId":"thread-1","model":"","effort":"high"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/config/set) error = %v", err)
	}
	var got dto.ThreadConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/config/set) error = %v", err)
	}
	if got.Effective.Model != "gpt-5.5" || got.Effective.Effort != "high" {
		t.Fatalf("Dispatch(thread/config/set) = %#v", got)
	}
	if stub.setConfigID != "thread-1" {
		t.Fatalf("setConfigID = %q, want thread-1", stub.setConfigID)
	}
	if stub.setConfigPatch.Model == nil || *stub.setConfigPatch.Model != empty {
		t.Fatalf("setConfigPatch.Model = %#v, want explicit empty string", stub.setConfigPatch.Model)
	}
	if stub.setConfigPatch.Effort == nil || *stub.setConfigPatch.Effort != high {
		t.Fatalf("setConfigPatch.Effort = %#v, want %q", stub.setConfigPatch.Effort, high)
	}
}

func TestNewThreadHandlersDispatchConfigGetPendingLaunch(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:      "thread-pending-launch",
		Model:         "stored-thread-model",
		Status:        statusCreated,
		PendingLaunch: true,
		ConfigOverride: mustStoredThreadConfigRaw(t, storedThreadConfig{
			Provider:  "codex",
			Model:     "gpt-5.5",
			Effort:    "high",
			Approvals: "never",
		}),
	}}
	svc := newConfigTestService(t, threads, &stubBindingStore{}, &stubSessionProvider{})
	server := newThreadTestServer(svc)

	got := dispatchThreadConfigGet(t, server, "thread-pending-launch")
	assertPendingLaunchOfflineConfig(t, got)
}

func dispatchThreadConfigGet(t *testing.T, server *rpcpkg.Server, threadID string) dto.ThreadConfig {
	t.Helper()

	req, err := json.Marshal(struct {
		ThreadID string `json:"threadId"`
	}{ThreadID: threadID})
	if err != nil {
		t.Fatalf("Marshal(thread/config/get request) error = %v", err)
	}
	raw, err := server.Dispatch(context.Background(), "thread/config/get", req)
	if err != nil {
		t.Fatalf("Dispatch(thread/config/get) error = %v, want pending_launch offline config", err)
	}
	var got dto.ThreadConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/config/get) error = %v", err)
	}
	return got
}

func assertPendingLaunchOfflineConfig(t *testing.T, got dto.ThreadConfig) {
	t.Helper()

	if got.ThreadID != "thread-pending-launch" || got.Provider != "codex" {
		t.Fatalf("Dispatch(thread/config/get) identity/provider = %#v", got)
	}
	if got.Override.Model != "gpt-5.5" || got.Override.Effort != "high" || got.Override.Approvals != "never" {
		t.Fatalf("Dispatch(thread/config/get) override = %#v", got.Override)
	}
	if got.Effective.Model != "gpt-5.5" || got.Effective.Effort != "high" || got.Effective.Approvals != "never" {
		t.Fatalf("Dispatch(thread/config/get) effective = %#v", got.Effective)
	}
}

func TestNewThreadHandlersDispatchModelSetReturnsServiceCapabilityError(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		setModelErr: newFriendlyCapabilityError(dto.CapModelSwitch, "claude", errRuntimeModelSwitchUnsupported),
	}
	server := newThreadTestServer(stub)
	_, err := server.Dispatch(context.Background(), "thread/model/set", json.RawMessage(`{"threadId":"thread-1","model":"sonnet"}`))
	if err == nil {
		t.Fatal("Dispatch(thread/model/set) error = nil, want capability error")
	}
	if stub.setModelID != "thread-1" || stub.setModelArg != "sonnet" {
		t.Fatalf("SetModel call = %q/%q", stub.setModelID, stub.setModelArg)
	}
	if !strings.Contains(err.Error(), errRuntimeModelSwitchUnsupported) {
		t.Fatalf("Dispatch(thread/model/set) error = %v, want contains %q", err, errRuntimeModelSwitchUnsupported)
	}
	if errors.Is(err, rpcpkg.ErrCapabilityGate("capability not supported by active provider")) {
		t.Fatalf("Dispatch(thread/model/set) error = %v, want service capability error", err)
	}
}

func TestNewThreadHandlersDispatchApprovalsSetAcceptsPolicyAlias(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{sendCommandResult: map[string]any{"ok": true}}
	server := newThreadTestServer(stub)
	if _, err := server.Dispatch(context.Background(), "thread/approvals/set", json.RawMessage(`{"threadId":"thread-3","policy":"on-request"}`)); err != nil {
		t.Fatalf("Dispatch(thread/approvals/set) error = %v", err)
	}
	if stub.sendCommandThread != "thread-3" || stub.sendCommandName != "/approvals" || stub.sendCommandArgs != "on-request" {
		t.Fatalf("SendCommand(thread/approvals/set) = (%q, %q, %q)", stub.sendCommandThread, stub.sendCommandName, stub.sendCommandArgs)
	}
}

func TestNewThreadHandlersDispatchApprovalsSetRejectsConflictingArgs(t *testing.T) {
	t.Parallel()

	server := newThreadTestServer(&stubThreadService{})
	_, err := server.Dispatch(context.Background(), "thread/approvals/set", json.RawMessage(`{"threadId":"thread-3","policy":"never","args":"always"}`))
	if err == nil || !strings.Contains(err.Error(), errApprovalsSetArgsConflict.Error()) {
		t.Fatalf("Dispatch(thread/approvals/set) error = %v", err)
	}
}

func TestNewThreadHandlersDispatchClear(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{sendCommandResult: map[string]any{"command": "/clear"}}
	server := newThreadTestServer(stub)
	if _, err := server.Dispatch(context.Background(), "thread/clear", json.RawMessage(`{"threadId":"thread-2"}`)); err != nil {
		t.Fatalf("Dispatch(thread/clear) error = %v", err)
	}
	if stub.sendCommandThread != "thread-2" || stub.sendCommandName != "/clear" || stub.sendCommandArgs != "" {
		t.Fatalf("SendCommand(thread/clear) = (%q, %q, %q)", stub.sendCommandThread, stub.sendCommandName, stub.sendCommandArgs)
	}
}

func TestNewThreadHandlersDispatchReadReturnsHistoryPayload(t *testing.T) {
	t.Parallel()

	server := newThreadTestServer(&stubThreadService{})
	raw, err := server.Dispatch(context.Background(), "thread/read", json.RawMessage(`{"threadId":"thread-1"}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/read) error = %v", err)
	}
	var got ReadHistoryResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/read) error = %v", err)
	}
	want := ReadHistoryResult{History: []ReadHistoryThread{{ThreadID: "thread-1"}}}
	if got.History == nil || len(got.History) != 1 || got.History[0] != want.History[0] {
		t.Fatalf("Dispatch(thread/read) = %#v, want %#v", got, want)
	}
}

func TestNewThreadHandlersDispatchMessagesReturnsEnvelope(t *testing.T) {
	t.Parallel()

	stub := &stubThreadService{
		readMessagesResult: dto.ThreadMessagesResult{
			Messages:   []dto.Message{{ID: 2, AgentID: "thread-1", Role: "assistant", EventType: "agent_message", Content: "world"}},
			Total:      7,
			HasMore:    true,
			NextBefore: "opaque-cursor",
		},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "thread/messages", json.RawMessage(`{"threadId":"thread-1","limit":2,"before":3}`))
	if err != nil {
		t.Fatalf("Dispatch(thread/messages) error = %v", err)
	}
	var got dto.ThreadMessagesResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal(thread/messages) error = %v", err)
	}
	requireThreadMessagesEnvelope(t, got)
	requireThreadMessagesDispatchCall(t, stub, "thread-1", 2, "3")
}

func requireThreadMessagesEnvelope(t *testing.T, got dto.ThreadMessagesResult) {
	t.Helper()
	if got.Total != 7 {
		t.Fatalf("total = %d, want 7", got.Total)
	}
	if !got.HasMore || got.NextBefore != "opaque-cursor" {
		t.Fatalf("page metadata = hasMore:%v nextBefore:%q", got.HasMore, got.NextBefore)
	}
	if len(got.Messages) != 1 || got.Messages[0].ID != 2 {
		t.Fatalf("messages = %#v, want id 2", got.Messages)
	}
}

func requireThreadMessagesDispatchCall(t *testing.T, stub *stubThreadService, threadID string, limit int, before string) {
	t.Helper()
	if stub.readMessagesThread != threadID {
		t.Fatalf("ReadMessages thread = %q, want %q", stub.readMessagesThread, threadID)
	}
	if stub.readMessagesLimit != limit {
		t.Fatalf("ReadMessages limit = %d, want %d", stub.readMessagesLimit, limit)
	}
	if stub.readMessagesBefore != before {
		t.Fatalf("ReadMessages before = %q, want %q", stub.readMessagesBefore, before)
	}
}

func newThreadTestServer(svc Service) *rpcpkg.Server {
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewThreadHandlers(svc, nil).Handlers)
	return server
}

type stubThreadService struct {
	stubThreadServiceLifecycleNoop
	stubThreadServiceListNoop
	stubThreadServicePromptHistory

	startReq           StartRequest
	startResult        StartResult
	resumeReq          ResumeRequest
	resumeResult       ResumeResult
	forkThreadID       string
	forkResult         ForkResult
	recoverThreadID    string
	recoverResult      RecoverResult
	listResult         []Ref
	listCalls          int
	setConfigPatch     dto.ThreadConfigPatch
	setConfigID        string
	setConfigResp      dto.ThreadConfig
	setModelID         string
	setModelArg        string
	setModelErr        error
	readMessagesThread string
	readMessagesLimit  int
	readMessagesBefore string
	readMessagesResult dto.ThreadMessagesResult
	sendCommandThread  string
	sendCommandName    string
	sendCommandArgs    string
	sendCommandResult  any
	handoffReq         HandoffRequest
	handoffResult      HandoffResult
}

func (s *stubThreadService) Start(_ context.Context, req StartRequest) (StartResult, error) {
	s.startReq = req
	return s.startResult, nil
}

type stubThreadServiceLifecycleNoop struct{}

func (stubThreadServiceLifecycleNoop) SpawnIfNeeded(context.Context, string, string, string) (bool, SpawnRouting, error) {
	return false, SpawnRouting{}, nil
}
func (stubThreadServiceLifecycleNoop) Stop(context.Context, string) error { return nil }
func (s *stubThreadService) Resume(_ context.Context, req ResumeRequest) (ResumeResult, error) {
	s.resumeReq = req
	return s.resumeResult, nil
}
func (s *stubThreadService) Fork(_ context.Context, threadID string) (ForkResult, error) {
	s.forkThreadID = threadID
	return s.forkResult, nil
}
func (s *stubThreadService) Recover(_ context.Context, threadID string) (RecoverResult, error) {
	s.recoverThreadID = threadID
	return s.recoverResult, nil
}
func (s *stubThreadService) Handoff(_ context.Context, req HandoffRequest) (HandoffResult, error) {
	s.handoffReq = req
	return s.handoffResult, nil
}
func (stubThreadServiceLifecycleNoop) Get(context.Context, string) (*Ref, error) {
	return &Ref{ID: "thread-1", AgentID: "agent-1"}, nil
}
func (stubThreadServiceLifecycleNoop) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}
func (s *stubThreadService) ReadMessages(_ context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error) {
	s.readMessagesThread = threadID
	s.readMessagesLimit = limit
	s.readMessagesBefore = before
	return s.readMessagesResult, nil
}

type stubThreadServicePromptHistory struct {
	promptHistoryReq    PromptHistoryRequest
	promptHistoryResult threaddto.PromptHistoryResult
	promptHistoryErr    error
	promptHistoryCalls  int
}

func (s *stubThreadServicePromptHistory) ScanPromptHistory(_ context.Context, req PromptHistoryRequest) (threaddto.PromptHistoryResult, error) {
	s.promptHistoryCalls++
	s.promptHistoryReq = req
	return s.promptHistoryResult, s.promptHistoryErr
}
func (stubThreadServiceLifecycleNoop) GetConfig(context.Context, string) (dto.ThreadConfig, error) {
	return dto.ThreadConfig{}, nil
}
func (s *stubThreadService) SetConfig(_ context.Context, threadID string, patch dto.ThreadConfigPatch) (dto.ThreadConfig, error) {
	s.setConfigID = threadID
	s.setConfigPatch = patch
	return s.setConfigResp, nil
}
func (s *stubThreadService) SetModel(_ context.Context, threadID, model string) (dto.ThreadConfig, error) {
	s.setModelID = threadID
	s.setModelArg = model
	return dto.ThreadConfig{}, s.setModelErr
}
func (stubThreadServiceLifecycleNoop) Compact(context.Context, string, string) (dto.ThreadCompactResult, error) {
	return dto.ThreadCompactResult{}, nil
}
func (stubThreadServiceLifecycleNoop) Archive(context.Context, string) error   { return nil }
func (stubThreadServiceLifecycleNoop) Unarchive(context.Context, string) error { return nil }

type stubThreadServiceListNoop struct{}

func (stubThreadServiceListNoop) ListByStatus(context.Context, string) ([]Ref, error) {
	return nil, nil
}
func (stubThreadServiceListNoop) ListByCWD(context.Context, string) ([]Ref, error) { return nil, nil }
func (s *stubThreadService) SendCommand(_ context.Context, threadID, command, args string) (any, error) {
	s.sendCommandThread = threadID
	s.sendCommandName = command
	s.sendCommandArgs = args
	return s.sendCommandResult, nil
}
func (stubThreadServiceListNoop) SetName(context.Context, string, string) error { return nil }
func (stubThreadServiceListNoop) Delete(context.Context, string) error          { return nil }
func (s *stubThreadService) List(context.Context) ([]Ref, error) {
	s.listCalls++
	return s.listResult, nil
}
