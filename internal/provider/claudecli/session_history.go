package claudecli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	historyjsonl "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/historyjsonl"
)

const (
	claudeResolvedCursorPrefix = "claude-resolved:"
	claudeResolvedCursorLimit  = 2048
)

type claudeResolvedCursor struct {
	Version int    `json:"version"`
	Source  string `json:"source"`
	Before  string `json:"before"`
}

// ReadHistory 从 Claude 历史后端读取统一消息列表。
// 旧调用方可能传入本地 agentID，空结果时会再尝试已解析的 Claude UUID，但后端错误不会被吞掉。
func (s *session) ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error) {
	if s.history == nil {
		return nil, errors.New("claudecli: history backend is not configured")
	}
	target := strings.TrimSpace(threadID)
	if target == "" {
		target = strings.TrimSpace(s.ThreadID())
	}
	messages, err := s.history.ReadHistory(ctx, target)
	if err != nil {
		return nil, err
	}
	// 兼容本地 agentID 与 Claude UUID 分离的旧线程，只有空结果才尝试已解析 ID。
	if len(messages) == 0 {
		resolved := strings.TrimSpace(s.ThreadID())
		if resolved != "" && resolved != target {
			fallback, err := s.history.ReadHistory(ctx, resolved)
			if err != nil {
				return nil, err
			}
			if len(fallback) > 0 {
				messages = fallback
			}
		}
	}
	messages = trimClaudeHistory(messages, limit)
	return toProviderHistory(messages)
}

// ReadMessagesPage 分页读取 Claude JSONL 历史并转换为 provider DTO。
// 只有请求 ID 没有任何条目时才尝试当前会话的真实 Claude UUID，避免掩盖读取错误。
func (s *session) ReadMessagesPage(ctx context.Context, threadID string, req dto.MessagePageRequest) (dto.MessagePageResult, error) {
	if s.history == nil {
		return dto.MessagePageResult{}, errors.New("claudecli: history backend is not configured")
	}
	target := strings.TrimSpace(threadID)
	if target == "" {
		target = strings.TrimSpace(s.ThreadID())
	}
	innerBefore, resolvedContinuation, err := decodeClaudeResolvedCursor(req.Before)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	resolved := strings.TrimSpace(s.ThreadID())
	if resolvedContinuation {
		return s.readResolvedMessagePage(ctx, target, resolved, innerBefore, req)
	}
	return s.readTargetMessagePage(ctx, target, resolved, req)
}

// readResolvedMessagePage 使用已固定的 resolved source 继续分页，禁止游标回落到请求 ID 对应文件。
func (s *session) readResolvedMessagePage(
	ctx context.Context,
	target string,
	resolved string,
	innerBefore string,
	req dto.MessagePageRequest,
) (dto.MessagePageResult, error) {
	if resolved == "" || resolved == target {
		return dto.MessagePageResult{}, invalidClaudeResolvedCursorError()
	}
	req.Before = innerBefore
	page, err := s.history.ReadMessagesPage(ctx, resolved, req)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	return claudeResolvedMessagePage(page)
}

// readTargetMessagePage 先读取请求 ID，仅在首屏无条目时尝试真实 Claude UUID。
func (s *session) readTargetMessagePage(
	ctx context.Context,
	target string,
	resolved string,
	req dto.MessagePageRequest,
) (dto.MessagePageResult, error) {
	page, err := s.history.ReadMessagesPage(ctx, target, req)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	if !shouldReadResolvedFallback(req, page, target, resolved) {
		return claudeMessagePage(page)
	}
	fallback, err := s.history.ReadMessagesPage(ctx, resolved, req)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	if hasClaudeHistoryPage(fallback) {
		return claudeResolvedMessagePage(fallback)
	}
	return claudeMessagePage(page)
}

// shouldReadResolvedFallback 将 fallback 条件集中为纯判断，续页请求绝不切换 source。
func shouldReadResolvedFallback(
	req dto.MessagePageRequest,
	page historyjsonl.JSONLPageResult[Message],
	target string,
	resolved string,
) bool {
	if req.Before != "" || len(page.Items) != 0 {
		return false
	}
	return resolved != "" && resolved != target
}

// hasClaudeHistoryPage 判断过滤后的页面是否仍代表可继续读取的 resolved source。
func hasClaudeHistoryPage(page historyjsonl.JSONLPageResult[Message]) bool {
	return len(page.Items) > 0 || page.HasMore
}

func claudeMessagePage(page historyjsonl.JSONLPageResult[Message]) (dto.MessagePageResult, error) {
	messages, err := toProviderHistoryWithOffsets(page.Items, page.Offsets)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	return dto.MessagePageResult{
		Messages:       messages,
		HasMore:        page.HasMore,
		NextBefore:     page.NextBefore,
		SourceRevision: page.SourceRevision,
	}, nil
}

