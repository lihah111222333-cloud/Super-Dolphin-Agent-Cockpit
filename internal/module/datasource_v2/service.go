// Package datasourcev2 提供文件正文导入、分块存储和语义检索能力，供 prompt 动态段和前端数据源管理页使用。
package datasourcev2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

var (
	errDatasourceV2StoreNotConfigured  = errors.New("datasource v2 store is not configured")
	errMissingSourcePath               = errors.New("datasource v2: sourcePath is required")
	errSourcePathMustBeAbsolute        = errors.New("datasource v2: sourcePath must be absolute")
	errSourcePathOutsideWorkspace      = errors.New("datasource v2: sourcePath outside workspace")
	errSourcePathMustBeFile            = errors.New("datasource v2: sourcePath must be a file")
	errUnsupportedFileExtension        = errors.New("datasource v2: unsupported file extension")
	errDatasourceV2ContentEmpty        = errors.New("datasource v2: extracted content is empty")
	errDatasourceV2InvalidUTF8         = errors.New("datasource v2: file is not valid UTF-8 text")
	errDatasourceV2TextTooLarge        = errors.New("datasource v2: text is too large")
	errDatasourceV2DocumentIDRequired  = errors.New("datasource v2: documentId is required")
	errDatasourceV2ListLimitRequired   = errors.New("datasource v2: limit must be positive")
	errDatasourceV2ListLimitTooLarge   = errors.New("datasource v2: limit is too large")
	errDatasourceV2SearchQueryRequired = errors.New("datasource v2: semantic query is required")
	errDatasourceV2MissingFileName     = errors.New("datasource v2: fileName is required")
	errDatasourceV2SizeBytesInvalid    = errors.New("datasource v2: sizeBytes must be non-negative")
	errDatasourceV2ChunkCursorRequired = errors.New("datasource v2: cursor is required")
)

const (
	datasourceV2MaxImportBytes        = 10 * 1024 * 1024
	datasourceV2MaxListLimit          = 1000
	datasourceV2DefaultChunkPageLimit = 50
	datasourceV2MaxChunkPageLimit     = 500
	datasourceV2MaxChunkResponseBytes = 128 * 1024
)

// Service 暴露 datasource_v2 的文件正文导入能力。
// 目前只接收本机绝对路径，并把 PDF/TXT/TEXT 正文按分块写入数据库。
type Service interface {
	ImportFileText(context.Context, ImportFileTextRequest) (ImportFileTextResult, error)
	ImportLocalFile(context.Context, ImportLocalFileRequest) (ImportFileTextResult, error)
	ListDocuments(context.Context, ListDocumentsRequest) (ListDocumentsResult, error)
	GetDocument(context.Context, GetDocumentRequest) (GetDocumentResult, error)
	ListChunks(context.Context, ListChunksRequest) (ListChunksResult, error)
	SearchRelevantChunks(context.Context, SearchRelevantChunksRequest) (SearchRelevantChunksResult, error)
	UpdateDocument(context.Context, UpdateDocumentRequest) (DocumentResult, error)
	DeleteDocument(context.Context, DeleteDocumentRequest) (DeleteDocumentResult, error)
}

// ImportFileTextRequest 是 datasourceV2/importText 的 RPC 入参。
type ImportFileTextRequest struct {
	SourcePath string `json:"sourcePath"`
}

// ImportLocalFileRequest 是 datasourceV2/importLocalFile 的 RPC 入参。
// 该接口只用于桌面端用户主动选择的本地文件，因此允许读取 workspace 外的绝对路径。
type ImportLocalFileRequest struct {
	SourcePath string `json:"sourcePath"`
}

// ImportFileTextResult 返回导入后的文档 id、摘要和分块统计。
type ImportFileTextResult struct {
	DocumentID  int64  `json:"documentId"`
	SourcePath  string `json:"sourcePath"`
	FileName    string `json:"fileName"`
	Extension   string `json:"extension"`
	SizeBytes   int64  `json:"sizeBytes"`
	ContentHash string `json:"contentHash"`
	ChunkCount  int32  `json:"chunkCount"`
	TotalChars  int32  `json:"totalChars"`
	Status      string `json:"status"`
}

