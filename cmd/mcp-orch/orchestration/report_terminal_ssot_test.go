package orchestration

import (
	"context"
	"testing"
)

// TestTerminalPublicReportUsesHeadVersionFence 防止 terminal SSOT 每轮把 report_seq 重置为 1。
func TestTerminalPublicReportUsesHeadVersionFence(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	if handled, err := svc.CommitTurnCompleted(context.Background(), terminalOutcomeEvent(true)); err != nil || !handled {
		t.Fatalf("CommitTurnCompleted() = (%v, %v), want handled nil", handled, err)
	}
	committed, err := svc.terminalOutcomes.GetPublicTerminalOutcome(context.Background(), agent.id)
	if err != nil {
		t.Fatalf("GetPublicTerminalOutcome() error = %v", err)
	}
	report, err := svc.GetReport(context.Background(), agent.id)
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if report.ReportSeq <= 0 || report.ReportSeq != int64(committed.Identity.HeadVersion) {
		t.Fatalf("GetReport().ReportSeq = %d, want canonical head fence %d", report.ReportSeq, committed.Identity.HeadVersion)
	}
}
