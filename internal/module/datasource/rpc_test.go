package datasource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestUploadRPCStoresAllowedFile(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(source, []byte("source data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	server := newDatasourceTestServer()
	payload, err := json.Marshal(map[string]string{
		"cwd":        project,
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

func TestUploadRPCRejectsMissingSourcePath(t *testing.T) {
	server := newDatasourceTestServer()
	payload, err := json.Marshal(map[string]string{"cwd": t.TempDir()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasource/upload", payload); err == nil {
		t.Fatalf("Dispatch datasource/upload accepted missing sourcePath")
	}
}

func newDatasourceTestServer() *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(NewService()).Handlers)
	return server
}
