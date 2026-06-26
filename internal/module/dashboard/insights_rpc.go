package dashboard

import (
	"context"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type InsightReader = contract.InsightService

// addDashboardInsightHandlers 向 handler.Map 注册 insights 相关 RPC，reader 为 nil 时跳过。
func addDashboardInsightHandlers(handlers handler.Map, reader InsightReader) {
	if reader == nil {
		return
	}
	handlers["dashboard/insights/list"] = platformrpc.StrictHandler(dashboardInsightsListHandler(reader))
	handlers["dashboard/insights/approvals"] = platformrpc.StrictHandler(dashboardInsightsApprovalsHandler(reader))
}

// insightsListParams 是 dashboard/insights/list 的请求参数。
type insightsListParams struct {
	ThreadID string `json:"thread_id,omitempty"`
	Limit    int32  `json:"limit,omitempty"`
}

// insightsApprovalsParams 是 dashboard/insights/approvals 的请求参数。
type insightsApprovalsParams struct {
	ThreadID string `json:"thread_id,omitempty"`
	Limit    int32  `json:"limit,omitempty"`
}

// dashboardInsightsListHandler 返回按 thread 或全局查询的 insight 快照列表。
func dashboardInsightsListHandler(reader InsightReader) func(context.Context, insightsListParams) (map[string]any, error) {
	return func(ctx context.Context, p insightsListParams) (map[string]any, error) {
		if err := validateDashboardInsightLimit(p.Limit); err != nil {
			return nil, err
		}
		var (
			snaps []contract.InsightSnapshot
			err   error
		)
		if p.ThreadID == "" {
			snaps, err = reader.ListRecent(ctx, p.Limit)
		} else {
			snaps, err = reader.ListByThread(ctx, p.ThreadID, p.Limit)
		}
		if err != nil {
			return nil, mapInsightRPCError(err)
		}
		if snaps == nil {
			snaps = []contract.InsightSnapshot{}
		}
		return map[string]any{"insights": snaps}, nil
	}
}

// dashboardInsightsApprovalsHandler 返回已观察到的审批请求列表。
func dashboardInsightsApprovalsHandler(reader InsightReader) func(context.Context, insightsApprovalsParams) (map[string]any, error) {
	return func(ctx context.Context, p insightsApprovalsParams) (map[string]any, error) {
		if err := validateDashboardInsightLimit(p.Limit); err != nil {
			return nil, err
		}
		rows, err := reader.ListObservedApprovalRequests(ctx, p.ThreadID, p.Limit)
		if err != nil {
			return nil, mapInsightRPCError(err)
		}
		if rows == nil {
			rows = []contract.InsightApprovalSnapshot{}
		}
		return map[string]any{"approvals": rows}, nil
	}
}

// validateDashboardInsightLimit 在进入 insight reader 前阻断过大的 dashboard 查询窗口。
func validateDashboardInsightLimit(limit int32) error {
	if limit < 0 || limit > int32(maxLogLimit) {
		return platformrpc.ErrInvalidParams(fmt.Sprintf("insight limit must be between 0 and %d", maxLogLimit))
	}
	return nil
}

// mapInsightRPCError 将 insight 业务错误映射为标准 jrpc2 错误码。
func mapInsightRPCError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return err
	}
	if errors.Is(err, contract.ErrInsightInvalidLimit) {
		return platformrpc.ErrInvalidParams(err.Error())
	}
	if err.Error() == "insight: thread_id is required" {
		return platformrpc.ErrInvalidParams(err.Error())
	}
	return err
}
