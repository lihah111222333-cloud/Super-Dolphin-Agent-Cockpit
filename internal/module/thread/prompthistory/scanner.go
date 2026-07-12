package prompthistory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"

	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
)

const (
	maxPromptHistoryThreads     = 100
	maxPromptHistoryLimit       = 50
	maxPromptHistoryCursorBytes = 2048
	promptHistoryCursorVersion  = 1
	promptHistoryNonceDomain    = "super-dolphin/prompthistory/nonce/v1\x00"
)

var (
	ErrInvalidRequest            = errors.New("invalid prompt history request")
	ErrInvalidCursor             = errors.New("invalid prompt history cursor")
	ErrStaleNonce                = errors.New("prompt history snapshot is stale")
	ErrThreadLimitExceeded       = errors.New("prompt history thread limit exceeded")
	ErrPageRead                  = errors.New("prompt history page read failed")
	ErrSourceRevisionUnavailable = errors.New("prompt history source revision unavailable")
	errInvalidPageContract       = errors.New("invalid prompt history page contract")
)

// ThreadSnapshot 是 scanner 所需的最小 canonical thread 快照。
type ThreadSnapshot struct {
	ThreadID  string
	Status    string
	UpdatedAt int64
}

// ReadPage 按 provider before 游标读取单个 thread 的消息页。
type ReadPage func(context.Context, string, providerdto.MessagePageRequest) (providerdto.MessagePageResult, error)

// Request 是 pure prompt-history scanner 的输入。
type Request struct {
	Threads        []ThreadSnapshot
	ActiveThreadID string
	Cursor         string
	Nonce          string
	Limit          int
	ReadPage       ReadPage
}

type cachedThread struct {
	snapshot ThreadSnapshot
	initial  providerdto.MessagePageResult
}

type sourceSnapshot struct {
	threads []cachedThread
	nonce   string
}

type promptHistoryCursor struct {
	Version     int    `json:"version"`
	Nonce       string `json:"nonce"`
	ThreadIndex int    `json:"threadIndex"`
	Before      string `json:"before"`
}

type pageReadError struct {
	cause error
}

// Error 返回稳定脱敏消息，禁止把 reader 原始错误暴露给 RPC 边界。
func (e *pageReadError) Error() string { return ErrPageRead.Error() }

// Unwrap 保留底层 cause，供 errors.Is 做程序化诊断。
func (e *pageReadError) Unwrap() error { return e.cause }

// Is 同时支持匹配稳定 ErrPageRead 和原始 cause。
func (e *pageReadError) Is(target error) bool {
	return target == ErrPageRead || errors.Is(e.cause, target)
}

// ScanPromptHistory 构造稳定 source snapshot；phase 2 将在其上完成消息扫描。
func ScanPromptHistory(ctx context.Context, req Request) (threaddto.PromptHistoryResult, error) {
	if err := contextError(ctx); err != nil {
		return threaddto.PromptHistoryResult{}, err
	}
	ordered, err := validateAndOrderRequest(req)
	if err != nil {
		return threaddto.PromptHistoryResult{}, err
	}
	snapshot, err := readInitialSnapshot(ctx, req, ordered)
	if err != nil {
		return threaddto.PromptHistoryResult{}, err
	}
	if err := validateRequestNonce(req.Nonce, snapshot.nonce); err != nil {
		return threaddto.PromptHistoryResult{}, err
	}
	cursor, err := decodeAndValidateCursor(req.Cursor, req.Nonce, snapshot)
	if err != nil {
		return threaddto.PromptHistoryResult{}, err
	}
	position := scanPosition{threadIndex: cursor.ThreadIndex, before: cursor.Before}
	return scanSourceSnapshot(ctx, req, snapshot, position)
}

type scanPosition struct {
	threadIndex int
	before      string
}

