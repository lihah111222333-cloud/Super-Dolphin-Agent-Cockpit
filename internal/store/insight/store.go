package insight

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	UpsertSessionInsight(ctx context.Context, arg sqlc.UpsertSessionInsightParams) (sqlc.SessionInsight, error)
	GetSessionInsightByLocalTurn(ctx context.Context, arg sqlc.GetSessionInsightByLocalTurnParams) (sqlc.SessionInsight, error)
	ListSessionInsightsByThread(ctx context.Context, arg sqlc.ListSessionInsightsByThreadParams) ([]sqlc.SessionInsight, error)
	ListRecentSessionInsights(ctx context.Context, arg sqlc.ListRecentSessionInsightsParams) ([]sqlc.SessionInsight, error)
	ListObservedApprovalRequests(ctx context.Context, arg sqlc.ListObservedApprovalRequestsParams) ([]sqlc.ListObservedApprovalRequestsRow, error)
	ListObservedTokenTurns(ctx context.Context, arg sqlc.ListObservedTokenTurnsParams) ([]sqlc.ListObservedTokenTurnsRow, error)
}

type store struct{ q querier }

// NewStore returns the production Store backed by sqlc queries.
// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// ----- helpers -----

func ts(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func fromTS(p pgtype.Timestamptz) time.Time {
	if !p.Valid {
		return time.Time{}
	}
	return p.Time
}

func bytesOrDefault(b []byte, def string) []byte {
	if len(b) == 0 {
		return []byte(def)
	}
	return b
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func wrap(err error, op string) error {
	return platformdb.WrapStoreError(err, op, "insight")
}

// ----- Store impl -----

// Upsert 新增或更新记录。
func (s *store) Upsert(ctx context.Context, p UpsertParams) (Insight, error) {
	now := p.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	createdAt := p.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	row, err := s.q.UpsertSessionInsight(ctx, sqlc.UpsertSessionInsightParams{
		ThreadID:                 strings.TrimSpace(p.ThreadID),
		AgentID:                  strings.TrimSpace(p.AgentID),
		SessionID:                strings.TrimSpace(p.SessionID),
		Provider:                 strings.TrimSpace(p.Provider),
		LocalTurnID:              strings.TrimSpace(p.LocalTurnID),
		ProviderTurnID:           strings.TrimSpace(p.ProviderTurnID),
		StartedAt:                ts(p.StartedAt),
		CompletedAt:              ts(p.CompletedAt),
		DurationMs:               p.DurationMS,
		Success:                  p.Success,
		Status:                   firstNonEmpty(p.Status, StatusUnknown),
		StopReason:               p.StopReason,
		ToolCalls:                p.ToolCalls,
		ToolCallsObserved:        p.ToolCallsObserved,
		ToolFailures:             p.ToolFailures,
		ToolFailuresObserved:     p.ToolFailuresObserved,
		ApprovalRequests:         p.ApprovalRequests,
		ApprovalRequestsObserved: p.ApprovalRequestsObserved,
		TokenInput:               p.TokenInput,
		TokenOutput:              p.TokenOutput,
		TokenTotal:               p.TokenTotal,
		TokenSnapshotObserved:    p.TokenSnapshotObserved,
		ContextWindowTokens:      p.ContextWindowTokens,
		UIProjection:             p.UIProjection,
		SkillsSelected:           bytesOrDefault(p.SkillsSelected, "[]"),
		CreatedAt:                ts(createdAt),
		UpdatedAt:                ts(now),
	})
	if err != nil {
		return Insight{}, wrap(err, "upsert")
	}
	return fromRow(row), nil
}

// GetByLocalTurn 按localturn读取insight存储。
func (s *store) GetByLocalTurn(ctx context.Context, threadID, localTurnID string) (Insight, error) {
	threadID = strings.TrimSpace(threadID)
	localTurnID = strings.TrimSpace(localTurnID)
	if threadID == "" || localTurnID == "" {
		return Insight{}, wrap(ErrEmptyID, "get_by_local_turn")
	}
	row, err := s.q.GetSessionInsightByLocalTurn(ctx, sqlc.GetSessionInsightByLocalTurnParams{
		ThreadID:    threadID,
		LocalTurnID: localTurnID,
	})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Insight{}, wrap(ErrNotFound, "get_by_local_turn")
		}
		return Insight{}, wrap(err, "get_by_local_turn")
	}
	return fromRow(row), nil
}

