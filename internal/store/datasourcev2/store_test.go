package datasourcev2

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

type datasourceV2QuerierStub struct {
	upsertImportingFn func(context.Context, sqlc.UpsertDatasourceV2DocumentImportingParams) (sqlc.DatasourceV2Document, error)
	deleteChunksFn    func(context.Context, sqlc.DeleteDatasourceV2ChunksByDocumentIDParams) (int64, error)
	insertChunkFn     func(context.Context, sqlc.InsertDatasourceV2ChunkParams) error
	markReadyFn       func(context.Context, sqlc.MarkDatasourceV2DocumentReadyParams) (sqlc.DatasourceV2Document, error)
	listDocumentsFn   func(context.Context, sqlc.ListDatasourceV2DocumentsParams) ([]sqlc.DatasourceV2Document, error)
	getDocumentFn     func(context.Context, sqlc.GetDatasourceV2DocumentParams) (sqlc.DatasourceV2Document, error)
	listChunksPageFn  func(context.Context, sqlc.ListDatasourceV2ChunksPageParams) ([]sqlc.ListDatasourceV2ChunksPageRow, error)
	searchChunksFn    func(context.Context, sqlc.SearchDatasourceV2ChunksByEmbeddingParams) ([]sqlc.SearchDatasourceV2ChunksByEmbeddingRow, error)
	updateDocumentFn  func(context.Context, sqlc.UpdateDatasourceV2DocumentMetadataParams) (sqlc.DatasourceV2Document, error)
	deleteDocumentFn  func(context.Context, sqlc.DeleteDatasourceV2DocumentParams) (int64, error)
}

func (s *datasourceV2QuerierStub) UpsertDatasourceV2DocumentImporting(
	ctx context.Context,
	arg sqlc.UpsertDatasourceV2DocumentImportingParams,
) (sqlc.DatasourceV2Document, error) {
	return s.upsertImportingFn(ctx, arg)
}

func (s *datasourceV2QuerierStub) DeleteDatasourceV2ChunksByDocumentID(
	ctx context.Context,
	arg sqlc.DeleteDatasourceV2ChunksByDocumentIDParams,
) (int64, error) {
	return s.deleteChunksFn(ctx, arg)
}

func (s *datasourceV2QuerierStub) InsertDatasourceV2Chunk(
	ctx context.Context,
	arg sqlc.InsertDatasourceV2ChunkParams,
) error {
	return s.insertChunkFn(ctx, arg)
}

func (s *datasourceV2QuerierStub) MarkDatasourceV2DocumentReady(
	ctx context.Context,
	arg sqlc.MarkDatasourceV2DocumentReadyParams,
) (sqlc.DatasourceV2Document, error) {
	return s.markReadyFn(ctx, arg)
}

func (s *datasourceV2QuerierStub) ListDatasourceV2Documents(
	ctx context.Context,
	arg sqlc.ListDatasourceV2DocumentsParams,
) ([]sqlc.DatasourceV2Document, error) {
	return s.listDocumentsFn(ctx, arg)
}

func (s *datasourceV2QuerierStub) GetDatasourceV2Document(
	ctx context.Context,
	arg sqlc.GetDatasourceV2DocumentParams,
) (sqlc.DatasourceV2Document, error) {
	return s.getDocumentFn(ctx, arg)
}

func (s *datasourceV2QuerierStub) ListDatasourceV2ChunksPage(
	ctx context.Context,
	arg sqlc.ListDatasourceV2ChunksPageParams,
) ([]sqlc.ListDatasourceV2ChunksPageRow, error) {
	return s.listChunksPageFn(ctx, arg)
}

func (s *datasourceV2QuerierStub) SearchDatasourceV2ChunksByEmbedding(
	ctx context.Context,
	arg sqlc.SearchDatasourceV2ChunksByEmbeddingParams,
) ([]sqlc.SearchDatasourceV2ChunksByEmbeddingRow, error) {
	return s.searchChunksFn(ctx, arg)
}

func (s *datasourceV2QuerierStub) UpdateDatasourceV2DocumentMetadata(
	ctx context.Context,
	arg sqlc.UpdateDatasourceV2DocumentMetadataParams,
) (sqlc.DatasourceV2Document, error) {
	return s.updateDocumentFn(ctx, arg)
}

func (s *datasourceV2QuerierStub) DeleteDatasourceV2Document(
	ctx context.Context,
	arg sqlc.DeleteDatasourceV2DocumentParams,
) (int64, error) {
	return s.deleteDocumentFn(ctx, arg)
}