// scanSourceSnapshot 从指定 thread/page 位置开始扫描，直到填满 limit 或耗尽来源。
func scanSourceSnapshot(ctx context.Context, req Request, snapshot sourceSnapshot, position scanPosition) (threaddto.PromptHistoryResult, error) {
	result := threaddto.PromptHistoryResult{
		Entries: make([]threaddto.PromptHistoryEntry, 0, req.Limit),
		Nonce:   snapshot.nonce,
	}
	visited := make(map[scanPosition]struct{})
	for position.threadIndex < len(snapshot.threads) {
		if err := markScanPosition(visited, position); err != nil {
			return threaddto.PromptHistoryResult{}, err
		}
		remaining := req.Limit - len(result.Entries)
		page, err := readScanPage(ctx, req, snapshot, position, remaining)
		if err != nil {
			return threaddto.PromptHistoryResult{}, err
		}
		result.Entries = append(result.Entries, userEntriesFromPage(snapshot.threads[position.threadIndex].snapshot.ThreadID, page)...)
		next, hasNext := nextScanPosition(position, page, len(snapshot.threads))
		if len(result.Entries) == req.Limit {
			return finishPromptHistoryPage(result, next, hasNext)
		}
		if !hasNext {
			return result, nil
		}
		position = next
	}
	return result, nil
}

// markScanPosition 拒绝 provider 游标形成环，避免无界扫描。
func markScanPosition(visited map[scanPosition]struct{}, position scanPosition) error {
	if _, exists := visited[position]; exists {
		return &pageReadError{cause: errInvalidPageContract}
	}
	visited[position] = struct{}{}
	return nil
}

// readScanPage 按剩余容量读取实际页面，并校验 source revision 未漂移。
func readScanPage(ctx context.Context, req Request, snapshot sourceSnapshot, position scanPosition, remaining int) (providerdto.MessagePageResult, error) {
	thread := snapshot.threads[position.threadIndex]
	pageReq := providerdto.MessagePageRequest{Limit: remaining, Before: position.before}
	page, err := readPageSanitized(ctx, req.ReadPage, thread.snapshot.ThreadID, pageReq)
	if err != nil {
		return providerdto.MessagePageResult{}, err
	}
	if err := validateSourcePage(page, remaining, position.before); err != nil {
		return providerdto.MessagePageResult{}, err
	}
	if page.SourceRevision != thread.initial.SourceRevision {
		return providerdto.MessagePageResult{}, ErrStaleNonce
	}
	return page, nil
}

// userEntriesFromPage 反向遍历时间正序页面，只保留 user 且不按文本去重。
func userEntriesFromPage(threadID string, page providerdto.MessagePageResult) []threaddto.PromptHistoryEntry {
	entries := make([]threaddto.PromptHistoryEntry, 0, len(page.Messages))
	for index := len(page.Messages) - 1; index >= 0; index-- {
		message := page.Messages[index]
		if message.Role != "user" {
			continue
		}
		entries = append(entries, threaddto.PromptHistoryEntry{
			ThreadID:  threadID,
			MessageID: strconv.FormatInt(message.ID, 10),
			Text:      message.Content,
			CreatedAt: message.Timestamp,
		})
	}
	return entries
}

// nextScanPosition 选择同 thread 的 raw continuation 或下一个有序 thread。
func nextScanPosition(current scanPosition, page providerdto.MessagePageResult, threadCount int) (scanPosition, bool) {
	if page.HasMore {
		return scanPosition{threadIndex: current.threadIndex, before: page.NextBefore}, true
	}
	next := scanPosition{threadIndex: current.threadIndex + 1}
	return next, next.threadIndex < threadCount
}

