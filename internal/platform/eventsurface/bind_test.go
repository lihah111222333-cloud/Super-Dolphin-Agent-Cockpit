package eventsurface

import (
	"context"
	"encoding/json"
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

	got := make(chan publishedEvent, 7)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	publishRecoveryAndToolSurfaceEvents(dispatcher)

	seen := map[string]map[string]any{}
	for range 7 {
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
	event.Publish(dispatcher, turndto.TurnInterrupted{
		TurnHeader: turnHeader,
		Reason:     "cancelled",
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
	if seen[MethodTurnInterrupted]["turnId"] != "turn-1" {
		t.Fatalf("turn interrupted payload = %#v", seen[MethodTurnInterrupted])
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
