package timeline_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate/timeline"
)

func requirePhase2TimelineShape(t *testing.T) {
	t.Helper()

	itemType := reflect.TypeOf(timeline.Item{})
	missing := make([]string, 0, 4)
	for _, name := range []string{"Tool", "Preview", "ElapsedMS", "Done"} {
		if _, ok := itemType.FieldByName(name); !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("timeline.Item missing Phase 2 fields: %s", strings.Join(missing, ", "))
	}

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()
	probeThreadID := "phase2-probe-thread"
	event.Publish(dispatcher, tooldto.ToolCallBegin{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: phase2TurnHeader(probeThreadID, "phase2-probe-agent", "phase2-probe-turn"),
			CallID:     "phase2-probe-call",
			ToolName:   "shell",
		},
	})
	waitForCondition(t, func() bool { return len(svc.GetByThread(probeThreadID)) == 1 }, "expected phase2 probe tool item")
	items := svc.GetByThread(probeThreadID)
	if got := items[0].Kind; got != "tool" {
		t.Fatalf("timeline projector kind = %q, want %q", got, "tool")
	}
}

func phase2TurnHeader(threadID, agentID, turnID string) shared.TurnHeader {
	return shared.TurnHeader{
		AgentHeader: shared.AgentHeader{
			ThreadHeader: shared.ThreadHeader{
				EventHeader: shared.EventHeader{Timestamp: time.Now()},
				ThreadID:    threadID,
			},
			AgentID: agentID,
		},
		TurnIDHeader: shared.TurnIDHeader{TurnID: turnID},
	}
}

func phase2AgentSessionHeader(threadID, agentID string) shared.AgentSessionHeader {
	return shared.AgentSessionHeader{
		AgentHeader: shared.AgentHeader{
			ThreadHeader: shared.ThreadHeader{
				EventHeader: shared.EventHeader{Timestamp: time.Now()},
				ThreadID:    threadID,
			},
			AgentID: agentID,
		},
		SessionID: threadID,
	}
}

func itemStringField(item timeline.Item, name string) string {
	field := reflect.ValueOf(item).FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return field.String()
}

func itemBoolField(item timeline.Item, name string) bool {
	field := reflect.ValueOf(item).FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.Bool {
		return false
	}
	return field.Bool()
}

func setTimelineItemField(item *timeline.Item, name string, value any) bool {
	if item == nil {
		return false
	}
	field := reflect.ValueOf(item).Elem().FieldByName(name)
	if !field.IsValid() || !field.CanSet() {
		return false
	}
	return assignReflectValue(field, value)
}

func assignReflectValue(field reflect.Value, value any) bool {
	switch field.Kind() {
	case reflect.Bool:
		v, ok := value.(bool)
		if !ok {
			return false
		}
		field.SetBool(v)
		return true
	case reflect.String:
		v, ok := value.(string)
		if !ok {
			return false
		}
		field.SetString(v)
		return true
	}
	return false
}