// ListDocumentsRequest 描述 datasource_v2 列表页的过滤条件；Limit 必须显式传入，避免接口静默返回过大结果集。
type ListDocumentsRequest struct {
	Keyword string `json:"keyword"`
	Limit   int32  `json:"limit"`
}

// ListDocumentsResult 返回数据源列表页展示的文档摘要。
type ListDocumentsResult struct {
	Documents []DocumentResult `json:"documents"`
}

// GetDocumentRequest 指定要读取的 datasource_v2 文档。
type GetDocumentRequest struct {
	DocumentID int64 `json:"documentId"`
}

// GetDocumentResult 返回单篇文档元信息和已持久化的正文分块，供详情页检查。
type GetDocumentResult struct {
	Document   DocumentResult    `json:"document"`
	Chunks     []TextChunkResult `json:"chunks"`
	HasMore    bool              `json:"hasMore"`
	NextCursor int32             `json:"nextCursor"`
}

// ListChunksRequest 指定 datasourceV2/list_chunks 的显式分页参数。
type ListChunksRequest struct {
	DocumentID int64  `json:"documentId"`
	Limit      int32  `json:"limit"`
	Cursor     *int32 `json:"cursor"`
}

// ListChunksResult 返回 datasource_v2 文档正文分块页。
type ListChunksResult struct {
	Chunks     []TextChunkResult `json:"chunks"`
	HasMore    bool              `json:"hasMore"`
	NextCursor int32             `json:"nextCursor"`
}

// SearchRelevantChunksRequest 是 chat 请求做 datasource_v2 语义检索的入参。
type SearchRelevantChunksRequest struct {
	Query string `json:"query"`
	Limit int32  `json:"limit"`
}

// SearchRelevantChunksResult 返回按语义距离排序后的 datasource_v2 分块。
type SearchRelevantChunksResult struct {
	Chunks []SemanticChunkResult `json:"chunks"`
}

// UpdateDocumentRequest 描述只更新元数据的请求；正文分块不会在此路径重写。
type UpdateDocumentRequest struct {
	DocumentID int64  `json:"documentId"`
	SourcePath string `json:"sourcePath"`
	FileName   string `json:"fileName"`
	Extension  string `json:"extension"`
	SizeBytes  int64  `json:"sizeBytes"`
}

// DeleteDocumentRequest 指定要删除的 datasource_v2 文档。
type DeleteDocumentRequest struct {
	DocumentID int64 `json:"documentId"`
}

// DeleteDocumentResult 确认 datasourceV2/delete 删除的文档 ID 和结果状态。
type DeleteDocumentResult struct {
	DocumentID int64 `json:"documentId"`
	Deleted    bool  `json:"deleted"`
}

