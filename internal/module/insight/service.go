package insight

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	insightstore "github.com/anthropic-ai/super-agent-v3/internal/store/insight"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// service is the read-only Service implementation. Writes go through the
// Flusher; this type does not keep state beyond its dependencies so it
// is trivially safe to share across goroutines.
type service struct {
	logger *slog.Logger
	store  insightstore.Store
}

var _ Service = (*service)(nil)

// NewService constructs the Service. A nil logger falls back to the
// package default.
// NewService 创建服务。
func NewService(logger *slog.Logger, store insightstore.Store) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &service{logger: logger, store: store}
}

// ListRecent 列出recent。
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

// ListByThread 按线程列出insight。
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

// ListObservedApprovalRequests 列出observed审批请求。
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

// toSnapshots maps store.Insight rows into the RPC-facing Snapshot DTOs,
// keeping the JSON-friendly time formatting + nullable Success in one
// place so both List methods stay consistent.
func toSnapshots(rows []insightstore.Insight) []Snapshot {
	out := make([]Snapshot, len(rows))
	for i, r := range rows {
		out[i] = toSnapshot(r)
	}
	return out
}

// toSnapshot 把insight处理为快照。
func toSnapshot(r insightstore.Insight) Snapshot {
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

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
