package datasourcev2

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	datasourcev2store "github.com/anthropic-ai/super-agent-v3/internal/store/datasourcev2"
)

const datasourceV2ChunkTargetBytes = 64 * 1024

var (
	errDatasourceV2StoreNotConfigured = errors.New("datasource v2 store is not configured")
	errMissingSourcePath              = errors.New("datasource v2: sourcePath is required")
	errSourcePathMustBeAbsolute       = errors.New("datasource v2: sourcePath must be absolute")
	errSourcePathMustBeFile           = errors.New("datasource v2: sourcePath must be a file")
	errUnsupportedFileExtension       = errors.New("datasource v2: unsupported file extension")
	errDatasourceV2ContentEmpty       = errors.New("datasource v2: extracted content is empty")
	errDatasourceV2InvalidUTF8        = errors.New("datasource v2: file is not valid UTF-8 text")
	errDatasourceV2TextTooLarge       = errors.New("datasource v2: text is too large")
	errDatasourceV2DocumentIDRequired = errors.New("datasource v2: documentId is required")
	errDatasourceV2ListLimitRequired  = errors.New("datasource v2: limit must be positive")
	errDatasourceV2MissingFileName    = errors.New("datasource v2: fileName is required")
	errDatasourceV2SizeBytesInvalid   = errors.New("datasource v2: sizeBytes must be non-negative")
)

// Service 暴露 datasource_v2 的文件正文导入能力。
// 目前只接收本机绝对路径，并把 PDF/TXT/TEXT 正文按分块写入数据库。
type Service interface {
	ImportFileText(context.Context, ImportFileTextRequest) (ImportFileTextResult, error)
	ListDocuments(context.Context, ListDocumentsRequest) (ListDocumentsResult, error)
	GetDocument(context.Context, GetDocumentRequest) (GetDocumentResult, error)
	UpdateDocument(context.Context, UpdateDocumentRequest) (DocumentResult, error)
	DeleteDocument(context.Context, DeleteDocumentRequest) (DeleteDocumentResult, error)
}

// ImportFileTextRequest 是 datasourceV2/importText 的 RPC 入参。
type ImportFileTextRequest struct {
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
	ID         int64     `json:"id"`
	DocumentID int64     `json:"documentId"`
	ChunkIndex int32     `json:"chunkIndex"`
	Content    string    `json:"content"`
	CharCount  int32     `json:"charCount"`
	ByteCount  int32     `json:"byteCount"`
	CreatedAt  time.Time `json:"createdAt"`
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
	sourcePath := strings.TrimSpace(req.SourcePath)
	if sourcePath == "" {
		return "", errMissingSourcePath
	}
	sourcePath = filepath.Clean(sourcePath)
	if !filepath.IsAbs(sourcePath) {
		return "", errSourcePathMustBeAbsolute
	}
	return sourcePath, nil
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

// writeSourceChunks 根据白名单后缀选择文本或 PDF 抽取路径。
// 所有格式最终都写成 UTF-8 文本分块，后续入库和摘要逻辑保持一致。
func writeSourceChunks(
	ctx context.Context,
	source importSource,
	documentID int64,
	store datasourcev2store.Store,
) (summary chunkWriteSummary, err error) {
	if source.extension == ".pdf" {
		return writePDFChunks(ctx, source.path, documentID, store)
	}
	return writeTextChunks(ctx, source.path, documentID, store)
}

// writeTextChunks 打开源文件并把 UTF-8 正文流式写成数据库分块。
// 文件关闭错误也会返回给调用方，确保导入成功只代表读取链路完整结束。
func writeTextChunks(
	ctx context.Context,
	sourcePath string,
	documentID int64,
	store datasourcev2store.Store,
) (summary chunkWriteSummary, err error) {
	file, err := os.Open(sourcePath)
	if err != nil {
		return chunkWriteSummary{}, fmt.Errorf("open source file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close source file: %w", closeErr)
		}
	}()

	writer := newChunkWriter(documentID, store)
	reader := bufio.NewReaderSize(file, datasourceV2ChunkTargetBytes)
	if err := writeReaderRunes(ctx, reader, writer); err != nil {
		return chunkWriteSummary{}, err
	}
	if err := writer.flush(ctx); err != nil {
		return chunkWriteSummary{}, err
	}
	return writer.summary()
}

