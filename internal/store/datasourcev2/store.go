package datasourcev2

import (
	"context"
	"errors"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type querier interface {
	UpsertDatasourceV2DocumentImporting(
		ctx context.Context,
		arg sqlc.UpsertDatasourceV2DocumentImportingParams,
	) (sqlc.DatasourceV2Document, error)
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

// NewStore 创建不带事务 runner 的 store，主要用于窄接口单元测试。
// 正式 fx 装配会使用 module.go 里的构造函数注入事务 runner。
func NewStore(q *sqlc.Queries) Store {
	return newStore(q, q, nil)
}

func newStore(q querier, queries *sqlc.Queries, runInTx txRunner) Store {
	return &store{q: q, queries: queries, runInTx: runInTx}
}

// WithTx 在数据库事务中执行 datasource_v2 写入流程。
// 该 store 没有事务 runner 时直接报错，避免分块重写退化成非原子写入。
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

// UpsertImporting 写入或重置文档元数据为 importing 状态。
// 同一路径重复导入会复用原 document id，后续 DeleteChunks 会清掉旧正文分块。
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

// DeleteChunks 删除指定文档的旧正文分块。
// 调用方应在同一事务里随后插入新分块并标记文档 ready。
func (s *store) DeleteChunks(ctx context.Context, documentID int64) error {
	if documentID <= 0 {
		return wrapDatasourceV2Error(errors.New("document id is required"), "delete_chunks")
	}
	_, err := s.q.DeleteDatasourceV2ChunksByDocumentID(ctx, sqlc.DeleteDatasourceV2ChunksByDocumentIDParams{
		DocumentID: documentID,
	})
	return wrapDatasourceV2Error(err, "delete_chunks")
}

// InsertChunk 写入一个正文分块。
// chunk_index 由 service 按读取顺序递增，数据库唯一约束负责阻止重复分块。
func (s *store) InsertChunk(ctx context.Context, params InsertChunkParams) error {
	if err := validateInsertChunkParams(params); err != nil {
		return wrapDatasourceV2Error(err, "insert_chunk")
	}
	return wrapDatasourceV2Error(s.q.InsertDatasourceV2Chunk(ctx, sqlc.InsertDatasourceV2ChunkParams{
		DocumentID: params.DocumentID,
		ChunkIndex: params.ChunkIndex,
		Content:    params.Content,
		CharCount:  params.CharCount,
		ByteCount:  params.ByteCount,
	}), "insert_chunk")
}

// MarkReady 把文档从 importing 标记为 ready，并记录正文摘要和分块统计。
// 这一步必须在所有分块写入成功后执行，失败时事务回滚会保留旧版本。
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

// validateInsertChunkParams 检查正文分块写入的最小完整性。
// 分块内容和计数必须先在 service 层算好，这里只阻止零长度内容、坏序号和坏计数入库。
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
		CreatedAt:    timeFromPG(row.CreatedAt),
		UpdatedAt:    timeFromPG(row.UpdatedAt),
	}
}

func stringFromPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timeFromPG(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}

func wrapDatasourceV2Error(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "datasource_v2")
}

var _ Store = (*store)(nil)
