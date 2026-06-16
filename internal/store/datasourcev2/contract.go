package datasourcev2

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

const (
	// StatusImporting 表示文档元数据已登记，但正文分块仍在事务中重写。
	StatusImporting = contract.DatasourceV2StatusImporting
	// StatusReady 表示文档正文分块和摘要已经完整写入。
	StatusReady = contract.DatasourceV2StatusReady
	// StatusFailed 预留给后续异步导入失败记录；当前同步导入失败会回滚事务。
	StatusFailed = contract.DatasourceV2StatusFailed
)

// Store 负责 datasource_v2 文档元数据和正文分块的持久化。
// 写入文件正文时必须通过 WithTx 包住元数据、清理旧分块、插入新分块和标记 ready 四个步骤。
type Store = contract.DatasourceV2Store

// UpsertDocumentParams 是导入文件的文档级元数据。
type UpsertDocumentParams = contract.DatasourceV2UpsertDocumentParams

// InsertChunkParams 是单个正文分块的写入参数。
type InsertChunkParams = contract.DatasourceV2InsertChunkParams

// MarkReadyParams 是所有正文分块写完后更新文档统计的参数。
type MarkReadyParams = contract.DatasourceV2MarkReadyParams

// Document 是 datasource_v2_documents 的领域 DTO，避免上层直接依赖 sqlc 生成类型。
type Document = contract.DatasourceV2Document
