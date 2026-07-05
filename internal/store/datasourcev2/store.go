package datasourcev2

import (
	"context"
	"errors"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	ListDatasourceV2Documents(
		ctx context.Context,
		arg sqlc.ListDatasourceV2DocumentsParams,
	) ([]sqlc.DatasourceV2Document, error)
	GetDatasourceV2Document(ctx context.Context, arg sqlc.GetDatasourceV2DocumentParams) (sqlc.DatasourceV2Document, error)
	ListDatasourceV2ChunksPage(ctx context.Context, arg sqlc.ListDatasourceV2ChunksPageParams) ([]sqlc.ListDatasourceV2ChunksPageRow, error)
	SearchDatasourceV2ChunksByEmbedding(
		ctx context.Context,
		arg sqlc.SearchDatasourceV2ChunksByEmbeddingParams,
	) ([]sqlc.SearchDatasourceV2ChunksByEmbeddingRow, error)
	UpsertDatasourceV2DocumentImporting(
		ctx context.Context,
		arg sqlc.UpsertDatasourceV2DocumentImportingParams,
	) (sqlc.DatasourceV2Document, error)
	UpdateDatasourceV2DocumentMetadata(
		ctx context.Context,
		arg sqlc.UpdateDatasourceV2DocumentMetadataParams,
	) (sqlc.DatasourceV2Document, error)
	DeleteDatasourceV2Document(ctx context.Context, arg sqlc.DeleteDatasourceV2DocumentParams) (int64, error)
	DeleteDatasourceV2ChunksByDocumentID(
		ctx context.Context,
		arg sqlc.DeleteDatasourceV2ChunksByDocumentIDParams,
	) (int64, error)
	InsertDatasourceV2Chunk(ctx context.Context, arg sqlc.InsertDatasourceV2ChunkParams) error
	MarkDatasourceV2DocumentReady(
		ctx context.Context,
		arg sqlc.MarkDatasourceV2DocumentReadyParams,
	) (sqlc.DatasourceV2Document, error)
}

type txRunner func(context.Context, func(*sqlc.Queries) error) error

type store struct {
	q       querier
	queries *sqlc.Queries
	runInTx txRunner
}

// NewStore 创建 datasource_v2 的 sqlc 存储实现。
// 该入口不配置事务 runner，仅适用于窄单元测试或只读场景。
func NewStore(q *sqlc.Queries) Store {
	return newStore(q, q, nil)
}

func newStore(q querier, queries *sqlc.Queries, runInTx txRunner) Store {
	return &store{q: q, queries: queries, runInTx: runInTx}
}

// WithTx 在同一个 SQLite 事务内执行 datasource_v2 写流程。
// 导入文档时需要先删除旧分块再写入新分块，缺少事务 runner 时直接返回错误。
func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
	if fn == nil {
		return wrapDatasourceV2Error(errors.New("transaction callback is required"), "with_tx")
	}
	if s.runInTx == nil || s.queries == nil {
		return wrapDatasourceV2Error(errors.New("transaction runner is not configured"), "with_tx")
	}
	err := s.runInTx(ctx, func(txQueries *sqlc.Queries) error {
		return fn(newStore(txQueries, txQueries, s.runInTx))
	})
	return wrapDatasourceV2Error(err, "with_tx")
}

// ListDocuments 按过滤条件列出 datasource_v2 文档元数据。
// 调用方必须显式传入正数 limit，避免无界扫描导入文档表。
func (s *store) ListDocuments(ctx context.Context, params ListDocumentsParams) ([]Document, error) {
	if err := validateListDocumentsParams(params); err != nil {
		return nil, wrapDatasourceV2Error(err, "list_documents")
	}
	rows, err := s.q.ListDatasourceV2Documents(ctx, sqlc.ListDatasourceV2DocumentsParams{
		Keyword: strings.TrimSpace(params.Keyword),
		Limit:   int64(params.Limit),
	})
	if err != nil {
		return nil, wrapDatasourceV2Error(err, "list_documents")
	}
	docs := make([]Document, 0, len(rows))
	for _, row := range rows {
		docs = append(docs, documentFromSQLC(row))
	}
	return docs, nil
}

// GetDocument 按 ID 读取单个 datasource_v2 文档元数据。
// 非正 ID 会在进入 sqlc 前失败，防止把无效主键交给存储层兜底。
func (s *store) GetDocument(ctx context.Context, documentID int64) (*Document, error) {
	if documentID <= 0 {
		return nil, wrapDatasourceV2Error(errors.New("document id is required"), "get_document")
	}
	row, err := s.q.GetDatasourceV2Document(ctx, sqlc.GetDatasourceV2DocumentParams{ID: documentID})
	if err != nil {
		return nil, wrapDatasourceV2Error(err, "get_document")
	}
	doc := documentFromSQLC(row)
	return &doc, nil
}

