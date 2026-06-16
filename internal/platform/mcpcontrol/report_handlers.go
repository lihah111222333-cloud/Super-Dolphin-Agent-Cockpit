package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

// RuntimeReportHandler handles runtime reports received through the MCP control plane.
type RuntimeReportHandler interface {
	HandleRuntimeReport(ctx context.Context, instance *ToolInstance, report dto.RuntimeReport, req dto.ReportRequest) (dto.ReportResponse, error)
}

// CompletionReportHandler handles completion reports received through the MCP control plane.
type CompletionReportHandler interface {
	HandleCompletionReport(ctx context.Context, instance *ToolInstance, report dto.CompletionReport, req dto.ReportRequest) (dto.ReportResponse, error)
}

func handleReport(
	ctx context.Context,
	registry *ToolRegistry,
	runtimeReports RuntimeReportHandler,
	completionReports CompletionReportHandler,
	req dto.ReportRequest,
) (dto.ReportResponse, error) {
	return withResolvedInstance(registry, req, func(req dto.ReportRequest) dto.LeaseKey {
		return dto.LeaseKey{InstanceID: req.InstanceID, Generation: req.Generation}
	}, func(instance *ToolInstance) (dto.ReportResponse, error) {
		cached, fingerprint, err := registry.reserveReport(instance.Lease, req)
		if err != nil {
			return dto.ReportResponse{}, err
		}
		if cached != nil {
			return *cached, nil
		}

		response, handleErr := dispatchReport(ctx, instance, runtimeReports, completionReports, req)
		registry.completeReport(instance.Lease, req.ReportID, fingerprint, response, handleErr)
		return response, handleErr
	})
}

// dispatchReport 派发report。
func dispatchReport(
	ctx context.Context,
	instance *ToolInstance,
	runtimeReports RuntimeReportHandler,
	completionReports CompletionReportHandler,
	req dto.ReportRequest,
) (dto.ReportResponse, error) {
	switch reportVariant(req.Report) {
	case dto.ReportVariantRuntime:
		if runtimeReports == nil {
			return dto.ReportResponse{}, errCapabilityMismatch("runtime report handler is not configured")
		}
		report := dto.RuntimeReport{}
		if req.Report.Runtime != nil {
			report = *req.Report.Runtime
		}
		return runtimeReports.HandleRuntimeReport(ctx, instance, report, req)
	case dto.ReportVariantCompletion:
		if completionReports == nil {
			return dto.ReportResponse{}, errCapabilityMismatch("completion report handler is not configured")
		}
		report := dto.CompletionReport{}
		if req.Report.Completion != nil {
			report = *req.Report.Completion
		}
		return completionReports.HandleCompletionReport(ctx, instance, report, req)
	case dto.ReportVariantProgress, dto.ReportVariantDiagnostic:
		return dto.ReportResponse{}, errInvalidParams("unsupported report variant %q", reportVariant(req.Report))
	default:
		return dto.ReportResponse{}, errInvalidParams("unsupported report variant %q", reportVariant(req.Report))
	}
}

type defaultRuntimeReportHandler struct {
	orchestration contract.OrchestrationService
}

// HandleRuntimeReport 处理运行时report。
func (h defaultRuntimeReportHandler) HandleRuntimeReport(
	ctx context.Context,
	instance *ToolInstance,
	report dto.RuntimeReport,
	_ dto.ReportRequest,
) (dto.ReportResponse, error) {
	if h.orchestration == nil {
		return dto.ReportResponse{}, errCapabilityMismatch("runtime report orchestration service is not configured")
	}
	if err := h.orchestration.UpdateRuntime(ctx, contract.RuntimeReport{
		AgentID:  instance.AgentID,
		Port:     report.Port,
		Provider: report.Provider,
	}); err != nil {
		return dto.ReportResponse{}, errReportConflict("failed to persist runtime report: %v", err)
	}
	return dto.ReportResponse{
		Accepted:        true,
		PersistedAt:     time.Now().UnixMilli(),
		CanonicalStatus: dto.ReportVariantRuntime,
		AppliedVariant:  dto.ReportVariantRuntime,
	}, nil
}

type defaultCompletionReportHandler struct {
	orchestration contract.OrchestrationService
}

// HandleCompletionReport 处理补全report。
func (h defaultCompletionReportHandler) HandleCompletionReport(
	ctx context.Context,
	instance *ToolInstance,
	report dto.CompletionReport,
	req dto.ReportRequest,
) (dto.ReportResponse, error) {
	if h.orchestration == nil {
		return dto.ReportResponse{}, errCapabilityMismatch("completion report orchestration service is not configured")
	}
	eventType := shared.FirstNonEmpty(report.Status, dto.ReportVariantCompletion)
	_, err := h.orchestration.HandleReportEvent(ctx, contract.ReportEvent{
		AgentID:   instance.AgentID,
		Report:    shared.FirstNonEmpty(report.Report, report.Status),
		EventType: eventType,
		EventData: completionEventData(req.Report),
	})
	if err != nil {
		return dto.ReportResponse{}, errReportConflict("failed to persist completion report: %v", err)
	}
	return dto.ReportResponse{
		Accepted:        true,
		PersistedAt:     time.Now().UnixMilli(),
		CanonicalStatus: eventType,
		AppliedVariant:  dto.ReportVariantCompletion,
	}, nil
}

func reportVariant(report dto.ReportEnvelope) string {
	if variant := strings.TrimSpace(report.Type); variant != "" {
		return variant
	}
	switch {
	case report.Runtime != nil:
		return dto.ReportVariantRuntime
	case report.Completion != nil:
		return dto.ReportVariantCompletion
	default:
		return ""
	}
}

func completionEventData(report dto.ReportEnvelope) json.RawMessage {
	if report.Completion != nil && len(report.Completion.Metadata) != 0 {
		return append(json.RawMessage(nil), report.Completion.Metadata...)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}