func claudeResolvedMessagePage(page historyjsonl.JSONLPageResult[Message]) (dto.MessagePageResult, error) {
	result, err := claudeMessagePage(page)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	if !page.HasMore {
		return result, nil
	}
	nextBefore, err := encodeClaudeResolvedCursor(page.NextBefore)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	result.NextBefore = nextBefore
	return result, nil
}

func encodeClaudeResolvedCursor(before string) (string, error) {
	if strings.TrimSpace(before) == "" {
		return "", invalidClaudeResolvedCursorError()
	}
	payload, err := json.Marshal(claudeResolvedCursor{Version: 1, Source: "resolved", Before: before})
	if err != nil || len(payload) > claudeResolvedCursorLimit {
		return "", invalidClaudeResolvedCursorError()
	}
	wire := claudeResolvedCursorPrefix + base64.RawURLEncoding.EncodeToString(payload)
	if len(wire) > claudeResolvedCursorLimit {
		return "", invalidClaudeResolvedCursorError()
	}
	return wire, nil
}

// decodeClaudeResolvedCursor 解码私有 resolved 游标；无私有前缀的旧 target 游标保持原样透传。
func decodeClaudeResolvedCursor(wire string) (string, bool, error) {
	if !strings.HasPrefix(wire, claudeResolvedCursorPrefix) {
		return wire, false, nil
	}
	payload, err := decodeClaudeResolvedCursorPayload(wire)
	if err != nil {
		return "", false, err
	}
	cursor, err := parseClaudeResolvedCursor(payload)
	if err != nil {
		return "", false, err
	}
	return cursor.Before, true, nil
}

// decodeClaudeResolvedCursorPayload 校验 wire 与 payload 上限并执行 base64url 解码。
func decodeClaudeResolvedCursorPayload(wire string) ([]byte, error) {
	if len(wire) > claudeResolvedCursorLimit {
		return nil, invalidClaudeResolvedCursorError()
	}
	encoded := strings.TrimPrefix(wire, claudeResolvedCursorPrefix)
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, invalidClaudeResolvedCursorError()
	}
	if len(payload) == 0 || len(payload) > claudeResolvedCursorLimit {
		return nil, invalidClaudeResolvedCursorError()
	}
	return payload, nil
}

// parseClaudeResolvedCursor 严格解析单个 JSON 对象，拒绝未知字段与尾随 token。
func parseClaudeResolvedCursor(payload []byte) (claudeResolvedCursor, error) {
	var cursor claudeResolvedCursor
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return claudeResolvedCursor{}, invalidClaudeResolvedCursorError()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return claudeResolvedCursor{}, invalidClaudeResolvedCursorError()
	}
	if err := validateClaudeResolvedCursor(cursor); err != nil {
		return claudeResolvedCursor{}, err
	}
	return cursor, nil
}

// validateClaudeResolvedCursor 固定版本、source 与非空 inner cursor 三项 schema 不变量。
func validateClaudeResolvedCursor(cursor claudeResolvedCursor) error {
	if cursor.Version != 1 {
		return invalidClaudeResolvedCursorError()
	}
	if cursor.Source != "resolved" {
		return invalidClaudeResolvedCursorError()
	}
	if strings.TrimSpace(cursor.Before) == "" {
		return invalidClaudeResolvedCursorError()
	}
	return nil
}

func invalidClaudeResolvedCursorError() error {
	return errors.New("claudecli: invalid resolved history cursor")
}

func trimClaudeHistory(messages []Message, limit int) []Message {
	messages = normalizeClaudeHistory(messages)
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	return append([]Message(nil), messages[len(messages)-limit:]...)
}

func toProviderHistory(messages []Message) ([]dto.Message, error) {
	out := make([]dto.Message, 0, len(messages))
	for i, msg := range messages {
		timestamp, metadata, err := providershared.DecodeHistoryFields(msg.Timestamp, msg.Metadata)
		if err != nil {
			return nil, fmt.Errorf("claudecli history message %d: %w", i, err)
		}
		out = append(out, dto.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			Metadata:  metadata,
			Timestamp: timestamp,
		})
	}
	return out, nil
}

func toProviderHistoryWithOffsets(messages []Message, offsets []int64) ([]dto.Message, error) {
	out := make([]dto.Message, 0, len(messages))
	for i, msg := range messages {
		normalized, ok := normalizeClaudeHistoryMessage(msg)
		if !ok {
			continue
		}
		mapped, err := toProviderHistory([]Message{normalized})
		if err != nil {
			return nil, fmt.Errorf("claudecli history message %d: %w", i, err)
		}
		next := mapped[0]
		if i < len(offsets) {
			next.ID = offsets[i] + 1
		}
		out = append(out, next)
	}
	return out, nil
}
