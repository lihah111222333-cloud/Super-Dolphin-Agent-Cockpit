package codexapp

import (
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// newRuntimeReportSessionForTest 构造只覆盖 finishStartedSession 所需字段的轻量 session。
func newRuntimeReportSessionForTest(agentID, serverURL string) *session {
	return &session{
		agentID: agentID,
		transport: &transport{
			serverURL: serverURL,
		},
		runtimeConfig: map[string]any{},
	}
}

// finishRuntimeReportSession 走生产 finishStartedSession 路径，避免只测 reportRuntime 私有细节。
func finishRuntimeReportSession(d *driver, s *session) {
	d.finishStartedSession(s, dto.StartSessionRequest{CWD: "/tmp/runtime-report-cwd"}, startResult{
		threadID: "thread-1",
		model:    "gpt-5",
	})
}

// assertRuntimeReportFromSessionURL 校验 runtimeConfig 和外部 reporter 都使用当前 session transport URL。
func assertRuntimeReportFromSessionURL(t *testing.T, reporter *stubRuntimeReporter, s *session, wantPort int) {
	t.Helper()
	if reporter.calls != 1 {
		t.Fatalf("ReportRuntime() calls = %d, want 1", reporter.calls)
	}
	if reporter.last.Port != wantPort {
		t.Fatalf("reported Port = %d, want %d from session transport URL", reporter.last.Port, wantPort)
	}
	if reporter.last.AgentID != "agent-1" {
		t.Fatalf("reported AgentID = %q, want agent-1", reporter.last.AgentID)
	}
	if got := s.RuntimeConfigSnapshot()["port"]; got != float64(wantPort) {
		t.Fatalf("runtimeConfig port = %#v (%T), want %d", got, got, wantPort)
	}
}

// TestFinishStartedSessionReportsRuntimeFromSessionURL 防止 pool 会话因 driver.serverURL 为空上报 0 端口。
func TestFinishStartedSessionReportsRuntimeFromSessionURL(t *testing.T) {
	reporter := &stubRuntimeReporter{}
	d := &driver{reporter: reporter}
	s := newRuntimeReportSessionForTest(" agent-1 ", " ws://127.0.0.1:4567/ws ")

	finishRuntimeReportSession(d, s)

	assertRuntimeReportFromSessionURL(t, reporter, s, 4567)
}