func TestUpsertImportingForwardsDocumentMetadata(t *testing.T) {
	t.Parallel()

	now := int64(1_000_000)
	var captured sqlc.UpsertDatasourceV2DocumentImportingParams
	s := &store{q: &datasourceV2QuerierStub{
		upsertImportingFn: func(_ context.Context, arg sqlc.UpsertDatasourceV2DocumentImportingParams) (sqlc.DatasourceV2Document, error) {
			captured = arg
			return sqlc.DatasourceV2Document{
				ID:         7,
				SourcePath: arg.SourcePath,
				FileName:   arg.FileName,
				Extension:  arg.Extension,
				SizeBytes:  arg.SizeBytes,
				Status:     StatusImporting,
				CreatedAt:  now,
				UpdatedAt:  now,
			}, nil
		},
	}}

	got, err := s.UpsertImporting(context.Background(), UpsertDocumentParams{
		SourcePath: `C:\tmp\notes.txt`,
		FileName:   "notes.txt",
		Extension:  ".txt",
		SizeBytes:  123,
	})
	if err != nil {
		t.Fatalf("UpsertImporting() unexpected error: %v", err)
	}
	if captured.SourcePath != `C:\tmp\notes.txt` || captured.FileName != "notes.txt" ||
		captured.Extension != ".txt" || captured.SizeBytes != 123 {
		t.Fatalf("UpsertImporting() forwarded wrong params: %+v", captured)
	}
	if got.ID != 7 || got.Status != StatusImporting {
		t.Fatalf("UpsertImporting() mapped row = %+v", got)
	}
}

func TestDeleteChunksForwardsDocumentID(t *testing.T) {
	t.Parallel()

	var deletedID int64
	s := &store{q: &datasourceV2QuerierStub{
		deleteChunksFn: func(_ context.Context, arg sqlc.DeleteDatasourceV2ChunksByDocumentIDParams) (int64, error) {
			deletedID = arg.DocumentID
			return 3, nil
		},
	}}

	if err := s.DeleteChunks(context.Background(), 9); err != nil {
		t.Fatalf("DeleteChunks() unexpected error: %v", err)
	}
	if deletedID != 9 {
		t.Fatalf("DeleteChunks() documentID = %d, want 9", deletedID)
	}
}

func TestInsertChunkForwardsArguments(t *testing.T) {
	t.Parallel()

	var inserted sqlc.InsertDatasourceV2ChunkParams
	s := &store{q: &datasourceV2QuerierStub{
		insertChunkFn: func(_ context.Context, arg sqlc.InsertDatasourceV2ChunkParams) error {
			inserted = arg
			return nil
		},
	}}

	if err := s.InsertChunk(context.Background(), InsertChunkParams{
		DocumentID:     9,
		ChunkIndex:     2,
		Content:        "chunk",
		CharCount:      5,
		ByteCount:      5,
		Embedding:      []byte{0, 0, 128, 63, 0, 0, 0, 64},
		EmbeddingModel: "test-embedding",
		EmbeddingDim:   2,
		TokenCount:     3,
	}); err != nil {
		t.Fatalf("InsertChunk() unexpected error: %v", err)
	}
	if !insertChunkParamsMatch(inserted, sqlc.InsertDatasourceV2ChunkParams{
		DocumentID:     9,
		ChunkIndex:     2,
		Content:        "chunk",
		CharCount:      5,
		ByteCount:      5,
		Embedding:      []byte{0, 0, 128, 63, 0, 0, 0, 64},
		EmbeddingModel: "test-embedding",
		EmbeddingDim:   2,
		TokenCount:     3,
	}) {
		t.Fatalf("InsertChunk() forwarded wrong params: %+v", inserted)
	}
}

func insertChunkParamsMatch(got, want sqlc.InsertDatasourceV2ChunkParams) bool {
	return got.DocumentID == want.DocumentID &&
		got.ChunkIndex == want.ChunkIndex &&
		got.Content == want.Content &&
		got.CharCount == want.CharCount &&
		got.ByteCount == want.ByteCount &&
		string(got.Embedding) == string(want.Embedding) &&
		got.EmbeddingModel == want.EmbeddingModel &&
		got.EmbeddingDim == want.EmbeddingDim &&
		got.TokenCount == want.TokenCount
}

