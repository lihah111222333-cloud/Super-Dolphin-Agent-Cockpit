package datasourcev2

import (
	"context"
	"time"
)

const (
	// StatusImporting 表示文档元数据已落库，但文本分块仍在重写中。
	StatusImporting = "importing"
	// StatusReady 表示文档分块、摘要字段和向量索引都已完成持久化。
	StatusReady = "ready"
	// StatusFailed 预留给异步导入失败记录，调用方不能把它当作 ready 文档检索。
	StatusFailed = "failed"
)

// Store 持久化 datasource_v2 文档元数据和文本分块。
// 文档导入必须通过 WithTx 组合元数据、旧分块清理、新分块写入和 ready 标记，避免半导入状态外泄。
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

// ListDocumentsParams 是 datasource_v2 文档列表查询的过滤条件。
// Limit 必须由调用方显式传入，避免无意拉取整张文档表。
type ListDocumentsParams struct {
	Keyword string
	Limit   int32
}

// ListChunksParams 是 datasource_v2 文档分块的显式分页参数。
// Cursor 表示上一页最后一个 chunk_index；第一页使用 -1。
type ListChunksParams struct {
	DocumentID int64
	Limit      int32
	Cursor     int32
}

// SearchChunksParams 是语义检索 datasource_v2 分块所需的查询向量和上限。
type SearchChunksParams struct {
	Embedding      []byte
	EmbeddingModel string
	EmbeddingDim   int32
	Limit          int32
}

// UpsertDocumentParams 携带文件导入阶段的文档级元数据。
// SourcePath 是幂等导入键，重复导入同一路径会覆盖 importing 状态。
type UpsertDocumentParams struct {
	SourcePath string
	FileName   string
	Extension  string
	SizeBytes  int64
}

// UpdateDocumentParams 更新文档元数据。
// 该 DTO 不包含分块和向量字段，避免普通编辑路径误改检索内容。
type UpdateDocumentParams struct {
	DocumentID int64
	SourcePath string
	FileName   string
	Extension  string
	SizeBytes  int64
}

// InsertChunkParams 表示一个待持久化的文本分块。
// Embedding 使用 float32 BLOB 形式跨越 store 边界，写入前必须校验长度与维度一致。
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

// MarkReadyParams 在所有分块写入完成后更新文档摘要字段。
// 调用方应只在同一导入事务末尾使用它，将文档从 importing 推进到 ready。
type MarkReadyParams struct {
	DocumentID  int64
	ContentHash string
	ChunkCount  int32
	TotalChars  int32
}

// Document 是 datasource_v2_documents 的跨模块 DTO。
// 它屏蔽 sqlc 行类型，保留 JSON 字段名供 UI 和服务层稳定消费。
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

// TextChunk 是 datasource_v2_text_chunks 的跨模块 DTO。
// Embedding 不输出到 JSON，只在 store 与检索逻辑之间传递原始向量字节。
type TextChunk struct {
	ID             int64     `json:"id"`
	DocumentID     int64     `json:"documentId"`
	ChunkIndex     int32     `json:"chunkIndex"`
	Content        string    `json:"content"`
	CharCount      int32     `json:"charCount"`
	ByteCount      int32     `json:"byteCount"`
	Embedding      []byte    `json:"-"`
	EmbeddingModel string    `json:"embeddingModel"`
	EmbeddingDim   int32     `json:"embeddingDim"`
	TokenCount     int32     `json:"tokenCount"`
	CreatedAt      time.Time `json:"createdAt"`
}

// TextChunkPage 是 datasource_v2 文档分块的有界分页结果。
type TextChunkPage struct {
	Chunks     []TextChunk `json:"chunks"`
	HasMore    bool        `json:"hasMore"`
	NextCursor int32       `json:"nextCursor"`
}

// SemanticChunk 是语义检索返回的分块，包含来源文件和距离分数。
type SemanticChunk struct {
	TextChunk
	SourcePath string  `json:"sourcePath"`
	FileName   string  `json:"fileName"`
	Distance   float64 `json:"distance"`
}
