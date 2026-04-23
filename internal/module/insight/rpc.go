package insight

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// dashboard/insights/* wire shapes.

type insightsListParams struct {
	ThreadID string `json:"thread_id,omitempty"`
	Limit    int32  `json:"limit,omitempty"`
}

type insightsApprovalsParams struct {
	ThreadID string `json:"thread_id,omitempty"`
	Limit    int32  `json:"limit,omitempty"`
}

// NewHandlers returns the host/UI RPC handler map for the dashboard
// insights API. ListInsights accepts an optional thread_id; when empty
// the backend returns the N most recent turns across all threads.
func NewHandlers(svc Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"dashboard/insights/list":      rpc.StrictHandler(listHandler(svc)),
		"dashboard/insights/approvals": rpc.StrictHandler(approvalsHandler(svc)),
	}}
}

func listHandler(svc Service) func(context.Context, insightsListParams) (map[string]any, error) {
	return func(ctx context.Context, p insightsListParams) (map[string]any, error) {
		var (
			snaps []Snapshot
			err   error
		)
		if p.ThreadID == "" {
			snaps, err = svc.ListRecent(ctx, p.Limit)
		} else {
			snaps, err = svc.ListByThread(ctx, p.ThreadID, p.Limit)
		}
		if err != nil {
			return nil, mapRPCError(err)
		}
		if snaps == nil {
			snaps = []Snapshot{}
		}
		return map[string]any{"insights": snaps}, nil
	}
}

func approvalsHandler(svc Service) func(context.Context, insightsApprovalsParams) (map[string]any, error) {
	return func(ctx context.Context, p insightsApprovalsParams) (map[string]any, error) {
		rows, err := svc.ListObservedApprovalRequests(ctx, p.ThreadID, p.Limit)
		if err != nil {
			return nil, mapRPCError(err)
		}
		if rows == nil {
			rows = []ApprovalSnapshot{}
		}
		return map[string]any{"approvals": rows}, nil
	}
}

func mapRPCError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return err
	}
	if errors.Is(err, ErrInvalidLimit) {
		return jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
	}
	if err.Error() == "insight: thread_id is required" {
		return jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
	}
	return err
}
