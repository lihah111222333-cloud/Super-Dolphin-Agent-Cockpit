package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

type stubWorkspaceService struct {
	getRun   func(context.Context, string) (*workspace.Run, error)
	listRuns func(context.Context, string, string, int) ([]workspace.Run, error)
}

func (s stubWorkspaceService) GetRun(ctx context.Context, runKey string) (*workspace.Run, error) {
	if s.getRun == nil {
		return nil, nil
	}
	return s.getRun(ctx, runKey)
}

func (stubWorkspaceService) CreateRun(context.Context, workspace.CreateRunRequest) (*workspace.Run, error) {
	return nil, errors.New("unexpected CreateRun call")
}

func (s stubWorkspaceService) ListRuns(ctx context.Context, status, dagKey string, limit int) ([]workspace.Run, error) {
	if s.listRuns != nil {
		return s.listRuns(ctx, status, dagKey, limit)
	}
	return nil, errors.New("unexpected ListRuns call")
}

func (stubWorkspaceService) MergeRun(context.Context, workspace.MergeRunRequest) (*workspace.MergeRunResult, error) {
	return nil, errors.New("unexpected MergeRun call")
}

func (stubWorkspaceService) AbortRun(context.Context, string, string, string) error {
	return errors.New("unexpected AbortRun call")
}

func (stubWorkspaceService) ListRunFiles(context.Context, string, string) ([]workspace.RunFile, error) {
	return nil, nil
}

func (stubWorkspaceService) GetRunFile(context.Context, string, string) (*workspace.RunFile, error) {
	return nil, nil
}

type stubPromptStore struct {
	promptstore.Store
	get                      func(context.Context, string) (*promptstore.PromptTemplate, error)
	list                     func(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error)
	listSectionsByTemplateID func(context.Context, int64) ([]promptstore.PromptTemplateSection, error)
}

func (s stubPromptStore) Get(ctx context.Context, key string) (*promptstore.PromptTemplate, error) {
	if s.get == nil {
		return nil, nil
	}
	return s.get(ctx, key)
}

func (s stubPromptStore) List(ctx context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	if s.list == nil {
		return nil, nil
	}
	return s.list(ctx, filter)
}

func (s stubPromptStore) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]promptstore.PromptTemplateSection, error) {
	if s.listSectionsByTemplateID == nil {
		return nil, nil
	}
	return s.listSectionsByTemplateID(ctx, templateID)
}

type stubCommandStore struct {
	commandcardstore.Store
	get  func(context.Context, string) (*commandcardstore.CommandCard, error)
	list func(context.Context, commandcardstore.ListFilter) ([]commandcardstore.CommandCard, error)
}

func (s stubCommandStore) Get(ctx context.Context, key string) (*commandcardstore.CommandCard, error) {
	if s.get == nil {
		return nil, nil
	}
	return s.get(ctx, key)
}

func (s stubCommandStore) List(ctx context.Context, filter commandcardstore.ListFilter) ([]commandcardstore.CommandCard, error) {
	if s.list == nil {
		return nil, errors.New("unexpected List call")
	}
	return s.list(ctx, filter)
}

type stubSharedFileStore struct {
	sharedfilestore.Store
	get    func(context.Context, string) (*sharedfilestore.SharedFile, error)
	upsert func(context.Context, sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error)
}

func (s stubSharedFileStore) Get(ctx context.Context, path string) (*sharedfilestore.SharedFile, error) {
	if s.get == nil {
		return nil, nil
	}
	return s.get(ctx, path)
}

func (s stubSharedFileStore) Upsert(ctx context.Context, params sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
	if s.upsert == nil {
		return nil, errors.New("unexpected Upsert call")
	}
	return s.upsert(ctx, params)
}

func mustRawInput(t *testing.T, input any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}

func promptToolTestContext() context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{CWD: "/repo/a"})
}

func TestHandleWorkspaceGetRunNilMapsToNotFound(t *testing.T) {
	var gotRunKey string
	handler := HandleWorkspaceGetRun(stubWorkspaceService{
		getRun: func(_ context.Context, runKey string) (*workspace.Run, error) {
			gotRunKey = runKey
			return nil, nil
		},
	})

	_, err := handler(context.Background(), mustRawInput(t, workspaceGetRunInput{RunKey: " run-1 "}))
	if err == nil || err.Error() != "workspace run run-1 not found" {
		t.Fatalf("HandleWorkspaceGetRun() error = %v", err)
	}
	if gotRunKey != "run-1" {
		t.Fatalf("HandleWorkspaceGetRun() run_key = %q, want %q", gotRunKey, "run-1")
	}
}