// DocumentResult 是 list、get、import、update 共用的 JSON 文档形状。
type DocumentResult struct {
	DocumentID   int64     `json:"documentId"`
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

// TextChunkResult 暴露单篇文档详情页需要展示的持久化分块。
type TextChunkResult struct {
	ID             int64     `json:"id"`
	DocumentID     int64     `json:"documentId"`
	ChunkIndex     int32     `json:"chunkIndex"`
	Content        string    `json:"content"`
	CharCount      int32     `json:"charCount"`
	ByteCount      int32     `json:"byteCount"`
	EmbeddingModel string    `json:"embeddingModel"`
	EmbeddingDim   int32     `json:"embeddingDim"`
	TokenCount     int32     `json:"tokenCount"`
	CreatedAt      time.Time `json:"createdAt"`
}

// SemanticChunkResult 是 prompt 检索可消费的 chunk DTO，包含来源文件和距离分数。
type SemanticChunkResult struct {
	TextChunkResult
	SourcePath string  `json:"sourcePath"`
	FileName   string  `json:"fileName"`
	Distance   float64 `json:"distance"`
}

type service struct {
	store datasourceV2Store
}

// NewService 创建 datasource_v2 service。
// store 必须由 fx 注入；如果缺失，调用导入接口会 fail-fast 返回配置错误。
func NewService(store datasourceV2Store) Service {
	return &service{store: store}
}

// ImportFileText 校验绝对路径，流式读取 UTF-8 正文，并把正文分块写入数据库。
// 元数据和分块写入在同一事务中完成，任一步失败都会回滚，避免留下半成品。
func (s *service) ImportFileText(ctx context.Context, req ImportFileTextRequest) (ImportFileTextResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.requireStore(); err != nil {
		return ImportFileTextResult{}, err
	}
	source, err := prepareImportSource(ctx, req)
	if err != nil {
		return ImportFileTextResult{}, err
	}
	imported, err := s.importSourceText(ctx, source)
	if err != nil {
		return ImportFileTextResult{}, err
	}
	return importFileTextResult(*imported), nil
}

// ImportLocalFile 读取用户在桌面端显式选择的本地文件，并把正文分块写入 datasource_v2 表。
// 它仍然要求绝对路径、普通文件和白名单扩展名，但不套用 workspace containment 限制。
func (s *service) ImportLocalFile(ctx context.Context, req ImportLocalFileRequest) (ImportFileTextResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.requireStore(); err != nil {
		return ImportFileTextResult{}, err
	}
	source, err := prepareLocalImportSource(ctx, req)
	if err != nil {
		return ImportFileTextResult{}, err
	}
	imported, err := s.importSourceText(ctx, source)
	if err != nil {
		return ImportFileTextResult{}, err
	}
	return importFileTextResult(*imported), nil
}

// ListDocuments 按关键词和显式 limit 读取 datasource_v2 文档摘要；store 未注入或 limit 缺失时 fail-fast。
func (s *service) ListDocuments(ctx context.Context, req ListDocumentsRequest) (ListDocumentsResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.requireStore(); err != nil {
		return ListDocumentsResult{}, err
	}
	if err := validateDatasourceV2Limit(req.Limit); err != nil {
		return ListDocumentsResult{}, err
	}
	docs, err := s.store.ListDocuments(ctx, datasourceV2ListDocumentsParams{
		Keyword: strings.TrimSpace(req.Keyword),
		Limit:   req.Limit,
	})
	if err != nil {
		return ListDocumentsResult{}, err
	}
	return ListDocumentsResult{Documents: documentResults(docs)}, nil
}

// GetDocument 读取单篇文档和对应正文分块；文档 ID 必须由调用方显式提供。
func (s *service) GetDocument(ctx context.Context, req GetDocumentRequest) (GetDocumentResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.requireStore(); err != nil {
		return GetDocumentResult{}, err
	}
	if req.DocumentID <= 0 {
		return GetDocumentResult{}, errDatasourceV2DocumentIDRequired
	}
	doc, err := s.store.GetDocument(ctx, req.DocumentID)
	if err != nil {
		return GetDocumentResult{}, err
	}
	firstCursor := int32(-1)
	page, err := s.listChunksPage(ctx, ListChunksRequest{
		DocumentID: req.DocumentID,
		Limit:      datasourceV2DefaultChunkPageLimit,
		Cursor:     &firstCursor,
	})
	if err != nil {
		return GetDocumentResult{}, err
	}
	return GetDocumentResult{
		Document:   documentResult(*doc),
		Chunks:     page.Chunks,
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	}, nil
}

// ListChunks 按显式 limit/cursor 读取 datasource_v2 文档分块页。
func (s *service) ListChunks(ctx context.Context, req ListChunksRequest) (ListChunksResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.requireStore(); err != nil {
		return ListChunksResult{}, err
	}
	return s.listChunksPage(ctx, req)
}

// SearchRelevantChunks 将当前 chat 请求向量化，并按语义距离取 datasource_v2 的前 N 个 ready 分块。
// 查询向量和导入分块使用同一个本地 embedding 模型；模型名或维度不匹配的历史分块不会参与排序。
func (s *service) SearchRelevantChunks(
	ctx context.Context,
	req SearchRelevantChunksRequest,
) (SearchRelevantChunksResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.requireStore(); err != nil {
		return SearchRelevantChunksResult{}, err
	}
	if err := validateDatasourceV2Limit(req.Limit); err != nil {
		return SearchRelevantChunksResult{}, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return SearchRelevantChunksResult{}, errDatasourceV2SearchQueryRequired
	}
	embedding, _, err := datasourceV2ChunkEmbedding(query)
	if err != nil {
		return SearchRelevantChunksResult{}, err
	}
	chunks, err := s.store.SearchChunks(ctx, datasourceV2SearchChunksParams{
		Embedding:      embedding,
		EmbeddingModel: datasourceV2EmbeddingModel,
		EmbeddingDim:   datasourceV2EmbeddingDimension,
		Limit:          req.Limit,
	})
	if err != nil {
		return SearchRelevantChunksResult{}, err
	}
	return SearchRelevantChunksResult{Chunks: semanticChunkResults(chunks)}, nil
}

// UpdateDocument 校验并持久化文档元数据编辑；它不触碰已导入的正文分块。
func (s *service) UpdateDocument(ctx context.Context, req UpdateDocumentRequest) (DocumentResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.requireStore(); err != nil {
		return DocumentResult{}, err
	}
	params, err := validateUpdateDocumentRequest(req)
	if err != nil {
		return DocumentResult{}, err
	}
	doc, err := s.store.UpdateDocument(ctx, params)
	if err != nil {
		return DocumentResult{}, err
	}
	return documentResult(*doc), nil
}

// DeleteDocument 删除指定文档，并依赖 store 层级联清理相关分块。
func (s *service) DeleteDocument(ctx context.Context, req DeleteDocumentRequest) (DeleteDocumentResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.requireStore(); err != nil {
		return DeleteDocumentResult{}, err
	}
	if req.DocumentID <= 0 {
		return DeleteDocumentResult{}, errDatasourceV2DocumentIDRequired
	}
	if err := s.store.DeleteDocument(ctx, req.DocumentID); err != nil {
		return DeleteDocumentResult{}, err
	}
	return DeleteDocumentResult{DocumentID: req.DocumentID, Deleted: true}, nil
}

// requireStore 检查 store 是否已注入，未注入时 fail-fast。
func (s *service) requireStore() error {
	if s == nil || s.store == nil {
		return errDatasourceV2StoreNotConfigured
	}
	return nil
}

func (s *service) listChunksPage(ctx context.Context, req ListChunksRequest) (ListChunksResult, error) {
	params, err := validateDatasourceV2ListChunksRequest(req)
	if err != nil {
		return ListChunksResult{}, err
	}
	page, err := s.store.ListChunksPage(ctx, params)
	if err != nil {
		return ListChunksResult{}, err
	}
	chunks := textChunkResults(page.Chunks)
	if err := enforceDatasourceV2ChunkResponseBytes(chunks); err != nil {
		return ListChunksResult{}, err
	}
	return ListChunksResult{
		Chunks:     chunks,
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	}, nil
}

// importSourceText 用 store 事务包住一次完整导入。
// imported 只在事务回调全部成功后返回，避免调用方拿到未标记 ready 的文档。
func (s *service) importSourceText(ctx context.Context, source importSource) (*datasourceV2Document, error) {
	var imported *datasourceV2Document
	err := s.store.WithTx(ctx, func(txStore datasourceV2Store) error {
		ready, err := importSourceTextInTx(ctx, txStore, source)
		if err != nil {
			return err
		}
		imported = ready
		return nil
	})
	if err != nil {
		return nil, err
	}
	if imported == nil {
		return nil, errDatasourceV2StoreNotConfigured
	}
	return imported, nil
}

// importSourceTextInTx 在同一事务中重置文档、清空旧分块、写新分块并标记 ready。
// 任一步失败都必须向上返回错误，让事务 runner 回滚旧版本不被破坏。
func importSourceTextInTx(
	ctx context.Context,
	txStore datasourceV2Store,
	source importSource,
) (*datasourceV2Document, error) {
	doc, err := txStore.UpsertImporting(ctx, datasourceV2UpsertDocumentParams{
		SourcePath: source.path,
		FileName:   source.fileName,
		Extension:  source.extension,
		SizeBytes:  source.sizeBytes,
	})
	if err != nil {
		return nil, err
	}
	if err := txStore.DeleteChunks(ctx, doc.ID); err != nil {
		return nil, err
	}
	summary, err := writeSourceChunks(ctx, source, doc.ID, txStore)
	if err != nil {
		return nil, err
	}
	return txStore.MarkReady(ctx, datasourceV2MarkReadyParams{
		DocumentID:  doc.ID,
		ContentHash: summary.contentHash,
		ChunkCount:  summary.chunkCount,
		TotalChars:  summary.totalChars,
	})
}

// importSource 保存已验证的导入文件元信息，供分块写入和入库使用。
type importSource struct {
	path      string
	fileName  string
	extension string
	sizeBytes int64
}

// prepareImportSource 校验 workspace 路径并读取文件元信息。
func prepareImportSource(ctx context.Context, req ImportFileTextRequest) (importSource, error) {
	sourcePath, err := validateImportFileRequest(req)
	if err != nil {
		return importSource{}, err
	}
	return prepareValidatedImportSource(ctx, sourcePath)
}

// prepareLocalImportSource 校验本地文件路径（不限 workspace），读取文件元信息。
func prepareLocalImportSource(ctx context.Context, req ImportLocalFileRequest) (importSource, error) {
	sourcePath, err := validateImportSourcePath(req.SourcePath)
	if err != nil {
		return importSource{}, err
	}
	return prepareValidatedImportSource(ctx, sourcePath)
}

// prepareValidatedImportSource 读取已验证路径的文件元信息并构建 importSource。
func prepareValidatedImportSource(ctx context.Context, sourcePath string) (importSource, error) {
	if err := ctx.Err(); err != nil {
		return importSource{}, err
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return importSource{}, fmt.Errorf("stat source file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return importSource{}, errSourcePathMustBeFile
	}
	extension := strings.ToLower(filepath.Ext(sourcePath))
	if !isSupportedDatasourceV2Extension(extension) {
		return importSource{}, fmt.Errorf("%w: %s", errUnsupportedFileExtension, extension)
	}
	if info.Size() > datasourceV2MaxImportBytes {
		return importSource{}, errDatasourceV2TextTooLarge
	}
	return importSource{
		path:      sourcePath,
		fileName:  filepath.Base(sourcePath),
		extension: extension,
		sizeBytes: info.Size(),
	}, nil
}

// validateImportFileRequest 检查 workspace 包含性，成功时返回清理后的绝对路径。
func validateImportFileRequest(req ImportFileTextRequest) (string, error) {
	sourcePath, err := validateImportSourcePath(req.SourcePath)
	if err != nil {
		return "", err
	}
	if err := ensureImportSourceInsideWorkspace(sourcePath); err != nil {
		return "", err
	}
	return sourcePath, nil
}

// validateImportSourcePath 清理并验证路径非空且为绝对路径。
func validateImportSourcePath(rawSourcePath string) (string, error) {
	sourcePath := strings.TrimSpace(rawSourcePath)
	if sourcePath == "" {
		return "", errMissingSourcePath
	}
	sourcePath = filepath.Clean(sourcePath)
	if !filepath.IsAbs(sourcePath) {
		return "", errSourcePathMustBeAbsolute
	}
	return sourcePath, nil
}

// ensureImportSourceInsideWorkspace 确保导入路径在当前 workspace 范围内。
func ensureImportSourceInsideWorkspace(sourcePath string) error {
	workspaceRoot, err := currentDatasourceV2WorkspaceRoot()
	if err != nil {
		return err
	}
	if !platformshared.ContainsPath(workspaceRoot, sourcePath) {
		return fmt.Errorf("%w: %s", errSourcePathOutsideWorkspace, sourcePath)
	}
	return nil
}

// currentDatasourceV2WorkspaceRoot 返回当前进程工作目录作为 workspace 根路径。
func currentDatasourceV2WorkspaceRoot() (string, error) {
	baseDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return filepath.Clean(baseDir), nil
}

// isSupportedDatasourceV2Extension 检查扩展名是否在支持列表中（pdf/txt/text）。
func isSupportedDatasourceV2Extension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".pdf", ".txt", ".text":
		return true
	default:
		return false
	}
}

