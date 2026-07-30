package historyjsonl

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

const (
	historyPageCursorPrefix = "histpage:"
	sourceRevisionDomain    = "super-dolphin/historyjsonl/source-revision/v1\x00"
	sourceRevisionReadChunk = 64 * 1024
)

// JSONLPageResult 是倒序分页读取 JSONL 后返回给上层的通用结果。
type JSONLPageResult[T any] struct {
	Items          []T     // 当前页按时间正序排列的业务项。
	Offsets        []int64 // 每条记录在文件中的起始偏移，用作稳定消息 ID。
	HasMore        bool    // 是否还有更早的记录可继续读取。
	NextBefore     string  // 下一页 before 游标；为空表示没有下一页。
	SourceRevision string  // 当前文件 snapshot 的不透明版本；跨页保持一致。
}

// offsetCursor 是 history page 游标的 wire 结构，编码后不能随意改字段名。
type offsetCursor struct {
	Offset int64 `json:"offset"`
}

// ReadProviderMessagesPage 从 provider 历史中按 before 游标读取一页消息。
func ReadProviderMessagesPage(req ReadRequest, pageReq dto.MessagePageRequest) (dto.MessagePageResult, error) {
	path, provider, err := resolvePath(req)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	page, err := ReadJSONLPageStrict(path, pageReq.Limit, pageReq.Before, func(raw []byte) (dto.Message, bool, error) {
		return parseLineStrict(raw, provider)
	})
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	return dto.MessagePageResult{
		Messages:       messagesWithPageOffsets(page.Items, page.Offsets),
		HasMore:        page.HasMore,
		NextBefore:     page.NextBefore,
		SourceRevision: page.SourceRevision,
	}, nil
}

// ReadProviderMessagesPageOrError 将历史缺失转换为调用方指定错误，其他读取错误保持原样。
func ReadProviderMessagesPageOrError(req ReadRequest, pageReq dto.MessagePageRequest, missingErr error) (dto.MessagePageResult, error) {
	path, provider, err := resolvePath(req)
	if err != nil {
		if IsMissingProviderHistory(err) {
			return dto.MessagePageResult{}, missingErr
		}
		return dto.MessagePageResult{}, err
	}
	page, err := ReadJSONLPageStrict(path, pageReq.Limit, pageReq.Before, func(raw []byte) (dto.Message, bool, error) {
		return parseLineStrict(raw, provider)
	})
	if err != nil {
		if IsMissingProviderHistory(err) {
			return dto.MessagePageResult{}, missingErr
		}
		return dto.MessagePageResult{}, err
	}
	return dto.MessagePageResult{
		Messages:       messagesWithPageOffsets(page.Items, page.Offsets),
		HasMore:        page.HasMore,
		NextBefore:     page.NextBefore,
		SourceRevision: page.SourceRevision,
	}, nil
}

// messagesWithPageOffsets 用 JSONL 偏移补齐消息 ID，保持跨页去重稳定。
func messagesWithPageOffsets(messages []dto.Message, offsets []int64) []dto.Message {
	out := append([]dto.Message(nil), messages...)
	for i := range out {
		if i < len(offsets) && out[i].ID == 0 {
			out[i].ID = offsets[i] + 1
		}
	}
	return out
}

// ReadJSONLPage 从文件尾部向前读取 JSONL 页，适合只取最近历史的场景。
// parse 返回 ok=false 的行会被跳过，但文件/游标错误会 fail-fast 返回。
func ReadJSONLPage[T any](path string, limit int, before string, parse func([]byte) (T, bool)) (JSONLPageResult[T], error) {
	if parse == nil {
		return JSONLPageResult[T]{}, errors.New("history page parser is required")
	}
	return ReadJSONLPageStrict(path, limit, before, func(raw []byte) (T, bool, error) {
		item, ok := parse(raw)
		return item, ok, nil
	})
}

// ReadJSONLPageStrict 分页读取 JSONL，并传播 parser 返回的损坏记录错误。
func ReadJSONLPageStrict[T any](path string, limit int, before string, parse func([]byte) (T, bool, error)) (JSONLPageResult[T], error) {
	if err := validateJSONLPageRequest(limit, parse); err != nil {
		return JSONLPageResult[T]{}, err
	}
	file, size, err := openJSONLPageFile(path)
	if err != nil {
		return JSONLPageResult[T]{}, err
	}
	defer func() { _ = file.Close() }()
	beforeOffset, err := pageBeforeOffset(size, before)
	if err != nil {
		return JSONLPageResult[T]{}, err
	}
	revision, err := computeJSONLSourceRevision(file, size)
	if err != nil {
		return JSONLPageResult[T]{}, err
	}
	if beforeOffset == 0 {
		return JSONLPageResult[T]{SourceRevision: revision}, nil
	}
	records, err := readJSONLRecordsBackward(file, beforeOffset, limit+1, parse)
	if err != nil {
		return JSONLPageResult[T]{}, err
	}
	page := buildJSONLPage(records, limit)
	page.SourceRevision = revision
	return page, nil
}

