package datasourcev2

import (
	"context"
	"time"
)

const (
	// StatusImporting means document metadata exists while text chunks are being rewritten.
	StatusImporting = "importing"
	// StatusReady means document chunks and summary fields were fully persisted.
	StatusReady = "ready"
	// StatusFailed is reserved for future async import failure records.
	StatusFailed = "failed"
)

// Store persists datasource_v2 document metadata and text chunks.
// Text imports must use WithTx to keep metadata, chunk cleanup, chunk insert, and ready marking atomic.
type Store interface {
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	ListDocuments(ctx context.Context, params ListDocumentsParams) ([]Document, error)
	GetDocument(ctx context.Context, documentID int64) (*Document, error)
	ListChunks(ctx context.Context, documentID int64) ([]TextChunk, error)
	UpsertImporting(ctx context.Context, params UpsertDocumentParams) (*Document, error)
	UpdateDocument(ctx context.Context, params UpdateDocumentParams) (*Document, error)
	DeleteDocument(ctx context.Context, documentID int64) error
	DeleteChunks(ctx context.Context, documentID int64) error
	InsertChunk(ctx context.Context, params InsertChunkParams) error
	MarkReady(ctx context.Context, params MarkReadyParams) (*Document, error)
}

// ListDocumentsParams controls datasource_v2 document list queries.
// Limit is explicit so callers do not accidentally pull the full table.
type ListDocumentsParams struct {
	Keyword string
	Limit   int32
}

// UpsertDocumentParams contains document-level metadata for file imports.
type UpsertDocumentParams struct {
	SourcePath string
	FileName   string
	Extension  string
	SizeBytes  int64
}

// UpdateDocumentParams edits document metadata without mutating text chunks.
type UpdateDocumentParams struct {
	DocumentID int64
	SourcePath string
	FileName   string
	Extension  string
	SizeBytes  int64
}

// InsertChunkParams contains one persisted text chunk.
type InsertChunkParams struct {
	DocumentID int64
	ChunkIndex int32
	Content    string
	CharCount  int32
	ByteCount  int32
}

// MarkReadyParams updates document summary fields after all chunks are written.
type MarkReadyParams struct {
	DocumentID  int64
	ContentHash string
	ChunkCount  int32
	TotalChars  int32
}

// Document is the stable datasource_v2_documents DTO exposed above sqlc rows.
type Document struct {
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

// TextChunk is the stable datasource_v2_text_chunks DTO exposed above sqlc rows.
type TextChunk struct {
	ID         int64     `json:"id"`
	DocumentID int64     `json:"documentId"`
	ChunkIndex int32     `json:"chunkIndex"`
	Content    string    `json:"content"`
	CharCount  int32     `json:"charCount"`
	ByteCount  int32     `json:"byteCount"`
	CreatedAt  time.Time `json:"createdAt"`
}