// chunkWriteSummary 存储整篇文件分块写入后的摘要统计。
type chunkWriteSummary struct {
	contentHash string
	chunkCount  int32
	totalChars  int32
}

// importFileTextResult 将 store Document 转换为 ImportFileTextResult。
func importFileTextResult(doc datasourceV2Document) ImportFileTextResult {
	return ImportFileTextResult{
		DocumentID:  doc.ID,
		SourcePath:  doc.SourcePath,
		FileName:    doc.FileName,
		Extension:   doc.Extension,
		SizeBytes:   doc.SizeBytes,
		ContentHash: doc.ContentHash,
		ChunkCount:  doc.ChunkCount,
		TotalChars:  doc.TotalChars,
		Status:      doc.Status,
	}
}

// validateDatasourceV2Limit 确保列表和语义检索都有明确上限，避免前端或 prompt 请求无界读取。
func validateDatasourceV2Limit(limit int32) error {
	if limit <= 0 {
		return errDatasourceV2ListLimitRequired
	}
	if limit > datasourceV2MaxListLimit {
		return errDatasourceV2ListLimitTooLarge
	}
	return nil
}

// validateDatasourceV2ListChunksRequest 校验分块分页请求，cursor 必须显式提供。
func validateDatasourceV2ListChunksRequest(req ListChunksRequest) (datasourceV2ListChunksParams, error) {
	if req.DocumentID <= 0 {
		return datasourceV2ListChunksParams{}, errDatasourceV2DocumentIDRequired
	}
	if req.Limit <= 0 {
		return datasourceV2ListChunksParams{}, errDatasourceV2ListLimitRequired
	}
	if req.Limit > datasourceV2MaxChunkPageLimit {
		return datasourceV2ListChunksParams{}, errDatasourceV2ListLimitTooLarge
	}
	if req.Cursor == nil {
		return datasourceV2ListChunksParams{}, errDatasourceV2ChunkCursorRequired
	}
	if *req.Cursor < -1 {
		return datasourceV2ListChunksParams{}, fmt.Errorf("datasource v2: cursor must be -1 or greater")
	}
	return datasourceV2ListChunksParams{
		DocumentID: req.DocumentID,
		Limit:      req.Limit,
		Cursor:     *req.Cursor,
	}, nil
}

