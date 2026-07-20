package eventsurface

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	crondto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/cron"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	taskdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/task"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

type publishedEvent struct {
	method  string
	payload any
}

func TestBindPublishesExpandedSurface(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 2)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	now := time.Unix(1710000000, 0).UTC()
	event.Publish(dispatcher, threaddto.Started{
		EventHeader:      shared.EventHeader{Timestamp: now},
		ThreadID:         "thread-1",
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/tmp/demo",
	})
	event.Publish(dispatcher, agentdto.AgentStopped{
		AgentSessionHeader: shared.AgentSessionHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{
					EventHeader: shared.EventHeader{Timestamp: now},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			SessionID: "session-1",
		},
		Reason: "done",
	})

	seen := map[string]bool{}
	for range 2 {
		seen[mustReceivePublished(t, got).method] = true
	}
	for _, method := range []string{MethodThreadStarted, MethodAgentStopped} {
		if !seen[method] {
			t.Fatalf("method %q missing from %#v", method, seen)
		}
	}
}

func TestBindPublishesCanonicalTurnTerminal(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 1)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	now := time.Unix(1710000000, 0).UTC()
	completed := turndto.TurnCompleted{
		TurnHeader: bindTestTurnHeader(now),
		Success:    true,
		Status:     "completed",
	}
	terminal, err := turndto.NewTurnTerminalV2(completed, "bind-terminal-event")
	if err != nil {
		t.Fatalf("NewTurnTerminalV2() error = %v", err)
	}
	completed, err = turndto.AttachCanonicalTurnTerminal(completed, terminal)
	if err != nil {
		t.Fatalf("AttachCanonicalTurnTerminal() error = %v", err)
	}
	event.Publish(dispatcher, completed)
	published := mustReceivePublished(t, got)
	if published.method != "turn/terminal" {
		t.Fatalf("terminal method = %q, want turn/terminal", published.method)
	}
	payload := payloadMap(published.payload)
	if err := turndto.ValidateTurnTerminalV2(payload); err != nil {
		t.Fatalf("canonical terminal payload: %v; payload=%#v", err, payload)
	}
	if payload["outcome"] != "success" {
		t.Fatalf("terminal payload = %#v, want success outcome", payload)
	}
	if _, legacy := payload["status"]; legacy {
		t.Fatalf("terminal payload leaked legacy status: %#v", payload)
	}
}

func TestRemoteTurnTerminalRequiresCanonicalPayloadAndExplicitOwner(t *testing.T) {
	legacy := map[string]any{
		"agent_id": "agent-1", "thread_id": "thread-1", "turn_id": "turn-1",
		"success": true, "status": "completed",
	}
	if _, err := DecodeRemoteTurnTerminal(jsonValueDecoder(t, legacy)); err == nil {
		t.Fatal("DecodeRemoteTurnTerminal() accepted legacy TurnCompleted payload")
	}
	canonical := turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "11111111-2222-4333-8444-555555555555",
		ThreadID:      "thread-1",
		TurnID:        "turn-1",
		Outcome:       "success",
		OccurredAt:    "2026-07-16T10:11:12.123Z",
	}
	decoded, err := DecodeRemoteTurnTerminal(jsonValueDecoder(t, canonical))
	if err != nil {
		t.Fatalf("DecodeRemoteTurnTerminal() error = %v", err)
	}
	if _, err := ProjectRemoteTurnTerminal(decoded, ""); err == nil {
		t.Fatal("ProjectRemoteTurnTerminal() accepted empty owner")
	}
	projected, err := ProjectRemoteTurnTerminal(decoded, "agent-1")
	if err != nil {
		t.Fatalf("ProjectRemoteTurnTerminal() error = %v", err)
	}
	if projected.AgentID != "agent-1" || projected.ThreadID != "thread-1" || projected.TurnID != "turn-1" || !projected.Success || projected.Status != "completed" {
		t.Fatalf("projected terminal = %#v, want explicit owner and canonical identity", projected)
	}
}

func TestDecodeRemoteTurnTerminalRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "legacy top-level success",
			raw:  `{"schemaVersion":2,"eventId":"event-1","threadId":"thread-1","turnId":"turn-1","outcome":"success","occurredAt":"2026-07-17T01:02:03Z","success":true}`,
		},
		{
			name: "nested public error extra",
			raw:  `{"schemaVersion":2,"eventId":"event-2","threadId":"thread-1","turnId":"turn-1","outcome":"failed","publicError":{"code":"FAILED","title":"Failed","message":"failed","diagnosticId":"diag-1","retryable":false,"recoveryActions":[],"extra":"forbidden"},"occurredAt":"2026-07-17T01:02:03Z"}`,
		},
		{
			name: "partial item id typo",
			raw:  `{"schemaVersion":2,"eventId":"event-3","threadId":"thread-1","turnId":"turn-1","outcome":"success","partialItemId":["item-1"],"occurredAt":"2026-07-17T01:02:03Z"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeRemoteTurnTerminal(func(target any) error {
				return json.Unmarshal([]byte(test.raw), target)
			}); err == nil {
				t.Fatal("DecodeRemoteTurnTerminal() accepted unknown field")
			}
		})
	}
}

const remotePublicErrorLeakFixture = "provider-token=secret-value /private/agent/config.go\nstack: remote failure"

func TestDecodeRemoteTurnTerminalSanitizesUntrustedPublicError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		code     string
		wantCode string
	}{
		{name: "known code", code: "PROVIDER_FAILED", wantCode: "PROVIDER_FAILED"},
		{name: "unknown safe code", code: "REMOTE_SECRET_FAILURE", wantCode: "REMOTE_SECRET_FAILURE"},
		{name: "unsafe code", code: "provider-token=secret-value", wantCode: remotePublicErrorCodeFallback},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			terminal, err := DecodeRemoteTurnTerminal(jsonValueDecoder(t, remoteTerminalWithPublicError(test.code)))
			if err != nil {
				t.Fatalf("DecodeRemoteTurnTerminal() error = %v", err)
			}
			assertSanitizedRemotePublicError(t, terminal.PublicError, test.wantCode)
		})
	}
}

// remoteTerminalWithPublicError 构造包含不可信公开错误字段的远端终态夹具。
func remoteTerminalWithPublicError(code string) turndto.TurnTerminalV2 {
	return turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "event-public-error",
		ThreadID:      "thread-1",
		TurnID:        "turn-1",
		Outcome:       "failed",
		PublicError: &turndto.PublicErrorV1{
			Code:            code,
			Title:           remotePublicErrorLeakFixture,
			Message:         remotePublicErrorLeakFixture,
			DiagnosticID:    "diag-remote-1",
			Retryable:       true,
			RecoveryActions: []string{},
		},
		OccurredAt: "2026-07-20T01:02:03Z",
	}
}

// assertSanitizedRemotePublicError 锁定远端边界只输出固定 wire 占位符。
func assertSanitizedRemotePublicError(t *testing.T, got *turndto.PublicErrorV1, wantCode string) {
	t.Helper()
	if got == nil {
		t.Fatal("DecodeRemoteTurnTerminal() public error = nil")
	}
	if got.Code != wantCode {
		t.Fatalf("public error code = %q, want %q", got.Code, wantCode)
	}
	if got.DiagnosticID != "diag-remote-1" {
		t.Fatalf("diagnostic ID = %q, want safe remote ID", got.DiagnosticID)
	}
	if got.Title != remotePublicErrorTitle || got.Message != remotePublicErrorMessage {
		t.Fatalf("wire copy = %#v, want fixed non-display placeholders", got)
	}
	if got.Retryable || len(got.RecoveryActions) != 0 {
		t.Fatalf("unsafe recovery metadata survived sanitization: %#v", got)
	}
	assertNoRemotePublicErrorLeak(t, got.Title)
	assertNoRemotePublicErrorLeak(t, got.Message)
	assertNoRemotePublicErrorLeak(t, got.DiagnosticID)
}

// assertNoRemotePublicErrorLeak 拒绝 token、私有路径和堆栈片段进入公开字段。
func assertNoRemotePublicErrorLeak(t *testing.T, value string) {
	t.Helper()
	if strings.Contains(value, "secret-value") {
		t.Fatalf("remote public error leaked secret: %q", value)
	}
	if strings.Contains(value, "/private/") {
		t.Fatalf("remote public error leaked private path: %q", value)
	}
	if strings.Contains(value, "stack:") {
		t.Fatalf("remote public error leaked stack: %q", value)
	}
}

func TestRemoteTurnTerminalPublishPreservesSanitizedCanonicalPayload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		terminal turndto.TurnTerminalV2
	}{
		{
			name: "failed with public error and partial items",
			terminal: turndto.TurnTerminalV2{
				SchemaVersion: 2,
				EventID:       "event-failed",
				ThreadID:      "thread-1",
				TurnID:        "turn-1",
				Outcome:       "failed",
				PublicError: &turndto.PublicErrorV1{
					Code:            "PROVIDER_FAILED",
					Title:           "Turn failed",
					Message:         "provider unavailable",
					DiagnosticID:    "diag-1",
					Retryable:       false,
					RecoveryActions: []string{"copy_diagnostics"},
				},
				PartialItemIDs: []string{"item-1", "item-2"},
				OccurredAt:     "2026-07-17T01:02:03.456Z",
			},
		},
		{
			name: "accepted user termination",
			terminal: turndto.TurnTerminalV2{
				SchemaVersion:        2,
				EventID:              "event-interrupted",
				ThreadID:             "thread-2",
				TurnID:               "turn-2",
				Outcome:              "interrupted",
				TerminationCause:     "user_request",
				TerminationRequestID: "request-2",
				PartialItemIDs:       []string{"item-3"},
				OccurredAt:           "2026-07-17T02:03:04Z",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded, err := DecodeRemoteTurnTerminal(jsonValueDecoder(t, test.terminal))
			if err != nil {
				t.Fatalf("DecodeRemoteTurnTerminal() error = %v", err)
			}
			projected, err := ProjectRemoteTurnTerminal(decoded, "owner-agent")
			if err != nil {
				t.Fatalf("ProjectRemoteTurnTerminal() error = %v", err)
			}
			var published publishedEvent
			publishTurnTerminal(nil, func(method string, payload any) {
				published = publishedEvent{method: method, payload: payload}
			}, projected)
			if published.method != MethodTurnTerminal {
				t.Fatalf("published method = %q, want %q", published.method, MethodTurnTerminal)
			}
			got, ok := published.payload.(turndto.TurnTerminalV2)
			if !ok {
				t.Fatalf("published payload type = %T, want TurnTerminalV2", published.payload)
			}
			want := test.terminal
			want.PublicError = sanitizeRemotePublicError(test.terminal.PublicError)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("published terminal = %#v, want sanitized input %#v", got, want)
			}
		})
	}
}

func jsonValueDecoder(t *testing.T, value any) RemoteParamDecoder {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal decoder fixture: %v", err)
	}
	return func(target any) error { return json.Unmarshal(raw, target) }
}

func TestBindPublishesRealtimeBridgeEvents(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 6)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	now := time.Unix(1710000000, 0).UTC()
	event.Publish(dispatcher, threaddto.Compacted{EventHeader: shared.EventHeader{Timestamp: now}, ThreadID: "thread-1", Compacted: true})
	event.Publish(dispatcher, uidto.UITokensUpdated{UITurnHeader: shared.UITurnHeader{UIProjectionHeader: shared.UIProjectionHeader{ThreadHeader: shared.ThreadHeader{ThreadID: "thread-1"}, Projection: "thread"}}, TotalTokens: 42, ContextWindowTokens: 128})
	event.Publish(dispatcher, uidto.SkillsChanged{EventHeader: shared.EventHeader{Timestamp: now}, SkillsDir: "/tmp/skills"})
	event.Publish(dispatcher, uidto.UIThreadPatch{ThreadID: "thread-1", Source: "turn/completed", Sequence: 3, Partial: true})
	event.Publish(dispatcher, uidto.UISharedFilesChanged{EventHeader: shared.EventHeader{Timestamp: now}, Path: "scratch/notes.md", Action: "write"})
	event.Publish(dispatcher, uidto.UIMemoryChanged{EventHeader: shared.EventHeader{Timestamp: now}, Action: "upsert"})

	seen := map[string]map[string]any{}
	for range 6 {
		ev := mustReceivePublished(t, got)
		seen[ev.method] = payloadMap(ev.payload)
	}
	if seen[MethodThreadCompacted]["threadId"] != "thread-1" {
		t.Fatalf("thread compacted payload = %#v", seen[MethodThreadCompacted])
	}
	if seen[MethodThreadTokenUsage]["contextWindowTokens"] != 128 {
		t.Fatalf("token usage payload = %#v", seen[MethodThreadTokenUsage])
	}
	if seen[MethodSkillsChanged]["skillsDir"] != "/tmp/skills" {
		t.Fatalf("skills changed payload = %#v", seen[MethodSkillsChanged])
	}
	if seen[MethodUIThreadPatch]["sequence"] != float64(3) || seen[MethodUIThreadPatch]["partial"] != true {
		t.Fatalf("thread patch payload = %#v", seen[MethodUIThreadPatch])
	}
	if seen[MethodUISharedFilesChanged]["path"] != "scratch/notes.md" || seen[MethodUISharedFilesChanged]["action"] != "write" {
		t.Fatalf("shared files changed payload = %#v", seen[MethodUISharedFilesChanged])
	}
	if seen[MethodUIMemoryChanged]["action"] != "upsert" {
		t.Fatalf("memory changed payload = %#v", seen[MethodUIMemoryChanged])
	}
}

func TestBindPublishesUIPromptsChanged(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 1)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	now := time.Unix(1710000000, 0).UTC()
	event.Publish(dispatcher, uidto.UIPromptsChanged{
		EventHeader: shared.EventHeader{Timestamp: now},
		Cwd:         "/repo/a",
		PromptKey:   "main/reviewer",
		Action:      "write",
	})

	ev := mustReceivePublished(t, got)
	if ev.method != MethodUIPromptsChanged {
		t.Fatalf("method = %q, want %q", ev.method, MethodUIPromptsChanged)
	}
	payload := payloadMap(ev.payload)
	if payload["cwd"] != "/repo/a" || payload["promptKey"] != "main/reviewer" || payload["action"] != "write" {
		t.Fatalf("prompts changed payload = %#v", payload)
	}
}

func TestToolCallEndPayloadIncludesPersistFailure(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 6)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	publishRecoveryAndToolSurfaceEvents(dispatcher)

	seen := map[string]map[string]any{}
	for range 6 {
		ev := mustReceivePublished(t, got)
		seen[ev.method] = payloadMap(ev.payload)
	}
	assertRecoveryAndToolSurfacePayloads(t, seen)
}

func TestToolApprovalResolvedPayloadIncludesRequestID(t *testing.T) {
	var resolved tooldto.ToolApprovalResolved
	if err := json.Unmarshal([]byte(`{"request_id":101}`), &resolved); err != nil {
		t.Fatalf("unmarshal resolved approval: %v", err)
	}
	payload := toolApprovalResolvedPayload(resolved)
	if payload["requestId"] != int64(101) {
		t.Fatalf("resolved requestId = %#v, want 101", payload["requestId"])
	}
}

func TestApprovalPayloadSerializersCoverDTOFields(t *testing.T) {
	now := time.Unix(1710000000, 0).UTC()
	header := bindTestToolApprovalHeader(bindTestTurnHeader(now))
	requested := tooldto.ToolApprovalRequested{
		ToolApprovalHeader: header,
		RequestID:          101,
		Reason:             "needs review",
		Kind:               "tool",
	}
	resolved := tooldto.ToolApprovalResolved{
		ToolApprovalHeader: header,
		RequestID:          101,
		Approved:           true,
		Decision:           "accept",
		ReviewedBy:         "user-1",
		Kind:               "tool",
	}
	assertDTOJSONFieldsReachPayload(t, requested, toolApprovalRequestedPayload(requested))
	assertDTOJSONFieldsReachPayload(t, resolved, toolApprovalResolvedPayload(resolved))
}

func assertDTOJSONFieldsReachPayload(t *testing.T, dtoValue any, payload map[string]any) {
	t.Helper()
	raw, err := json.Marshal(dtoValue)
	if err != nil {
		t.Fatalf("marshal DTO: %v", err)
	}
	var dtoFields map[string]any
	if err := json.Unmarshal(raw, &dtoFields); err != nil {
		t.Fatalf("unmarshal DTO fields: %v", err)
	}
	for dtoKey := range dtoFields {
		if dtoKey == "timestamp" {
			continue
		}
		wireKey := snakeJSONKeyToLowerCamel(dtoKey)
		if _, ok := payload[wireKey]; !ok {
			t.Errorf("DTO field %q missing from event payload key %q: %#v", dtoKey, wireKey, payload)
		}
	}
}

func snakeJSONKeyToLowerCamel(key string) string {
	parts := strings.Split(key, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func publishRecoveryAndToolSurfaceEvents(dispatcher *event.Dispatcher) {
	now := time.Unix(1710000000, 0).UTC()
	sessionHeader := bindTestAgentSessionHeader(now)
	turnHeader := bindTestTurnHeader(now)
	event.Publish(dispatcher, agentdto.AgentRecovering{
		AgentSessionHeader: sessionHeader,
		Reason:             "reconnecting",
		Attempt:            2,
	})
	event.Publish(dispatcher, agentdto.AgentFailed{
		AgentSessionHeader: sessionHeader,
		Error:              "boom",
		Recoverable:        true,
	})
	event.Publish(dispatcher, tooldto.ToolCallBegin{
		ToolCallHeader:   bindTestToolCallHeader(turnHeader, "call-1", "search"),
		RequestID:        11,
		ArgumentsPreview: "{}",
	})
	event.Publish(dispatcher, tooldto.ToolCallEnd{
		ToolCallHeader: bindTestToolCallHeader(turnHeader, "call-1", "search"),
		Success:        true,
		PersistFailed:  true,
		PersistError:   "write cache: permission denied",
		ElapsedMS:      25,
	})
	event.Publish(dispatcher, tooldto.ToolApprovalRequested{
		ToolApprovalHeader: bindTestToolApprovalHeader(turnHeader),
		RequestID:          99,
		Reason:             "needs review",
		Kind:               "request_user_input",
	})
	event.Publish(dispatcher, tooldto.ToolApprovalResolved{
		ToolApprovalHeader: bindTestToolApprovalHeader(turnHeader),
		Approved:           true,
		Decision:           "accept",
		Kind:               "request_user_input",
	})
}

func bindTestAgentSessionHeader(now time.Time) shared.AgentSessionHeader {
	return shared.AgentSessionHeader{
		AgentHeader: shared.AgentHeader{
			ThreadHeader: shared.ThreadHeader{
				EventHeader: shared.EventHeader{Timestamp: now},
				ThreadID:    "thread-1",
			},
			AgentID: "agent-1",
		},
		SessionID: "session-1",
	}
}

func bindTestTurnHeader(now time.Time) shared.TurnHeader {
	return shared.TurnHeader{
		AgentHeader:  bindTestAgentSessionHeader(now).AgentHeader,
		TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
	}
}

func bindTestToolCallHeader(turnHeader shared.TurnHeader, callID, toolName string) shared.ToolCallHeader {
	return shared.ToolCallHeader{
		TurnHeader: turnHeader,
		CallID:     callID,
		ToolName:   toolName,
	}
}

func bindTestToolApprovalHeader(turnHeader shared.TurnHeader) shared.ToolApprovalHeader {
	return shared.ToolApprovalHeader{
		ToolCallHeader: bindTestToolCallHeader(turnHeader, "call-2", "shell"),
		SessionScope:   "approval-session-scope",
		ApprovalID:     "approval-1",
	}
}

func assertRecoveryAndToolSurfacePayloads(t *testing.T, seen map[string]map[string]any) {
	t.Helper()
	if seen[MethodAgentRecovering]["attempt"] != 2 {
		t.Fatalf("agent recovering payload = %#v", seen[MethodAgentRecovering])
	}
	if seen[MethodAgentFailed]["recoverable"] != true {
		t.Fatalf("agent failed payload = %#v", seen[MethodAgentFailed])
	}
	if seen[MethodToolCall]["callId"] != "call-1" {
		t.Fatalf("tool call payload = %#v", seen[MethodToolCall])
	}
	if seen[MethodItemCompleted]["elapsedMs"] != int64(25) {
		t.Fatalf("tool completion payload = %#v", seen[MethodItemCompleted])
	}
	if seen[MethodItemCompleted]["persistFailed"] != true || seen[MethodItemCompleted]["persistError"] != "write cache: permission denied" {
		t.Fatalf("tool completion payload = %#v, want persist failure", seen[MethodItemCompleted])
	}
	assertApprovalSurfacePayloads(t, seen)
}

func assertApprovalSurfacePayloads(t *testing.T, seen map[string]map[string]any) {
	t.Helper()
	if seen[MethodCommandApprovalRequested]["requestId"] != int64(99) {
		t.Fatalf("approval requested payload = %#v", seen[MethodCommandApprovalRequested])
	}
	if seen[MethodCommandApprovalRequested]["sessionScope"] != "approval-session-scope" {
		t.Fatalf("approval requested payload = %#v, want sessionScope", seen[MethodCommandApprovalRequested])
	}
	if seen[MethodApprovalResolved]["decision"] != "accept" {
		t.Fatalf("approval resolved payload = %#v", seen[MethodApprovalResolved])
	}
	if seen[MethodApprovalResolved]["sessionScope"] != "approval-session-scope" {
		t.Fatalf("approval resolved payload = %#v, want sessionScope", seen[MethodApprovalResolved])
	}
}

func TestBindPublishesTurnOutputMethods(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 3)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	header := shared.TurnHeader{
		AgentHeader:  shared.AgentHeader{ThreadHeader: shared.ThreadHeader{ThreadID: "thread-1"}, AgentID: "agent-1"},
		TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
	}
	event.Publish(dispatcher, turndto.TurnOutputDelta{TurnHeader: header, Stream: "message", Delta: "hello"})
	event.Publish(dispatcher, turndto.TurnOutputDelta{TurnHeader: header, Stream: "reasoning", Delta: "thinking"})
	event.Publish(dispatcher, turndto.TurnOutputDelta{TurnHeader: header, Stream: "stdout", Delta: "out"})

	gotMethods := []string{
		mustReceivePublished(t, got).method,
		mustReceivePublished(t, got).method,
		mustReceivePublished(t, got).method,
	}
	wantMethods := []string{MethodAgentMessageDelta, MethodReasoningTextDelta, MethodCommandOutputDelta}
	for i, want := range wantMethods {
		if gotMethods[i] != want {
			t.Fatalf("method[%d] = %q, want %q; all=%#v", i, gotMethods[i], want, gotMethods)
		}
	}
}

func TestBindPublishesTaskNodeStatusChanged(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 1)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	event.Publish(dispatcher, taskdto.TaskNodeStatusChanged{
		TaskNodeHeader: shared.TaskNodeHeader{
			TaskDAGHeader: shared.TaskDAGHeader{
				DAGHeader: shared.DAGHeader{
					EventHeader: shared.EventHeader{Timestamp: time.Unix(1710000000, 0).UTC()},
					DagKey:      "dag-1",
				},
			},
			NodeKey: "node-1",
			RunID:   77,
			RunKey:  "dag-1#run-77",
		},
		AssignedTo:     "agent-x",
		NewStatus:      "done",
		OldStatus:      "running",
		ActiveTurnID:   "turn-7",
		ActiveWakeupID: 42,
	})

	ev := mustReceivePublished(t, got)
	if ev.method != MethodTaskNodeStatusChanged {
		t.Fatalf("method = %q, want %q", ev.method, MethodTaskNodeStatusChanged)
	}
	payload, ok := ev.payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map", ev.payload)
	}
	want := map[string]any{
		"dag_key":          "dag-1",
		"node_key":         "node-1",
		"run_id":           int64(77),
		"run_key":          "dag-1#run-77",
		"new_status":       "done",
		"old_status":       "running",
		"assigned_to":      "agent-x",
		"active_turn_id":   "turn-7",
		"active_wakeup_id": int64(42),
	}
	for key, wantVal := range want {
		if payload[key] != wantVal {
			t.Fatalf("payload[%q] = %v, want %v", key, payload[key], wantVal)
		}
	}
}

func cancelAll(cancels []context.CancelFunc) {
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func mustReceivePublished(t *testing.T, ch <-chan publishedEvent) publishedEvent {
	t.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("expected published event")
		return publishedEvent{}
	}
}

func TestBindPublishesCronJobRunStateChanged(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 1)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	now := time.Unix(1710000000, 0).UTC()
	event.Publish(dispatcher, crondto.JobRunStateChanged{
		EventHeader: shared.EventHeader{Timestamp: now},
		JobID:       "job-1",
		RunID:       "run-1",
		Status:      "running",
		TurnID:      "turn-1",
		ScheduledAt: now,
	})

	ev := mustReceivePublished(t, got)
	if ev.method != MethodCronJobRunStateChanged {
		t.Fatalf("method = %q, want %q", ev.method, MethodCronJobRunStateChanged)
	}
	payload := payloadMap(ev.payload)
	if payload["job_id"] != "job-1" || payload["run_id"] != "run-1" {
		t.Fatalf("payload ids = %#v", payload)
	}
	if payload["status"] != "running" || payload["turn_id"] != "turn-1" {
		t.Fatalf("payload status/turn_id = %#v", payload)
	}
	if _, ok := payload["scheduled_at"].(string); !ok {
		t.Fatalf("scheduled_at missing or non-string in %#v", payload)
	}
}