func TestInsertChunkRequiresVectorFields(t *testing.T) {
	t.Parallel()

	s := &store{q: &datasourceV2QuerierStub{}}
	err := s.InsertChunk(context.Background(), InsertChunkParams{
		DocumentID: 9,
		ChunkIndex: 0,
		Content:    "chunk",
		CharCount:  5,
		ByteCount:  5,
	})
	if err == nil || !strings.Contains(err.Error(), "embedding is required") {
		t.Fatalf("InsertChunk() error = %v, want embedding validation", err)
	}
}

func TestMarkReadyForwardsSummaryAndMapsRow(t *testing.T) {
	t.Parallel()

	var ready sqlc.MarkDatasourceV2DocumentReadyParams
	s := &store{q: &datasourceV2QuerierStub{
		markReadyFn: func(_ context.Context, arg sqlc.MarkDatasourceV2DocumentReadyParams) (sqlc.DatasourceV2Document, error) {
			ready = arg
			return sqlc.DatasourceV2Document{
				ID:          arg.ID,
				SourcePath:  "/tmp/notes.txt",
				FileName:    "notes.txt",
				Extension:   ".txt",
				SizeBytes:   12,
				ContentHash: arg.ContentHash,
				ChunkCount:  arg.ChunkCount,
				TotalChars:  arg.TotalChars,
				Status:      StatusReady,
			}, nil
		},
	}}

	got, err := s.MarkReady(context.Background(), MarkReadyParams{
		DocumentID:  9,
		ContentHash: "sha256:abc",
		ChunkCount:  3,
		TotalChars:  15,
	})
	if err != nil {
		t.Fatalf("MarkReady() unexpected error: %v", err)
	}
	if ready.ID != 9 || ready.ContentHash == nil || *ready.ContentHash != "sha256:abc" ||
		ready.ChunkCount != 3 || ready.TotalChars != 15 {
		t.Fatalf("MarkReady() forwarded wrong params: %+v", ready)
	}
	if got.Status != StatusReady || got.ChunkCount != 3 {
		t.Fatalf("MarkReady() mapped row = %+v", got)
	}
}

func TestListDocumentsForwardsKeywordLimitAndMapsRows(t *testing.T) {
	t.Parallel()

	var captured sqlc.ListDatasourceV2DocumentsParams
	s := &store{q: &datasourceV2QuerierStub{
		listDocumentsFn: func(_ context.Context, arg sqlc.ListDatasourceV2DocumentsParams) ([]sqlc.DatasourceV2Document, error) {
			captured = arg
			return []sqlc.DatasourceV2Document{{
				ID:         11,
				SourcePath: "/tmp/alpha.txt",
				FileName:   "alpha.txt",
				Extension:  ".txt",
				SizeBytes:  42,
				Status:     StatusReady,
			}}, nil
		},
	}}

	got, err := s.ListDocuments(context.Background(), ListDocumentsParams{
		Keyword: " alpha ",
		Limit:   25,
	})
	if err != nil {
		t.Fatalf("ListDocuments() unexpected error: %v", err)
	}
	if captured.Keyword != "alpha" || captured.Limit != 25 {
		t.Fatalf("ListDocuments() params = %+v, want keyword alpha limit 25", captured)
	}
	if len(got) != 1 || got[0].ID != 11 || got[0].FileName != "alpha.txt" {
		t.Fatalf("ListDocuments() rows = %+v", got)
	}
}