func enforceDatasourceV2ChunkResponseBytes(chunks []TextChunkResult) error {
	total := 0
	for _, chunk := range chunks {
		total += len([]byte(chunk.Content))
		if total > datasourceV2MaxChunkResponseBytes {
			return fmt.Errorf("datasource v2: response byte cap exceeded: %d > %d", total, datasourceV2MaxChunkResponseBytes)
		}
	}
	return nil
}

// validateUpdateDocumentRequest 校验更新请求并构建 store 参数，路径或扩展名不合法时 fail-fast。
func validateUpdateDocumentRequest(req UpdateDocumentRequest) (datasourceV2UpdateDocumentParams, error) {
	if req.DocumentID <= 0 {
		return datasourceV2UpdateDocumentParams{}, errDatasourceV2DocumentIDRequired
	}
	sourcePath, err := validateImportFileRequest(ImportFileTextRequest{SourcePath: req.SourcePath})
	if err != nil {
		return datasourceV2UpdateDocumentParams{}, err
	}
	fileName := strings.TrimSpace(req.FileName)
	if fileName == "" {
		return datasourceV2UpdateDocumentParams{}, errDatasourceV2MissingFileName
	}
	sourceExtension := strings.ToLower(filepath.Ext(sourcePath))
	if !isSupportedDatasourceV2Extension(sourceExtension) {
		return datasourceV2UpdateDocumentParams{}, fmt.Errorf("%w: %s", errUnsupportedFileExtension, sourceExtension)
	}
	extension := strings.ToLower(strings.TrimSpace(req.Extension))
	if extension == "" {
		extension = sourceExtension
	}
	if !isSupportedDatasourceV2Extension(extension) {
		return datasourceV2UpdateDocumentParams{}, fmt.Errorf("%w: %s", errUnsupportedFileExtension, extension)
	}
	if req.SizeBytes < 0 {
		return datasourceV2UpdateDocumentParams{}, errDatasourceV2SizeBytesInvalid
	}
	return datasourceV2UpdateDocumentParams{
		DocumentID: req.DocumentID,
		SourcePath: sourcePath,
		FileName:   fileName,
		Extension:  extension,
		SizeBytes:  req.SizeBytes,
	}, nil
}

