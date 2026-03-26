package dashboard

import (
	"context"
	"testing"

	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
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
	if got.Agents == nil || got.TaskTraces == nil {
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
	if stub.listByCategoryCategory != "api_request" || stub.listByCategoryKeyword != "" || stub.listByCategoryLimit != 7 {
		t.Fatalf("ListByCategory() args = (%q, %q, %d)", stub.listByCategoryCategory, stub.listByCategoryKeyword, stub.listByCategoryLimit)
	}
	if len(got) != 1 || got[0].ID != 7 || got[0].Category != "api_request" {
		t.Fatalf("GetAILogsByCategory() = %#v", got)
	}
}

func TestGetAILogsByCategoryPassesKeywordToStore(t *testing.T) {
	t.Parallel()

	stub := &stubAILogStore{
		listByCategoryResult: []ailogstore.AILog{
			{ID: 7, Category: "api_request", Message: "GET /v1/models"},
			{ID: 8, Category: "api_request", Message: "unfiltered here, store owns keyword match"},
		},
	}
	svc := &service{aiLogs: stub}

	got, err := svc.GetAILogsByCategory(context.Background(), " api_request ", " models ", 2)
	if err != nil {
		t.Fatalf("GetAILogsByCategory() error = %v", err)
	}
	if stub.listByCategoryKeyword != "models" || stub.listByCategoryLimit != 2 {
		t.Fatalf("ListByCategory() args = (%q, %d)", stub.listByCategoryKeyword, stub.listByCategoryLimit)
	}
	if len(got) != 2 {
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

func TestGetAuditLogsUsesStore(t *testing.T) {
	t.Parallel()

	stub := &stubAuditLogStore{
		listResult: []auditlogstore.AuditEvent{{ID: 7, EventType: "tool", Action: "run"}},
	}
	svc := &service{auditLogs: stub}

	got, err := svc.GetAuditLogs(context.Background(), auditlogstore.ListFilter{
		EventType: " tool ",
		Action:    " run ",
		Actor:     " agent-1 ",
		Keyword:   " failed ",
		Limit:     7,
	})
	if err != nil {
		t.Fatalf("GetAuditLogs() error = %v", err)
	}
	if stub.listCalls != 1 {
		t.Fatalf("List() calls = %d, want 1", stub.listCalls)
	}
	if stub.listFilter.EventType != "tool" || stub.listFilter.Action != "run" || stub.listFilter.Actor != "agent-1" || stub.listFilter.Keyword != "failed" || stub.listFilter.Limit != 7 {
		t.Fatalf("List() filter = %#v", stub.listFilter)
	}
	if len(got) != 1 || got[0].ID != 7 {
		t.Fatalf("GetAuditLogs() = %#v", got)
	}
}

func TestGetBusLogsUsesStore(t *testing.T) {
	t.Parallel()

	stub := &stubBusLogStore{
		listResult: []buslogstore.BusExceptionLog{{ID: 9, Category: "rpc", Severity: "error"}},
	}
	svc := &service{busLogs: stub}

	got, err := svc.GetBusLogs(context.Background(), buslogstore.ListFilter{
		Category: " rpc ",
		Severity: " error ",
		Keyword:  " timeout ",
		Limit:    9,
	})
	if err != nil {
		t.Fatalf("GetBusLogs() error = %v", err)
	}
	if stub.listCalls != 1 {
		t.Fatalf("List() calls = %d, want 1", stub.listCalls)
	}
	if stub.listFilter.Category != "rpc" || stub.listFilter.Severity != "error" || stub.listFilter.Keyword != "timeout" || stub.listFilter.Limit != 9 {
		t.Fatalf("List() filter = %#v", stub.listFilter)
	}
	if len(got) != 1 || got[0].ID != 9 {
		t.Fatalf("GetBusLogs() = %#v", got)
	}
}

type stubAILogStore struct {
	listByCategoryResult   []ailogstore.AILog
	listByCategoryErr      error
	listByCategoryCalls    int
	listByCategoryCategory string
	listByCategoryKeyword  string
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

func (s *stubAILogStore) ListByCategory(_ context.Context, category string, keyword string, limit int32) ([]ailogstore.AILog, error) {
	s.listByCategoryCalls++
	s.listByCategoryCategory = category
	s.listByCategoryKeyword = keyword
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

type stubAuditLogStore struct {
	listResult []auditlogstore.AuditEvent
	listErr    error
	listFilter auditlogstore.ListFilter
	listCalls  int
}

var _ auditlogstore.Store = (*stubAuditLogStore)(nil)

func (s *stubAuditLogStore) List(_ context.Context, filter auditlogstore.ListFilter) ([]auditlogstore.AuditEvent, error) {
	s.listCalls++
	s.listFilter = filter
	return s.listResult, s.listErr
}

func (*stubAuditLogStore) Insert(context.Context, auditlogstore.InsertParams) error {
	return nil
}

type stubBusLogStore struct {
	listResult []buslogstore.BusExceptionLog
	listErr    error
	listFilter buslogstore.ListFilter
	listCalls  int
}

var _ buslogstore.Store = (*stubBusLogStore)(nil)

func (s *stubBusLogStore) List(_ context.Context, filter buslogstore.ListFilter) ([]buslogstore.BusExceptionLog, error) {
	s.listCalls++
	s.listFilter = filter
	return s.listResult, s.listErr
}
