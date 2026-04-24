package dashboard

import (
	"context"
	"errors"

	insightmodule "github.com/anthropic-ai/super-agent-v3/internal/module/insight"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type InsightReader = insightmodule.Service

func addDashboardInsightHandlers(handlers handler.Map, reader InsightReader) {
	if reader == nil {
		return
	}
	handlers["dashboard/insights/list"] = rpc.StrictHandler(dashboardInsightsListHandler(reader))
	handlers["dashboard/insights/approvals"] = rpc.StrictHandler(dashboardInsightsApprovalsHandler(reader))
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
			snaps []insightmodule.Snapshot
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
			snaps = []insightmodule.Snapshot{}
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
			rows = []insightmodule.ApprovalSnapshot{}
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
	if errors.Is(err, insightmodule.ErrInvalidLimit) {
		return jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
	}
	if err.Error() == "insight: thread_id is required" {
		return jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
	}
	return err
}