// documentResults 将 store Document 切片批量转换为 DocumentResult。
func documentResults(docs []datasourceV2Document) []DocumentResult {
	results := make([]DocumentResult, 0, len(docs))
	for _, doc := range docs {
		results = append(results, documentResult(doc))
	}
	return results
}

// documentResult 将 store Document 转换为 DocumentResult。
func documentResult(doc datasourceV2Document) DocumentResult {
	return DocumentResult{
		DocumentID:   doc.ID,
		SourcePath:   doc.SourcePath,
		FileName:     doc.FileName,
		Extension:    doc.Extension,
		SizeBytes:    doc.SizeBytes,
		ContentHash:  doc.ContentHash,
		ChunkCount:   doc.ChunkCount,
		TotalChars:   doc.TotalChars,
		Status:       doc.Status,
		ErrorMessage: doc.ErrorMessage,
		CreatedAt:    doc.CreatedAt,
		UpdatedAt:    doc.UpdatedAt,
	}
}

// textChunkResults 将 store TextChunk 切片批量转换为 TextChunkResult。
func textChunkResults(chunks []datasourceV2TextChunk) []TextChunkResult {
	results := make([]TextChunkResult, 0, len(chunks))
	for _, chunk := range chunks {
		results = append(results, textChunkResult(chunk))
	}
	return results
}