// TestDatasourceV2ListChunksUsesCursorAndLimit 固定 store 分页查询下沉 document/cursor/limit。
func TestDatasourceV2ListChunksUsesCursorAndLimit(t *testing.T) {
	t.Parallel()

	var gotDocumentID int64
	var gotChunksParams sqlc.ListDatasourceV2ChunksPageParams
	s := &store{q: &datasourceV2QuerierStub{
		getDocumentFn: func(_ context.Context, arg sqlc.GetDatasourceV2DocumentParams) (sqlc.DatasourceV2Document, error) {
			gotDocumentID = arg.ID
			return sqlc.DatasourceV2Document{
				ID:         arg.ID,
				SourcePath: "/tmp/beta.md",
				FileName:   "beta.md",
				Extension:  ".md",
				SizeBytes:  12,
				Status:     StatusReady,
			}, nil
		},
		listChunksPageFn: func(_ context.Context, arg sqlc.ListDatasourceV2ChunksPageParams) ([]sqlc.ListDatasourceV2ChunksPageRow, error) {
			gotChunksParams = arg
			return []sqlc.ListDatasourceV2ChunksPageRow{{
				ID:             70,
				DocumentID:     arg.DocumentID,
				ChunkIndex:     0,
				Content:        "body",
				CharCount:      4,
				ByteCount:      4,
				Embedding:      []byte{0, 0, 128, 63},
				EmbeddingModel: "test-embedding",
				EmbeddingDim:   1,
				TokenCount:     1,
			}}, nil
		},
	}}

	doc, err := s.GetDocument(context.Background(), 12)
	if err != nil {
		t.Fatalf("GetDocument() unexpected error: %v", err)
	}
	chunks, err := s.ListChunksPage(context.Background(), ListChunksParams{DocumentID: 12, Limit: 5, Cursor: -1})
	if err != nil {
		t.Fatalf("ListChunksPage() unexpected error: %v", err)
	}
	assertDatasourceV2DocumentLookup(t, gotDocumentID, doc)
	assertDatasourceV2ChunkPageParams(t, gotChunksParams)
	assertDatasourceV2ChunkPageBody(t, chunks)
}

// assertDatasourceV2DocumentLookup verifies get document used the requested id.
func assertDatasourceV2DocumentLookup(t *testing.T, gotDocumentID int64, doc *Document) {
	t.Helper()
	if gotDocumentID != 12 {
		t.Fatalf("GetDocument() forwarded id = %d, want 12", gotDocumentID)
	}
	if doc.ID != 12 {
		t.Fatalf("GetDocument() id = %d, want 12", doc.ID)
	}
}

// assertDatasourceV2ChunkPageParams verifies chunk page SQL parameters.
func assertDatasourceV2ChunkPageParams(t *testing.T, got sqlc.ListDatasourceV2ChunksPageParams) {
	t.Helper()
	if got.DocumentID != 12 {
		t.Fatalf("ListChunksPage() document id = %d, want 12", got.DocumentID)
	}
	if got.Cursor != -1 {
		t.Fatalf("ListChunksPage() cursor = %d, want -1", got.Cursor)
	}
	if got.Limit != int64(5) {
		t.Fatalf("ListChunksPage() limit = %v, want 5", got.Limit)
	}
}

// assertDatasourceV2ChunkPageBody verifies chunk page DTO mapping.
func assertDatasourceV2ChunkPageBody(t *testing.T, chunks TextChunkPage) {
	t.Helper()
	if len(chunks.Chunks) != 1 {
		t.Fatalf("ListChunksPage() chunk count = %d, want 1", len(chunks.Chunks))
	}
	if chunks.Chunks[0].Content != "body" {
		t.Fatalf("ListChunksPage() content = %q, want body", chunks.Chunks[0].Content)
	}
	if chunks.Chunks[0].EmbeddingDim != 1 {
		t.Fatalf("ListChunksPage() embedding dim = %d, want 1", chunks.Chunks[0].EmbeddingDim)
	}
}

