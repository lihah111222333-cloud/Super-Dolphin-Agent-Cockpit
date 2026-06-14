package historyjsonl

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

const historyPageCursorPrefix = "histpage:"

type JSONLPageResult[T any] struct {
	Items      []T
	Offsets    []int64
	HasMore    bool
	NextBefore string
}

type offsetCursor struct {
	Offset int64 `json:"offset"`
}

// ReadProviderMessagesPage 读取provider消息page。
func ReadProviderMessagesPage(req ReadRequest, pageReq dto.MessagePageRequest) (dto.MessagePageResult, error) {
	path, provider, err := resolvePath(req)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	page, err := ReadJSONLPage(path, pageReq.Limit, pageReq.Before, func(raw []byte) (dto.Message, bool) {
		return parseLine(raw, provider)
	})
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	return dto.MessagePageResult{
		Messages:   messagesWithPageOffsets(page.Items, page.Offsets),
		HasMore:    page.HasMore,
		NextBefore: page.NextBefore,
	}, nil
}

// ReadProviderMessagesPageOrError 读取provider消息page错误。
func ReadProviderMessagesPageOrError(req ReadRequest, pageReq dto.MessagePageRequest, missingErr error) (dto.MessagePageResult, error) {
	path, provider, err := resolvePath(req)
	if err != nil {
		if IsMissingProviderHistory(err) {
			return dto.MessagePageResult{}, missingErr
		}
		return dto.MessagePageResult{}, err
	}
	page, err := ReadJSONLPage(path, pageReq.Limit, pageReq.Before, func(raw []byte) (dto.Message, bool) {
		return parseLine(raw, provider)
	})
	if err != nil {
		if IsMissingProviderHistory(err) {
			return dto.MessagePageResult{}, missingErr
		}
		return dto.MessagePageResult{}, err
	}
	return dto.MessagePageResult{
		Messages:   messagesWithPageOffsets(page.Items, page.Offsets),
		HasMore:    page.HasMore,
		NextBefore: page.NextBefore,
	}, nil
}

func messagesWithPageOffsets(messages []dto.Message, offsets []int64) []dto.Message {
	out := append([]dto.Message(nil), messages...)
	for i := range out {
		if i < len(offsets) && out[i].ID == 0 {
			out[i].ID = offsets[i] + 1
		}
	}
	return out
}

// ReadJSONLPage 读取JSONLpage。
func ReadJSONLPage[T any](path string, limit int, before string, parse func([]byte) (T, bool)) (JSONLPageResult[T], error) {
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
	if beforeOffset == 0 {
		return JSONLPageResult[T]{}, nil
	}
	records, err := readJSONLRecordsBackward(file, beforeOffset, limit+1, parse)
	if err != nil {
		return JSONLPageResult[T]{}, err
	}
	return buildJSONLPage(records, limit), nil
}

func validateJSONLPageRequest[T any](limit int, parse func([]byte) (T, bool)) error {
	if limit <= 0 {
		return errors.New("history page limit must be positive")
	}
	if parse == nil {
		return errors.New("history page parser is required")
	}
	return nil
}

func openJSONLPageFile(path string) (*os.File, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
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

type jsonlRecord[T any] struct {
	item   T
	offset int64
}

// readJSONLRecordsBackward 读取JSONL记录backward。
func readJSONLRecordsBackward[T any](
	reader io.ReaderAt,
	beforeOffset int64,
	limit int,
	parse func([]byte) (T, bool),
) ([]jsonlRecord[T], error) {
	records := make([]jsonlRecord[T], 0, limit)
	suffix := []byte(nil)
	pos := beforeOffset
	for pos > 0 && len(records) < limit {
		start, data, err := readPreviousJSONLChunk(reader, pos, suffix)
		if err != nil {
			return nil, fmt.Errorf("read history jsonl page: %w", err)
		}
		suffix = collectCompleteJSONLLines(data, start, limit, parse, &records, suffix)
		pos = start
	}
	if pos == 0 && len(suffix) > 0 && len(records) < limit {
		appendParsedRecord(suffix, 0, parse, &records)
	}
	return records, nil
}

func readPreviousJSONLChunk(reader io.ReaderAt, pos int64, suffix []byte) (int64, []byte, error) {
	const chunkSize int64 = 64 * 1024
	readSize := chunkSize
	if pos < readSize {
		readSize = pos
	}
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

func collectCompleteJSONLLines[T any](
	data []byte,
	start int64,
	limit int,
	parse func([]byte) (T, bool),
	records *[]jsonlRecord[T],
	suffix []byte,
) []byte {
	end := len(data)
	for end > 0 && len(*records) < limit {
		idx := bytes.LastIndexByte(data[:end], '\n')
		if idx < 0 {
			break
		}
		appendParsedRecord(data[idx+1:end], start+int64(idx+1), parse, records)
		end = idx
	}
	return append(suffix[:0], data[:end]...)
}

func appendParsedRecord[T any](line []byte, offset int64, parse func([]byte) (T, bool), records *[]jsonlRecord[T]) {
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	if item, ok := parse(line); ok {
		*records = append(*records, jsonlRecord[T]{item: item, offset: offset})
	}
}

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

func encodeOffsetCursor(offset int64) string {
	raw, err := json.Marshal(offsetCursor{Offset: offset})
	if err != nil {
		return ""
	}
	return historyPageCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

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