// textChunkResult 将 store TextChunk 转换为 TextChunkResult。
func textChunkResult(chunk datasourceV2TextChunk) TextChunkResult {
	return TextChunkResult{
		ID:             chunk.ID,
		DocumentID:     chunk.DocumentID,
		ChunkIndex:     chunk.ChunkIndex,
		Content:        chunk.Content,
		CharCount:      chunk.CharCount,
		ByteCount:      chunk.ByteCount,
		EmbeddingModel: chunk.EmbeddingModel,
		EmbeddingDim:   chunk.EmbeddingDim,
		TokenCount:     chunk.TokenCount,
		CreatedAt:      chunk.CreatedAt,
	}
}

// semanticChunkResults 将 store SemanticChunk 切片批量转换为 SemanticChunkResult。
func semanticChunkResults(chunks []datasourceV2SemanticChunk) []SemanticChunkResult {
	results := make([]SemanticChunkResult, 0, len(chunks))
	for _, chunk := range chunks {
		results = append(results, semanticChunkResult(chunk))
	}
	return results
}

// semanticChunkResult 将 store SemanticChunk 转换为 SemanticChunkResult。
func semanticChunkResult(chunk datasourceV2SemanticChunk) SemanticChunkResult {
	return SemanticChunkResult{
		TextChunkResult: textChunkResult(chunk.datasourceV2TextChunk),
		SourcePath:      chunk.SourcePath,
		FileName:        chunk.FileName,
		Distance:        chunk.Distance,
	}
}
