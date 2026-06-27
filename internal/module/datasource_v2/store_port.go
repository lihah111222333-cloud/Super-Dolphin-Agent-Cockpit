// Package datasourcev2 提供文件正文导入、分块存储和语义检索能力，供 prompt 动态段和前端数据源管理页使用。
package datasourcev2

import (
	"context"
	"time"
)

// datasourceV2Store 是 datasource_v2 模块访问持久化能力的窄端口。
// 具体 sqlc/store 实现由 module.go 的装配适配器注入，业务文件不直接依赖 store 包。
type datasourceV2Store interface {
	WithTx(ctx context.Context, fn func(txStore datasourceV2Store) error) error
	ListDocuments(ctx context.Context, params datasourceV2ListDocumentsParams) ([]datasourceV2Document, error)
	GetDocument(ctx context.Context, documentID int64) (*datasourceV2Document, error)
	ListChunks(ctx context.Context, documentID int64) ([]datasourceV2TextChunk, error)
	SearchChunks(ctx context.Context, params datasourceV2SearchChunksParams) ([]datasourceV2SemanticChunk, error)
	UpsertImporting(ctx context.Context, params datasourceV2UpsertDocumentParams) (*datasourceV2Document, error)
	UpdateDocument(ctx context.Context, params datasourceV2UpdateDocumentParams) (*datasourceV2Document, error)
	DeleteDocument(ctx context.Context, documentID int64) error
	DeleteChunks(ctx context.Context, documentID int64) error
	InsertChunk(ctx context.Context, params datasourceV2InsertChunkParams) error
	MarkReady(ctx context.Context, params datasourceV2MarkReadyParams) (*datasourceV2Document, error)
}

type datasourceV2ListDocumentsParams struct {
	Keyword string
	Limit   int32
}

type datasourceV2SearchChunksParams struct {
	Embedding      []byte
	EmbeddingModel string
	EmbeddingDim   int32
	Limit          int32
}

type datasourceV2UpsertDocumentParams struct {
	SourcePath string
	FileName   string
	Extension  string
	SizeBytes  int64
}

type datasourceV2UpdateDocumentParams struct {
	DocumentID int64
	SourcePath string
	FileName   string
	Extension  string
	SizeBytes  int64
}

type datasourceV2InsertChunkParams struct {
	DocumentID     int64
	ChunkIndex     int32
	Content        string
	CharCount      int32
	ByteCount      int32
	Embedding      []byte
	EmbeddingModel string
	EmbeddingDim   int32
	TokenCount     int32
}

type datasourceV2MarkReadyParams struct {
	DocumentID  int64
	ContentHash string
	ChunkCount  int32
	TotalChars  int32
}

type datasourceV2Document struct {
	ID           int64
	SourcePath   string
	FileName     string
	Extension    string
	SizeBytes    int64
	ContentHash  string
	ChunkCount   int32
	TotalChars   int32
	Status       string
	ErrorMessage string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type datasourceV2TextChunk struct {
	ID             int64
	DocumentID     int64
	ChunkIndex     int32
	Content        string
	CharCount      int32
	ByteCount      int32
	Embedding      []byte
	EmbeddingModel string
	EmbeddingDim   int32
	TokenCount     int32
	CreatedAt      time.Time
}

type datasourceV2SemanticChunk struct {
	datasourceV2TextChunk
	SourcePath string
	FileName   string
	Distance   float64
}
