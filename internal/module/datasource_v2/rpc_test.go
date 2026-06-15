package datasourcev2

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	datasourcev2store "github.com/anthropic-ai/super-agent-v3/internal/store/datasourcev2"
)

func TestImportTextRPCStoresFileChunks(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(source, []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	store := newRecordingDatasourceV2Store()
	server := newDatasourceV2TestServer(NewService(store))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: source})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "datasourceV2/importText", payload)
	if err != nil {
		t.Fatalf("Dispatch datasourceV2/importText: %v", err)
	}

	var got ImportFileTextResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.DocumentID != 101 {
		t.Fatalf("DocumentID = %d, want 101", got.DocumentID)
	}
	if got.ChunkCount != 1 || got.TotalChars != 12 {
		t.Fatalf("chunk summary = (%d, %d), want (1, 12)", got.ChunkCount, got.TotalChars)
	}
	if got.ContentHash == "" {
		t.Fatal("ContentHash is empty")
	}
	if store.inserted[0].Content != "hello\nworld\n" {
		t.Fatalf("stored chunk content = %q", store.inserted[0].Content)
	}
}

func TestImportTextRPCRejectsRelativePath(t *testing.T) {
	t.Parallel()

	server := newDatasourceV2TestServer(NewService(newRecordingDatasourceV2Store()))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: "notes.txt"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasourceV2/importText", payload); err == nil {
		t.Fatal("Dispatch accepted relative sourcePath")
	}
}

func TestImportTextRPCPreservesWhitespaceOnlyContent(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "blank.txt")
	if err := os.WriteFile(source, []byte(" \n\t"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	store := newRecordingDatasourceV2Store()
	server := newDatasourceV2TestServer(NewService(store))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: source})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "datasourceV2/importText", payload)
	if err != nil {
		t.Fatalf("Dispatch datasourceV2/importText: %v", err)
	}

	var got ImportFileTextResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.TotalChars != 3 {
		t.Fatalf("TotalChars = %d, want 3", got.TotalChars)
	}
	if store.inserted[0].Content != " \n\t" {
		t.Fatalf("stored whitespace chunk = %q", store.inserted[0].Content)
	}
}

func newDatasourceV2TestServer(svc Service) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	return server
}

type recordingDatasourceV2Store struct {
	document datasourcev2store.Document
	inserted []datasourcev2store.InsertChunkParams
}

func newRecordingDatasourceV2Store() *recordingDatasourceV2Store {
	return &recordingDatasourceV2Store{
		document: datasourcev2store.Document{
			ID:         101,
			SourcePath: "placeholder",
			FileName:   "placeholder.txt",
			Extension:  ".txt",
			SizeBytes:  12,
			Status:     datasourcev2store.StatusImporting,
		},
	}
}

func (s *recordingDatasourceV2Store) WithTx(ctx context.Context, fn func(datasourcev2store.Store) error) error {
	return fn(s)
}

func (s *recordingDatasourceV2Store) UpsertImporting(
	context.Context,
	datasourcev2store.UpsertDocumentParams,
) (*datasourcev2store.Document, error) {
	doc := s.document
	return &doc, nil
}

func (s *recordingDatasourceV2Store) DeleteChunks(context.Context, int64) error {
	s.inserted = nil
	return nil
}

func (s *recordingDatasourceV2Store) InsertChunk(_ context.Context, params datasourcev2store.InsertChunkParams) error {
	s.inserted = append(s.inserted, params)
	return nil
}

func (s *recordingDatasourceV2Store) MarkReady(
	_ context.Context,
	params datasourcev2store.MarkReadyParams,
) (*datasourcev2store.Document, error) {
	doc := s.document
	doc.Status = datasourcev2store.StatusReady
	doc.ContentHash = params.ContentHash
	doc.ChunkCount = params.ChunkCount
	doc.TotalChars = params.TotalChars
	return &doc, nil
}