// writePDFChunks 先抽取 PDF 文本，再复用分块写入器入库。
// PDF 没有可抽取文本时直接返回错误，避免把空数据源标记为 ready。
func writePDFChunks(
	ctx context.Context,
	sourcePath string,
	documentID int64,
	store datasourcev2store.Store,
) (chunkWriteSummary, error) {
	text, err := extractPDFText(sourcePath)
	if err != nil {
		return chunkWriteSummary{}, err
	}
	writer := newChunkWriter(documentID, store)
	reader := bufio.NewReaderSize(strings.NewReader(text), datasourceV2ChunkTargetBytes)
	if err := writeReaderRunes(ctx, reader, writer); err != nil {
		return chunkWriteSummary{}, err
	}
	if err := writer.flush(ctx); err != nil {
		return chunkWriteSummary{}, err
	}
	return writer.summary()
}

// writeReaderRunes 按 UTF-8 rune 流式读取文件并交给 chunkWriter 聚合。
// 它不缓存整篇文本；遇到非法编码会立即返回错误并中断导入。
func writeReaderRunes(ctx context.Context, reader *bufio.Reader, writer *chunkWriter) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, done, err := readTextRune(reader)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if err := writer.writeRune(ctx, r); err != nil {
			return err
		}
	}
}

func readTextRune(reader *bufio.Reader) (rune, bool, error) {
	r, size, err := reader.ReadRune()
	if errors.Is(err, io.EOF) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read source file: %w", err)
	}
	if r == unicode.ReplacementChar && size == 1 {
		return 0, false, errDatasourceV2InvalidUTF8
	}
	return r, false, nil
}

type chunkWriter struct {
	documentID int64
	store      datasourcev2store.Store
	hash       hashWriter
	builder    strings.Builder
	chunkIndex int32
	chunkBytes int32
	chunkChars int32
	totalChars int32
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func newChunkWriter(documentID int64, store datasourcev2store.Store) *chunkWriter {
	return &chunkWriter{
		documentID: documentID,
		store:      store,
		hash:       sha256.New(),
	}
}

// writeRune 把一个合法 UTF-8 rune 追加到当前分块，并同步更新摘要。
// 当前分块达到目标字节数时会立即 flush，避免大文件长期占用内存。
func (w *chunkWriter) writeRune(ctx context.Context, r rune) error {
	if w.chunkChars == math.MaxInt32 || w.totalChars == math.MaxInt32 {
		return errDatasourceV2TextTooLarge
	}
	var encoded [utf8.UTFMax]byte
	encodedBytes := utf8.EncodeRune(encoded[:], r)
	if encodedBytes > math.MaxInt32-int(w.chunkBytes) {
		return errDatasourceV2TextTooLarge
	}
	if _, err := w.hash.Write(encoded[:encodedBytes]); err != nil {
		return err
	}
	w.builder.WriteRune(r)
	w.chunkBytes += int32(encodedBytes)
	w.chunkChars++
	if w.chunkBytes >= datasourceV2ChunkTargetBytes {
		return w.flush(ctx)
	}
	return nil
}

// flush 将当前分块写入数据库并重置内存缓冲。
// 分块序号和总字符数在这里推进，防止写库失败时本地状态先行变化。
func (w *chunkWriter) flush(ctx context.Context) error {
	if w.chunkChars == 0 {
		return nil
	}
	if int64(w.totalChars)+int64(w.chunkChars) > math.MaxInt32 {
		return errDatasourceV2TextTooLarge
	}
	if w.chunkIndex == math.MaxInt32 {
		return errDatasourceV2TextTooLarge
	}
	if err := w.store.InsertChunk(ctx, datasourcev2store.InsertChunkParams{
		DocumentID: w.documentID,
		ChunkIndex: w.chunkIndex,
		Content:    w.builder.String(),
		CharCount:  w.chunkChars,
		ByteCount:  w.chunkBytes,
	}); err != nil {
		return err
	}
	w.totalChars += w.chunkChars
	w.chunkIndex++
	w.builder.Reset()
	w.chunkBytes = 0
	w.chunkChars = 0
	return nil
}

// summary 返回整篇文件的摘要和分块统计。
// 没有写入任何分块说明文件没有可用正文，调用方必须把导入视为失败。
func (w *chunkWriter) summary() (chunkWriteSummary, error) {
	if w.chunkIndex == 0 {
		return chunkWriteSummary{}, errDatasourceV2ContentEmpty
	}
	return chunkWriteSummary{
		contentHash: "sha256:" + hex.EncodeToString(w.hash.Sum(nil)),
		chunkCount:  w.chunkIndex,
		totalChars:  w.totalChars,
	}, nil
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
		ID:         chunk.ID,
		DocumentID: chunk.DocumentID,
		ChunkIndex: chunk.ChunkIndex,
		Content:    chunk.Content,
		CharCount:  chunk.CharCount,
		ByteCount:  chunk.ByteCount,
		CreatedAt:  chunk.CreatedAt,
	}
}
