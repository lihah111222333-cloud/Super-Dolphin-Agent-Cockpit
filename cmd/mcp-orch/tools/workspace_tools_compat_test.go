package tools

import (
	"context"
	"testing"

	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

type compatWorkspaceServiceStub struct {
	stubWorkspaceService
	mergeRun func(context.Context, workspace.MergeRunRequest) (*workspace.MergeRunResult, error)
	files    []workspace.RunFile
}

func (s compatWorkspaceServiceStub) MergeRun(ctx context.Context, req workspace.MergeRunRequest) (*workspace.MergeRunResult, error) {
	if s.mergeRun == nil {
		return s.stubWorkspaceService.MergeRun(ctx, req)
	}
	return s.mergeRun(ctx, req)
}

func (s compatWorkspaceServiceStub) ListRunFiles(context.Context, string, string) ([]workspace.RunFile, error) {
	return append([]workspace.RunFile(nil), s.files...), nil
}

func TestHandleWorkspaceGetRunIncludesV2CompatFields(t *testing.T) {
	t.Parallel()

	handler := HandleWorkspaceGetRun(compatWorkspaceServiceStub{
		stubWorkspaceService: stubWorkspaceService{
			getRun: func(context.Context, string) (*workspace.Run, error) {
				return &workspace.Run{
					RunKey:        "run-1",
					Status:        "created",
					SourceRoot:    "/tmp/source",
					WorkspacePath: "/tmp/workspaces/run-1",
				}, nil
			},
		},
		files: []workspace.RunFile{
			{RelativePath: "b.txt"},
			{RelativePath: "a.txt"},
		},
	})

	result, err := handler(context.Background(), mustRawInput(t, workspaceGetRunInput{RunKey: "run-1"}))
	if err != nil {
		t.Fatalf("HandleWorkspaceGetRun() error = %v", err)
	}
	dto, ok := result.(*workspaceRunDTO)
	if !ok {
		t.Fatalf("HandleWorkspaceGetRun() result type = %T, want *workspaceRunDTO", result)
	}
	if dto.WorkspaceRoot != "/tmp/workspaces/run-1" {
		t.Fatalf("workspace_root = %q, want %q", dto.WorkspaceRoot, "/tmp/workspaces/run-1")
	}
	if len(dto.Files) != 2 || dto.Files[0] != "a.txt" || dto.Files[1] != "b.txt" {
		t.Fatalf("files = %#v, want sorted run files", dto.Files)
	}
}

func TestHandleWorkspaceMergeRunIncludesV2CompatFields(t *testing.T) {
	t.Parallel()

	handler := HandleWorkspaceMergeRun(compatWorkspaceServiceStub{
		mergeRun: func(context.Context, workspace.MergeRunRequest) (*workspace.MergeRunResult, error) {
			return &workspace.MergeRunResult{
				RunKey:        "run-1",
				Status:        "merged",
				SourceRoot:    "/tmp/source",
				WorkspacePath: "/tmp/workspaces/run-1",
				DryRun:        true,
				Merged:        2,
			}, nil
		},
	})

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: t.TempDir()})
	result, err := handler(ctx, mustRawInput(t, WorkspaceMergeRunRequest{
		RunKey:        "run-1",
		DryRun:        true,
		DeleteRemoved: true,
	}))
	if err != nil {
		t.Fatalf("HandleWorkspaceMergeRun() error = %v", err)
	}
	dto, ok := result.(*WorkspaceMergeRunResult)
	if !ok {
		t.Fatalf("HandleWorkspaceMergeRun() result type = %T, want *WorkspaceMergeRunResult", result)
	}
	if dto.WorkspaceRoot != "/tmp/workspaces/run-1" {
		t.Fatalf("workspace_root = %q, want %q", dto.WorkspaceRoot, "/tmp/workspaces/run-1")
	}
	if !dto.DeleteRemoved {
		t.Fatal("delete_removed = false, want true")
	}
	if dto.FilesMerged != 2 || dto.Merged != 2 {
		t.Fatalf("files_merged/merged = %d/%d, want 2/2", dto.FilesMerged, dto.Merged)
	}
}
