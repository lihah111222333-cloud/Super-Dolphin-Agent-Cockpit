package datasourcev2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestImportTextRPCStoresFileChunks(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	source := filepath.Join(project, "notes.txt")
	if err := os.WriteFile(source, []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	store := newRecordingDatasourceV2Store()
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
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
	if got.DocumentID != 101 || got.Status != "ready" {
		t.Fatalf("create result = %+v", got)
	}
}

func TestCreateRPCStoresExtractedPDFChunks(t *testing.T) {
	source := datasourceV2RPCWorkspaceSource(t, "manual.pdf", minimalPDFWithText("Hello PDF datasource"))
	store := newRecordingDatasourceV2Store()
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(newRecordingDatasourceV2Store()))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(newRecordingDatasourceV2Store()))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(newRecordingDatasourceV2Store()))
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
	server := newDatasourceV2TestServer(newDatasourceV2TestService(newRecordingDatasourceV2Store()))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: link})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasourceV2/importText", payload); err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("Dispatch error = %v, want symlink outside workspace rejection", err)
	}
}

func TestImportLocalFileRejectsWorkspaceOutsidePathWithoutPickerCapability(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	source := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(source, []byte("outside workspace datasource"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	store := newRecordingDatasourceV2Store()
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
	payload, err := json.Marshal(ImportLocalFileRequest{SourcePath: source})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if _, err := server.Dispatch(context.Background(), "datasourceV2/importLocalFile", payload); err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("Dispatch error = %v, want outside workspace rejection without picker token", err)
	}
	if len(store.inserted) != 0 {
		t.Fatalf("store inserted chunks after rejected local import: %+v", store.inserted)
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
	verifier := &recordingLocalFilePickerTokenVerifier{
		sourcePath: source,
		token:      "picker-token",
	}
	server := newDatasourceV2TestServerWithPickerVerifier(newDatasourceV2TestService(store), verifier)
	payload, err := json.Marshal(ImportLocalFileRequest{
		SourcePath:  source,
		PickerToken: "picker-token",
	})
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
	assertDatasourceV2LocalFileImportResult(t, got, source, "fj.txt")
	assertDatasourceV2StoredText(t, store, "outside workspace datasource")
	if verifier.calls != 1 {
		t.Fatalf("picker token verifier calls = %d, want 1", verifier.calls)
	}
}

func TestImportTextRPCRejectsWhitespaceOnlyContent(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	source := filepath.Join(project, "blank.txt")
	if err := os.WriteFile(source, []byte(" \n\t"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	store := newRecordingDatasourceV2Store()
	server := newDatasourceV2TestServer(newDatasourceV2TestService(store))
	payload, err := json.Marshal(ImportFileTextRequest{SourcePath: source})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	_, err = server.Dispatch(context.Background(), "datasourceV2/importText", payload)
	if err == nil || !strings.Contains(err.Error(), "visible body is empty") {
		t.Fatalf("Dispatch error = %v, want visible body rejection", err)
	}
	if len(store.inserted) != 0 {
		t.Fatalf("inserted chunks = %d, want 0", len(store.inserted))
	}
}

func newDatasourceV2TestServer(svc Service) *platformrpc.Server {
	return newDatasourceV2TestServerWithPickerVerifier(svc, nil)
}

func newDatasourceV2TestServerWithPickerVerifier(svc Service, verifier LocalFilePickerTokenVerifier) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc, verifier).Handlers)
	return server
}

func newDatasourceV2TestService(store Store) Service {
	return NewService(store)
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

func assertDatasourceV2LocalFileImportResult(t *testing.T, got ImportFileTextResult, source string, fileName string) {
	t.Helper()
	if got.SourcePath != source || got.FileName != fileName || got.ChunkCount != 1 {
		t.Fatalf("import local file result = %+v", got)
	}
}

func assertDatasourceV2StoredText(t *testing.T, store *recordingDatasourceV2Store, content string) {
	t.Helper()
	if len(store.inserted) != 1 || store.inserted[0].Content != content {
		t.Fatalf("stored chunks = %+v", store.inserted)
	}
}

type recordingLocalFilePickerTokenVerifier struct {
	sourcePath string
	token      string
	calls      int
}

func (v *recordingLocalFilePickerTokenVerifier) VerifyDatasourceImportPickerToken(sourcePath, token string) bool {
	v.calls++
	return sourcePath == v.sourcePath && token == v.token
}

type recordingDatasourceV2Store struct {
	recordingDatasourceV2SemanticNoopStore

	document Document
	inserted []InsertChunkParams
	chunks   []TextChunk
	updated  UpdateDocumentParams

	listKeyword string
	listLimit   int32
	deletedID   int64
	withTxCalls int
}

func newRecordingDatasourceV2Store() *recordingDatasourceV2Store {
	return &recordingDatasourceV2Store{
		document: Document{
			ID:         101,
			SourcePath: "placeholder",
			FileName:   "placeholder.txt",
			Extension:  ".txt",
			SizeBytes:  12,
			Status:     "importing",
		},
	}
}

func newReadyDatasourceV2Store() *recordingDatasourceV2Store {
	store := newRecordingDatasourceV2Store()
	store.document.SourcePath = "/tmp/source.txt"
	store.document.FileName = "source.txt"
	store.document.Extension = ".txt"
	store.document.SizeBytes = 7
	store.document.Status = "ready"
	store.document.ChunkCount = 1
	store.document.TotalChars = 7
	store.document.ContentHash = "sha256:abc"
	store.chunks = []TextChunk{{
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

func assertDatasourceV2InsertedVector(t *testing.T, chunk InsertChunkParams, wantTokens int32) {
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

func (s *recordingDatasourceV2Store) WithTx(ctx context.Context, fn func(Store) error) error {
	s.withTxCalls++
	return fn(s)
}

func (s *recordingDatasourceV2Store) UpsertImporting(
	_ context.Context,
	params UpsertDocumentParams,
) (*Document, error) {
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

func (s *recordingDatasourceV2Store) InsertChunk(_ context.Context, params InsertChunkParams) error {
	s.inserted = append(s.inserted, params)
	return nil
}

func (s *recordingDatasourceV2Store) MarkReady(
	_ context.Context,
	params MarkReadyParams,
) (*Document, error) {
	doc := s.document
	doc.Status = "ready"
	doc.ContentHash = params.ContentHash
	doc.ChunkCount = params.ChunkCount
	doc.TotalChars = params.TotalChars
	doc.QualityStatus = params.QualityStatus
	doc.QualityReason = params.QualityReason
	doc.ExtractorName = params.ExtractorName
	doc.ExtractorVersion = params.ExtractorVersion
	doc.PageCount = params.PageCount
	doc.RuneCount = params.RuneCount
	doc.VisibleRunes = params.VisibleRunes
	doc.ControlRunes = params.ControlRunes
	doc.NULRunes = params.NULRunes
	doc.ReplacementRunes = params.ReplacementRunes
	doc.UnmappedFonts = params.UnmappedFonts
	return &doc, nil
}

func (s *recordingDatasourceV2Store) ListDocuments(
	_ context.Context,
	params ListDocumentsParams,
) ([]Document, error) {
	s.listKeyword = params.Keyword
	s.listLimit = params.Limit
	return []Document{s.document}, nil
}

func (s *recordingDatasourceV2Store) GetDocument(context.Context, int64) (*Document, error) {
	doc := s.document
	return &doc, nil
}

func (s *recordingDatasourceV2Store) ListChunksPage(
	_ context.Context,
	params ListChunksParams,
) (TextChunkPage, error) {
	limit := int(params.Limit)
	pageChunks := make([]TextChunk, 0, limit+1)
	for _, chunk := range s.chunks {
		if chunk.DocumentID == params.DocumentID && chunk.ChunkIndex > params.Cursor {
			pageChunks = append(pageChunks, chunk)
		}
		if len(pageChunks) >= limit+1 {
			break
		}
	}
	hasMore := len(pageChunks) > limit
	if hasMore {
		pageChunks = pageChunks[:limit]
	}
	nextCursor := int32(0)
	if hasMore && len(pageChunks) > 0 {
		nextCursor = pageChunks[len(pageChunks)-1].ChunkIndex
	}
	return TextChunkPage{
		Chunks:     append([]TextChunk(nil), pageChunks...),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

type recordingDatasourceV2SemanticNoopStore struct{}

func (recordingDatasourceV2SemanticNoopStore) SearchChunks(
	context.Context,
	SearchChunksParams,
) ([]SemanticChunk, error) {
	return nil, errors.New("unexpected datasource_v2 RPC test semantic search")
}

func (s *recordingDatasourceV2Store) UpdateDocument(
	_ context.Context,
	params UpdateDocumentParams,
) (*Document, error) {
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
	return buildDatasourceV2TestPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(body), body),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
}

func buildDatasourceV2TestPDF(objects []string) []byte {
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return output.Bytes()
}
