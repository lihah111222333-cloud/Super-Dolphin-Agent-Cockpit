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
	workspacestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

type stubWorkspaceStore struct {
	workspacestore.Store
	getRun func(context.Context, string) (*workspacestore.WorkspaceRun, error)
}

func (s stubWorkspaceStore) GetRun(ctx context.Context, runKey string) (*workspacestore.WorkspaceRun, error) {
	if s.getRun == nil {
		return nil, nil
	}
	return s.getRun(ctx, runKey)
}

func (stubWorkspaceStore) CreateRun(context.Context, WorkspaceCreateRunRequest) (*workspacestore.WorkspaceRun, error) {
	return nil, errors.New("unexpected CreateRun call")
}

func (stubWorkspaceStore) MergeRun(context.Context, WorkspaceMergeRunRequest) (*WorkspaceMergeRunResult, error) {
	return nil, errors.New("unexpected MergeRun call")
}

func (stubWorkspaceStore) AbortRun(context.Context, string, string, string) (*workspacestore.WorkspaceRun, error) {
	return nil, errors.New("unexpected AbortRun call")
}

type stubPromptStore struct {
	promptstore.Store
	get func(context.Context, string) (*promptstore.PromptTemplate, error)
}

func (s stubPromptStore) Get(ctx context.Context, key string) (*promptstore.PromptTemplate, error) {
	if s.get == nil {
		return nil, nil
	}
	return s.get(ctx, key)
}

type stubCommandStore struct {
	commandcardstore.Store
	get func(context.Context, string) (*commandcardstore.CommandCard, error)
}

func (s stubCommandStore) Get(ctx context.Context, key string) (*commandcardstore.CommandCard, error) {
	if s.get == nil {
		return nil, nil
	}
	return s.get(ctx, key)
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

func TestHandleWorkspaceGetRunNilMapsToNotFound(t *testing.T) {
	var gotRunKey string
	handler := HandleWorkspaceGetRun(stubWorkspaceStore{
		getRun: func(_ context.Context, runKey string) (*workspacestore.WorkspaceRun, error) {
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

func TestHandleWorkspaceGetRunWrappedNotFoundMapsToNotFound(t *testing.T) {
	var gotRunKey string
	handler := HandleWorkspaceGetRun(stubWorkspaceStore{
		getRun: func(_ context.Context, runKey string) (*workspacestore.WorkspaceRun, error) {
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
	})

	_, err := handler(context.Background(), mustRawInput(t, promptGetInput{PromptKey: " missing "}))
	if err == nil || err.Error() != "prompt missing not found" {
		t.Fatalf("HandlePromptGet() error = %v", err)
	}
	if gotKey != "missing" {
		t.Fatalf("HandlePromptGet() prompt_key = %q, want %q", gotKey, "missing")
	}
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

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "backslashes", in: `\a\b`, want: "a/b"},
		{name: "duplicate slashes", in: "//a//b/", want: "a/b"},
		{name: "dot segments", in: "./a/../b", want: "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizePath(tt.in); got != tt.want {
				t.Fatalf("normalizePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
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

	_, err := handler(context.Background(), mustRawInput(t, sharedFileReadInput{Path: ` \\config\\settings.json\\ `}))
	if err == nil || err.Error() != "file config/settings.json not found" {
		t.Fatalf("HandleSharedFileRead() error = %v", err)
	}
	if gotPath != "config/settings.json" {
		t.Fatalf("HandleSharedFileRead() path = %q, want %q", gotPath, "config/settings.json")
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
		Path:    " ./config/../config/settings.json ",
		Content: "hello",
	}))
	if err != nil {
		t.Fatalf("HandleSharedFileWrite() error = %v", err)
	}
	if got.Path != "config/settings.json" {
		t.Fatalf("HandleSharedFileWrite() path = %q, want %q", got.Path, "config/settings.json")
	}
	if got.UpdatedBy != sharedFileUpdatedBy {
		t.Fatalf("HandleSharedFileWrite() updated_by = %q, want %q", got.UpdatedBy, sharedFileUpdatedBy)
	}
	dto, ok := result.(sharedFileDTO)
	if !ok {
		t.Fatalf("HandleSharedFileWrite() result type = %T, want sharedFileDTO", result)
	}
	if dto.Path != "config/settings.json" {
		t.Fatalf("HandleSharedFileWrite() result path = %q, want %q", dto.Path, "config/settings.json")
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
		Path:    "//config//settings.json/",
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
		Path:    "config/settings.json",
		Content: strings.Repeat("a", maxSharedFileContentBytes+1),
	}))
	if err == nil || err.Error() != "content exceeds 10485760 byte limit" {
		t.Fatalf("HandleSharedFileWrite() error = %v", err)
	}
	if called {
		t.Fatal("HandleSharedFileWrite() unexpectedly called Upsert")
	}
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
