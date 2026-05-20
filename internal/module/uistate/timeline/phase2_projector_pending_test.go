package timeline_test

import (
	"encoding/json"
	"testing"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
	"github.com/kelindar/event"
)

func TestItemStarted_CommandKind(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	event.Publish(dispatcher, turndto.ItemStarted{
		TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
		ItemType:   "command",
		Command:    "echo hi",
		CallID:     "call-command",
	})

	waitForCondition(t, func() bool { return len(svc.GetByThread("t1")) == 1 }, "expected command timeline item")
	items := svc.GetByThread("t1")
	if got := items[0].Kind; got != "command" {
		t.Fatalf("items[0].Kind = %q, want %q", got, "command")
	}
}

func TestItemStarted_FileKind(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	event.Publish(dispatcher, turndto.ItemStarted{
		TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
		ItemType:   "file",
		File:       "main.go",
		CallID:     "call-file",
	})

	waitForCondition(t, func() bool { return len(svc.GetByThread("t1")) == 1 }, "expected file timeline item")
	items := svc.GetByThread("t1")
	if got := items[0].Kind; got != "file" {
		t.Fatalf("items[0].Kind = %q, want %q", got, "file")
	}
}

func TestItemStarted_CommandExecutionAliasKind(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	event.Publish(dispatcher, turndto.ItemStarted{
		TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
		ItemType:   "command_execution",
		CallID:     "call-command-exec",
	})

	waitForCondition(t, func() bool { return len(svc.GetByThread("t1")) == 1 }, "expected aliased command timeline item")
	if got := svc.GetByThread("t1")[0].Kind; got != "command" {
		t.Fatalf("items[0].Kind = %q, want %q", got, "command")
	}
}

func TestItemStarted_UnknownKindFallsBackToCommand(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	event.Publish(dispatcher, turndto.ItemStarted{
		TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
		ItemType:   "request_user_input",
		CallID:     "call-unknown-kind",
	})

	waitForCondition(t, func() bool { return len(svc.GetByThread("t1")) == 1 }, "expected fallback command timeline item")
	if got := svc.GetByThread("t1")[0].Kind; got != "command" {
		t.Fatalf("items[0].Kind = %q, want %q", got, "command")
	}
}

func TestItemCompleted_FileChangeMarksSaved(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	begin := turndto.ItemStarted{
		TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
		ItemType:   "file_change",
		CallID:     "call-file-change",
		File:       "main.go",
	}
	end := turndto.ItemCompleted{
		TurnHeader: begin.TurnHeader,
		ItemType:   begin.ItemType,
		CallID:     begin.CallID,
		File:       begin.File,
		Success:    true,
	}

	event.Publish(dispatcher, begin)
	event.Publish(dispatcher, end)

	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Kind == "file" && items[0].Status == "saved"
	}, "expected file change completion to map to saved status")
}

func TestToolCallBegin_ToolKind(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	event.Publish(dispatcher, tooldto.ToolCallBegin{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
			CallID:     "call-tool",
			ToolName:   "shell",
		},
		RequestID: 7,
	})

	waitForCondition(t, func() bool { return len(svc.GetByThread("t1")) == 1 }, "expected tool timeline item")
	items := svc.GetByThread("t1")
	if got := items[0].Kind; got != "tool" {
		t.Fatalf("items[0].Kind = %q, want %q", got, "tool")
	}
}

func TestApprovalRequested_ApprovalKind(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	event.Publish(dispatcher, tooldto.ToolApprovalRequested{
		ToolApprovalHeader: shared.ToolApprovalHeader{
			ToolCallHeader: shared.ToolCallHeader{
				TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
				CallID:     "call-approval",
				ToolName:   "shell",
			},
			ApprovalID: "approval-1",
		},
		Kind: "request_user_input",
	})

	waitForCondition(t, func() bool { return len(svc.GetByThread("t1")) == 1 }, "expected approval timeline item")
	items := svc.GetByThread("t1")
	if got := items[0].Kind; got != "approval" {
		t.Fatalf("items[0].Kind = %q, want %q", got, "approval")
	}
}

func TestToolCallEnd_SetsElapsedMSAndDone(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	begin := tooldto.ToolCallBegin{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
			CallID:     "call-tool-end",
			ToolName:   "shell",
		},
		RequestID: 9,
	}
	end := tooldto.ToolCallEnd{
		ToolCallHeader: begin.ToolCallHeader,
		Success:        true,
		ElapsedMS:      321,
	}

	event.Publish(dispatcher, begin)
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Kind == "tool"
	}, "expected tool item before tool call completion")
	event.Publish(dispatcher, end)

	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Status == "completed" && items[0].Done && items[0].ElapsedMS != nil
	}, "expected tool call item to expose done flag")
	item := svc.GetByThread("t1")[0]
	if item.ElapsedMS == nil || *item.ElapsedMS != 321 {
		t.Fatalf("item.ElapsedMS = %v, want 321", item.ElapsedMS)
	}
	if !item.Done {
		t.Fatal("item.Done = false, want true")
	}
}

func TestToolCallEnd_FailedStatus(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	begin := tooldto.ToolCallBegin{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
			CallID:     "call-tool-failed",
			ToolName:   "shell",
		},
	}
	end := tooldto.ToolCallEnd{
		ToolCallHeader: begin.ToolCallHeader,
		Success:        false,
		Error:          "boom",
	}

	event.Publish(dispatcher, begin)
	event.Publish(dispatcher, end)

	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Status == "failed"
	}, "expected failed tool call status")
}