// ListChunksPage 读取指定文档已经持久化的文本分块页。
// 查询结果由 SQL 保持分块顺序，调用方必须显式传入 limit 和 cursor。
func (s *store) ListChunksPage(ctx context.Context, params ListChunksParams) (TextChunkPage, error) {
	if params.DocumentID <= 0 {
		return TextChunkPage{}, wrapDatasourceV2Error(errors.New("document id is required"), "list_chunks")
	}
	if params.Limit <= 0 {
		return TextChunkPage{}, wrapDatasourceV2Error(errors.New("limit is required"), "list_chunks")
	}
	rows, err := s.q.ListDatasourceV2ChunksPage(ctx, sqlc.ListDatasourceV2ChunksPageParams{
		DocumentID: params.DocumentID,
		Cursor:     params.Cursor,
		Limit:      int64(params.Limit),
	})
	if err != nil {
		return TextChunkPage{}, wrapDatasourceV2Error(err, "list_chunks")
	}
	return textChunkPageFromSQLC(rows, int(params.Limit)), nil
}

func textChunkPageFromSQLC(rows []sqlc.ListDatasourceV2ChunksPageRow, limit int) TextChunkPage {
	pageRows := rows
	hasMore := len(rows) > limit
	if hasMore {
		pageRows = rows[:limit]
	}
	chunks := make([]TextChunk, 0, len(pageRows))
	for _, row := range pageRows {
		chunks = append(chunks, textChunkFromSQLCPage(row))
	}
	page := TextChunkPage{Chunks: chunks, HasMore: hasMore}
	if hasMore && len(chunks) > 0 {
		page.NextCursor = chunks[len(chunks)-1].ChunkIndex
	}
	return page
}

// SearchChunks 根据查询向量检索 ready 文档中最相近的 datasource_v2 分块。
// 调用方必须传入与导入阶段同模型、同维度的 float32 BLOB，避免 sqlite-vec 运行时报维度错误。
func (s *store) SearchChunks(ctx context.Context, params SearchChunksParams) ([]SemanticChunk, error) {
	if err := validateSearchChunksParams(params); err != nil {
		return nil, wrapDatasourceV2Error(err, "search_chunks")
	}
	rows, err := s.q.SearchDatasourceV2ChunksByEmbedding(ctx, sqlc.SearchDatasourceV2ChunksByEmbeddingParams{
		Embedding:      params.Embedding,
		EmbeddingModel: strings.TrimSpace(params.EmbeddingModel),
		EmbeddingDim:   params.EmbeddingDim,
		Limit:          int64(params.Limit),
	})
	if err != nil {
		return nil, wrapDatasourceV2Error(err, "search_chunks")
	}
	chunks := make([]SemanticChunk, 0, len(rows))
	for _, row := range rows {
		chunks = append(chunks, semanticChunkFromSQLC(row))
	}
	return chunks, nil
}

// UpsertImporting 写入或重置文档元数据为 importing 状态。
// 重新导入同一路径时会复用唯一键并清空后续 ready 流程需要重新生成的摘要字段。
func (s *store) UpsertImporting(ctx context.Context, params UpsertDocumentParams) (*Document, error) {
	if err := validateUpsertDocumentParams(params); err != nil {
		return nil, wrapDatasourceV2Error(err, "upsert_importing")
	}
	row, err := s.q.UpsertDatasourceV2DocumentImporting(ctx, sqlc.UpsertDatasourceV2DocumentImportingParams{
		SourcePath: params.SourcePath,
		FileName:   params.FileName,
		Extension:  params.Extension,
		SizeBytes:  params.SizeBytes,
	})
	if err != nil {
		return nil, wrapDatasourceV2Error(err, "upsert_importing")
	}
	doc := documentFromSQLC(row)
	return &doc, nil
}

// UpdateDocument 更新文档基础元数据。
// 该方法不触碰分块、向量和 ready 状态，避免编辑文件名时破坏已完成导入结果。
func (s *store) UpdateDocument(ctx context.Context, params UpdateDocumentParams) (*Document, error) {
	if err := validateUpdateDocumentParams(params); err != nil {
		return nil, wrapDatasourceV2Error(err, "update_document")
	}
	row, err := s.q.UpdateDatasourceV2DocumentMetadata(ctx, sqlc.UpdateDatasourceV2DocumentMetadataParams{
		SourcePath: strings.TrimSpace(params.SourcePath),
		FileName:   strings.TrimSpace(params.FileName),
		Extension:  strings.TrimSpace(params.Extension),
		SizeBytes:  params.SizeBytes,
		ID:         params.DocumentID,
	})
	if err != nil {
		return nil, wrapDatasourceV2Error(err, "update_document")
	}
	doc := documentFromSQLC(row)
	return &doc, nil
}

