package contract

import (
	"context"
	"time"
)

const (
	// DatasourceV2StatusImporting means document metadata exists while chunks
	// are being rewritten in the current transaction.
	DatasourceV2StatusImporting = "importing"
	// DatasourceV2StatusReady means document chunks and summary have been saved.
	DatasourceV2StatusReady = "ready"
	// DatasourceV2StatusFailed is reserved for future async import failures.
	DatasourceV2StatusFailed = "failed"
)

// DatasourceV2Store is the persistence port for datasource_v2 imports.
type DatasourceV2Store interface {
	// WithTx runs the import mutation flow in one transaction.
	WithTx(ctx context.Context, fn func(txStore DatasourceV2Store) error) error
	// UpsertImporting inserts or resets document metadata to importing.
	UpsertImporting(ctx context.Context, params DatasourceV2UpsertDocumentParams) (*DatasourceV2Document, error)
	// DeleteChunks removes all chunks for a document.
	DeleteChunks(ctx context.Context, documentID int64) error
	// InsertChunk persists one text chunk.
	InsertChunk(ctx context.Context, params DatasourceV2InsertChunkParams) error
	// MarkReady marks the document ready with final chunk statistics.
	MarkReady(ctx context.Context, params DatasourceV2MarkReadyParams) (*DatasourceV2Document, error)
}

// DatasourceV2UpsertDocumentParams is document metadata for an import.
type DatasourceV2UpsertDocumentParams struct {
	SourcePath string
	FileName   string
	Extension  string
	SizeBytes  int64
}

// DatasourceV2InsertChunkParams is one text chunk write.
type DatasourceV2InsertChunkParams struct {
	DocumentID int64
	ChunkIndex int32
	Content    string
	CharCount  int32
	ByteCount  int32
}

// DatasourceV2MarkReadyParams is the final document summary written after all
// chunks are saved.
type DatasourceV2MarkReadyParams struct {
	DocumentID  int64
	ContentHash string
	ChunkCount  int32
	TotalChars  int32
}

// DatasourceV2Document is the cross-layer projection of a datasource_v2
// document row.
type DatasourceV2Document struct {
	ID           int64     `json:"id"`
	SourcePath   string    `json:"sourcePath"`
	FileName     string    `json:"fileName"`
	Extension    string    `json:"extension"`
	SizeBytes    int64     `json:"sizeBytes"`
	ContentHash  string    `json:"contentHash"`
	ChunkCount   int32     `json:"chunkCount"`
	TotalChars   int32     `json:"totalChars"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"errorMessage"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
