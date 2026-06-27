package insight

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// service 是只读的 Service 实现，写入由 Flusher 完成。
// 该类型无额外状态，可安全跨 goroutine 共享。
type service struct {
	logger *slog.Logger
	store  insightReader
}

var _ Service = (*service)(nil)

// NewService 构建 Service。logger 为 nil 时回退到包默认 logger。
func NewService(logger *slog.Logger, store insightReader) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &service{logger: logger, store: store}
}

// ListRecent 按时间倒序返回最近 limit 条 insight 快照。
func (s *service) ListRecent(ctx context.Context, limit int32) ([]Snapshot, error) {
	if limit < 0 {
		return nil, ErrInvalidLimit
	}
	rows, err := s.store.ListRecent(ctx, limit)
	if err != nil {
		return nil, err
	}
	return toSnapshots(rows), nil
}

// ListByThread 返回指定线程的 insight 快照列表。
func (s *service) ListByThread(ctx context.Context, threadID string, limit int32) ([]Snapshot, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("insight: thread_id is required")
	}
	if limit < 0 {
		return nil, ErrInvalidLimit
	}
	rows, err := s.store.ListByThread(ctx, threadID, limit)
	if err != nil {
		return nil, err
	}
	return toSnapshots(rows), nil
}

// ListObservedApprovalRequests 返回指定线程中审批请求数据被观测到的快照列表。
func (s *service) ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]ApprovalSnapshot, error) {
	if limit < 0 {
		return nil, ErrInvalidLimit
	}
	rows, err := s.store.ListObservedApprovalRequests(ctx, threadID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ApprovalSnapshot, len(rows))
	for i, r := range rows {
		out[i] = ApprovalSnapshot{
			ID:               r.ID,
			ThreadID:         r.ThreadID,
			AgentID:          r.AgentID,
			LocalTurnID:      r.LocalTurnID,
			ProviderTurnID:   r.ProviderTurnID,
			ApprovalRequests: r.ApprovalRequests,
			CreatedAt:        formatTime(r.CreatedAt),
		}
	}
	return out, nil
}

// toSnapshots 批量将 store.Insight 行映射为 RPC 侧 Snapshot DTO。
// JSON 友好的时间格式化和可空 Success 统一在此处理，确保两个 List 方法结果一致。
func toSnapshots(rows []insightRecord) []Snapshot {
	out := make([]Snapshot, len(rows))
	for i, r := range rows {
		out[i] = toSnapshot(r)
	}
	return out
}

// toSnapshot 将单条 store.Insight 行转换为 Snapshot DTO。
func toSnapshot(r insightRecord) Snapshot {
	var skills []string
	if len(r.SkillsSelected) > 0 {
		_ = json.Unmarshal(r.SkillsSelected, &skills)
	}
	return Snapshot{
		ID:                       r.ID,
		ThreadID:                 r.ThreadID,
		AgentID:                  r.AgentID,
		SessionID:                r.SessionID,
		Provider:                 r.Provider,
		LocalTurnID:              r.LocalTurnID,
		ProviderTurnID:           r.ProviderTurnID,
		StartedAt:                formatTime(r.StartedAt),
		CompletedAt:              formatTime(r.CompletedAt),
		DurationMS:               r.DurationMS,
		Success:                  cloneBoolPtr(r.Success),
		Status:                   r.Status,
		StopReason:               r.StopReason,
		ToolCalls:                r.ToolCalls,
		ToolCallsObserved:        r.ToolCallsObserved,
		ToolFailures:             r.ToolFailures,
		ToolFailuresObserved:     r.ToolFailuresObserved,
		ApprovalRequests:         r.ApprovalRequests,
		ApprovalRequestsObserved: r.ApprovalRequestsObserved,
		TokenInput:               r.TokenInput,
		TokenOutput:              r.TokenOutput,
		TokenTotal:               r.TokenTotal,
		TokenSnapshotObserved:    r.TokenSnapshotObserved,
		ContextWindowTokens:      r.ContextWindowTokens,
		UIProjection:             r.UIProjection,
		SkillsSelected:           skills,
		CreatedAt:                formatTime(r.CreatedAt),
	}
}

// formatTime 将时间格式化为 RFC3339 字符串；零值返回空字符串。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