func TestWorkspaceCreateRunToolRejectsSourceRootOutsideScope(t *testing.T) {
	handler := HandleWorkspaceCreateRun(stubWorkspaceService{})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: t.TempDir()})

	_, err := handler(ctx, mustRawInput(t, WorkspaceCreateRunRequest{SourceRoot: t.TempDir()}))
	if err == nil {
		t.Fatal("HandleWorkspaceCreateRun() error = nil, want source root scope rejection")
	}
	if !strings.Contains(err.Error(), "outside allowed workspace roots") {
		t.Fatalf("HandleWorkspaceCreateRun() error = %v, want allowed workspace roots rejection", err)
	}
}

func TestHandleWorkspaceGetRunWrappedNotFoundMapsToNotFound(t *testing.T) {
	var gotRunKey string
	handler := HandleWorkspaceGetRun(stubWorkspaceService{
		getRun: func(_ context.Context, runKey string) (*workspace.Run, error) {
			gotRunKey = runKey
			return nil, platformdb.WrapStoreError(platformdb.ErrNotFound, "get", "workspace_run")
		},
	})

	_, err := handler(context.Background(), mustRawInput(t, workspaceGetRunInput{RunKey: " run-1 "}))
	if err == nil || err.Error() != "workspace run run-1 not found" {
		t.Fatalf("HandleWorkspaceGetRun() wrapped error = %v", err)
	}
	if gotRunKey != "run-1" {
		t.Fatalf("HandleWorkspaceGetRun() run_key = %q, want %q", gotRunKey, "run-1")
	}
}

func TestHandlePromptGetTranslatesNotFound(t *testing.T) {
	var gotKey string
	handler := HandlePromptGet(stubPromptStore{
		get: func(_ context.Context, key string) (*promptstore.PromptTemplate, error) {
			gotKey = key
			return nil, platformdb.ErrNotFound
		},
	}, nil)

	_, err := handler(promptToolTestContext(), mustRawInput(t, promptGetInput{PromptKey: " missing "}))
	if err == nil || err.Error() != "prompt missing not found" {
		t.Fatalf("HandlePromptGet() error = %v", err)
	}
	if gotKey != "missing" {
		t.Fatalf("HandlePromptGet() prompt_key = %q, want %q", gotKey, "missing")
	}
}

func TestHandlePromptGetAssemblesInjectableSections(t *testing.T) {
	t.Parallel()

	handler := HandlePromptGet(stubPromptStore{
		get: func(_ context.Context, key string) (*promptstore.PromptTemplate, error) {
			if key != "custom/prompt" {
				t.Fatalf("Get() key = %q, want custom/prompt", key)
			}
			return &promptstore.PromptTemplate{
				ID:         42,
				PromptKey:  "custom/prompt",
				Title:      "Custom Prompt",
				AgentKey:   "custom",
				PromptText: "legacy fallback text",
				Tags:       json.RawMessage(`["scope.cwd:/repo/a"]`),
				Enabled:    true,
			}, nil
		},
		listSectionsByTemplateID: func(_ context.Context, templateID int64) ([]promptstore.PromptTemplateSection, error) {
			if templateID != 42 {
				t.Fatalf("ListSectionsByTemplateID() templateID = %d, want 42", templateID)
			}
			return []promptstore.PromptTemplateSection{
				{ID: 2, TemplateID: 42, SectionKey: "workflow", Region: "dynamic", Ordinal: 0, Body: "Workflow body", TriggerType: "keyword", Enabled: true},
				{ID: 3, TemplateID: 42, SectionKey: "recall_sqlc", Region: "dynamic", Ordinal: 1, Body: "Recall pack body must stay hidden", TriggerType: "recall", Enabled: true},
				{ID: 1, TemplateID: 42, SectionKey: "identity", Region: "static", Ordinal: 10, Body: "Identity body", TriggerType: "always", Enabled: true},
			}, nil
		},
	}, nil)

	result, err := handler(promptToolTestContext(), mustRawInput(t, promptGetInput{PromptKey: " custom/prompt "}))
	if err != nil {
		t.Fatalf("HandlePromptGet() error = %v", err)
	}
	got := result.(promptTemplateDTO)
	if got.PromptText != "Identity body\n\nWorkflow body" {
		t.Fatalf("PromptText = %q, want assembled injectable sections", got.PromptText)
	}
	if strings.Contains(got.PromptText, "Recall pack body") {
		t.Fatalf("PromptText leaked recall body: %q", got.PromptText)
	}
}

