package datasourcev2

import (
	"context"
	"time"
)

const (
	// StatusImporting 表示文档元数据已登记，但正文分块仍在事务中重写。
	StatusImporting = "importing"
	// StatusReady 表示文档正文分块和摘要已经完整写入。
	StatusReady = "ready"
	// StatusFailed 预留给后续异步导入失败记录；当前同步导入失败会回滚事务。
	StatusFailed = "failed"
)

// Store 负责 datasource_v2 文档元数据和正文分块的持久化。
// 写入文件正文时必须通过 WithTx 包住元数据、清理旧分块、插入新分块和标记 ready 四个步骤。
type Store interface {
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	UpsertImporting(ctx context.Context, params UpsertDocumentParams) (*Document, error)
	DeleteChunks(ctx context.Context, documentID int64) error
	InsertChunk(ctx context.Context, params InsertChunkParams) error
	MarkReady(ctx context.Context, params MarkReadyParams) (*Document, error)
}

// UpsertDocumentParams 是导入文件的文档级元数据。
type UpsertDocumentParams struct {
	SourcePath string
	FileName   string
	Extension  string
	SizeBytes  int64
}

// InsertChunkParams 是单个正文分块的写入参数。
type InsertChunkParams struct {
	DocumentID int64
	ChunkIndex int32
	Content    string
	CharCount  int32
	ByteCount  int32
}

// MarkReadyParams 是所有正文分块写完后更新文档统计的参数。
type MarkReadyParams struct {
	DocumentID  int64
	ContentHash string
	ChunkCount  int32
	TotalChars  int32
}

// Document 是 datasource_v2_documents 的领域 DTO，避免上层直接依赖 sqlc 生成类型。
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
