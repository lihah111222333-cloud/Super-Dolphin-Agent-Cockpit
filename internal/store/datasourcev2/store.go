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
	ListDatasourceV2Chunks(ctx context.Context, arg sqlc.ListDatasourceV2ChunksParams) ([]sqlc.ListDatasourceV2ChunksRow, error)
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

// NewStore creates a datasource_v2 store without a transaction runner for narrow unit tests.
func NewStore(q *sqlc.Queries) Store {
	return newStore(q, q, nil)
}

func newStore(q querier, queries *sqlc.Queries, runInTx txRunner) Store {
	return &store{q: q, queries: queries, runInTx: runInTx}
}

// WithTx runs the datasource_v2 write flow inside a SQLite transaction.
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

// ListDocuments reads datasource_v2 documents with an explicit limit.
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

// GetDocument reads one datasource_v2 document metadata row.
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

// ListChunks reads persisted document text chunks in chunk order.
func (s *store) ListChunks(ctx context.Context, documentID int64) ([]TextChunk, error) {
	if documentID <= 0 {
		return nil, wrapDatasourceV2Error(errors.New("document id is required"), "list_chunks")
	}
	rows, err := s.q.ListDatasourceV2Chunks(ctx, sqlc.ListDatasourceV2ChunksParams{DocumentID: documentID})
	if err != nil {
		return nil, wrapDatasourceV2Error(err, "list_chunks")
	}
	chunks := make([]TextChunk, 0, len(rows))
	for _, row := range rows {
		chunks = append(chunks, textChunkFromSQLC(row))
	}
	return chunks, nil
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

// UpsertImporting writes or resets document metadata to importing status.
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

// UpdateDocument edits document metadata without touching chunks or ready status.
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

// DeleteDocument deletes a document and its cascade-owned text chunks.
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

// DeleteChunks deletes old text chunks for one document.
func (s *store) DeleteChunks(ctx context.Context, documentID int64) error {
	if documentID <= 0 {
		return wrapDatasourceV2Error(errors.New("document id is required"), "delete_chunks")
	}
	_, err := s.q.DeleteDatasourceV2ChunksByDocumentID(ctx, sqlc.DeleteDatasourceV2ChunksByDocumentIDParams{
		DocumentID: documentID,
	})
	return wrapDatasourceV2Error(err, "delete_chunks")
}

// InsertChunk persists one text chunk.
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

// MarkReady marks an importing document ready and writes text summary fields.
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

func textChunkFromSQLC(row sqlc.ListDatasourceV2ChunksRow) TextChunk {
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