func TestHandlePromptListKeepsLegacyPromptText(t *testing.T) {
	t.Parallel()

	sectionsQueried := false
	handler := HandlePromptList(stubPromptStore{
		list: func(_ context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			if filter.Keyword != "" {
				t.Fatalf("List() keyword = %q, want empty keyword for tool-side filtering", filter.Keyword)
			}
			if filter.CWD != "/repo/a" || !filter.RuntimeVisible {
				t.Fatalf("List() filter scope = %+v, want runtime-visible /repo/a", filter)
			}
			return []promptstore.PromptTemplate{{
				ID:         42,
				PromptKey:  "custom/prompt",
				Title:      "Custom Prompt",
				AgentKey:   "custom",
				PromptText: "legacy list text",
				Tags:       json.RawMessage(`["scope.cwd:/repo/a"]`),
				Enabled:    true,
			}}, nil
		},
		listSectionsByTemplateID: func(context.Context, int64) ([]promptstore.PromptTemplateSection, error) {
			sectionsQueried = true
			return nil, nil
		},
	}, nil)

	result, err := handler(promptToolTestContext(), mustRawInput(t, promptListInput{Keyword: " custom "}))
	if err != nil {
		t.Fatalf("HandlePromptList() error = %v", err)
	}
	got := result.([]promptTemplateDTO)
	if len(got) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(got))
	}
	if got[0].PromptText != "legacy list text" {
		t.Fatalf("PromptText = %q, want legacy list text", got[0].PromptText)
	}
	if sectionsQueried {
		t.Fatal("HandlePromptList() queried sections; prompt_list must keep shared mapper behavior")
	}
}

func TestHandlePromptListEnvelopeKeepsLegacyDefault(t *testing.T) {
	t.Parallel()

	handler := HandlePromptList(stubPromptStore{
		list: func(_ context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			if filter.Keyword != "" {
				t.Fatalf("List() keyword = %q, want empty keyword for tool-side filtering", filter.Keyword)
			}
			return []promptstore.PromptTemplate{{
				ID:        1,
				PromptKey: "main/reviewer",
				Title:     "Reviewer",
				Enabled:   true,
				Tags:      json.RawMessage(`["scope.global"]`),
			}}, nil
		},
	}, nil)

	legacy, err := handler(promptToolTestContext(), mustRawInput(t, promptListInput{Keyword: " review "}))
	if err != nil {
		t.Fatalf("HandlePromptList() legacy error = %v", err)
	}
	if _, ok := legacy.([]promptTemplateDTO); !ok {
		t.Fatalf("HandlePromptList() legacy response = %T, want []promptTemplateDTO", legacy)
	}

	result, err := handler(promptToolTestContext(), mustRawInput(t, promptListInput{Keyword: " review ", Envelope: true}))
	if err != nil {
		t.Fatalf("HandlePromptList() envelope error = %v", err)
	}
	out, ok := result.(PromptListOutput)
	if !ok {
		t.Fatalf("HandlePromptList() envelope response = %T, want PromptListOutput", result)
	}
	if len(out.Prompts) != 1 {
		t.Fatalf("HandlePromptList() prompts = %#v", out.Prompts)
	}
	assertEnvelopeCounts(t, "HandlePromptList()", len(out.Data), out.Total, out.Showing, out.Truncated, out.Hint)
}

func TestHandlePromptListKeepsTask8RuntimeDiscoveryBoundary(t *testing.T) {
	t.Parallel()

	handler := HandlePromptList(stubPromptStore{
		list: func(_ context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			if filter.CWD != "/repo/a" || !filter.RuntimeVisible {
				t.Fatalf("List() filter scope = %+v, want runtime-visible /repo/a", filter)
			}
			if filter.AgentKey != "" || filter.Keyword != "" {
				t.Fatalf("List() filter = %+v, want no agent/key prefix filter", filter)
			}
			return []promptstore.PromptTemplate{
				{PromptKey: "main/dag_designer_zh", Title: "DAG 流程设计师", Tags: json.RawMessage(`["scope.global","intent:dag_designer"]`), Enabled: true, CreatedBy: "system.seed"},
				{PromptKey: "main/morning_briefer", Title: "企业晨报", Tags: json.RawMessage(`["scope.global","intent:enterprise_workflow"]`), Enabled: true, CreatedBy: "system.seed"},
				{PromptKey: "main/code-generate", Title: "User Code Generate", Tags: json.RawMessage(`["scope.global","intent:expert"]`), Enabled: true, CreatedBy: "rpc.prompts", UpdatedBy: "rpc.prompts"},
			}, nil
		},
	}, nil)

	result, err := handler(promptToolTestContext(), mustRawInput(t, promptListInput{}))
	if err != nil {
		t.Fatalf("HandlePromptList() error = %v", err)
	}
	got := result.([]promptTemplateDTO)
	assertPromptListKeys(t, got,
		[]string{"main/dag_designer_zh", "main/morning_briefer", "main/code-generate"},
		[]string{"main/dag_designer_en", "main/code-refactor", "main/code-test", "main/code-explain", "sql/expert"},
	)
}

