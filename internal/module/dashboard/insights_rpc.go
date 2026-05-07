package dashboard

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type InsightReader = contract.InsightService

func addDashboardInsightHandlers(handlers handler.Map, reader InsightReader) {
	if reader == nil {
		return
	}
	handlers["dashboard/insights/list"] = platformrpc.StrictHandler(dashboardInsightsListHandler(reader))
	handlers["dashboard/insights/approvals"] = platformrpc.StrictHandler(dashboardInsightsApprovalsHandler(reader))
}

type insightsListParams struct {
	ThreadID string `json:"thread_id,omitempty"`
	Limit    int32  `json:"limit,omitempty"`
}

type insightsApprovalsParams struct {
	ThreadID string `json:"thread_id,omitempty"`
	Limit    int32  `json:"limit,omitempty"`
}

func dashboardInsightsListHandler(reader InsightReader) func(context.Context, insightsListParams) (map[string]any, error) {
	return func(ctx context.Context, p insightsListParams) (map[string]any, error) {
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

func dashboardInsightsApprovalsHandler(reader InsightReader) func(context.Context, insightsApprovalsParams) (map[string]any, error) {
	return func(ctx context.Context, p insightsApprovalsParams) (map[string]any, error) {
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
