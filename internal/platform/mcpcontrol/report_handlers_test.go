package mcpcontrol

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/creachadair/jrpc2"
)

type runtimeReportUpdaterStub struct {
	got contract.RuntimeReport
	err error
}

func (s *runtimeReportUpdaterStub) UpdateRuntime(_ context.Context, report contract.RuntimeReport) error {
	s.got = report
	return s.err
}

type reportEventHandlerStub struct {
	got contract.ReportEvent
	err error
}

func (s *reportEventHandlerStub) HandleReportEvent(_ context.Context, event contract.ReportEvent) (contract.ReportEventResult, error) {
	s.got = event
	return contract.ReportEventResult{}, s.err
}

func TestDefaultRuntimeReportHandlerUsesNarrowUpdater(t *testing.T) {
	t.Parallel()

	updater := &runtimeReportUpdaterStub{}
	resp, err := (defaultRuntimeReportHandler{updates: updater}).HandleRuntimeReport(
		context.Background(),
		&ToolInstance{AgentID: "agent-runtime"},
		dto.RuntimeReport{Port: 7301, Provider: "codex"},
		dto.ReportRequest{},
	)
	if err != nil {
		t.Fatalf("HandleRuntimeReport() error = %v", err)
	}
	if !resp.Accepted || resp.CanonicalStatus != dto.ReportVariantRuntime || resp.AppliedVariant != dto.ReportVariantRuntime {
		t.Fatalf("HandleRuntimeReport() response = %+v, want accepted runtime", resp)
	}
	if updater.got != (contract.RuntimeReport{AgentID: "agent-runtime", Port: 7301, Provider: "codex"}) {
		t.Fatalf("UpdateRuntime() report = %+v", updater.got)
	}
}

func TestDefaultRuntimeReportHandlerRequiresUpdater(t *testing.T) {
	t.Parallel()

	_, err := (defaultRuntimeReportHandler{}).HandleRuntimeReport(
		context.Background(),
		&ToolInstance{AgentID: "agent-runtime"},
		dto.RuntimeReport{},
		dto.ReportRequest{},
	)
	assertMCPControlErrorCode(t, err, dto.ErrCodeCapabilityMismatch, "runtime report orchestration service is not configured")
}

func TestDefaultRuntimeReportHandlerMapsUpdaterError(t *testing.T) {
	t.Parallel()

	_, err := (defaultRuntimeReportHandler{updates: &runtimeReportUpdaterStub{err: errors.New("write failed")}}).HandleRuntimeReport(
		context.Background(),
		&ToolInstance{AgentID: "agent-runtime"},
		dto.RuntimeReport{},
		dto.ReportRequest{},
	)
	assertMCPControlErrorCode(t, err, dto.ErrCodeReportConflict, "failed to persist runtime report")
}

func TestDefaultCompletionReportHandlerUsesNarrowEventHandler(t *testing.T) {
	t.Parallel()

	events := &reportEventHandlerStub{}
	resp, err := (defaultCompletionReportHandler{events: events}).HandleCompletionReport(
		context.Background(),
		&ToolInstance{AgentID: "agent-complete"},
		dto.CompletionReport{Status: "done", Report: "finished"},
		dto.ReportRequest{Report: dto.ReportEnvelope{
			Completion: &dto.CompletionReport{Metadata: []byte(`{"source":"test"}`)},
		}},
	)
	if err != nil {
		t.Fatalf("HandleCompletionReport() error = %v", err)
	}
	if !resp.Accepted || resp.CanonicalStatus != "done" || resp.AppliedVariant != dto.ReportVariantCompletion {
		t.Fatalf("HandleCompletionReport() response = %+v, want accepted completion", resp)
	}
	if events.got.AgentID != "agent-complete" || events.got.Report != "finished" || events.got.EventType != "done" {
		t.Fatalf("HandleReportEvent() event = %+v", events.got)
	}
	if got := string(events.got.EventData); got != `{"source":"test"}` {
		t.Fatalf("HandleReportEvent() event data = %s", got)
	}
}

func TestDefaultCompletionReportHandlerRequiresEventHandler(t *testing.T) {
	t.Parallel()

	_, err := (defaultCompletionReportHandler{}).HandleCompletionReport(
		context.Background(),
		&ToolInstance{AgentID: "agent-complete"},
		dto.CompletionReport{},
		dto.ReportRequest{},
	)
	assertMCPControlErrorCode(t, err, dto.ErrCodeCapabilityMismatch, "completion report orchestration service is not configured")
}

func TestDefaultCompletionReportHandlerMapsEventHandlerError(t *testing.T) {
	t.Parallel()

	_, err := (defaultCompletionReportHandler{events: &reportEventHandlerStub{err: errors.New("event failed")}}).HandleCompletionReport(
		context.Background(),
		&ToolInstance{AgentID: "agent-complete"},
		dto.CompletionReport{},
		dto.ReportRequest{},
	)
	assertMCPControlErrorCode(t, err, dto.ErrCodeReportConflict, "failed to persist completion report")
}

func assertMCPControlErrorCode(t *testing.T, err error, wantCode int, wantText string) {
	t.Helper()

	if err == nil || !strings.Contains(err.Error(), wantText) {
		t.Fatalf("error = %v, want containing %q", err, wantText)
	}
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %T, want *jrpc2.Error", err)
	}
	if got := int(rpcErr.Code); got != wantCode {
		t.Fatalf("error code = %d, want %d", got, wantCode)
	}
}
