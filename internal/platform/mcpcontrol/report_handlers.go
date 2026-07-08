package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// RuntimeReportHandler 处理 MCP 控制面收到的 runtime report，并负责把运行时端口等状态持久化。
type RuntimeReportHandler interface {
	HandleRuntimeReport(ctx context.Context, instance *ToolInstance, report dto.RuntimeReport, req dto.ReportRequest) (dto.ReportResponse, error)
}

// CompletionReportHandler 处理 completion report，并把任务完成事件写回 orchestration。
type CompletionReportHandler interface {
	HandleCompletionReport(ctx context.Context, instance *ToolInstance, report dto.CompletionReport, req dto.ReportRequest) (dto.ReportResponse, error)
}

// RuntimeReportUpdater 是默认 runtime report handler 需要的最小持久化端口。
type RuntimeReportUpdater interface {
	UpdateRuntime(ctx context.Context, report contract.RuntimeReport) error
}

// ReportEventHandler 是默认 completion report handler 需要的最小事件端口。
type ReportEventHandler interface {
	HandleReportEvent(ctx context.Context, event contract.ReportEvent) (contract.ReportEventResult, error)
}

// handleReport 先为 report_id 做幂等预留，再按报告类型分发；成功结果会缓存供重复提交复用。
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

// dispatchReport 根据 envelope 类型调用对应处理器，progress/diagnostic 当前显式拒绝。
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

// defaultRuntimeReportHandler 将 runtime report 写入 orchestration 服务。
type defaultRuntimeReportHandler struct {
	updates RuntimeReportUpdater
}

// HandleRuntimeReport 持久化 agent 运行时端口和 provider，缺少 orchestration 时直接报能力不匹配。
func (h defaultRuntimeReportHandler) HandleRuntimeReport(
	ctx context.Context,
	instance *ToolInstance,
	report dto.RuntimeReport,
	_ dto.ReportRequest,
) (dto.ReportResponse, error) {
	if h.updates == nil {
		return dto.ReportResponse{}, errCapabilityMismatch("runtime report orchestration service is not configured")
	}
	if err := h.updates.UpdateRuntime(ctx, contract.RuntimeReport{
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

// defaultCompletionReportHandler 将 completion report 转成 orchestration report event。
type defaultCompletionReportHandler struct {
	events ReportEventHandler
}

// HandleCompletionReport 持久化 completion 事件，并保留原始 metadata 作为事件数据。
func (h defaultCompletionReportHandler) HandleCompletionReport(
	ctx context.Context,
	instance *ToolInstance,
	report dto.CompletionReport,
	req dto.ReportRequest,
) (dto.ReportResponse, error) {
	if h.events == nil {
		return dto.ReportResponse{}, errCapabilityMismatch("completion report orchestration service is not configured")
	}
	eventType := shared.FirstNonEmpty(report.Status, dto.ReportVariantCompletion)
	_, err := h.events.HandleReportEvent(ctx, contract.ReportEvent{
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

// reportVariant 从显式 type 或非空 report 分支推断报告类型。
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

// completionEventData 优先透传 completion metadata，缺失时回退为完整 envelope 快照。
func completionEventData(report dto.ReportEnvelope) json.RawMessage {
	if report.Completion != nil && len(report.Completion.Metadata) != 0 {
		return append(json.RawMessage(nil), report.Completion.Metadata...)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil
	}
	return raw
}
