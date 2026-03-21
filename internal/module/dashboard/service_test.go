package dashboard

import (
	"context"
	"testing"

	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
)

func TestGetDashboardPageReturnsStructuredPage(t *testing.T) {
	t.Parallel()

	svc := &service{}
	got, err := svc.GetDashboardPage(context.Background(), "commands")
	if err != nil {
		t.Fatalf("GetDashboardPage() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetDashboardPage() = nil")
	}
	if got.Agents == nil || got.TaskAcks == nil || got.TaskTraces == nil {
		t.Fatalf("GetDashboardPage() missing core slices: %#v", got)
	}
	if got.Skills == nil || got.CommandCards == nil || got.Prompts == nil || got.Memory == nil {
		t.Fatalf("GetDashboardPage() missing page slices: %#v", got)
	}
	if len(got.CommandCards) != 0 || len(got.Prompts) != 0 {
		t.Fatalf("GetDashboardPage(commands) = %#v, want empty command page", got)
	}
}

func TestGetAILogsByCategoryUsesStore(t *testing.T) {
	t.Parallel()

	stub := &stubAILogStore{
		listByCategoryResult: []ailogstore.AILog{{
			ID:       7,
			Category: "api_request",
			Message:  "GET /v1/models",
			Status:   "200",
		}},
	}
	svc := &service{aiLogs: stub}

	got, err := svc.GetAILogsByCategory(context.Background(), " api_request ", "", 7)
	if err != nil {
		t.Fatalf("GetAILogsByCategory() error = %v", err)
	}
	if stub.listByCategoryCalls != 1 {
		t.Fatalf("ListByCategory() calls = %d, want 1", stub.listByCategoryCalls)
	}
	if stub.listByCategoryCategory != "api_request" || stub.listByCategoryLimit != 7 {
		t.Fatalf("ListByCategory() args = (%q, %d)", stub.listByCategoryCategory, stub.listByCategoryLimit)
	}
	if len(got) != 1 || got[0].ID != 7 || got[0].Category != "api_request" {
		t.Fatalf("GetAILogsByCategory() = %#v", got)
	}
}

func TestGetAILogStatsUsesStore(t *testing.T) {
	t.Parallel()

	stub := &stubAILogStore{
		countByStatusResult: []ailogstore.StatusCount{
			{Status: "200", Count: 3},
			{Status: "500", Count: 1},
		},
	}
	svc := &service{aiLogs: stub}

	got, err := svc.GetAILogStats(context.Background())
	if err != nil {
		t.Fatalf("GetAILogStats() error = %v", err)
	}
	if stub.countByStatusCalls != 1 {
		t.Fatalf("CountByStatus() calls = %d, want 1", stub.countByStatusCalls)
	}
	if len(got) != 2 || got[0].Status != "200" || got[0].Count != 3 || got[1].Status != "500" || got[1].Count != 1 {
		t.Fatalf("GetAILogStats() = %#v", got)
	}
}

func TestGetRecentAILogsUsesStore(t *testing.T) {
	t.Parallel()

	stub := &stubAILogStore{
		listRecentResult: []ailogstore.AILog{{
			ID:      9,
			Message: "recent",
			Status:  "201",
		}},
	}
	svc := &service{aiLogs: stub}

	got, err := svc.GetRecentAILogs(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetRecentAILogs() error = %v", err)
	}
	if stub.listRecentCalls != 1 {
		t.Fatalf("ListRecent() calls = %d, want 1", stub.listRecentCalls)
	}
	if stub.listRecentLimit != 5 {
		t.Fatalf("ListRecent() limit = %d, want 5", stub.listRecentLimit)
	}
	if len(got) != 1 || got[0].ID != 9 || got[0].Message != "recent" {
		t.Fatalf("GetRecentAILogs() = %#v", got)
	}
}

type stubAILogStore struct {
	listByCategoryResult   []ailogstore.AILog
	listByCategoryErr      error
	listByCategoryCalls    int
	listByCategoryCategory string
	listByCategoryLimit    int32

	countByStatusResult []ailogstore.StatusCount
	countByStatusErr    error
	countByStatusCalls  int

	listRecentResult []ailogstore.AILog
	listRecentErr    error
	listRecentCalls  int
	listRecentLimit  int32
}

var _ ailogstore.Store = (*stubAILogStore)(nil)

func (s *stubAILogStore) List(context.Context, ailogstore.ListFilter) ([]ailogstore.AILog, error) {
	return []ailogstore.AILog{}, nil
}

func (s *stubAILogStore) ListByCategory(_ context.Context, category string, limit int32) ([]ailogstore.AILog, error) {
	s.listByCategoryCalls++
	s.listByCategoryCategory = category
	s.listByCategoryLimit = limit
	return s.listByCategoryResult, s.listByCategoryErr
}

func (s *stubAILogStore) CountByStatus(context.Context) ([]ailogstore.StatusCount, error) {
	s.countByStatusCalls++
	return s.countByStatusResult, s.countByStatusErr
}

func (s *stubAILogStore) ListRecent(_ context.Context, limit int32) ([]ailogstore.AILog, error) {
	s.listRecentCalls++
	s.listRecentLimit = limit
	return s.listRecentResult, s.listRecentErr
}
