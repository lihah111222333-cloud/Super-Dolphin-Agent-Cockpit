package insight

import (
	"context"
	"strings"
	"time"

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
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// ----- helpers -----

func ts(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return platformdb.Millis(t)
}

func tsPtr(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	ms := platformdb.Millis(t)
	return &ms
}

func fromTS(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return platformdb.TimeFromMillis(ms)
}

func fromTSPtr(ms *int64) time.Time {
	if ms == nil {
		return time.Time{}
	}
	return fromTS(*ms)
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func boolToIntPtr(b *bool) *int64 {
	if b == nil {
		return nil
	}
	var v int64
	if *b {
		v = 1
	}
	return &v
}

func intPtrToBoolPtr(v *int64) *bool {
	if v == nil {
		return nil
	}
	b := *v != 0
	return &b
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
		StartedAt:                tsPtr(p.StartedAt),
		CompletedAt:              tsPtr(p.CompletedAt),
		DurationMs:               int64(p.DurationMS),
		Success:                  boolToIntPtr(p.Success),
		Status:                   firstNonEmpty(p.Status, StatusUnknown),
		StopReason:               p.StopReason,
		ToolCalls:                int64(p.ToolCalls),
		ToolCallsObserved:        boolToInt64(p.ToolCallsObserved),
		ToolFailures:             int64(p.ToolFailures),
		ToolFailuresObserved:     boolToInt64(p.ToolFailuresObserved),
		ApprovalRequests:         int64(p.ApprovalRequests),
		ApprovalRequestsObserved: boolToInt64(p.ApprovalRequestsObserved),
		TokenInput:               int64(p.TokenInput),
		TokenOutput:              int64(p.TokenOutput),
		TokenTotal:               int64(p.TokenTotal),
		TokenSnapshotObserved:    boolToInt64(p.TokenSnapshotObserved),
		ContextWindowTokens:      int64(p.ContextWindowTokens),
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
		Limit:    int64(limit),
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

func (s *store) ListRecent(ctx context.Context, limit int32) ([]Insight, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListRecentSessionInsights(ctx, sqlc.ListRecentSessionInsightsParams{Limit: int64(limit)})
	if err != nil {
		return nil, wrap(err, "list_recent")
	}
	out := make([]Insight, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

func (s *store) ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]ApprovalRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListObservedApprovalRequests(ctx, sqlc.ListObservedApprovalRequestsParams{
		Column1: strings.TrimSpace(threadID),
		Limit:   int64(limit),
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
			ApprovalRequests: int32(r.ApprovalRequests),
			CreatedAt:        fromTS(r.CreatedAt),
		}
	}
	return out, nil
}

func (s *store) ListObservedTokenTurns(ctx context.Context, threadID string, limit int32) ([]TokenRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListObservedTokenTurns(ctx, sqlc.ListObservedTokenTurnsParams{
		Column1: strings.TrimSpace(threadID),
		Limit:   int64(limit),
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
			TokenInput:          int32(r.TokenInput),
			TokenOutput:         int32(r.TokenOutput),
			TokenTotal:          int32(r.TokenTotal),
			ContextWindowTokens: int32(r.ContextWindowTokens),
			CreatedAt:           fromTS(r.CreatedAt),
		}
	}
	return out, nil
}

// fromRow maps a sqlc SessionInsight row into the domain Insight. Time
// fields follow the same "zero value = NULL" convention used across this
// project's stores.
func fromRow(r sqlc.SessionInsight) Insight {
	return Insight{
		ID:                       r.ID,
		ThreadID:                 r.ThreadID,
		AgentID:                  r.AgentID,
		SessionID:                r.SessionID,
		Provider:                 r.Provider,
		LocalTurnID:              r.LocalTurnID,
		ProviderTurnID:           r.ProviderTurnID,
		StartedAt:                fromTSPtr(r.StartedAt),
		CompletedAt:              fromTSPtr(r.CompletedAt),
		DurationMS:               int32(r.DurationMs),
		Success:                  intPtrToBoolPtr(r.Success),
		Status:                   r.Status,
		StopReason:               r.StopReason,
		ToolCalls:                int32(r.ToolCalls),
		ToolCallsObserved:        r.ToolCallsObserved != 0,
		ToolFailures:             int32(r.ToolFailures),
		ToolFailuresObserved:     r.ToolFailuresObserved != 0,
		ApprovalRequests:         int32(r.ApprovalRequests),
		ApprovalRequestsObserved: r.ApprovalRequestsObserved != 0,
		TokenInput:               int32(r.TokenInput),
		TokenOutput:              int32(r.TokenOutput),
		TokenTotal:               int32(r.TokenTotal),
		TokenSnapshotObserved:    r.TokenSnapshotObserved != 0,
		ContextWindowTokens:      int32(r.ContextWindowTokens),
		UIProjection:             r.UIProjection,
		SkillsSelected:           cloneBytes(r.SkillsSelected),
		CreatedAt:                fromTS(r.CreatedAt),
		UpdatedAt:                fromTS(r.UpdatedAt),
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
