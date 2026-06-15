package datasource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestUploadRPCStoresAllowedFile(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "source.txt")
	if err := os.WriteFile(source, []byte("source data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	server := newDatasourceTestServer()
	payload, err := json.Marshal(map[string]string{
		"sourcePath": source,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "datasource/upload", payload)
	if err != nil {
		t.Fatalf("Dispatch datasource/upload: %v", err)
	}
	var got UploadFileResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.StoredPath != filepath.Join(project, ".agent", "datasources", "uploads", "source.txt") {
		t.Fatalf("StoredPath = %q", got.StoredPath)
	}
}

func TestUploadRPCPersistsTextFileContent(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "notes.md")
	if err := os.WriteFile(source, []byte("# Datasource\nbody"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	store := &recordingDatasourceStore{}
	server := newDatasourceTestServerWithService(NewServiceWithStore(store))
	payload, err := json.Marshal(map[string]string{
		"sourcePath": source,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasource/upload", payload); err != nil {
		t.Fatalf("Dispatch datasource/upload: %v", err)
	}

	if store.upsertCalls != 1 {
		t.Fatalf("store upsert calls = %d, want 1", store.upsertCalls)
	}
	if store.upserted.Name != "notes.md" || store.upserted.Extension != ".md" {
		t.Fatalf("upserted identity = %+v", store.upserted)
	}
	if store.upserted.Content != "# Datasource\nbody" {
		t.Fatalf("Content = %q, want parsed markdown text", store.upserted.Content)
	}
}

func TestUploadRPCRejectsMissingSourcePath(t *testing.T) {
	server := newDatasourceTestServer()
	payload, err := json.Marshal(map[string]string{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasource/upload", payload); err == nil {
		t.Fatalf("Dispatch datasource/upload accepted missing sourcePath")
	}
}

func TestListRPCReturnsDatasourceFileNames(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	uploadDir := filepath.Join(project, ".agent", "datasources", "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}
	for _, name := range []string{"b.txt", "a.pdf"} {
		if err := os.WriteFile(filepath.Join(uploadDir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	server := newDatasourceTestServer()
	payload, err := json.Marshal(map[string]string{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "datasource/list", payload)
	if err != nil {
		t.Fatalf("Dispatch datasource/list: %v", err)
	}
	var got ListFilesResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	want := []string{"a.pdf", "b.txt"}
	if !slices.Equal(got.FileNames, want) {
		t.Fatalf("FileNames = %#v, want %#v", got.FileNames, want)
	}
}

func TestDeleteRPCRemovesDatasourceFile(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	uploadDir := filepath.Join(project, ".agent", "datasources", "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}
	targetPath := filepath.Join(uploadDir, "source.txt")
	if err := os.WriteFile(targetPath, []byte("source data"), 0o600); err != nil {
		t.Fatalf("write datasource file: %v", err)
	}
	server := newDatasourceTestServer()
	payload, err := json.Marshal(map[string]string{
		"fileName": "source.txt",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "datasource/delete", payload)
	if err != nil {
		t.Fatalf("Dispatch datasource/delete: %v", err)
	}
	var got DeleteFileResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Deleted {
		t.Fatalf("Deleted = false, want true")
	}
	if got.Name != "source.txt" {
		t.Fatalf("Name = %q, want source.txt", got.Name)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("deleted file stat error = %v, want not exist", err)
	}
}

func TestDeleteRPCRejectsUnsafeFileName(t *testing.T) {
	server := newDatasourceTestServer()
	payload, err := json.Marshal(map[string]string{
		"fileName": "../outside.txt",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasource/delete", payload); err == nil {
		t.Fatalf("Dispatch datasource/delete accepted unsafe fileName")
	}
}

func newDatasourceTestServer() *platformrpc.Server {
	return newDatasourceTestServerWithService(NewService())
}

func newDatasourceTestServerWithService(svc Service) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	return server
}
