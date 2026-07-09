package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestDashboardLogDetailReturnsSanitizedRawAndExtra(t *testing.T) {
	db := &logDetailQueryStub{rows: []map[string]any{{
		"id":             int64(42),
		"ts":             time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC),
		"level":          "ERROR",
		"logger":         "provider",
		"message":        "request failed",
		"raw":            `{"token":"sk-abcdefghijklmnopqrstuvwxyz","message":"request failed"}`,
		"source":         "provider",
		"component":      "codex",
		"agent_id":       "agent-1",
		"thread_id":      "thread-1",
		"trace_id":       "trace-1",
		"span_id":        "span-1",
		"parent_span_id": "parent-1",
		"event_type":     "provider.error",
		"tool_name":      "shell",
		"duration_ms":    int64(123),
		"extra":          `{"password":"hunter2","safe":"visible"}`,
	}}}
	svc := NewService(nil, nil, nil, nil, nil, nil, nil, db, nil, nil, nil, nil)
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewDashboardHandlers(svc).Handlers)

	result, err := server.Dispatch(context.Background(), "dashboard/logDetail", json.RawMessage(`{"source":"system","id":42}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	assertSanitizedLogDetailResponse(t, result)
	assertLogDetailQuery(t, db)
}

type logDetailQueryStub struct {
	rows  []map[string]any
	query string
	args  []any
	calls int
}

func (s *logDetailQueryStub) Query(_ context.Context, query string, args ...any) ([]map[string]any, error) {
	s.calls++
	s.query = query
	s.args = append([]any(nil), args...)
	return s.rows, nil
}

func assertSanitizedLogDetailResponse(t *testing.T, result json.RawMessage) {
	t.Helper()
	encoded := string(result)
	if strings.Contains(encoded, "sk-") || strings.Contains(encoded, "hunter2") {
		t.Fatalf("log detail leaked secret: %s", encoded)
	}
	if !strings.Contains(encoded, "[REDACTED]") || !strings.Contains(encoded, "visible") {
		t.Fatalf("log detail = %s, want redacted secret and safe extra", encoded)
	}
	var response logDetailResponse
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	assertLogDetailResponsePopulated(t, response)
	assertLogDetailTraceFields(t, response.Detail)
}

func assertLogDetailResponsePopulated(t *testing.T, response logDetailResponse) {
	t.Helper()
	if response.Detail == nil || response.Detail.ID != 42 || response.Detail.Raw == "" || len(response.Detail.Extra) == 0 {
		t.Fatalf("log detail response = %#v, want populated detail", response)
	}
}

func assertLogDetailTraceFields(t *testing.T, detail *LogDetail) {
	t.Helper()
	if detail.TraceID != "trace-1" || detail.SpanID != "span-1" || detail.ParentSpanID != "parent-1" {
		t.Fatalf("log detail trace fields = trace:%q span:%q parent:%q", detail.TraceID, detail.SpanID, detail.ParentSpanID)
	}
}

func assertLogDetailQuery(t *testing.T, db *logDetailQueryStub) {
	t.Helper()
	if db.calls != 1 || !strings.Contains(strings.ToLower(db.query), "from system_logs") {
		t.Fatalf("db query calls/query = %d/%q, want system_logs detail query", db.calls, db.query)
	}
}