func assertPromptListKeys(t *testing.T, got []promptTemplateDTO, wantPresent, wantAbsent []string) {
	t.Helper()
	keys := map[string]bool{}
	for _, item := range got {
		keys[item.PromptKey] = true
	}
	for _, want := range wantPresent {
		if !keys[want] {
			t.Fatalf("prompt_list result missing %q: %#v", want, got)
		}
	}
	for _, absent := range wantAbsent {
		if keys[absent] {
			t.Fatalf("prompt_list result unexpectedly includes %q: %#v", absent, got)
		}
	}
}

func TestHandlePromptToolsRequireTrustedCWD(t *testing.T) {
	t.Parallel()

	listHandler := HandlePromptList(stubPromptStore{
		list: func(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			t.Fatal("List() must not be called without trusted cwd")
			return nil, nil
		},
	}, nil)
	if _, err := listHandler(context.Background(), mustRawInput(t, promptListInput{})); err == nil || !strings.Contains(err.Error(), "trusted cwd") {
		t.Fatalf("HandlePromptList() error = %v, want trusted cwd", err)
	}

	getHandler := HandlePromptGet(stubPromptStore{
		get: func(context.Context, string) (*promptstore.PromptTemplate, error) {
			t.Fatal("Get() must not be called without trusted cwd")
			return nil, nil
		},
	}, nil)
	if _, err := getHandler(context.Background(), mustRawInput(t, promptGetInput{PromptKey: "custom/prompt"})); err == nil || !strings.Contains(err.Error(), "trusted cwd") {
		t.Fatalf("HandlePromptGet() error = %v, want trusted cwd", err)
	}
}

func TestHandlePromptGetHidesOutOfScopeTemplate(t *testing.T) {
	t.Parallel()

	sectionsQueried := false
	handler := HandlePromptGet(stubPromptStore{
		get: func(_ context.Context, key string) (*promptstore.PromptTemplate, error) {
			return &promptstore.PromptTemplate{
				ID:        42,
				PromptKey: key,
				Title:     "Other Repo Prompt",
				Tags:      json.RawMessage(`["scope.cwd:/repo/b"]`),
				Enabled:   true,
			}, nil
		},
		listSectionsByTemplateID: func(context.Context, int64) ([]promptstore.PromptTemplateSection, error) {
			sectionsQueried = true
			return nil, nil
		},
	}, nil)

	_, err := handler(promptToolTestContext(), mustRawInput(t, promptGetInput{PromptKey: "other/prompt"}))
	if err == nil || err.Error() != "prompt other/prompt not found" {
		t.Fatalf("HandlePromptGet() error = %v, want hidden as not found", err)
	}
	if sectionsQueried {
		t.Fatal("HandlePromptGet() queried sections for out-of-scope prompt")
	}
}

func TestHandleCommandListEnvelopeKeepsLegacyDefault(t *testing.T) {
	t.Parallel()

	handler := HandleCommandList(stubCommandStore{
		list: func(_ context.Context, filter commandcardstore.ListFilter) ([]commandcardstore.CommandCard, error) {
			if filter.Keyword != "build" {
				t.Fatalf("List() keyword = %q, want build", filter.Keyword)
			}
			return []commandcardstore.CommandCard{{CardKey: "cmd/build", Title: "Build"}}, nil
		},
	})

	legacy, err := handler(context.Background(), mustRawInput(t, commandListInput{Keyword: " build "}))
	if err != nil {
		t.Fatalf("HandleCommandList() legacy error = %v", err)
	}
	if _, ok := legacy.([]commandCardDTO); !ok {
		t.Fatalf("HandleCommandList() legacy response = %T, want []commandCardDTO", legacy)
	}

	result, err := handler(context.Background(), mustRawInput(t, commandListInput{Keyword: " build ", Envelope: true}))
	if err != nil {
		t.Fatalf("HandleCommandList() envelope error = %v", err)
	}
	out, ok := result.(CommandListOutput)
	if !ok {
		t.Fatalf("HandleCommandList() envelope response = %T, want CommandListOutput", result)
	}
	if len(out.Commands) != 1 {
		t.Fatalf("HandleCommandList() commands = %#v", out.Commands)
	}
	assertEnvelopeCounts(t, "HandleCommandList()", len(out.Data), out.Total, out.Showing, out.Truncated, out.Hint)
}

