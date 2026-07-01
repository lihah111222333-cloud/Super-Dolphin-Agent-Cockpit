package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestGetDashboardPageReturnsStructuredPage(t *testing.T) {
	t.Parallel()

	svc := &service{}
	got, err := svc.GetDashboardPage(context.Background(), "commands")
	if err != nil {
		t.Fatalf("GetDashboardPage() error = %v", err)
	}
	assertDashboardPageInitialized(t, got)
	assertEmptyDashboardCommandPage(t, got)
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

func assertDashboardPageInitialized(t *testing.T, got *DashboardPage) {
	t.Helper()

	if got == nil {
		t.Fatal("GetDashboardPage() = nil")
	}
	assertDashboardCoreSlices(t, got)
	assertDashboardPageSlices(t, got)
}

func assertDashboardCoreSlices(t *testing.T, got *DashboardPage) {
	t.Helper()

	if got.Agents == nil {
		t.Fatalf("GetDashboardPage() missing agents slice: %#v", got)
	}
	if got.DAGs == nil {
		t.Fatalf("GetDashboardPage() missing DAGs slice: %#v", got)
	}
}

func assertDashboardPageSlices(t *testing.T, got *DashboardPage) {
	t.Helper()

	if got.Skills == nil {
		t.Fatalf("GetDashboardPage() missing skills slice: %#v", got)
	}
	if got.CommandCards == nil {
		t.Fatalf("GetDashboardPage() missing command cards slice: %#v", got)
	}
	if got.Prompts == nil {
		t.Fatalf("GetDashboardPage() missing prompts slice: %#v", got)
	}
	if got.Memory == nil {
		t.Fatalf("GetDashboardPage() missing memory slice: %#v", got)
	}
}

func assertEmptyDashboardCommandPage(t *testing.T, got *DashboardPage) {
	t.Helper()

	if len(got.CommandCards) != 0 {
		t.Fatalf("GetDashboardPage(commands).CommandCards = %#v, want empty", got.CommandCards)
	}
	if len(got.Prompts) != 0 {
		t.Fatalf("GetDashboardPage(commands).Prompts = %#v, want empty", got.Prompts)
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
		result: []SharedFile{
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
	assertDashboardFinalOutputRefs(t, got)
	assertDashboardRetentionSummary(t, got)
}

func TestGetDashboardPageMemorySurfacesFinalOutputRefErrors(t *testing.T) {
	t.Parallel()

	shared := &stubSharedFileReader{
		result: []SharedFile{{Path: "reports/daily-brief.pptx", Content: "deck"}},
	}
	orchestration := &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{{DagKey: "dag-1", Title: "Daily Brief"}},
		listRunsErr:    errDashboardStub,
	}
	svc := &service{sharedFiles: shared, orchestration: orchestration}

	got, err := svc.GetDashboardPage(context.Background(), "memory")
	if !errors.Is(err, errDashboardStub) {
		t.Fatalf("GetDashboardPage(memory) error = %v, want final output refs error", err)
	}
	if got != nil {
		t.Fatalf("GetDashboardPage(memory) = %#v, want nil page on final output refs error", got)
	}
}

func assertDashboardFinalOutputRefs(t *testing.T, got *DashboardPage) {
	t.Helper()

	if len(got.FinalOutputRefs) != 1 {
		t.Fatalf("FinalOutputRefs len = %d, want 1 (%#v)", len(got.FinalOutputRefs), got.FinalOutputRefs)
	}
	ref := got.FinalOutputRefs[0]
	if ref.Path != "reports/daily-brief.pptx" {
		t.Fatalf("FinalOutputRefs[0].Path = %q, want final output path", ref.Path)
	}
	if ref.RunKey != "run-1" || ref.DagKey != "dag-1" || ref.SourceNodeKey != "report" {
		t.Fatalf("FinalOutputRefs[0] = %#v", ref)
	}
}

func assertDashboardRetentionSummary(t *testing.T, got *DashboardPage) {
	t.Helper()

	if got.SharedFileRetention.ProtectedCount != 1 {
		t.Fatalf("SharedFileRetention protected count = %#v", got.SharedFileRetention)
	}
	if got.SharedFileRetention.CleanupCandidateCount != 1 {
		t.Fatalf("SharedFileRetention cleanup count = %#v", got.SharedFileRetention)
	}
	retentionByPath := dashboardRetentionByPath(got.SharedFileRetention.Items)
	assertDashboardRetentionItem(t, retentionByPath["reports/daily-brief.pptx"], true, false, "final_output")
	assertDashboardRetentionItem(t, retentionByPath["scratch/intermediate.json"], false, true, "unreferenced")
}

func dashboardRetentionByPath(items []SharedFileRetentionItem) map[string]SharedFileRetentionItem {
	retentionByPath := map[string]SharedFileRetentionItem{}
	for _, item := range items {
		retentionByPath[item.Path] = item
	}
	return retentionByPath
}

func assertDashboardRetentionItem(t *testing.T, item SharedFileRetentionItem, protected, cleanup bool, reason string) {
	t.Helper()

	if item.Protected != protected || item.CleanupCandidate != cleanup || item.Reason != reason {
		t.Fatalf("retention item = %#v, want protected=%t cleanup=%t reason=%q", item, protected, cleanup, reason)
	}
}

type stubSkillLister struct {
	items []contract.SkillInfo
	err   error
}

func (s stubSkillLister) ListSkills(context.Context) ([]contract.SkillInfo, error) {
	return s.items, s.err
}

func TestGetDashboardPageSkillsKeepsVisibleSkillsWhenSameNameConflictExists(t *testing.T) {
	t.Parallel()

	svc := &service{skills: stubSkillLister{
		items: []contract.SkillInfo{{Name: "safe", Scope: "project"}},
		err:   contract.ErrSkillSameNameConflict,
	}}

	got, err := svc.GetDashboardPage(context.Background(), "skills")
	if err != nil {
		t.Fatalf("GetDashboardPage(skills) error = %v, want nil for same-name conflict", err)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "safe" {
		t.Fatalf("GetDashboardPage(skills).Skills = %+v, want visible non-conflicted skills", got.Skills)
	}
}

func TestGetDashboardPageSkillsStillReturnsUnexpectedSkillErrors(t *testing.T) {
	t.Parallel()

	svc := &service{skills: stubSkillLister{err: errors.New("boom")}}

	_, err := svc.GetDashboardPage(context.Background(), "skills")
	if err == nil {
		t.Fatal("GetDashboardPage(skills) error = nil, want unexpected skill error")
	}
}

type stubSharedFileReader struct {
	result     []SharedFile
	err        error
	lastFilter SharedFileFilter
}

var _ SharedFileReader = (*stubSharedFileReader)(nil)

func (s *stubSharedFileReader) Get(_ context.Context, path string) (*SharedFile, error) {
	for _, item := range s.result {
		if item.Path == path {
			return &item, nil
		}
	}
	return nil, s.err
}

func (s *stubSharedFileReader) List(_ context.Context, filter SharedFileFilter) ([]SharedFile, error) {
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
				result: []PromptTemplate{
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
		result: []PromptTemplate{
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

	prompts := decodeDashboardPromptsResponse(t, result)
	assertDashboardPromptKeys(t, prompts, []string{"global", "other"})
}

func TestDashboardPromptsHandlerRequiresCWD(t *testing.T) {
	t.Parallel()

	stub := &stubPromptReader{
		result: []PromptTemplate{
			{PromptKey: "other-project", Title: "Other", Tags: json.RawMessage(`["scope.cwd:/repo-b"]`)},
		},
	}
	server := newDashboardTestServer(t, &service{prompts: stub})

	_, err := server.Dispatch(context.Background(), "dashboard/prompts", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("Dispatch() error = nil, want cwd required")
	}
	if stub.calls != 0 {
		t.Fatalf("List() calls = %d, want 0 when cwd is missing", stub.calls)
	}
}

func TestDashboardPromptsHandlerHidesSystemManagedPromptRows(t *testing.T) {
	t.Parallel()

	stub := &stubPromptReader{
		result: []PromptTemplate{
			{PromptKey: "user-expert", Title: "User Expert", Tags: json.RawMessage(`["scope.cwd:/repo-a","intent:expert"]`), CreatedBy: "rpc.prompts", UpdatedBy: "rpc.prompts"},
			{PromptKey: "rpc-updated-seed", Title: "RPC Updated Seed", Tags: json.RawMessage(`["scope.cwd:/repo-a","intent:expert"]`), CreatedBy: "system.seed", UpdatedBy: "rpc.prompts"},
			{PromptKey: "user-edited-seed", Title: "User Edited Seed", Tags: json.RawMessage(`["scope.cwd:/repo-a","intent:expert"]`), CreatedBy: "system.seed", UpdatedBy: "system.seed", ManuallyEdited: true},
			{PromptKey: "builtin-tagged", Title: "Builtin Tagged", Tags: json.RawMessage(`["scope.cwd:/repo-a","builtin:system"]`), CreatedBy: "rpc.prompts", UpdatedBy: "rpc.prompts"},
			{PromptKey: "edited-builtin-tagged", Title: "Edited Builtin Tagged", Tags: json.RawMessage(`["scope.cwd:/repo-a","builtin:system"]`), CreatedBy: "system.seed", UpdatedBy: "rpc.prompts", ManuallyEdited: true},
			{PromptKey: "system-seed", Title: "System Seed", Tags: json.RawMessage(`["scope.cwd:/repo-a","intent:expert"]`), CreatedBy: "system.seed", UpdatedBy: "system.seed"},
			{PromptKey: "registry-row", Title: "Registry Row", Tags: json.RawMessage(`["scope.cwd:/repo-a","intent:expert"]`), CreatedBy: "builtin.registry", UpdatedBy: "builtin.registry"},
		},
	}
	server := newDashboardTestServer(t, &service{prompts: stub})

	result, err := server.Dispatch(context.Background(), "dashboard/prompts", json.RawMessage(`{"cwd":"/repo-a"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	prompts := decodeDashboardPromptsResponse(t, result)
	assertDashboardPromptKeys(t, prompts, []string{"user-expert", "rpc-updated-seed", "user-edited-seed"})
}

func TestGetAILogsByCategoryUsesStore(t *testing.T) {
	t.Parallel()

	stub := &stubAILogStore{
		listByCategoryResult: []AILog{{
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

func decodeDashboardPromptsResponse(t *testing.T, result json.RawMessage) []PromptTemplate {
	t.Helper()

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

	var prompts []PromptTemplate
	if err := json.Unmarshal(promptsRaw, &prompts); err != nil {
		t.Fatalf("json.Unmarshal(prompts) error = %v", err)
	}
	return prompts
}

func assertDashboardPromptKeys(t *testing.T, prompts []PromptTemplate, wantKeys []string) {
	t.Helper()

	if len(prompts) != len(wantKeys) {
		t.Fatalf("Dispatch() prompts = %#v, want %d prompts", prompts, len(wantKeys))
	}
	for idx, want := range wantKeys {
		if prompts[idx].PromptKey != want {
			t.Fatalf("Dispatch() prompts[%d] = %q, want %q", idx, prompts[idx].PromptKey, want)
		}
	}
}

func TestGetAILogsByCategoryPassesKeywordToStore(t *testing.T) {
	t.Parallel()

	stub := &stubAILogStore{
		listByCategoryResult: []AILog{
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
		countByStatusResult: []AILogStatusCount{
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
		listRecentResult: []AILog{{
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

func TestGetLogsRequiresEnabledReaders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		svc  *service
		src  string
		want error
	}{
		{name: "system", svc: &service{}, src: logSourceSystem, want: errDashboardSystemLogsNotConfigured},
		{name: "ai", svc: &service{}, src: logSourceAI, want: errDashboardAILogsNotConfigured},
		{name: "all requires ai after system", svc: &service{systemLogs: &stubSystemLogStore{}}, src: logSourceAll, want: errDashboardAILogsNotConfigured},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.svc.GetLogs(context.Background(), LogFilter{Source: tc.src})
			if !errors.Is(err, tc.want) {
				t.Fatalf("GetLogs(%q) error = %v, want %v", tc.src, err, tc.want)
			}
		})
	}
}

func TestAILogAPIsRequireStore(t *testing.T) {
	t.Parallel()

	svc := &service{}
	cases := []struct {
		name string
		call func() error
	}{
		{name: "category", call: func() error {
			_, err := svc.GetAILogsByCategory(context.Background(), "api_request", "", 10)
			return err
		}},
		{name: "stats", call: func() error {
			_, err := svc.GetAILogStats(context.Background())
			return err
		}},
		{name: "recent", call: func() error {
			_, err := svc.GetRecentAILogs(context.Background(), 10)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.call(); !errors.Is(err, errDashboardAILogsNotConfigured) {
				t.Fatalf("%s error = %v, want %v", tc.name, err, errDashboardAILogsNotConfigured)
			}
		})
	}
}

func TestGetAuditLogsUsesStore(t *testing.T) {
	t.Parallel()

	stub := &stubAuditLogStore{
		listResult: []AuditEvent{{ID: 7, EventType: "tool", Action: "run"}},
	}
	svc := &service{auditLogs: stub}

	got, err := svc.GetAuditLogs(context.Background(), AuditLogFilter{
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
		listResult: []BusExceptionLog{{ID: 9, Category: "rpc", Severity: "error"}},
	}
	svc := &service{busLogs: stub}

	got, err := svc.GetBusLogs(context.Background(), BusLogFilter{
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
	result     []PromptTemplate
	err        error
	calls      int
	lastFilter PromptTemplateFilter
}

var _ PromptTemplateReader = (*stubPromptReader)(nil)

func (s *stubPromptReader) List(_ context.Context, filter PromptTemplateFilter) ([]PromptTemplate, error) {
	s.calls++
	s.lastFilter = filter
	return s.result, s.err
}

type stubAILogStore struct {
	listResult []AILog
	listErr    error
	listFilter AILogFilter
	listCalls  int

	listByCategoryResult   []AILog
	listByCategoryErr      error
	listByCategoryCalls    int
	listByCategoryCategory string
	listByCategoryKeyword  string
	listByCategoryLimit    int32

	countByStatusResult []AILogStatusCount
	countByStatusErr    error
	countByStatusCalls  int

	listRecentResult []AILog
	listRecentErr    error
	listRecentCalls  int
	listRecentLimit  int32
}

var _ AILogReader = (*stubAILogStore)(nil)

type stubSystemLogStore struct {
	listResult []SystemLog
	listErr    error
	listFilter SystemLogFilter
	listCalls  int
}

var _ SystemLogReader = (*stubSystemLogStore)(nil)

func (s *stubSystemLogStore) List(_ context.Context, filter SystemLogFilter) ([]SystemLog, error) {
	s.listCalls++
	s.listFilter = filter
	return s.listResult, s.listErr
}

func (s *stubAILogStore) List(_ context.Context, filter AILogFilter) ([]AILog, error) {
	s.listCalls++
	s.listFilter = filter
	return s.listResult, s.listErr
}

func (s *stubAILogStore) ListByCategory(_ context.Context, category string, keyword string, limit int32) ([]AILog, error) {
	s.listByCategoryCalls++
	s.listByCategoryCategory = category
	s.listByCategoryKeyword = keyword
	s.listByCategoryLimit = limit
	return s.listByCategoryResult, s.listByCategoryErr
}

func (s *stubAILogStore) CountByStatus(context.Context) ([]AILogStatusCount, error) {
	s.countByStatusCalls++
	return s.countByStatusResult, s.countByStatusErr
}

func (s *stubAILogStore) ListRecent(_ context.Context, limit int32) ([]AILog, error) {
	s.listRecentCalls++
	s.listRecentLimit = limit
	return s.listRecentResult, s.listRecentErr
}

type stubAuditLogStore struct {
	listResult []AuditEvent
	listErr    error
	listFilter AuditLogFilter
	listCalls  int
}

var _ AuditLogReader = (*stubAuditLogStore)(nil)

func (s *stubAuditLogStore) List(_ context.Context, filter AuditLogFilter) ([]AuditEvent, error) {
	s.listCalls++
	s.listFilter = filter
	return s.listResult, s.listErr
}

type stubBusLogStore struct {
	listResult []BusExceptionLog
	listErr    error
	listFilter BusLogFilter
	listCalls  int
}

var _ BusLogReader = (*stubBusLogStore)(nil)

func (s *stubBusLogStore) List(_ context.Context, filter BusLogFilter) ([]BusExceptionLog, error) {
	s.listCalls++
	s.listFilter = filter
	return s.listResult, s.listErr
}
