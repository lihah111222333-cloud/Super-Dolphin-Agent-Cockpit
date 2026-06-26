package datasourcev2

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	datasourcev2store "github.com/anthropic-ai/super-agent-v3/internal/store/datasourcev2"
)

func TestImportTextRPCStoresFileChunks(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	source := filepath.Join(project, "notes.txt")
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
	assertDatasourceV2InsertedVector(t, store.inserted[0], 2)
}

func TestImportTextRPCSplitsEvery256Tokens(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	source := filepath.Join(project, "tokens.txt")
	if err := os.WriteFile(source, []byte(strings.Repeat("word ", 257)), 0o600); err != nil {
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
	if got.ChunkCount != 2 || len(store.inserted) != 2 {
		t.Fatalf("chunk count = result %d inserted %d, want 2", got.ChunkCount, len(store.inserted))
	}
	assertDatasourceV2InsertedVector(t, store.inserted[0], 256)
	assertDatasourceV2InsertedVector(t, store.inserted[1], 1)
}

func TestCreateRPCAliasesImportText(t *testing.T) {
	source := datasourceV2RPCWorkspaceSource(t, "create.txt", []byte("created"))
	store := newRecordingDatasourceV2Store()
	server := newDatasourceV2TestServer(NewService(store))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: source})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "datasourceV2/create", payload)
	if err != nil {
		t.Fatalf("Dispatch datasourceV2/create: %v", err)
	}

	var got ImportFileTextResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.DocumentID != 101 || got.Status != datasourcev2store.StatusReady {
		t.Fatalf("create result = %+v", got)
	}
}

func TestCreateRPCStoresExtractedPDFChunks(t *testing.T) {
	source := datasourceV2RPCWorkspaceSource(t, "manual.pdf", minimalPDFWithText("Hello PDF datasource"))
	store := newRecordingDatasourceV2Store()
	server := newDatasourceV2TestServer(NewService(store))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: source})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "datasourceV2/create", payload)
	if err != nil {
		t.Fatalf("Dispatch datasourceV2/create: %v", err)
	}

	var got ImportFileTextResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Extension != ".pdf" || got.ChunkCount != 1 || got.TotalChars != int32(len("Hello PDF datasource")) {
		t.Fatalf("PDF import result = %+v", got)
	}
	if store.inserted[0].Content != "Hello PDF datasource" {
		t.Fatalf("stored PDF chunk = %q, want extracted text", store.inserted[0].Content)
	}
}

func TestCreateRPCRejectsUnsupportedExtension(t *testing.T) {
	source := datasourceV2RPCWorkspaceSource(t, "manual.docx", []byte("not supported"))
	server := newDatasourceV2TestServer(NewService(newRecordingDatasourceV2Store()))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: source})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasourceV2/create", payload); err == nil {
		t.Fatal("Dispatch accepted unsupported extension")
	}
}

func TestListDocumentsRPCUsesStoreFilter(t *testing.T) {
	t.Parallel()

	store := newReadyDatasourceV2Store()
	server := newDatasourceV2TestServer(NewService(store))
	listRaw, err := server.Dispatch(context.Background(), "datasourceV2/list", json.RawMessage(`{"keyword":"source","limit":20}`))
	if err != nil {
		t.Fatalf("Dispatch datasourceV2/list: %v", err)
	}
	var listGot ListDocumentsResult
	if err := json.Unmarshal(listRaw, &listGot); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(listGot.Documents) != 1 || listGot.Documents[0].DocumentID != 101 {
		t.Fatalf("list result = %+v", listGot)
	}
	if store.listKeyword != "source" || store.listLimit != 20 {
		t.Fatalf("list params = (%q, %d), want (source, 20)", store.listKeyword, store.listLimit)
	}
}