func TestHandleCommandGetTranslatesNotFound(t *testing.T) {
	var gotKey string
	handler := HandleCommandGet(stubCommandStore{
		get: func(_ context.Context, key string) (*commandcardstore.CommandCard, error) {
			gotKey = key
			return nil, platformdb.ErrNotFound
		},
	})

	_, err := handler(context.Background(), mustRawInput(t, commandGetInput{CardKey: " missing "}))
	if err == nil || err.Error() != "command missing not found" {
		t.Fatalf("HandleCommandGet() error = %v", err)
	}
	if gotKey != "missing" {
		t.Fatalf("HandleCommandGet() card_key = %q, want %q", gotKey, "missing")
	}
}

func TestHandleSharedFileReadNormalizesPathAndTranslatesNotFound(t *testing.T) {
	var gotPath string
	handler := HandleSharedFileRead(stubSharedFileStore{
		get: func(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
			gotPath = path
			return nil, platformdb.ErrNotFound
		},
	})

	_, err := handler(context.Background(), mustRawInput(t, sharedFileReadInput{Path: ` handoff\\task-1\\settings.json\\ `}))
	if err == nil || err.Error() != "file handoff/task-1/settings.json not found" {
		t.Fatalf("HandleSharedFileRead() error = %v", err)
	}
	if gotPath != "handoff/task-1/settings.json" {
		t.Fatalf("HandleSharedFileRead() path = %q, want %q", gotPath, "handoff/task-1/settings.json")
	}
}

func TestHandleSharedFileWriteNormalizesPath(t *testing.T) {
	now := time.Unix(1, 0).UTC()
	var got sharedfilestore.UpsertParams
	handler := HandleSharedFileWrite(stubSharedFileStore{
		upsert: func(_ context.Context, params sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
			got = params
			return &sharedfilestore.SharedFile{
				Path:      params.Path,
				Content:   params.Content,
				UpdatedBy: params.UpdatedBy,
				CreatedAt: now,
				UpdatedAt: now,
			}, nil
		},
	})

	result, err := handler(context.Background(), mustRawInput(t, sharedFileWriteInput{
		Path:    " ./handoff/task-1/../task-1/settings.json ",
		Content: "hello",
	}))
	if err != nil {
		t.Fatalf("HandleSharedFileWrite() error = %v", err)
	}
	if got.Path != "handoff/task-1/settings.json" {
		t.Fatalf("HandleSharedFileWrite() path = %q, want %q", got.Path, "handoff/task-1/settings.json")
	}
	if got.UpdatedBy != sharedFileUpdatedBy {
		t.Fatalf("HandleSharedFileWrite() updated_by = %q, want %q", got.UpdatedBy, sharedFileUpdatedBy)
	}
	dto, ok := result.(sharedFileDTO)
	if !ok {
		t.Fatalf("HandleSharedFileWrite() result type = %T, want sharedFileDTO", result)
	}
	if dto.Path != "handoff/task-1/settings.json" {
		t.Fatalf("HandleSharedFileWrite() result path = %q, want %q", dto.Path, "handoff/task-1/settings.json")
	}
}

func TestHandleSharedFileWriteAllowsExactLimit(t *testing.T) {
	content := strings.Repeat("a", maxSharedFileContentBytes)
	called := false
	handler := HandleSharedFileWrite(stubSharedFileStore{
		upsert: func(_ context.Context, params sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
			called = true
			return &sharedfilestore.SharedFile{
				Path:      params.Path,
				Content:   params.Content,
				UpdatedBy: params.UpdatedBy,
			}, nil
		},
	})

	_, err := handler(context.Background(), mustRawInput(t, sharedFileWriteInput{
		Path:    "handoff/task-1//settings.json/",
		Content: content,
	}))
	if err != nil {
		t.Fatalf("HandleSharedFileWrite() exact limit error = %v", err)
	}
	if !called {
		t.Fatal("HandleSharedFileWrite() did not call Upsert for exact limit content")
	}
}

