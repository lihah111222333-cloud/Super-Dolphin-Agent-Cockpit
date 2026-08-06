package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

// Message 表示 Codex rollout 历史中的单条 provider 消息。
// 它先保持 provider 原始 role/content/metadata，再由 session history 转成统一 DTO。
type Message struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
}

type rolloutReader struct {
	logger       *slog.Logger
	transport    *transport
	skillMetrics *skillmetrics.Registry
}

// ReadHistory 从本地 rollout 文件读取 Codex 历史消息。
// 本地历史缺失时返回空列表并告警；远端 history API 当前不可用，不做静默远端兜底。
func (r *rolloutReader) ReadHistory(ctx context.Context, threadID, codexHome string, limit int) ([]Message, error) {
	if messages, err := readLocalRollout(threadID, codexHome, limit, r.skillMetrics); err == nil && len(messages) > 0 {
		return messages, nil
	} else if err != nil && r.logger != nil {
		r.logger.Warn("codexapp: local rollout history unavailable", "thread_id", threadID, "error", err)
	}
	if r.logger != nil {
		r.logger.Warn("codexapp: remote history API unavailable; returning empty history", "thread_id", threadID)
	}
	return []Message{}, nil
}

// ReadHistory 读取当前 session 的 provider 历史并转换为统一 DTO。
// 优先使用请求 threadID；若绑定未更新导致查不到 rollout，会尝试 session 当前 Codex thread UUID。
func (s *session) ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error) {
	if s.history == nil {
		return nil, errors.New("codexapp: history backend is not configured")
	}
	target, err := requireThreadID(s, threadID)
	if err != nil {
		return nil, err
	}
	codexHome := s.runtimeConfigString("codexHome")
	messages, primaryErr := s.history.ReadHistory(ctx, target, codexHome, limit)
	if primaryErr == nil && len(messages) > 0 {
		return toProviderHistory(messages)
	}
	if fallback := s.readHistoryFallback(ctx, target, codexHome, limit); fallback != nil {
		return toProviderHistory(fallback)
	}
	if primaryErr != nil {
		return nil, primaryErr
	}
	return toProviderHistory(messages)
}

// readHistoryFallback 用 session 当前 Codex thread UUID 再查一次本地 rollout。
// 只有主查询没有消息时才走该路径，避免绑定漂移时用户看不到已发生的对话。
func (s *session) readHistoryFallback(ctx context.Context, primaryTarget, codexHome string, limit int) []Message {
	codexThreadID := strings.TrimSpace(s.ThreadID())
	if codexThreadID == "" || codexThreadID == primaryTarget {
		return nil
	}
	messages, err := s.history.ReadHistory(ctx, codexThreadID, codexHome, limit)
	if err != nil || len(messages) == 0 {
		return nil
	}
	if s.logger != nil {
		s.logger.Info("codexapp: history fallback to codex thread UUID",
			"requested", primaryTarget, "used", codexThreadID)
	}
	return messages
}

func toProviderHistory(messages []Message) ([]dto.Message, error) {
	out := make([]dto.Message, 0, len(messages))
	for i, msg := range messages {
		timestamp, metadata, err := providershared.DecodeHistoryFields(msg.Timestamp, msg.Metadata)
		if err != nil {
			return nil, fmt.Errorf("codexapp history message %d: %w", i, err)
		}
		out = append(out, dto.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			Timestamp: timestamp,
			Metadata:  metadata,
		})
	}
	return out, nil
}

// CompactThread 请求 Codex app-server 对指定线程启动 compact。
// threadID 为空会先经 requireThreadID 阻断，args 为空时只发送必要 threadId。
func (s *session) CompactThread(ctx context.Context, threadID, args string) error {
	target, err := requireThreadID(s, threadID)
	if err != nil {
		return err
	}
	params := map[string]any{"threadId": target}
	if arg := strings.TrimSpace(args); arg != "" {
		params["args"] = arg
	}
	_, err = callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "thread/compact/start", params)
	return err
}