func TestListDocumentsRPCRejectsOversizedLimit(t *testing.T) {
	store := newReadyDatasourceV2Store()
	server := newDatasourceV2TestServer(NewService(store))
	_, err := server.Dispatch(context.Background(), "datasourceV2/list", json.RawMessage(`{"keyword":"source","limit":1001}`))
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("Dispatch datasourceV2/list error = %v, want oversized limit rejection", err)
	}
	if store.listLimit != 0 {
		t.Fatalf("store list limit = %d, want no store call after oversized limit", store.listLimit)
	}
}

func TestGetDocumentRPCReturnsChunks(t *testing.T) {
	t.Parallel()

	store := newReadyDatasourceV2Store()
	server := newDatasourceV2TestServer(NewService(store))
	getRaw, err := server.Dispatch(context.Background(), "datasourceV2/get", json.RawMessage(`{"documentId":101}`))
	if err != nil {
		t.Fatalf("Dispatch datasourceV2/get: %v", err)
	}
	var getGot GetDocumentResult
	if err := json.Unmarshal(getRaw, &getGot); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	if getGot.Document.DocumentID != 101 || len(getGot.Chunks) != 1 || getGot.Chunks[0].Content != "content" {
		t.Fatalf("get result = %+v", getGot)
	}
	if getGot.Chunks[0].EmbeddingModel != datasourceV2EmbeddingModel || getGot.Chunks[0].EmbeddingDim != datasourceV2EmbeddingDimension {
		t.Fatalf("chunk vector metadata = %+v", getGot.Chunks[0])
	}
}

func TestUpdateDocumentRPCPersistsMetadata(t *testing.T) {
	store := newReadyDatasourceV2Store()
	server := newDatasourceV2TestServer(NewService(store))
	sourcePath := datasourceV2RPCWorkspaceSource(t, "updated.text", []byte("updated"))
	payload, err := json.Marshal(UpdateDocumentRequest{
		DocumentID: 101,
		SourcePath: sourcePath,
		FileName:   "updated.text",
		Extension:  ".text",
		SizeBytes:  99,
	})
	if err != nil {
		t.Fatalf("marshal update payload: %v", err)
	}
	updateRaw, err := server.Dispatch(context.Background(), "datasourceV2/update", payload)
	if err != nil {
		t.Fatalf("Dispatch datasourceV2/update: %v", err)
	}
	var updateGot DocumentResult
	if err := json.Unmarshal(updateRaw, &updateGot); err != nil {
		t.Fatalf("unmarshal update: %v", err)
	}
	if updateGot.DocumentID != 101 || updateGot.SourcePath != sourcePath {
		t.Fatalf("update result = %+v", updateGot)
	}
	if store.updated.SourcePath != sourcePath || store.updated.SizeBytes != 99 {
		t.Fatalf("update params = %+v", store.updated)
	}
}

func TestDeleteDocumentRPCReturnsDocumentID(t *testing.T) {
	t.Parallel()

	store := newReadyDatasourceV2Store()
	server := newDatasourceV2TestServer(NewService(store))
	deleteRaw, err := server.Dispatch(context.Background(), "datasourceV2/delete", json.RawMessage(`{"documentId":101}`))
	if err != nil {
		t.Fatalf("Dispatch datasourceV2/delete: %v", err)
	}
	var deleteGot DeleteDocumentResult
	if err := json.Unmarshal(deleteRaw, &deleteGot); err != nil {
		t.Fatalf("unmarshal delete: %v", err)
	}
	if !deleteGot.Deleted || deleteGot.DocumentID != 101 || store.deletedID != 101 {
		t.Fatalf("delete result = %+v deletedID=%d", deleteGot, store.deletedID)
	}
}

func TestImportTextRPCRejectsRelativePath(t *testing.T) {
	server := newDatasourceV2TestServer(NewService(newRecordingDatasourceV2Store()))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: "notes.txt"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasourceV2/importText", payload); err == nil {
		t.Fatal("Dispatch accepted relative sourcePath")
	}
}

func TestImportTextRPCRejectsSourceOutsideWorkspace(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	source := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(source, []byte("outside workspace"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	server := newDatasourceV2TestServer(NewService(newRecordingDatasourceV2Store()))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: source})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasourceV2/importText", payload); err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("Dispatch error = %v, want outside workspace rejection", err)
	}
}

