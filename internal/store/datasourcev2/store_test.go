package datasourcev2

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type datasourceV2QuerierStub struct {
	upsertImportingFn func(context.Context, sqlc.UpsertDatasourceV2DocumentImportingParams) (sqlc.DatasourceV2Document, error)
	deleteChunksFn    func(context.Context, sqlc.DeleteDatasourceV2ChunksByDocumentIDParams) (int64, error)
	insertChunkFn     func(context.Context, sqlc.InsertDatasourceV2ChunkParams) error
	markReadyFn       func(context.Context, sqlc.MarkDatasourceV2DocumentReadyParams) (sqlc.DatasourceV2Document, error)
	listDocumentsFn   func(context.Context, sqlc.ListDatasourceV2DocumentsParams) ([]sqlc.DatasourceV2Document, error)
	getDocumentFn     func(context.Context, sqlc.GetDatasourceV2DocumentParams) (sqlc.DatasourceV2Document, error)
	listChunksFn      func(context.Context, sqlc.ListDatasourceV2ChunksParams) ([]sqlc.DatasourceV2TextChunk, error)
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

func (s *datasourceV2QuerierStub) ListDatasourceV2Chunks(
	ctx context.Context,
	arg sqlc.ListDatasourceV2ChunksParams,
) ([]sqlc.DatasourceV2TextChunk, error) {
	return s.listChunksFn(ctx, arg)
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
		DocumentID: 9,
		ChunkIndex: 2,
		Content:    "chunk",
		CharCount:  5,
		ByteCount:  5,
	}); err != nil {
		t.Fatalf("InsertChunk() unexpected error: %v", err)
	}
	if inserted.DocumentID != 9 || inserted.ChunkIndex != 2 || inserted.Content != "chunk" ||
		inserted.CharCount != 5 || inserted.ByteCount != 5 {
		t.Fatalf("InsertChunk() forwarded wrong params: %+v", inserted)
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

func TestGetDocumentAndListChunksForwardDocumentID(t *testing.T) {
	t.Parallel()

	var gotDocumentID int64
	var gotChunksID int64
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
		listChunksFn: func(_ context.Context, arg sqlc.ListDatasourceV2ChunksParams) ([]sqlc.DatasourceV2TextChunk, error) {
			gotChunksID = arg.DocumentID
			return []sqlc.DatasourceV2TextChunk{{
				ID:         70,
				DocumentID: arg.DocumentID,
				ChunkIndex: 0,
				Content:    "body",
				CharCount:  4,
				ByteCount:  4,
			}}, nil
		},
	}}

	doc, err := s.GetDocument(context.Background(), 12)
	if err != nil {
		t.Fatalf("GetDocument() unexpected error: %v", err)
	}
	chunks, err := s.ListChunks(context.Background(), 12)
	if err != nil {
		t.Fatalf("ListChunks() unexpected error: %v", err)
	}
	if gotDocumentID != 12 || doc.ID != 12 {
		t.Fatalf("GetDocument() id = (%d, %+v), want 12", gotDocumentID, doc)
	}
	if gotChunksID != 12 || len(chunks) != 1 || chunks[0].Content != "body" {
		t.Fatalf("ListChunks() result = id %d chunks %+v", gotChunksID, chunks)
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
