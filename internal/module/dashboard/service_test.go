package dashboard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
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
	if got.Agents == nil || got.DAGs == nil || got.TaskTraces == nil {
		t.Fatalf("GetDashboardPage() missing core slices: %#v", got)
	}
	if got.Skills == nil || got.CommandCards == nil || got.Prompts == nil || got.Memory == nil {
		t.Fatalf("GetDashboardPage() missing page slices: %#v", got)
	}
	if len(got.CommandCards) != 0 || len(got.Prompts) != 0 {
		t.Fatalf("GetDashboardPage(commands) = %#v, want empty command page", got)
	}
}

func TestGetDashboardPageLoadsDAGs(t *testing.T) {
	t.Parallel()

	orchestration := &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{{DagKey: "dag-1", Title: "Dag One", Status: "running"}},
	}
	svc := &service{orchestration: orchestration}

	got, err := svc.GetDashboardPage(context.Background(), "dags")
	if err != nil {
		t.Fatalf("GetDashboardPage(dags) error = %v", err)
	}
	if got == nil || len(got.DAGs) != 1 || got.DAGs[0].DagKey != "dag-1" {
		t.Fatalf("GetDashboardPage(dags) = %#v", got)
	}
	if orchestration.listDAGsFilter.Limit != dashboardPageDefaultLimit {
		t.Fatalf("ListDAGs() filter = %#v", orchestration.listDAGsFilter)
	}
}

func TestGetDashboardPageDAGsWithoutOrchestrationIsEmpty(t *testing.T) {
	t.Parallel()

	svc := &service{}
	got, err := svc.GetDashboardPage(context.Background(), "dags")
	if err != nil {
		t.Fatalf("GetDashboardPage(dags) error = %v", err)
	}
	if got == nil || got.DAGs == nil || len(got.DAGs) != 0 {
		t.Fatalf("GetDashboardPage(dags) = %#v, want empty dag slice", got)
	}
}

func TestGetDashboardPageMemoryIncludesFinalOutputRefs(t *testing.T) {
	t.Parallel()

	shared := &stubSharedFileReader{
		result: []sharedfilestore.SharedFile{
			{Path: "reports/daily-brief.pptx", Content: "deck"},
			{Path: "scratch/intermediate.json", Content: "{}"},
		},
	}
	orchestration := &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{{DagKey: "dag-1", Title: "Daily Brief"}},
		listRunsResult: contract.ListRunsResponse{Runs: []contract.Run{{
			RunKey: "run-1",
			DagKey: "dag-1",
			Status: "succeeded",
			Metadata: json.RawMessage(`{
				"final_output": {
					"kind": "file",
					"role": "final_output",
					"path": "reports/daily-brief.pptx",
					"source_node_key": "report"
				}
			}`),
		}}},
	}
	svc := &service{sharedFiles: shared, orchestration: orchestration}

	got, err := svc.GetDashboardPage(context.Background(), "memory")
	if err != nil {
		t.Fatalf("GetDashboardPage(memory) error = %v", err)
	}
	if shared.lastFilter.Limit != dashboardMemoryLimit {
		t.Fatalf("shared file List() filter = %#v", shared.lastFilter)
	}
	if orchestration.listRunsRequest.DagKey != "dag-1" || orchestration.listRunsRequest.Limit != dashboardFinalOutputRunLimit {
		t.Fatalf("ListRuns() request = %#v", orchestration.listRunsRequest)
	}
	if len(got.Memory) != 2 {
		t.Fatalf("GetDashboardPage(memory).Memory = %#v", got.Memory)
	}
	if len(got.FinalOutputRefs) != 1 {
		t.Fatalf("FinalOutputRefs len = %d, want 1 (%#v)", len(got.FinalOutputRefs), got.FinalOutputRefs)
	}
	ref := got.FinalOutputRefs[0]
	if ref.Path != "reports/daily-brief.pptx" || ref.RunKey != "run-1" || ref.DagKey != "dag-1" || ref.SourceNodeKey != "report" {
		t.Fatalf("FinalOutputRefs[0] = %#v", ref)
	}
}

func TestGetDashboardPageMemoryPropagatesFinalOutputRefErrors(t *testing.T) {
	t.Parallel()

	shared := &stubSharedFileReader{
		result: []sharedfilestore.SharedFile{{Path: "reports/daily-brief.pptx", Content: "deck"}},
	}
	orchestration := &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{{DagKey: "dag-1", Title: "Daily Brief"}},
		listRunsErr:    errDashboardStub,
	}
	svc := &service{sharedFiles: shared, orchestration: orchestration}

	if _, err := svc.GetDashboardPage(context.Background(), "memory"); err == nil {
		t.Fatal("GetDashboardPage(memory) error = nil, want final output ref error")
	}
}