// DeleteDocument 删除文档及其级联拥有的文本分块。
// 当 SQL 未删除任何行时返回 not found，调用方不应把重复删除当作成功。
func (s *store) DeleteDocument(ctx context.Context, documentID int64) error {
	if documentID <= 0 {
		return wrapDatasourceV2Error(errors.New("document id is required"), "delete_document")
	}
	rows, err := s.q.DeleteDatasourceV2Document(ctx, sqlc.DeleteDatasourceV2DocumentParams{ID: documentID})
	if err != nil {
		return wrapDatasourceV2Error(err, "delete_document")
	}
	if rows == 0 {
		return wrapDatasourceV2Error(platformdb.ErrNotFound, "delete_document")
	}
	return nil
}

// DeleteChunks 删除指定文档的旧文本分块。
// 导入重跑会先清空旧分块，再在同一事务内写入新的向量分块。
func (s *store) DeleteChunks(ctx context.Context, documentID int64) error {
	if documentID <= 0 {
		return wrapDatasourceV2Error(errors.New("document id is required"), "delete_chunks")
	}
	_, err := s.q.DeleteDatasourceV2ChunksByDocumentID(ctx, sqlc.DeleteDatasourceV2ChunksByDocumentIDParams{
		DocumentID: documentID,
	})
	return wrapDatasourceV2Error(err, "delete_chunks")
}

// InsertChunk 持久化一个文本分块及其向量。
// 写入前校验 embedding 字节长度必须等于维度乘以 float32 宽度，避免 sqlite-vec 查询期才失败。
func (s *store) InsertChunk(ctx context.Context, params InsertChunkParams) error {
	if err := validateInsertChunkParams(params); err != nil {
		return wrapDatasourceV2Error(err, "insert_chunk")
	}
	return wrapDatasourceV2Error(s.q.InsertDatasourceV2Chunk(ctx, sqlc.InsertDatasourceV2ChunkParams{
		DocumentID:     params.DocumentID,
		ChunkIndex:     params.ChunkIndex,
		Content:        params.Content,
		CharCount:      params.CharCount,
		ByteCount:      params.ByteCount,
		Embedding:      params.Embedding,
		EmbeddingModel: strings.TrimSpace(params.EmbeddingModel),
		EmbeddingDim:   params.EmbeddingDim,
		TokenCount:     params.TokenCount,
	}), "insert_chunk")
}

// MarkReady 将 importing 文档标记为 ready。
// 只有所有分块写入完成后才能调用，并同步写入内容哈希、分块数和总字符数。
func (s *store) MarkReady(ctx context.Context, params MarkReadyParams) (*Document, error) {
	if err := validateMarkReadyParams(params); err != nil {
		return nil, wrapDatasourceV2Error(err, "mark_ready")
	}
	hash := strings.TrimSpace(params.ContentHash)
	row, err := s.q.MarkDatasourceV2DocumentReady(ctx, sqlc.MarkDatasourceV2DocumentReadyParams{
		ID:          params.DocumentID,
		ContentHash: &hash,
		ChunkCount:  params.ChunkCount,
		TotalChars:  params.TotalChars,
	})
	if err != nil {
		return nil, wrapDatasourceV2Error(err, "mark_ready")
	}
	doc := documentFromSQLC(row)
	return &doc, nil
}

func validateListDocumentsParams(params ListDocumentsParams) error {
	if params.Limit <= 0 {
		return errors.New("limit must be positive")
	}
	return nil
}

// validateSearchChunksParams 校验向量检索参数。
// embedding 必须是与导入模型一致的 float32 BLOB，否则 sqlite-vec 会在查询阶段报维度错误。
func validateSearchChunksParams(params SearchChunksParams) error {
	switch {
	case params.Limit <= 0:
		return errors.New("limit must be positive")
	case len(params.Embedding) == 0:
		return errors.New("embedding is required")
	case strings.TrimSpace(params.EmbeddingModel) == "":
		return errors.New("embedding model is required")
	case params.EmbeddingDim <= 0:
		return errors.New("embedding dim must be positive")
	case len(params.Embedding) != int(params.EmbeddingDim)*4:
		return errors.New("embedding byte length must match embedding dim")
	default:
		return nil
	}
}

func validateUpsertDocumentParams(params UpsertDocumentParams) error {
	switch {
	case strings.TrimSpace(params.SourcePath) == "":
		return errors.New("source path is required")
	case strings.TrimSpace(params.FileName) == "":
		return errors.New("file name is required")
	case params.SizeBytes < 0:
		return errors.New("size bytes must be non-negative")
	default:
		return nil
	}
}