func TestSearchChunksForwardsVectorAndMapsSemanticRows(t *testing.T) {
	t.Parallel()

	var captured sqlc.SearchDatasourceV2ChunksByEmbeddingParams
	query := []byte{0, 0, 128, 63, 0, 0, 0, 64}
	s := &store{q: &datasourceV2QuerierStub{
		searchChunksFn: func(_ context.Context, arg sqlc.SearchDatasourceV2ChunksByEmbeddingParams) ([]sqlc.SearchDatasourceV2ChunksByEmbeddingRow, error) {
			captured = arg
			return []sqlc.SearchDatasourceV2ChunksByEmbeddingRow{{
				ID:             71,
				DocumentID:     12,
				ChunkIndex:     3,
				Content:        "semantic body",
				CharCount:      13,
				ByteCount:      13,
				Embedding:      []byte{0, 0, 128, 63},
				EmbeddingModel: "test-embedding",
				EmbeddingDim:   1,
				TokenCount:     2,
				SourcePath:     "/tmp/beta.txt",
				FileName:       "beta.txt",
				Distance:       0.125,
			}}, nil
		},
	}}

	got, err := s.SearchChunks(context.Background(), SearchChunksParams{
		Embedding:      query,
		EmbeddingModel: " test-embedding ",
		EmbeddingDim:   2,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("SearchChunks() unexpected error: %v", err)
	}
	assertSearchChunksParams(t, captured, query)
	assertSemanticSearchRows(t, got)
}

func assertSearchChunksParams(
	t *testing.T,
	got sqlc.SearchDatasourceV2ChunksByEmbeddingParams,
	wantEmbedding []byte,
) {
	t.Helper()
	capturedEmbedding, ok := got.Embedding.([]byte)
	if !ok {
		t.Fatalf("SearchChunks() embedding param type = %T, want []byte", got.Embedding)
	}
	if !bytes.Equal(capturedEmbedding, wantEmbedding) {
		t.Fatalf("SearchChunks() embedding = %v, want %v", capturedEmbedding, wantEmbedding)
	}
	if got.EmbeddingModel != "test-embedding" {
		t.Fatalf("SearchChunks() embedding model = %q, want test-embedding", got.EmbeddingModel)
	}
	if got.EmbeddingDim != 2 {
		t.Fatalf("SearchChunks() embedding dim = %d, want 2", got.EmbeddingDim)
	}
	if got.Limit != 10 {
		t.Fatalf("SearchChunks() limit = %d, want 10", got.Limit)
	}
}

func assertSemanticSearchRows(t *testing.T, got []SemanticChunk) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("SearchChunks() rows = %+v, want one row", got)
	}
	row := got[0]
	if row.Content != "semantic body" {
		t.Fatalf("SearchChunks() content = %q, want semantic body", row.Content)
	}
	if row.FileName != "beta.txt" {
		t.Fatalf("SearchChunks() fileName = %q, want beta.txt", row.FileName)
	}
	if row.Distance != 0.125 {
		t.Fatalf("SearchChunks() distance = %f, want 0.125", row.Distance)
	}
}

func TestSearchChunksRequiresValidQueryVector(t *testing.T) {
	t.Parallel()

	s := &store{q: &datasourceV2QuerierStub{}}
	_, err := s.SearchChunks(context.Background(), SearchChunksParams{
		Embedding:      []byte{0, 0, 128, 63},
		EmbeddingModel: "test-embedding",
		EmbeddingDim:   2,
		Limit:          10,
	})
	if err == nil || !strings.Contains(err.Error(), "embedding byte length must match embedding dim") {
		t.Fatalf("SearchChunks() error = %v, want embedding length validation", err)
	}
}

func TestUpdateDocumentForwardsMetadata(t *testing.T) {
	t.Parallel()

	var captured sqlc.UpdateDatasourceV2DocumentMetadataParams
	s := &store{q: &datasourceV2QuerierStub{
		updateDocumentFn: func(_ context.Context, arg sqlc.UpdateDatasourceV2DocumentMetadataParams) (sqlc.DatasourceV2Document, error) {
			captured = arg
			return sqlc.DatasourceV2Document{
				ID:         arg.ID,
				SourcePath: arg.SourcePath,
				FileName:   arg.FileName,
				Extension:  arg.Extension,
				SizeBytes:  arg.SizeBytes,
				Status:     StatusReady,
			}, nil
		},
	}}

	got, err := s.UpdateDocument(context.Background(), UpdateDocumentParams{
		DocumentID: 44,
		SourcePath: " /tmp/gamma.txt ",
		FileName:   " gamma.txt ",
		Extension:  " .txt ",
		SizeBytes:  88,
	})
	if err != nil {
		t.Fatalf("UpdateDocument() unexpected error: %v", err)
	}
	if captured.ID != 44 || captured.SourcePath != "/tmp/gamma.txt" || captured.FileName != "gamma.txt" ||
		captured.Extension != ".txt" || captured.SizeBytes != 88 {
		t.Fatalf("UpdateDocument() params = %+v", captured)
	}
	if got.ID != 44 || got.SourcePath != "/tmp/gamma.txt" {
		t.Fatalf("UpdateDocument() row = %+v", got)
	}
}

func TestDeleteDocumentRequiresExistingRow(t *testing.T) {
	t.Parallel()

	var deletedID int64
	s := &store{q: &datasourceV2QuerierStub{
		deleteDocumentFn: func(_ context.Context, arg sqlc.DeleteDatasourceV2DocumentParams) (int64, error) {
			deletedID = arg.ID
			return 1, nil
		},
	}}

	if err := s.DeleteDocument(context.Background(), 55); err != nil {
		t.Fatalf("DeleteDocument() unexpected error: %v", err)
	}
	if deletedID != 55 {
		t.Fatalf("DeleteDocument() id = %d, want 55", deletedID)
	}
}