func TestGetDashboardPageKeepsSkillDisclosureTier(t *testing.T) {
	t.Parallel()

	svc := &service{skills: stubSkillLister{
		items: []contract.SkillInfo{{Name: "HotSkill", DisclosureTier: "hot"}},
	}}
	got, err := svc.GetDashboardPage(context.Background(), "skills")
	if err != nil {
		t.Fatalf("GetDashboardPage(skills) error = %v", err)
	}
	if len(got.Skills) != 1 || got.Skills[0].DisclosureTier != "hot" {
		t.Fatalf("GetDashboardPage(skills).Skills = %#v, want disclosure tier", got.Skills)
	}
}

type stubSkillLister struct {
	items []contract.SkillInfo
}

func (s stubSkillLister) ListSkills(context.Context) ([]contract.SkillInfo, error) {
	return s.items, nil
}

type stubSharedFileReader struct {
	result     []sharedfilestore.SharedFile
	err        error
	lastFilter sharedfilestore.ListFilter
}

var _ sharedfilestore.Reader = (*stubSharedFileReader)(nil)

func (s *stubSharedFileReader) Get(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
	for _, item := range s.result {
		if item.Path == path {
			return &item, nil
		}
	}
	return nil, s.err
}

func (s *stubSharedFileReader) List(_ context.Context, filter sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	s.lastFilter = filter
	return s.result, s.err
}

func TestGetDashboardPageFiltersPromptsByScopedCWD(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		cwd      string
		wantKeys []string
	}{
		{name: "repo_a", cwd: "/repo-a", wantKeys: []string{"global", "match"}},
		{name: "repo_b", cwd: "/repo-b", wantKeys: []string{"global", "other"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stub := &stubPromptReader{
				result: []promptstore.PromptTemplate{
					{PromptKey: "global", Title: "Global", Tags: json.RawMessage(`[]`)},
					{PromptKey: "match", Title: "Match", Tags: json.RawMessage(`["scope.cwd:/repo-a"]`)},
					{PromptKey: "other", Title: "Other", Tags: json.RawMessage(`["scope.cwd:/repo-b"]`)},
				},
			}
			svc := &service{prompts: stub}

			got, err := svc.GetDashboardPage(withDashboardPromptScopeCWD(context.Background(), tc.cwd), "commands")
			if err != nil {
				t.Fatalf("GetDashboardPage() error = %v", err)
			}
			if stub.calls != 1 {
				t.Fatalf("List() calls = %d, want 1", stub.calls)
			}
			if stub.lastFilter.CWD != tc.cwd || stub.lastFilter.Limit != dashboardPageDefaultLimit {
				t.Fatalf("List() filter = %#v", stub.lastFilter)
			}
			if len(got.Prompts) != len(tc.wantKeys) {
				t.Fatalf("GetDashboardPage(commands).Prompts len = %d, want %d (%#v)", len(got.Prompts), len(tc.wantKeys), got.Prompts)
			}
			for idx, wantKey := range tc.wantKeys {
				if got.Prompts[idx].PromptKey != wantKey {
					t.Fatalf("GetDashboardPage(commands).Prompts[%d] = %q, want %q", idx, got.Prompts[idx].PromptKey, wantKey)
				}
			}
		})
	}
}

func TestDashboardPromptsHandlerScopesByCWDAndReturnsPromptsKey(t *testing.T) {
	t.Parallel()

	stub := &stubPromptReader{
		result: []promptstore.PromptTemplate{
			{PromptKey: "global", Title: "Global", Tags: json.RawMessage(`[]`)},
			{PromptKey: "match", Title: "Match", Tags: json.RawMessage(`["scope.cwd:/repo-a"]`)},
			{PromptKey: "other", Title: "Other", Tags: json.RawMessage(`["scope.cwd:/repo-b"]`)},
		},
	}
	server := newDashboardTestServer(t, &service{prompts: stub})

	result, err := server.Dispatch(context.Background(), "dashboard/prompts", json.RawMessage(`{"cwd":"/repo-b"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("List() calls = %d, want 1", stub.calls)
	}
	if stub.lastFilter.CWD != "/repo-b" || stub.lastFilter.Limit != dashboardPageDefaultLimit {
		t.Fatalf("List() filter = %#v", stub.lastFilter)
	}

	var response map[string]json.RawMessage
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	promptsRaw, ok := response["prompts"]
	if !ok {
		t.Fatalf("Dispatch() response keys = %#v, want prompts", response)
	}
	if _, ok := response["commands"]; ok {
		t.Fatalf("Dispatch() response unexpectedly retained legacy commands key: %#v", response)
	}

	var prompts []promptstore.PromptTemplate
	if err := json.Unmarshal(promptsRaw, &prompts); err != nil {
		t.Fatalf("json.Unmarshal(prompts) error = %v", err)
	}
	if len(prompts) != 2 || prompts[0].PromptKey != "global" || prompts[1].PromptKey != "other" {
		t.Fatalf("Dispatch() prompts = %#v", prompts)
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

type stubPromptReader struct {
	result     []promptstore.PromptTemplate
	err        error
	calls      int
	lastFilter promptstore.ListFilter
}

var _ promptstore.Reader = (*stubPromptReader)(nil)

func (s *stubPromptReader) List(_ context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	s.calls++
	s.lastFilter = filter
	return s.result, s.err
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