func TestHandleSharedFileWriteRejectsOversizeContent(t *testing.T) {
	called := false
	handler := HandleSharedFileWrite(stubSharedFileStore{
		upsert: func(context.Context, sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
			called = true
			return nil, nil
		},
	})

	_, err := handler(context.Background(), mustRawInput(t, sharedFileWriteInput{
		Path:    "handoff/task-1/settings.json",
		Content: strings.Repeat("a", maxSharedFileContentBytes+1),
	}))
	if err == nil || err.Error() != "content exceeds 10485760 byte limit" {
		t.Fatalf("HandleSharedFileWrite() error = %v", err)
	}
	if called {
		t.Fatal("HandleSharedFileWrite() unexpectedly called Upsert")
	}
}

func TestHandleSharedFileWriteAllowsNestedUserPrefix(t *testing.T) {
	called := false
	handler := HandleSharedFileWrite(stubSharedFileStore{
		upsert: func(context.Context, sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
			called = true
			return &sharedfilestore.SharedFile{Path: "reports/task-1.md", Content: "agent note"}, nil
		},
	})

	if _, err := handler(context.Background(), mustRawInput(t, sharedFileWriteInput{
		Path:    "reports/task-1.md",
		Content: "agent note",
	})); err != nil {
		t.Fatalf("HandleSharedFileWrite() error = %v", err)
	}
	if !called {
		t.Fatal("HandleSharedFileWrite() did not call Upsert")
	}
}

func TestWorkspaceListRunsEnvelopeKeepsLegacyDefault(t *testing.T) {
	handler := HandleWorkspaceListRuns(stubWorkspaceService{
		listRuns: func(_ context.Context, status, dagKey string, limit int) ([]workspace.Run, error) {
			if status != "active" || dagKey != "dag-1" || limit != 5 {
				t.Fatalf("ListRuns() filter = status:%q dag:%q limit:%d", status, dagKey, limit)
			}
			return []workspace.Run{{RunKey: "ws-1", DagKey: "dag-1", Status: "active"}}, nil
		},
	})

	legacy, err := handler(context.Background(), mustRawInput(t, workspaceListRunsInput{Status: "active", DagKey: "dag-1", Limit: 5}))
	if err != nil {
		t.Fatalf("HandleWorkspaceListRuns() legacy error = %v", err)
	}
	if _, ok := legacy.([]workspaceRunDTO); !ok {
		t.Fatalf("HandleWorkspaceListRuns() legacy response = %T, want []workspaceRunDTO", legacy)
	}

	result, err := handler(context.Background(), mustRawInput(t, workspaceListRunsInput{Status: "active", DagKey: "dag-1", Limit: 5, Envelope: true}))
	if err != nil {
		t.Fatalf("HandleWorkspaceListRuns() envelope error = %v", err)
	}
	out, ok := result.(WorkspaceListRunsOutput)
	if !ok {
		t.Fatalf("HandleWorkspaceListRuns() envelope response = %T, want WorkspaceListRunsOutput", result)
	}
	if len(out.Runs) != 1 {
		t.Fatalf("HandleWorkspaceListRuns() runs = %#v", out.Runs)
	}
	assertEnvelopeCounts(t, "HandleWorkspaceListRuns()", len(out.Data), out.Total, out.Showing, out.Truncated, out.Hint)
}

func TestWorkspaceListRunsLimitSchemaUsesInteger(t *testing.T) {
	defs := workspaceToolDefinitions(nil)
	for _, def := range defs {
		if def.Name != "workspace_list_runs" {
			continue
		}
		props := def.InputSchema["properties"].(map[string]any)
		limit := props["limit"].(map[string]any)
		if limit["type"] != "integer" {
			t.Fatalf("workspace_list_runs limit type = %v, want %q", limit["type"], "integer")
		}
		return
	}
	t.Fatal("workspace_list_runs definition not found")
}

func TestWorkspaceMergeRunResultIncludesFinishedAt(t *testing.T) {
	now := time.Unix(2, 0).UTC()
	payload, err := json.Marshal(WorkspaceMergeRunResult{
		RunKey:     "run-1",
		Status:     "merged",
		FinishedAt: &now,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(payload), `"finished_at":"1970-01-01T00:00:02Z"`) {
		t.Fatalf("workspace merge payload = %s", payload)
	}
}
