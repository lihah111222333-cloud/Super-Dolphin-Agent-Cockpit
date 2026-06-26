package dashboard

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
)

func TestDashboardInsightsRejectsOversizedLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		method    string
		payload   string
		wasCalled func(*dashboardInsightReaderStub) bool
	}{
		{
			name:    "recent list",
			method:  "dashboard/insights/list",
			payload: `{"limit":501}`,
			wasCalled: func(s *dashboardInsightReaderStub) bool {
				return s.listRecentCalled
			},
		},
		{
			name:    "thread list",
			method:  "dashboard/insights/list",
			payload: `{"thread_id":"thread-1","limit":501}`,
			wasCalled: func(s *dashboardInsightReaderStub) bool {
				return s.listByThreadCalled
			},
		},
		{
			name:    "approval list",
			method:  "dashboard/insights/approvals",
			payload: `{"limit":501}`,
			wasCalled: func(s *dashboardInsightReaderStub) bool {
				return s.approvalsCalled
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader := &dashboardInsightReaderStub{}
			server := newDashboardInsightsTestServer(t, reader)
			err := dispatchDashboardInto(server, tc.method, tc.payload, &struct{}{})
			var rpcErr *jrpc2.Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.Code(contract.CodeInvalidParams) {
				t.Fatalf("dispatch error = %v, want invalid params", err)
			}
			if tc.wasCalled(reader) {
				t.Fatalf("%s called reader for oversized limit", tc.method)
			}
		})
	}
}

func newDashboardInsightsTestServer(t *testing.T, reader InsightReader) *platformrpc.Server {
	t.Helper()

	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewDashboardHandlersWithInsights(dashboardHandlersParams{
		Service:  &service{},
		Insights: reader,
	}).Handlers)
	return server
}

type dashboardInsightReaderStub struct {
	listRecentCalled   bool
	listByThreadCalled bool
	approvalsCalled    bool
}

func (s *dashboardInsightReaderStub) ListRecent(context.Context, int32) ([]contract.InsightSnapshot, error) {
	s.listRecentCalled = true
	return nil, nil
}

func (s *dashboardInsightReaderStub) ListByThread(context.Context, string, int32) ([]contract.InsightSnapshot, error) {
	s.listByThreadCalled = true
	return nil, nil
}

func (s *dashboardInsightReaderStub) ListObservedApprovalRequests(context.Context, string, int32) ([]contract.InsightApprovalSnapshot, error) {
	s.approvalsCalled = true
	return nil, nil
}