func TestImportTextRPCRejectsSymlinkEscapingWorkspace(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	outside := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(outside, []byte("outside workspace through symlink"), 0o600); err != nil {
		t.Fatalf("write outside source: %v", err)
	}
	link := filepath.Join(project, "linked.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	server := newDatasourceV2TestServer(NewService(newRecordingDatasourceV2Store()))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: link})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasourceV2/importText", payload); err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("Dispatch error = %v, want symlink outside workspace rejection", err)
	}
}

func TestExtractPDFTextRejectsOversizedFlateStream(t *testing.T) {
	source := filepath.Join(t.TempDir(), "large.pdf")
	if err := os.WriteFile(source, compressedPDFWithText(t, strings.Repeat("x", 10*1024*1024+1)), 0o600); err != nil {
		t.Fatalf("write compressed pdf: %v", err)
	}

	_, err := extractPDFText(source)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("extractPDFText() error = %v, want decompressed size rejection", err)
	}
}

func TestImportLocalFileRPCStoresOutsideWorkspaceSource(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	source := filepath.Join(t.TempDir(), "fj.txt")
	if err := os.WriteFile(source, []byte("outside workspace datasource"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	store := newRecordingDatasourceV2Store()
	server := newDatasourceV2TestServer(NewService(store))
	payload, err := json.Marshal(ImportLocalFileRequest{SourcePath: source})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	raw, err := server.Dispatch(context.Background(), "datasourceV2/importLocalFile", payload)
	if err != nil {
		t.Fatalf("Dispatch datasourceV2/importLocalFile: %v", err)
	}

	var got ImportFileTextResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.SourcePath != source || got.FileName != "fj.txt" || got.ChunkCount != 1 {
		t.Fatalf("import local file result = %+v", got)
	}
	if len(store.inserted) != 1 || store.inserted[0].Content != "outside workspace datasource" {
		t.Fatalf("stored chunks = %+v", store.inserted)
	}
}

func TestImportTextRPCPreservesWhitespaceOnlyContent(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	source := filepath.Join(project, "blank.txt")
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
	assertDatasourceV2InsertedVector(t, store.inserted[0], 0)
}

func newDatasourceV2TestServer(svc Service) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	return server
}

func datasourceV2RPCWorkspaceSource(t *testing.T, name string, body []byte) string {
	t.Helper()
	project := t.TempDir()
	t.Chdir(project)
	source := filepath.Join(project, name)
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return source
}

type recordingDatasourceV2Store struct {
	document datasourcev2store.Document
	inserted []datasourcev2store.InsertChunkParams
	chunks   []datasourcev2store.TextChunk
	updated  datasourcev2store.UpdateDocumentParams

	listKeyword string
	listLimit   int32
	deletedID   int64
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

func newReadyDatasourceV2Store() *recordingDatasourceV2Store {
	store := newRecordingDatasourceV2Store()
	store.document.SourcePath = "/tmp/source.txt"
	store.document.FileName = "source.txt"
	store.document.Extension = ".txt"
	store.document.SizeBytes = 7
	store.document.Status = datasourcev2store.StatusReady
	store.document.ChunkCount = 1
	store.document.TotalChars = 7
	store.document.ContentHash = "sha256:abc"
	store.chunks = []datasourcev2store.TextChunk{{
		ID:             501,
		DocumentID:     101,
		ChunkIndex:     0,
		Content:        "content",
		CharCount:      7,
		ByteCount:      7,
		Embedding:      make([]byte, datasourceV2EmbeddingBytes),
		EmbeddingModel: datasourceV2EmbeddingModel,
		EmbeddingDim:   datasourceV2EmbeddingDimension,
		TokenCount:     1,
	}}
	return store
}

func assertDatasourceV2InsertedVector(t *testing.T, chunk datasourcev2store.InsertChunkParams, wantTokens int32) {
	t.Helper()
	if len(chunk.Embedding) != datasourceV2EmbeddingBytes {
		t.Fatalf("embedding bytes = %d, want %d", len(chunk.Embedding), datasourceV2EmbeddingBytes)
	}
	if chunk.EmbeddingModel != datasourceV2EmbeddingModel {
		t.Fatalf("embedding model = %q, want %q", chunk.EmbeddingModel, datasourceV2EmbeddingModel)
	}
	if chunk.EmbeddingDim != datasourceV2EmbeddingDimension {
		t.Fatalf("embedding dim = %d, want %d", chunk.EmbeddingDim, datasourceV2EmbeddingDimension)
	}
	if chunk.TokenCount != wantTokens {
		t.Fatalf("token count = %d, want %d", chunk.TokenCount, wantTokens)
	}
}

func (s *recordingDatasourceV2Store) WithTx(ctx context.Context, fn func(datasourcev2store.Store) error) error {
	return fn(s)
}

func (s *recordingDatasourceV2Store) UpsertImporting(
	_ context.Context,
	params datasourcev2store.UpsertDocumentParams,
) (*datasourcev2store.Document, error) {
	doc := s.document
	doc.SourcePath = params.SourcePath
	doc.FileName = params.FileName
	doc.Extension = params.Extension
	doc.SizeBytes = params.SizeBytes
	s.document = doc
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

func (s *recordingDatasourceV2Store) ListDocuments(
	_ context.Context,
	params datasourcev2store.ListDocumentsParams,
) ([]datasourcev2store.Document, error) {
	s.listKeyword = params.Keyword
	s.listLimit = params.Limit
	return []datasourcev2store.Document{s.document}, nil
}

func (s *recordingDatasourceV2Store) GetDocument(context.Context, int64) (*datasourcev2store.Document, error) {
	doc := s.document
	return &doc, nil
}

func (s *recordingDatasourceV2Store) ListChunks(context.Context, int64) ([]datasourcev2store.TextChunk, error) {
	return append([]datasourcev2store.TextChunk(nil), s.chunks...), nil
}

func (s *recordingDatasourceV2Store) SearchChunks(
	context.Context,
	datasourcev2store.SearchChunksParams,
) ([]datasourcev2store.SemanticChunk, error) {
	return nil, errors.New("unexpected datasource_v2 RPC test semantic search")
}

func (s *recordingDatasourceV2Store) UpdateDocument(
	_ context.Context,
	params datasourcev2store.UpdateDocumentParams,
) (*datasourcev2store.Document, error) {
	s.updated = params
	doc := s.document
	doc.ID = params.DocumentID
	doc.SourcePath = params.SourcePath
	doc.FileName = params.FileName
	doc.Extension = params.Extension
	doc.SizeBytes = params.SizeBytes
	return &doc, nil
}

func (s *recordingDatasourceV2Store) DeleteDocument(_ context.Context, documentID int64) error {
	s.deletedID = documentID
	return nil
}

func minimalPDFWithText(text string) []byte {
	body := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	return []byte("%PDF-1.4\n" +
		"1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n" +
		"2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n" +
		"3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >> endobj\n" +
		"4 0 obj << /Length 0 >> stream\n" + body + "\nendstream endobj\n" +
		"5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj\n" +
		"trailer << /Root 1 0 R >>\n%%EOF\n")
}

func compressedPDFWithText(t *testing.T, text string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write([]byte("BT (" + text + ") Tj ET")); err != nil {
		t.Fatalf("compress pdf stream: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zlib writer: %v", err)
	}
	return []byte("%PDF-1.4\n" +
		"1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n" +
		"2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n" +
		"3 0 obj << /Type /Page /Parent 2 0 R /Contents 4 0 R >> endobj\n" +
		"4 0 obj << /Filter /FlateDecode /Length 0 >> stream\n" +
		compressed.String() + "\nendstream endobj\n" +
		"trailer << /Root 1 0 R >>\n%%EOF\n")
}