// finishPromptHistoryPage 在确有后续来源时编码下一页游标。
func finishPromptHistoryPage(result threaddto.PromptHistoryResult, next scanPosition, hasNext bool) (threaddto.PromptHistoryResult, error) {
	if !hasNext {
		return result, nil
	}
	cursor, err := encodePromptHistoryCursor(promptHistoryCursor{
		Version:     promptHistoryCursorVersion,
		Nonce:       result.Nonce,
		ThreadIndex: next.threadIndex,
		Before:      next.before,
	})
	if err != nil {
		return threaddto.PromptHistoryResult{}, err
	}
	result.NextCursor = cursor
	result.HasMore = true
	return result, nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidRequest
	}
	return ctx.Err()
}

// validateAndOrderRequest 校验请求边界并生成稳定 thread 顺序。
func validateAndOrderRequest(req Request) ([]ThreadSnapshot, error) {
	if req.Limit < 1 || req.Limit > maxPromptHistoryLimit || req.ReadPage == nil {
		return nil, ErrInvalidRequest
	}
	if len(req.Threads) > maxPromptHistoryThreads {
		return nil, ErrThreadLimitExceeded
	}
	if len(req.Nonce) > maxPromptHistoryCursorBytes {
		return nil, ErrInvalidRequest
	}
	ordered := append([]ThreadSnapshot(nil), req.Threads...)
	if err := validateThreadSnapshots(ordered, req.ActiveThreadID); err != nil {
		return nil, err
	}
	sortThreadSnapshots(ordered, req.ActiveThreadID)
	return ordered, nil
}

// validateThreadSnapshots 拒绝空、重复或不存在的 active thread identity。
func validateThreadSnapshots(threads []ThreadSnapshot, activeThreadID string) error {
	seen := make(map[string]struct{}, len(threads))
	activeFound := strings.TrimSpace(activeThreadID) == ""
	for _, thread := range threads {
		if strings.TrimSpace(thread.ThreadID) == "" {
			return ErrInvalidRequest
		}
		if _, exists := seen[thread.ThreadID]; exists {
			return ErrInvalidRequest
		}
		seen[thread.ThreadID] = struct{}{}
		if thread.ThreadID == activeThreadID {
			activeFound = true
		}
	}
	if !activeFound {
		return ErrInvalidRequest
	}
	return nil
}

func sortThreadSnapshots(threads []ThreadSnapshot, activeThreadID string) {
	sort.Slice(threads, func(i, j int) bool {
		leftActive := threads[i].ThreadID == activeThreadID
		rightActive := threads[j].ThreadID == activeThreadID
		if leftActive != rightActive {
			return leftActive
		}
		if threads[i].UpdatedAt != threads[j].UpdatedAt {
			return threads[i].UpdatedAt > threads[j].UpdatedAt
		}
		return threads[i].ThreadID < threads[j].ThreadID
	})
}

func readInitialSnapshot(ctx context.Context, req Request, ordered []ThreadSnapshot) (sourceSnapshot, error) {
	cached := make([]cachedThread, 0, len(ordered))
	for _, thread := range ordered {
		if err := contextError(ctx); err != nil {
			return sourceSnapshot{}, err
		}
		page, err := readPageSanitized(ctx, req.ReadPage, thread.ThreadID, providerdto.MessagePageRequest{Limit: req.Limit})
		if err != nil {
			return sourceSnapshot{}, err
		}
		if err := validateSourcePage(page, req.Limit, ""); err != nil {
			return sourceSnapshot{}, err
		}
		cached = append(cached, cachedThread{snapshot: thread, initial: page})
	}
	return sourceSnapshot{threads: cached, nonce: computeSnapshotNonce(cached)}, nil
}

func readPageSanitized(ctx context.Context, reader ReadPage, threadID string, req providerdto.MessagePageRequest) (providerdto.MessagePageResult, error) {
	if err := contextError(ctx); err != nil {
		return providerdto.MessagePageResult{}, err
	}
	page, err := reader(ctx, threadID, req)
	if err != nil {
		return providerdto.MessagePageResult{}, &pageReadError{cause: err}
	}
	if err := contextError(ctx); err != nil {
		return providerdto.MessagePageResult{}, err
	}
	return page, nil
}

