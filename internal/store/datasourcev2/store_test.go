package datasourcev2

import (
	"context"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type datasourceV2QuerierStub struct {
	upsertImportingFn func(context.Context, sqlc.UpsertDatasourceV2DocumentImportingParams) (sqlc.DatasourceV2Document, error)
	deleteChunksFn    func(context.Context, sqlc.DeleteDatasourceV2ChunksByDocumentIDParams) (int64, error)
	insertChunkFn     func(context.Context, sqlc.InsertDatasourceV2ChunkParams) error
	markReadyFn       func(context.Context, sqlc.MarkDatasourceV2DocumentReadyParams) (sqlc.DatasourceV2Document, error)
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

func TestUpsertImportingForwardsDocumentMetadata(t *testing.T) {
	t.Parallel()

	now := pgtype.Timestamptz{Time: time.Unix(1_000, 0).UTC(), Valid: true}
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
