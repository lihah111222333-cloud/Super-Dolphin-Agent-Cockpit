package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
)

func TestListSharedFilesDoesNotRequireDAGRuntime(t *testing.T) {
	t.Parallel()

	shared := &stubSharedFileReader{
		result: []sharedfilestore.SharedFile{{Path: "reports/final.md", Content: "final summary"}},
	}
	orchestration := &stubDashboardOrchestration{
		listDAGsErr: errDashboardStub,
	}
	svc := &service{sharedFiles: shared, orchestration: orchestration}

	got, err := svc.ListSharedFiles(context.Background())
	if err != nil {
		t.Fatalf("ListSharedFiles() error = %v", err)
	}
	if len(got) != 1 || got[0].Path != "reports/final.md" {
		t.Fatalf("ListSharedFiles() = %#v", got)
	}
	if orchestration.listDAGsFilter.Limit != 0 {
		t.Fatalf("ListSharedFiles() called DAG runtime with filter %#v", orchestration.listDAGsFilter)
	}
}

func TestDashboardSharedFilesHandlerDoesNotRequireDAGRuntime(t *testing.T) {
	t.Parallel()

	shared := &stubSharedFileReader{
		result: []sharedfilestore.SharedFile{{Path: "reports/final.md", Content: "final summary"}},
	}
	orchestration := &stubDashboardOrchestration{
		listDAGsErr: errDashboardStub,
	}
	svc := &service{sharedFiles: shared, orchestration: orchestration}
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewDashboardHandlers(svc).Handlers)

	result, err := server.Dispatch(context.Background(), "dashboard/sharedFiles", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(dashboard/sharedFiles) error = %v", err)
	}
	var response filesResponse
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(response.Files) != 1 || response.Files[0].Path != "reports/final.md" {
		t.Fatalf("Dispatch(dashboard/sharedFiles) = %#v", response)
	}
	if response.FinalOutputRefs == nil {
		t.Fatalf("Dispatch(dashboard/sharedFiles) did not include finalOutputRefs")
	}
	if response.SharedFileRetention.Items == nil {
		t.Fatalf("Dispatch(dashboard/sharedFiles) did not include sharedFileRetention")
	}
	if orchestration.listDAGsFilter.Limit != 0 {
		t.Fatalf("dashboard/sharedFiles called DAG runtime with filter %#v", orchestration.listDAGsFilter)
	}
}

func TestGetDashboardPageMemorySurfacesMalformedFinalOutputMetadata(t *testing.T) {
	t.Parallel()

	shared := &stubSharedFileReader{
		result: []sharedfilestore.SharedFile{{Path: "reports/daily-brief.pptx", Content: "deck"}},
	}
	orchestration := &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{{DagKey: "dag-1", Title: "Daily Brief"}},
		listRunsResult: contract.ListRunsResponse{Runs: []contract.Run{{
			RunKey:   "run-1",
			DagKey:   "dag-1",
			Status:   "succeeded",
			Metadata: json.RawMessage(`{"final_output":{"sharedfile":"reports/daily-brief.pptx"}}`),
		}}},
	}
	svc := &service{sharedFiles: shared, orchestration: orchestration}

	got, err := svc.GetDashboardPage(context.Background(), "memory")
	if err == nil || !strings.Contains(err.Error(), "final_output") {
		t.Fatalf("GetDashboardPage(memory) error = %v, want final_output metadata error", err)
	}
	if got != nil {
		t.Fatalf("GetDashboardPage(memory) = %#v, want nil page on malformed final_output metadata", got)
	}
}

func TestDashboardSharedFilesHandlerRejectsProjectCWD(t *testing.T) {
	t.Parallel()

	svc := &service{sharedFiles: &stubSharedFileReader{}}
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewDashboardHandlers(svc).Handlers)

	if _, err := server.Dispatch(context.Background(), "dashboard/sharedFiles", json.RawMessage(`{"cwd":"/repo/app"}`)); err == nil {
		t.Fatalf("Dispatch(dashboard/sharedFiles) with cwd succeeded, want invalid params")
	}
}

func TestDashboardWorkflowMaterialWriteStoresUploadUnderWorkflowPrefix(t *testing.T) {
	t.Parallel()

	shared := &stubSharedFileStore{}
	svc := &service{sharedFiles: shared}
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewDashboardHandlers(svc).Handlers)

	result, err := server.Dispatch(context.Background(), "dashboard/workflowMaterialWrite", json.RawMessage(`{
		"path":"reports/workflows/uploads/approval/source/material.md",
		"content":"approval text"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(dashboard/workflowMaterialWrite) error = %v", err)
	}
	var response workflowMaterialWriteResponse
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Path != "reports/workflows/uploads/approval/source/material.md" {
		t.Fatalf("response.Path = %q", response.Path)
	}
	if shared.upserted.Path != response.Path || shared.upserted.Content != "approval text" {
		t.Fatalf("Upsert() params = %#v", shared.upserted)
	}
	if shared.upserted.UpdatedBy != dashboardUICreatedBy {
		t.Fatalf("Upsert() updatedBy = %q, want %q", shared.upserted.UpdatedBy, dashboardUICreatedBy)
	}
}

func TestDashboardWorkflowMaterialWriteRejectsUnsafeOrEmptyPayloads(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload string
	}{
		{
			name:    "outside workflow upload prefix",
			payload: `{"path":"reports/other/material.md","content":"approval text"}`,
		},
		{
			name:    "path cleans outside workflow upload prefix",
			payload: `{"path":"reports/workflows/uploads/../../escape.md","content":"approval text"}`,
		},
		{
			name:    "empty content",
			payload: `{"path":"reports/workflows/uploads/approval/source/material.md","content":"   "}`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := &service{sharedFiles: &stubSharedFileStore{}}
			server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
			server.Register(NewDashboardHandlers(svc).Handlers)

			if _, err := server.Dispatch(context.Background(), "dashboard/workflowMaterialWrite", json.RawMessage(tc.payload)); err == nil {
				t.Fatalf("Dispatch(dashboard/workflowMaterialWrite) succeeded, want error")
			}
		})
	}
}

func TestDashboardWorkflowMaterialWriteRequiresWritableSharedFileStore(t *testing.T) {
	t.Parallel()

	svc := &service{sharedFiles: &stubSharedFileReader{}}
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewDashboardHandlers(svc).Handlers)

	if _, err := server.Dispatch(context.Background(), "dashboard/workflowMaterialWrite", json.RawMessage(`{
		"path":"reports/workflows/uploads/approval/source/material.md",
		"content":"approval text"
	}`)); err == nil {
		t.Fatalf("Dispatch(dashboard/workflowMaterialWrite) succeeded, want missing writer error")
	}
}

type stubSharedFileStore struct {
	stubSharedFileReader
	upserted sharedfilestore.UpsertParams
}

var _ sharedfilestore.Upserter = (*stubSharedFileStore)(nil)

func (s *stubSharedFileStore) Upsert(_ context.Context, params sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
	s.upserted = params
	return &sharedfilestore.SharedFile{
		Path:      params.Path,
		Content:   params.Content,
		UpdatedBy: params.UpdatedBy,
	}, nil
}