// validateSourcePage 校验 provider page 的 revision、大小和游标推进契约。
func validateSourcePage(page providerdto.MessagePageResult, limit int, before string) error {
	if strings.TrimSpace(page.SourceRevision) == "" {
		return ErrSourceRevisionUnavailable
	}
	if len(page.Messages) > limit {
		return &pageReadError{cause: errInvalidPageContract}
	}
	if page.HasMore && strings.TrimSpace(page.NextBefore) == "" {
		return &pageReadError{cause: errInvalidPageContract}
	}
	if page.HasMore && page.NextBefore == before {
		return &pageReadError{cause: errInvalidPageContract}
	}
	if !page.HasMore && page.NextBefore != "" {
		return &pageReadError{cause: errInvalidPageContract}
	}
	return nil
}

func computeSnapshotNonce(threads []cachedThread) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(promptHistoryNonceDomain))
	writeNonceUint64(hash, uint64(len(threads)))
	for _, thread := range threads {
		writeNonceString(hash, thread.snapshot.ThreadID)
		writeNonceString(hash, thread.snapshot.Status)
		writeNonceInt64(hash, thread.snapshot.UpdatedAt)
		writeNonceString(hash, thread.initial.SourceRevision)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type nonceWriter interface {
	Write([]byte) (int, error)
}

func writeNonceString(writer nonceWriter, value string) {
	writeNonceUint64(writer, uint64(len(value)))
	_, _ = writer.Write([]byte(value))
}

func writeNonceInt64(writer nonceWriter, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	writeNonceUint64(writer, uint64(len(encoded)))
	_, _ = writer.Write(encoded[:])
}

func writeNonceUint64(writer nonceWriter, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}

func validateRequestNonce(requestNonce, currentNonce string) error {
	if requestNonce != "" && requestNonce != currentNonce {
		return ErrStaleNonce
	}
	return nil
}

// decodeAndValidateCursor 解码游标并验证 request/cursor/snapshot nonce 一致。
func decodeAndValidateCursor(raw, requestNonce string, snapshot sourceSnapshot) (promptHistoryCursor, error) {
	if raw == "" {
		return promptHistoryCursor{}, nil
	}
	if requestNonce == "" {
		return promptHistoryCursor{}, ErrStaleNonce
	}
	cursor, err := decodePromptHistoryCursor(raw)
	if err != nil {
		return promptHistoryCursor{}, err
	}
	if cursor.Nonce != snapshot.nonce || requestNonce != snapshot.nonce {
		return promptHistoryCursor{}, ErrStaleNonce
	}
	if cursor.ThreadIndex < 0 || cursor.ThreadIndex >= len(snapshot.threads) {
		return promptHistoryCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

// decodePromptHistoryCursor 严格解码 version 1 base64url 游标并拒绝尾随值。
func decodePromptHistoryCursor(raw string) (promptHistoryCursor, error) {
	if len(raw) == 0 || len(raw) > maxPromptHistoryCursorBytes {
		return promptHistoryCursor{}, ErrInvalidCursor
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) == 0 || len(decoded) > maxPromptHistoryCursorBytes {
		return promptHistoryCursor{}, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var cursor promptHistoryCursor
	if err := decoder.Decode(&cursor); err != nil {
		return promptHistoryCursor{}, ErrInvalidCursor
	}
	if err := requireJSONEOF(decoder); err != nil {
		return promptHistoryCursor{}, err
	}
	if cursor.Version != promptHistoryCursorVersion {
		return promptHistoryCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidCursor
	}
	return nil
}

func encodePromptHistoryCursor(cursor promptHistoryCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil || len(raw) > maxPromptHistoryCursorBytes {
		return "", ErrInvalidCursor
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > maxPromptHistoryCursorBytes {
		return "", ErrInvalidCursor
	}
	return encoded, nil
}
