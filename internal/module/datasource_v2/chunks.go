// Package datasourcev2 提供文件正文导入、分块存储和语义检索能力，供 prompt 动态段和前端数据源管理页使用。
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
	"strings"
	"unicode"
	"unicode/utf8"
)

// writeSourceChunks 根据白名单后缀选择文本或 PDF 抽取路径。
// 所有格式最终都写成 UTF-8 文本分块，后续入库和摘要逻辑保持一致。
func writeSourceChunks(
	ctx context.Context,
	source importSource,
	documentID int64,
	store Store,
) (summary chunkWriteSummary, err error) {
	if source.content == nil {
		return chunkWriteSummary{}, errors.New("datasource v2: prepared content is required")
	}
	writer := newChunkWriter(documentID, store)
	reader := bufio.NewReaderSize(strings.NewReader(source.content.text), datasourceV2ChunkMaxBytes)
	if err := writeReaderRunes(ctx, reader, writer); err != nil {
		return chunkWriteSummary{}, err
	}
	if err := writer.flush(ctx); err != nil {
		return chunkWriteSummary{}, err
	}
	return writer.summary()
}

// writeTextChunks 打开源文件并把 UTF-8 正文流式写成数据库分块。
// 文件关闭错误也会返回给调用方，确保导入成功只代表读取链路完整结束。
func writeTextChunks(
	ctx context.Context,
	sourcePath string,
	documentID int64,
	store Store,
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
	reader := bufio.NewReaderSize(file, datasourceV2ChunkMaxBytes)
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
	store Store,
) (chunkWriteSummary, error) {
	text, err := extractPDFText(ctx, sourcePath)
	if err != nil {
		return chunkWriteSummary{}, err
	}
	writer := newChunkWriter(documentID, store)
	reader := bufio.NewReaderSize(strings.NewReader(text), datasourceV2ChunkMaxBytes)
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

// readTextRune 从 buffered reader 读取一个合法 UTF-8 rune；遇到替换字符视为无效编码。
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

// chunkWriter 负责将文本流式切分为固定大小的分块并逐块写入数据库。
// asciiOpen 跟踪当前是否处于 ASCII token 中间，避免在 token 内部截断。
type chunkWriter struct {
	documentID  int64
	store       Store
	hash        hashWriter
	builder     strings.Builder
	chunkIndex  int32
	chunkBytes  int32
	chunkChars  int32
	chunkTokens int32
	totalChars  int32
	asciiOpen   bool
}

// hashWriter 是 chunkWriter 内部用于增量计算 SHA-256 的接口。
type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

// newChunkWriter 创建分块写入器，使用 SHA-256 对整篇文件做增量摘要。
func newChunkWriter(documentID int64, store Store) *chunkWriter {
	return &chunkWriter{
		documentID: documentID,
		store:      store,
		hash:       sha256.New(),
	}
}

// writeRune 把一个合法 UTF-8 rune 追加到当前分块，并同步更新摘要。
// 当前分块达到目标字节数时会立即 flush，避免大文件长期占用内存。
func (w *chunkWriter) writeRune(ctx context.Context, r rune) error {
	if forbiddenDatasourceTextRune(r) {
		return fmt.Errorf("datasource v2: forbidden text rune U+%04X", r)
	}
	if w.chunkChars == math.MaxInt32 || w.totalChars == math.MaxInt32 {
		return errDatasourceV2TextTooLarge
	}
	if w.shouldFlushBeforeRune(r) {
		if err := w.flush(ctx); err != nil {
			return err
		}
	}
	if err := w.appendRune(r); err != nil {
		return err
	}
	return w.flushIfFull(ctx)
}

// forbiddenDatasourceTextRune 识别不可进入 embedding 和 chunk store 的字符。
func forbiddenDatasourceTextRune(r rune) bool {
	return r == 0 || r == unicode.ReplacementChar || (unicode.IsControl(r) && r != '\n' && r != '\t')
}

// appendRune 将 rune 编码后追加到分块缓冲，同步更新字节数、字符数和 token 状态。
func (w *chunkWriter) appendRune(r rune) error {
	var encoded [utf8.UTFMax]byte
	encodedBytes := utf8.EncodeRune(encoded[:], r)
	if encodedBytes > math.MaxInt32-int(w.chunkBytes) {
		return errDatasourceV2TextTooLarge
	}
	tokenStarted, asciiOpen := datasourceV2AdvanceChunkTokenState(r, w.asciiOpen)
	if tokenStarted {
		if w.chunkTokens == math.MaxInt32 {
			return errDatasourceV2TextTooLarge
		}
		w.chunkTokens++
	}
	if _, err := w.hash.Write(encoded[:encodedBytes]); err != nil {
		return err
	}
	w.builder.WriteRune(r)
	w.chunkBytes += int32(encodedBytes)
	w.chunkChars++
	w.asciiOpen = asciiOpen
	return nil
}

// flushIfFull 当前分块字节数或 token 数达到阈值时立即 flush。
func (w *chunkWriter) flushIfFull(ctx context.Context) error {
	if w.chunkBytes >= datasourceV2ChunkMaxBytes {
		return w.flush(ctx)
	}
	if w.chunkTokens >= datasourceV2ChunkTargetTokens && !w.asciiOpen {
		return w.flush(ctx)
	}
	return nil
}

// shouldFlushBeforeRune 判断当前 rune 写入前是否需要先 flush，避免在 ASCII token 中间截断。
func (w *chunkWriter) shouldFlushBeforeRune(r rune) bool {
	if w.chunkChars == 0 || w.chunkTokens < datasourceV2ChunkTargetTokens {
		return false
	}
	if w.asciiOpen {
		return !isDatasourceV2ASCIITokenRune(r)
	}
	return true
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
	content := w.builder.String()
	embedding, tokenCount, err := datasourceV2ChunkEmbedding(content)
	if err != nil {
		return err
	}
	if err := w.store.InsertChunk(ctx, InsertChunkParams{
		DocumentID:     w.documentID,
		ChunkIndex:     w.chunkIndex,
		Content:        content,
		CharCount:      w.chunkChars,
		ByteCount:      w.chunkBytes,
		Embedding:      embedding,
		EmbeddingModel: datasourceV2EmbeddingModel,
		EmbeddingDim:   datasourceV2EmbeddingDimension,
		TokenCount:     tokenCount,
	}); err != nil {
		return err
	}
	w.totalChars += w.chunkChars
	w.chunkIndex++
	w.builder.Reset()
	w.chunkBytes = 0
	w.chunkChars = 0
	w.chunkTokens = 0
	w.asciiOpen = false
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