// validateJSONLPageRequest 拒绝无效分页参数，避免后续循环出现空 parser 或非正 limit。
func validateJSONLPageRequest[T any](limit int, parse func([]byte) (T, bool, error)) error {
	if limit <= 0 {
		return errors.New("history page limit must be positive")
	}
	if parse == nil {
		return errors.New("history page parser is required")
	}
	return nil
}

// openJSONLPageFile 打开并确认 JSONL 路径是普通文件；失败时负责关闭已打开句柄。
func openJSONLPageFile(path string) (*os.File, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, fmt.Errorf("%w: %w", errProviderHistoryNotFound, err)
		}
		return nil, 0, fmt.Errorf("open history jsonl: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("stat history jsonl: %w", err)
	}
	if info.IsDir() {
		_ = file.Close()
		return nil, 0, errors.New("history jsonl path is a directory")
	}
	return file, info.Size(), nil
}

// computeJSONLSourceRevision 由 snapshot 长度和最后一条完整记录摘要生成不透明版本。
// 全程使用 ReadAt，不能移动分页读取依赖的文件游标。
func computeJSONLSourceRevision(file *os.File, size int64) (string, error) {
	recordDigest, err := lastCompleteJSONLRecordDigest(file, size)
	if err != nil {
		return "", err
	}
	var encodedSize [8]byte
	binary.BigEndian.PutUint64(encodedSize[:], uint64(size))
	hash := sha256.New()
	_, _ = hash.Write([]byte(sourceRevisionDomain))
	_, _ = hash.Write(encodedSize[:])
	_, _ = hash.Write(recordDigest[:])
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// lastCompleteJSONLRecordDigest 定位最后一个换行结束的非空记录，并仅返回其摘要。
func lastCompleteJSONLRecordDigest(file *os.File, size int64) ([sha256.Size]byte, error) {
	emptyDigest := sha256.Sum256(nil)
	end, err := findPreviousJSONLNewline(file, size)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	for end >= 0 {
		previous, err := findPreviousJSONLNewline(file, end)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		digest, nonEmpty, err := digestJSONLRecordRange(file, previous+1, end)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		if nonEmpty {
			return digest, nil
		}
		if previous < 0 {
			break
		}
		end = previous
	}
	return emptyDigest, nil
}

// findPreviousJSONLNewline 在 before 之前分块查找换行位置；找不到时返回 -1。
func findPreviousJSONLNewline(file *os.File, before int64) (int64, error) {
	for end := before; end > 0; {
		start := max(end-sourceRevisionReadChunk, int64(0))
		buffer := make([]byte, int(end-start))
		if _, err := file.ReadAt(buffer, start); err != nil {
			return 0, fmt.Errorf("read history jsonl revision tail: %w", err)
		}
		if index := bytes.LastIndexByte(buffer, '\n'); index >= 0 {
			return start + int64(index), nil
		}
		end = start
	}
	return -1, nil
}

// digestJSONLRecordRange 流式摘要一条记录，并区分空白行与真实 JSONL 记录。
func digestJSONLRecordRange(file *os.File, start, end int64) ([sha256.Size]byte, bool, error) {
	hash := sha256.New()
	nonEmpty := false
	buffer := make([]byte, sourceRevisionReadChunk)
	for offset := start; offset < end; {
		readSize := int64(len(buffer))
		if remaining := end - offset; remaining < readSize {
			readSize = remaining
		}
		chunk := buffer[:int(readSize)]
		if _, err := file.ReadAt(chunk, offset); err != nil {
			return [sha256.Size]byte{}, false, fmt.Errorf("read history jsonl revision record: %w", err)
		}
		if len(bytes.TrimSpace(chunk)) > 0 {
			nonEmpty = true
		}
		_, _ = hash.Write(chunk)
		offset += readSize
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nonEmpty, nil
}

// buildJSONLPage 将倒序读取的记录恢复为页面正序，并生成下一页游标。
func buildJSONLPage[T any](records []jsonlRecord[T], limit int) JSONLPageResult[T] {
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	if len(records) == 0 {
		return JSONLPageResult[T]{}
	}
	items := make([]T, 0, len(records))
	offsets := make([]int64, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- {
		items = append(items, records[i].item)
		offsets = append(offsets, records[i].offset)
	}
	nextBefore := ""
	if hasMore {
		nextBefore = encodeOffsetCursor(records[len(records)-1].offset)
	}
	return JSONLPageResult[T]{Items: items, Offsets: offsets, HasMore: hasMore, NextBefore: nextBefore}
}

// jsonlRecord 绑定解析后的业务项和它在 JSONL 文件中的起始偏移。
type jsonlRecord[T any] struct {
	item   T
	offset int64
}

// readJSONLRecordsBackward 从 beforeOffset 向前按块读取完整 JSONL 行。
// 跨块半行通过 suffix 拼接，避免大文件分页时一次性读入内存。
func readJSONLRecordsBackward[T any](
	reader io.ReaderAt,
	beforeOffset int64,
	limit int,
	parse func([]byte) (T, bool, error),
) ([]jsonlRecord[T], error) {
	records := make([]jsonlRecord[T], 0, limit)
	suffix := []byte(nil)
	pos := beforeOffset
	for pos > 0 && len(records) < limit {
		start, data, err := readPreviousJSONLChunk(reader, pos, suffix)
		if err != nil {
			return nil, fmt.Errorf("read history jsonl page: %w", err)
		}
		suffix, err = collectCompleteJSONLLines(data, start, limit, parse, &records, suffix)
		if err != nil {
			return nil, err
		}
		pos = start
	}
	if pos == 0 && len(suffix) > 0 && len(records) < limit {
		if err := appendParsedRecord(suffix, 0, parse, &records); err != nil {
			return nil, err
		}
	}
	return records, nil
}

// readPreviousJSONLChunk 读取当前偏移之前的一块内容，并拼接上轮遗留后缀。
func readPreviousJSONLChunk(reader io.ReaderAt, pos int64, suffix []byte) (int64, []byte, error) {
	const chunkSize int64 = 64 * 1024
	readSize := min(chunkSize, pos)
	start := pos - readSize
	chunk := make([]byte, readSize)
	if _, err := reader.ReadAt(chunk, start); err != nil {
		return 0, nil, err
	}
	data := make([]byte, 0, len(chunk)+len(suffix))
	data = append(data, chunk...)
	data = append(data, suffix...)
	return start, data, nil
}

// collectCompleteJSONLLines 从块尾向前收集完整行，并返回尚未完整的前缀。
func collectCompleteJSONLLines[T any](
	data []byte,
	start int64,
	limit int,
	parse func([]byte) (T, bool, error),
	records *[]jsonlRecord[T],
	suffix []byte,
) ([]byte, error) {
	end := len(data)
	for end > 0 && len(*records) < limit {
		idx := bytes.LastIndexByte(data[:end], '\n')
		if idx < 0 {
			break
		}
		if err := appendParsedRecord(data[idx+1:end], start+int64(idx+1), parse, records); err != nil {
			return nil, err
		}
		end = idx
	}
	return append(suffix[:0], data[:end]...), nil
}

// appendParsedRecord 去掉空行和 CR 后追加解析成功的 JSONL 记录。
func appendParsedRecord[T any](line []byte, offset int64, parse func([]byte) (T, bool, error), records *[]jsonlRecord[T]) error {
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	item, ok, err := parse(line)
	if err != nil {
		return err
	}
	if ok {
		*records = append(*records, jsonlRecord[T]{item: item, offset: offset})
	}
	return nil
}

// pageBeforeOffset 将 before 游标解析为文件偏移，空游标表示从文件末尾开始。
func pageBeforeOffset(size int64, before string) (int64, error) {
	cursor := strings.TrimSpace(before)
	if cursor == "" {
		return size, nil
	}
	offset, err := decodeOffsetCursor(cursor)
	if err != nil {
		return 0, err
	}
	if offset < 0 || offset > size {
		return 0, fmt.Errorf("history page cursor offset %d outside file size %d", offset, size)
	}
	return offset, nil
}

// encodeOffsetCursor 将文件偏移编码为对外稳定的分页游标。
func encodeOffsetCursor(offset int64) string {
	raw, err := json.Marshal(offsetCursor{Offset: offset})
	if err != nil {
		return ""
	}
	return historyPageCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// decodeOffsetCursor 解码分页游标，并拒绝非 history page 前缀。
func decodeOffsetCursor(raw string) (int64, error) {
	cursor := strings.TrimSpace(raw)
	if !strings.HasPrefix(cursor, historyPageCursorPrefix) {
		return 0, errors.New("invalid history page cursor")
	}
	payload := strings.TrimPrefix(cursor, historyPageCursorPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return 0, fmt.Errorf("decode history page cursor: %w", err)
	}
	var parsed offsetCursor
	if err := json.Unmarshal(decoded, &parsed); err != nil {
		return 0, fmt.Errorf("parse history page cursor: %w", err)
	}
	return parsed.Offset, nil
}
