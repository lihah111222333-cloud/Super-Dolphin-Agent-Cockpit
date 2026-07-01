package dashboard

import (
	"encoding/json"
	"testing"
	"time"
)

func TestLogsParamsToFilterIncludesTraceSpanAliases(t *testing.T) {
	filter := (logsParams{
		TraceID:           " trace-camel ",
		TraceIDSnake:      "trace-snake",
		SpanID:            " span-camel ",
		SpanIDSnake:       "span-snake",
		ParentSpanID:      " parent-camel ",
		ParentSpanIDSnake: "parent-snake",
	}).ToFilter(logSourceSystem)

	if filter.TraceID != "trace-camel" || filter.SpanID != "span-camel" || filter.ParentSpanID != "parent-camel" {
		t.Fatalf("ToFilter() trace fields = trace:%q span:%q parent:%q", filter.TraceID, filter.SpanID, filter.ParentSpanID)
	}
}

func TestMapLogEntryIncludesTraceSpanFields(t *testing.T) {
	duration := int32(17)
	entry := mapLogEntry(SystemLog{
		ID:           5,
		Ts:           time.Date(2026, 7, 1, 1, 2, 3, 0, time.UTC),
		Level:        "warn",
		Logger:       "mcp-control",
		Message:      "trace log",
		Raw:          "raw",
		Component:    "mcp-lsp",
		AgentID:      "agent-1",
		ThreadID:     "thread-1",
		TraceID:      "trace-1",
		SpanID:       "span-1",
		ParentSpanID: "parent-1",
		EventType:    "ctl/log",
		ToolName:     "definition",
		DurationMs:   &duration,
		Extra:        json.RawMessage(`{"ok":true}`),
	}, logSourceSystem)

	if entry.TraceID != "trace-1" || entry.SpanID != "span-1" || entry.ParentSpanID != "parent-1" {
		t.Fatalf("mapLogEntry() trace fields = trace:%q span:%q parent:%q", entry.TraceID, entry.SpanID, entry.ParentSpanID)
	}
	if !matchesLogFilter(entry, LogFilter{TraceID: "trace-1", SpanID: "span-1", ParentSpanID: "parent-1"}) {
		t.Fatalf("matchesLogFilter() missed trace/span fields: %+v", entry)
	}
	if matchesLogFilter(entry, LogFilter{ParentSpanID: "other-parent"}) {
		t.Fatalf("matchesLogFilter() matched wrong parent span: %+v", entry)
	}
}