func TestToolCallEnd_SetsPreview(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	const raw = "preview:" + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	want := raw[:200]

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	begin := tooldto.ToolCallBegin{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
			CallID:     "call-preview",
			ToolName:   "shell",
		},
	}
	end := tooldto.ToolCallEnd{
		ToolCallHeader: begin.ToolCallHeader,
		Success:        true,
		Result:         raw,
		ElapsedMS:      88,
	}

	event.Publish(dispatcher, begin)
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Kind == "tool"
	}, "expected tool item before preview update")
	event.Publish(dispatcher, end)

	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Preview != ""
	}, "expected tool call preview to be populated")
	item := svc.GetByThread("t1")[0]
	if got := item.Preview; got != want {
		t.Fatalf("item.Preview = %q, want %q", got, want)
	}
}

func TestToolCallEnd_FailedNullResultUsesErrorPreview(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	begin := tooldto.ToolCallBegin{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
			CallID:     "call-null-result-failed",
			ToolName:   "file",
		},
	}
	end := tooldto.ToolCallEnd{
		ToolCallHeader: begin.ToolCallHeader,
		Success:        false,
		Result:         "null",
		Error:          "读取文件失败：missing path",
	}

	event.Publish(dispatcher, begin)
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Kind == "tool"
	}, "expected tool item before failed completion")
	event.Publish(dispatcher, end)

	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Status == "failed" && items[0].Done
	}, "expected failed tool completion")
	item := svc.GetByThread("t1")[0]
	if got := item.Preview; got != end.Error {
		t.Fatalf("item.Preview = %q, want %q", got, end.Error)
	}
}

func TestToolCallEnd_FallbackFailedNullResultUsesErrorPreview(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	end := tooldto.ToolCallEnd{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
			CallID:     "call-null-result-fallback",
			ToolName:   "file",
		},
		Success: false,
		Result:  "null",
		Error:   "读取文件失败：missing path",
	}

	event.Publish(dispatcher, end)

	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Kind == "tool" && items[0].Status == "failed" && items[0].Done
	}, "expected orphan failed completion to append fallback tool item")
	item := svc.GetByThread("t1")[0]
	if got := item.Preview; got != end.Error {
		t.Fatalf("item.Preview = %q, want %q", got, end.Error)
	}
}

func TestUpdateByCallID_MergesFields(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc := timeline.New(nil, nil, 10)
	item := timeline.Item{
		ID:        "item-merge",
		Kind:      "tool",
		Status:    "running",
		CallID:    "call-merge",
		RequestID: 11,
		Text:      "before",
	}
	setTimelineItemField(&item, "Preview", "before-preview")
	setTimelineItemField(&item, "Done", false)
	svc.Append("t1", "agent-1", item)

	updated := svc.UpdateByCallID("t1", "agent-1", "call-merge", func(current *timeline.Item) {
		current.Status = "completed"
		current.Text = "after"
		setTimelineItemField(current, "Preview", "after-preview")
		setTimelineItemField(current, "Done", true)
	})
	if !updated {
		t.Fatal("UpdateByCallID() = false, want true")
	}

	items := svc.GetByThread("t1")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if got := items[0].Status; got != "completed" {
		t.Fatalf("items[0].Status = %q, want %q", got, "completed")
	}
	if got := items[0].Text; got != "after" {
		t.Fatalf("items[0].Text = %q, want %q", got, "after")
	}
	if got := itemStringField(items[0], "Preview"); got != "after-preview" {
		t.Fatalf("items[0].Preview = %q, want %q", got, "after-preview")
	}
	if !itemBoolField(items[0], "Done") {
		t.Fatal("items[0].Done = false, want true")
	}
}

func TestPlanDelta_PlanKind(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	event.Publish(dispatcher, turndto.PlanDelta{
		TurnHeader: phase2TurnHeader("t1", "agent-1", "turn-1"),
		Delta:      "step 1",
		Payload:    json.RawMessage(`{"status":"in_progress"}`),
	})

	waitForCondition(t, func() bool { return len(svc.GetByThread("t1")) == 1 }, "expected plan timeline item")
	items := svc.GetByThread("t1")
	if got := items[0].Kind; got != "plan" {
		t.Fatalf("items[0].Kind = %q, want %q", got, "plan")
	}
}

func TestAgentError_ErrorKind(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	event.Publish(dispatcher, agentdto.AgentError{
		AgentSessionHeader: phase2AgentSessionHeader("t1", "agent-1"),
		Message:            "boom",
	})

	waitForCondition(t, func() bool { return len(svc.GetByThread("t1")) == 1 }, "expected error timeline item")
	items := svc.GetByThread("t1")
	if got := items[0].Kind; got != "error" {
		t.Fatalf("items[0].Kind = %q, want %q", got, "error")
	}
}

func newPhase2TimelineHarness(t *testing.T) (timeline.Service, *event.Dispatcher, func()) {
	t.Helper()

	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	cleanup := func() {
		for _, cancel := range cancels {
			cancel()
		}
		_ = dispatcher.Close()
	}
	return svc, dispatcher, cleanup
}