func validateUpdateDocumentParams(params UpdateDocumentParams) error {
	switch {
	case params.DocumentID <= 0:
		return errors.New("document id is required")
	case strings.TrimSpace(params.SourcePath) == "":
		return errors.New("source path is required")
	case strings.TrimSpace(params.FileName) == "":
		return errors.New("file name is required")
	case params.SizeBytes < 0:
		return errors.New("size bytes must be non-negative")
	default:
		return nil
	}
}

// validateInsertChunkParams 校验分块写入的文档、顺序和正文计数字段。
// 向量字段交给 validateInsertChunkVectorParams 统一检查，保持错误边界清晰。
func validateInsertChunkParams(params InsertChunkParams) error {
	switch {
	case params.DocumentID <= 0:
		return errors.New("document id is required")
	case params.ChunkIndex < 0:
		return errors.New("chunk index must be non-negative")
	case params.Content == "":
		return errors.New("chunk content is required")
	case params.CharCount <= 0:
		return errors.New("char count must be positive")
	case params.ByteCount <= 0:
		return errors.New("byte count must be positive")
	default:
		return validateInsertChunkVectorParams(params)
	}
}

// validateInsertChunkVectorParams 校验分块向量的模型、维度和字节长度。
// 这里按 float32 四字节约束提前失败，避免持久化不可检索的数据。
func validateInsertChunkVectorParams(params InsertChunkParams) error {
	switch {
	case len(params.Embedding) == 0:
		return errors.New("embedding is required")
	case strings.TrimSpace(params.EmbeddingModel) == "":
		return errors.New("embedding model is required")
	case params.EmbeddingDim <= 0:
		return errors.New("embedding dim must be positive")
	case len(params.Embedding) != int(params.EmbeddingDim)*4:
		return errors.New("embedding byte length must match embedding dim")
	case params.TokenCount < 0:
		return errors.New("token count must be non-negative")
	default:
		return nil
	}
}

func validateMarkReadyParams(params MarkReadyParams) error {
	switch {
	case params.DocumentID <= 0:
		return errors.New("document id is required")
	case strings.TrimSpace(params.ContentHash) == "":
		return errors.New("content hash is required")
	case params.ChunkCount <= 0:
		return errors.New("chunk count must be positive")
	case params.TotalChars <= 0:
		return errors.New("total chars must be positive")
	default:
		return nil
	}
}

func documentFromSQLC(row sqlc.DatasourceV2Document) Document {
	return Document{
		ID:           row.ID,
		SourcePath:   row.SourcePath,
		FileName:     row.FileName,
		Extension:    row.Extension,
		SizeBytes:    row.SizeBytes,
		ContentHash:  stringFromPtr(row.ContentHash),
		ChunkCount:   row.ChunkCount,
		TotalChars:   row.TotalChars,
		Status:       row.Status,
		ErrorMessage: stringFromPtr(row.ErrorMessage),
		CreatedAt:    timeFromMillis(row.CreatedAt),
		UpdatedAt:    timeFromMillis(row.UpdatedAt),
	}
}

func textChunkFromSQLCPage(row sqlc.ListDatasourceV2ChunksPageRow) TextChunk {
	return TextChunk{
		ID:             row.ID,
		DocumentID:     row.DocumentID,
		ChunkIndex:     row.ChunkIndex,
		Content:        row.Content,
		CharCount:      row.CharCount,
		ByteCount:      row.ByteCount,
		Embedding:      row.Embedding,
		EmbeddingModel: row.EmbeddingModel,
		EmbeddingDim:   row.EmbeddingDim,
		TokenCount:     row.TokenCount,
		CreatedAt:      timeFromMillis(row.CreatedAt),
	}
}

func semanticChunkFromSQLC(row sqlc.SearchDatasourceV2ChunksByEmbeddingRow) SemanticChunk {
	return SemanticChunk{
		TextChunk: TextChunk{
			ID:             row.ID,
			DocumentID:     row.DocumentID,
			ChunkIndex:     row.ChunkIndex,
			Content:        row.Content,
			CharCount:      row.CharCount,
			ByteCount:      row.ByteCount,
			Embedding:      row.Embedding,
			EmbeddingModel: row.EmbeddingModel,
			EmbeddingDim:   row.EmbeddingDim,
			TokenCount:     row.TokenCount,
			CreatedAt:      timeFromMillis(row.CreatedAt),
		},
		SourcePath: row.SourcePath,
		FileName:   row.FileName,
		Distance:   row.Distance,
	}
}

func stringFromPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timeFromMillis(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return platformdb.TimeFromMillis(value)
}

func wrapDatasourceV2Error(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "datasource_v2")
}

var _ Store = (*store)(nil)