// ListByThread 按线程列出insight存储。
func (s *store) ListByThread(ctx context.Context, threadID string, limit int32) ([]Insight, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, wrap(ErrEmptyID, "list_by_thread")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListSessionInsightsByThread(ctx, sqlc.ListSessionInsightsByThreadParams{
		ThreadID: threadID,
		Limit:    limit,
	})
	if err != nil {
		return nil, wrap(err, "list_by_thread")
	}
	out := make([]Insight, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

// ListRecent 列出recent。
func (s *store) ListRecent(ctx context.Context, limit int32) ([]Insight, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListRecentSessionInsights(ctx, sqlc.ListRecentSessionInsightsParams{Limit: limit})
	if err != nil {
		return nil, wrap(err, "list_recent")
	}
	out := make([]Insight, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

// ListObservedApprovalRequests 列出observed审批请求。
func (s *store) ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]ApprovalRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListObservedApprovalRequests(ctx, sqlc.ListObservedApprovalRequestsParams{
		Column1: strings.TrimSpace(threadID),
		Limit:   limit,
	})
	if err != nil {
		return nil, wrap(err, "list_observed_approval_requests")
	}
	out := make([]ApprovalRow, len(rows))
	for i, r := range rows {
		out[i] = ApprovalRow{
			ID:               r.ID,
			ThreadID:         r.ThreadID,
			AgentID:          r.AgentID,
			LocalTurnID:      r.LocalTurnID,
			ProviderTurnID:   r.ProviderTurnID,
			ApprovalRequests: r.ApprovalRequests,
			CreatedAt:        fromTS(r.CreatedAt),
		}
	}
	return out, nil
}

// ListObservedTokenTurns 列出observed令牌turn。
func (s *store) ListObservedTokenTurns(ctx context.Context, threadID string, limit int32) ([]TokenRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListObservedTokenTurns(ctx, sqlc.ListObservedTokenTurnsParams{
		Column1: strings.TrimSpace(threadID),
		Limit:   limit,
	})
	if err != nil {
		return nil, wrap(err, "list_observed_token_turns")
	}
	out := make([]TokenRow, len(rows))
	for i, r := range rows {
		out[i] = TokenRow{
			ID:                  r.ID,
			ThreadID:            r.ThreadID,
			AgentID:             r.AgentID,
			LocalTurnID:         r.LocalTurnID,
			ProviderTurnID:      r.ProviderTurnID,
			TokenInput:          r.TokenInput,
			TokenOutput:         r.TokenOutput,
			TokenTotal:          r.TokenTotal,
			ContextWindowTokens: r.ContextWindowTokens,
			CreatedAt:           fromTS(r.CreatedAt),
		}
	}
	return out, nil
}

// fromRow maps a sqlc SessionInsight row into the domain Insight. Time
// fields follow the same "zero value = NULL" convention used across this
// project's stores.
// fromRow 从row处理insight存储。
func fromRow(r sqlc.SessionInsight) Insight {
	return Insight{
		ID:                       r.ID,
		ThreadID:                 r.ThreadID,
		AgentID:                  r.AgentID,
		SessionID:                r.SessionID,
		Provider:                 r.Provider,
		LocalTurnID:              r.LocalTurnID,
		ProviderTurnID:           r.ProviderTurnID,
		StartedAt:                fromTS(r.StartedAt),
		CompletedAt:              fromTS(r.CompletedAt),
		DurationMS:               r.DurationMs,
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
		SkillsSelected:           cloneBytes(r.SkillsSelected),
		CreatedAt:                fromTS(r.CreatedAt),
		UpdatedAt:                fromTS(r.UpdatedAt),
	}
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
