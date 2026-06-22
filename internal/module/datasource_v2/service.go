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
	datasourcev2store "github.com/anthropic-ai/super-agent-v3/internal/store/datasourcev2"
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
	errDatasourceV2SearchQueryRequired = errors.New("datasource v2: semantic query is required")
	errDatasourceV2MissingFileName     = errors.New("datasource v2: fileName is required")
	errDatasourceV2SizeBytesInvalid    = errors.New("datasource v2: sizeBytes must be non-negative")
)

// Service 暴露 datasource_v2 的文件正文导入能力。
// 目前只接收本机绝对路径，并把 PDF/TXT/TEXT 正文按分块写入数据库。
type Service interface {
	ImportFileText(context.Context, ImportFileTextRequest) (ImportFileTextResult, error)
	ImportLocalFile(context.Context, ImportLocalFileRequest) (ImportFileTextResult, error)
	ListDocuments(context.Context, ListDocumentsRequest) (ListDocumentsResult, error)
	GetDocument(context.Context, GetDocumentRequest) (GetDocumentResult, error)
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

// ListDocumentsRequest filters datasource_v2 documents and always requires an explicit limit.
type ListDocumentsRequest struct {
	Keyword string `json:"keyword"`
	Limit   int32  `json:"limit"`
}

// ListDocumentsResult returns the documents shown by the datasource table.
type ListDocumentsResult struct {
	Documents []DocumentResult `json:"documents"`
}

// GetDocumentRequest identifies one datasource_v2 document.
type GetDocumentRequest struct {
	DocumentID int64 `json:"documentId"`
}

// GetDocumentResult returns document metadata with persisted text chunks for inspection.
type GetDocumentResult struct {
	Document DocumentResult    `json:"document"`
	Chunks   []TextChunkResult `json:"chunks"`
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

// UpdateDocumentRequest edits metadata without rewriting imported text chunks.
type UpdateDocumentRequest struct {
	DocumentID int64  `json:"documentId"`
	SourcePath string `json:"sourcePath"`
	FileName   string `json:"fileName"`
	Extension  string `json:"extension"`
	SizeBytes  int64  `json:"sizeBytes"`
}

// DeleteDocumentRequest identifies one datasource_v2 document to remove.
type DeleteDocumentRequest struct {
	DocumentID int64 `json:"documentId"`
}

// DeleteDocumentResult confirms the document removed by datasourceV2/delete.
type DeleteDocumentResult struct {
	DocumentID int64 `json:"documentId"`
	Deleted    bool  `json:"deleted"`
}

// DocumentResult is the JSON shape shared by list, get, import, and update.
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

// TextChunkResult exposes stored chunks for a single document detail view.
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
	store datasourcev2store.Store
}

// NewService 创建 datasource_v2 service。
// store 必须由 fx 注入；如果缺失，调用导入接口会 fail-fast 返回配置错误。
func NewService(store datasourcev2store.Store) Service {
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

// ListDocuments reads datasource metadata for the UI table.
func (s *service) ListDocuments(ctx context.Context, req ListDocumentsRequest) (ListDocumentsResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.requireStore(); err != nil {
		return ListDocumentsResult{}, err
	}
	if req.Limit <= 0 {
		return ListDocumentsResult{}, errDatasourceV2ListLimitRequired
	}
	docs, err := s.store.ListDocuments(ctx, datasourcev2store.ListDocumentsParams{
		Keyword: strings.TrimSpace(req.Keyword),
		Limit:   req.Limit,
	})
	if err != nil {
		return ListDocumentsResult{}, err
	}
	return ListDocumentsResult{Documents: documentResults(docs)}, nil
}

// GetDocument reads one document and its text chunks for the detail view.
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
	chunks, err := s.store.ListChunks(ctx, req.DocumentID)
	if err != nil {
		return GetDocumentResult{}, err
	}
	return GetDocumentResult{
		Document: documentResult(*doc),
		Chunks:   textChunkResults(chunks),
	}, nil
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
	if req.Limit <= 0 {
		return SearchRelevantChunksResult{}, errDatasourceV2ListLimitRequired
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return SearchRelevantChunksResult{}, errDatasourceV2SearchQueryRequired
	}
	embedding, _, err := datasourceV2ChunkEmbedding(query)
	if err != nil {
		return SearchRelevantChunksResult{}, err
	}
	chunks, err := s.store.SearchChunks(ctx, datasourcev2store.SearchChunksParams{
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

// UpdateDocument validates metadata edits and persists them without touching chunks.
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

// DeleteDocument removes the document row and relies on store-level cascade for chunks.
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

func (s *service) requireStore() error {
	if s == nil || s.store == nil {
		return errDatasourceV2StoreNotConfigured
	}
	return nil
}

// importSourceText 用 store 事务包住一次完整导入。
// imported 只在事务回调全部成功后返回，避免调用方拿到未标记 ready 的文档。
func (s *service) importSourceText(ctx context.Context, source importSource) (*datasourcev2store.Document, error) {
	var imported *datasourcev2store.Document
	err := s.store.WithTx(ctx, func(txStore datasourcev2store.Store) error {
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
	txStore datasourcev2store.Store,
	source importSource,
) (*datasourcev2store.Document, error) {
	doc, err := txStore.UpsertImporting(ctx, datasourcev2store.UpsertDocumentParams{
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
	return txStore.MarkReady(ctx, datasourcev2store.MarkReadyParams{
		DocumentID:  doc.ID,
		ContentHash: summary.contentHash,
		ChunkCount:  summary.chunkCount,
		TotalChars:  summary.totalChars,
	})
}

type importSource struct {
	path      string
	fileName  string
	extension string
	sizeBytes int64
}

func prepareImportSource(ctx context.Context, req ImportFileTextRequest) (importSource, error) {
	sourcePath, err := validateImportFileRequest(req)
	if err != nil {
		return importSource{}, err
	}
	return prepareValidatedImportSource(ctx, sourcePath)
}

func prepareLocalImportSource(ctx context.Context, req ImportLocalFileRequest) (importSource, error) {
	sourcePath, err := validateImportSourcePath(req.SourcePath)
	if err != nil {
		return importSource{}, err
	}
	return prepareValidatedImportSource(ctx, sourcePath)
}

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
	return importSource{
		path:      sourcePath,
		fileName:  filepath.Base(sourcePath),
		extension: extension,
		sizeBytes: info.Size(),
	}, nil
}

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

func currentDatasourceV2WorkspaceRoot() (string, error) {
	baseDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return filepath.Clean(baseDir), nil
}

func isSupportedDatasourceV2Extension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".pdf", ".txt", ".text":
		return true
	default:
		return false
	}
}

type chunkWriteSummary struct {
	contentHash string
	chunkCount  int32
	totalChars  int32
}

func importFileTextResult(doc datasourcev2store.Document) ImportFileTextResult {
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

func validateUpdateDocumentRequest(req UpdateDocumentRequest) (datasourcev2store.UpdateDocumentParams, error) {
	if req.DocumentID <= 0 {
		return datasourcev2store.UpdateDocumentParams{}, errDatasourceV2DocumentIDRequired
	}
	sourcePath, err := validateImportFileRequest(ImportFileTextRequest{SourcePath: req.SourcePath})
	if err != nil {
		return datasourcev2store.UpdateDocumentParams{}, err
	}
	fileName := strings.TrimSpace(req.FileName)
	if fileName == "" {
		return datasourcev2store.UpdateDocumentParams{}, errDatasourceV2MissingFileName
	}
	sourceExtension := strings.ToLower(filepath.Ext(sourcePath))
	if !isSupportedDatasourceV2Extension(sourceExtension) {
		return datasourcev2store.UpdateDocumentParams{}, fmt.Errorf("%w: %s", errUnsupportedFileExtension, sourceExtension)
	}
	extension := strings.ToLower(strings.TrimSpace(req.Extension))
	if extension == "" {
		extension = sourceExtension
	}
	if !isSupportedDatasourceV2Extension(extension) {
		return datasourcev2store.UpdateDocumentParams{}, fmt.Errorf("%w: %s", errUnsupportedFileExtension, extension)
	}
	if req.SizeBytes < 0 {
		return datasourcev2store.UpdateDocumentParams{}, errDatasourceV2SizeBytesInvalid
	}
	return datasourcev2store.UpdateDocumentParams{
		DocumentID: req.DocumentID,
		SourcePath: sourcePath,
		FileName:   fileName,
		Extension:  extension,
		SizeBytes:  req.SizeBytes,
	}, nil
}

func documentResults(docs []datasourcev2store.Document) []DocumentResult {
	results := make([]DocumentResult, 0, len(docs))
	for _, doc := range docs {
		results = append(results, documentResult(doc))
	}
	return results
}

func documentResult(doc datasourcev2store.Document) DocumentResult {
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

func textChunkResults(chunks []datasourcev2store.TextChunk) []TextChunkResult {
	results := make([]TextChunkResult, 0, len(chunks))
	for _, chunk := range chunks {
		results = append(results, textChunkResult(chunk))
	}
	return results
}

func textChunkResult(chunk datasourcev2store.TextChunk) TextChunkResult {
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

func semanticChunkResults(chunks []datasourcev2store.SemanticChunk) []SemanticChunkResult {
	results := make([]SemanticChunkResult, 0, len(chunks))
	for _, chunk := range chunks {
		results = append(results, semanticChunkResult(chunk))
	}
	return results
}

func semanticChunkResult(chunk datasourcev2store.SemanticChunk) SemanticChunkResult {
	return SemanticChunkResult{
		TextChunkResult: textChunkResult(chunk.TextChunk),
		SourcePath:      chunk.SourcePath,
		FileName:        chunk.FileName,
		Distance:        chunk.Distance,
	}
}
