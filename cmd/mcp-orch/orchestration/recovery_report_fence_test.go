package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

// TestRecoveryReportPreservesCanonicalFence 验证durable终态切换到恢复后的runtime读端时序号与正文不倒退。
func TestRecoveryReportPreservesCanonicalFence(t *testing.T) {
	svc, _, agent, backend := processExitAutoRecoveryFixture(t)
	ctx := context.Background()
	if err := svc.SubmitTurn(ctx, TurnSubmission{AgentID: agent.id, ThreadID: agent.threadID, Inputs: []shareddto.InputItem{{Type: "text", Content: "first"}}}); err != nil {
		t.Fatal(err)
	}
	commitProcessExitRecoveryTurn(t, svc, agent, true)
	first := recoveryFenceReport(t, svc, agent.id)
	if err := svc.ProcessTerminalOutcomeOutbox(ctx, "first-report", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	backend.running, backend.reserved = false, false
	if handled, err := svc.CommitProcessExitTerminalLocked(ctx, agent, 1, errors.New("provider crashed")); err != nil || !handled {
		t.Fatalf("process terminal: handled=%v err=%v", handled, err)
	}
	failed := recoveryFenceReport(t, svc, agent.id)
	if failed.ReportSeq <= first.ReportSeq {
		t.Fatal("process terminal did not advance canonical report fence")
	}
	if err := svc.Recover(ctx, agent.id); err != nil {
		t.Fatal(err)
	}
	assertCanonicalRecoveryReport(t, recoveryFenceReport(t, svc, agent.id), failed)
	if err := svc.ProcessTerminalOutcomeOutbox(ctx, "old-process", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	assertCanonicalRecoveryReport(t, recoveryFenceReport(t, svc, agent.id), failed)
}

// recoveryFenceReport 读取真实public路径而非直接比较fixture内存计数。
func recoveryFenceReport(t *testing.T, svc *service, agentID string) AgentReportResult {
	t.Helper()
	report, err := svc.GetReport(context.Background(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// assertCanonicalRecoveryReport active head不能生成新报告，也不能让旧报告的公开序号和时间回退。
func assertCanonicalRecoveryReport(t *testing.T, actual, expected AgentReportResult) {
	t.Helper()
	if actual.ReportSeq != expected.ReportSeq || actual.Report != expected.Report || !actual.UpdatedAt.Equal(expected.UpdatedAt) {
		t.Fatalf("canonical report drift after recovery: seq=%d want=%d updated=%v want=%v", actual.ReportSeq, expected.ReportSeq, actual.UpdatedAt, expected.UpdatedAt)
	}
}
