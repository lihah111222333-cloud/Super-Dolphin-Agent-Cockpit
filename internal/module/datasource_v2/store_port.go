// Package datasourcev2 提供文件正文导入、分块存储和语义检索能力，供 prompt 动态段和前端数据源管理页使用。
package datasourcev2

import (
	"context"
	"time"
)

// Store 是 datasource_v2 模块访问持久化能力的窄端口。
// 具体 sqlc/store 实现由 App 组合边界适配，业务文件不直接依赖 store 包。
type Store interface {
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	ListDocuments(ctx context.Context, params ListDocumentsParams) ([]Document, error)
	GetDocument(ctx context.Context, documentID int64) (*Document, error)
	ListChunksPage(ctx context.Context, params ListChunksParams) (TextChunkPage, error)
	SearchChunks(ctx context.Context, params SearchChunksParams) ([]SemanticChunk, error)
	UpsertImporting(ctx context.Context, params UpsertDocumentParams) (*Document, error)
	UpdateDocument(ctx context.Context, params UpdateDocumentParams) (*Document, error)
	DeleteDocument(ctx context.Context, documentID int64) error
	DeleteChunks(ctx context.Context, documentID int64) error
	InsertChunk(ctx context.Context, params InsertChunkParams) error
	MarkReady(ctx context.Context, params MarkReadyParams) (*Document, error)
}

// ListDocumentsParams 限定文档列表的关键词和显式上限。
type ListDocumentsParams struct {
	Keyword string
	Limit   int32
}

// ListChunksParams 限定单篇文档的分块分页范围。
type ListChunksParams struct {
	DocumentID int64
	Limit      int32
	Cursor     int32
}

// SearchChunksParams 携带语义检索向量及模型信息。
type SearchChunksParams struct {
	Embedding      []byte
	EmbeddingModel string
	EmbeddingDim   int32
	Limit          int32
}

// UpsertDocumentParams 携带导入阶段的文档元数据。
type UpsertDocumentParams struct {
	SourcePath string
	FileName   string
	Extension  string
	SizeBytes  int64
}

// UpdateDocumentParams 携带普通文档元数据更新。
type UpdateDocumentParams struct {
	DocumentID int64
	SourcePath string
	FileName   string
	Extension  string
	SizeBytes  int64
}

// InsertChunkParams 携带单个待持久化文本分块。
type InsertChunkParams struct {
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

// MarkReadyParams 在导入事务末尾推进文档状态。
type MarkReadyParams struct {
	DocumentID  int64
	ContentHash string
	ChunkCount  int32
	TotalChars  int32
}

// Document 是 datasource_v2 模块消费的稳定文档视图。
type Document struct {
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

// TextChunk 是 datasource_v2 模块消费的稳定文本分块视图。
type TextChunk struct {
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

// TextChunkPage 是有界分页后的文本分块结果。
type TextChunkPage struct {
	Chunks     []TextChunk
	HasMore    bool
	NextCursor int32
}

// SemanticChunk 在文本分块上附加来源与语义距离。
type SemanticChunk struct {
	TextChunk
	SourcePath string
	FileName   string
	Distance   float64
}
