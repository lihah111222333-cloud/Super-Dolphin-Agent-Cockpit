package insight

import (
	"context"
	"errors"
	"strings"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

const (
	defaultListLimit int32 = 100
	maxListLimit     int32 = 500
)

var errUnsupportedInsightRowType = errors.New("session insight: unsupported row type")

type querier interface {
	UpsertSessionInsight(ctx context.Context, arg sqlc.UpsertSessionInsightParams) (sqlc.UpsertSessionInsightRow, error)
	GetSessionInsightByLocalTurn(ctx context.Context, arg sqlc.GetSessionInsightByLocalTurnParams) (sqlc.GetSessionInsightByLocalTurnRow, error)
	ListSessionInsightsByThread(ctx context.Context, arg sqlc.ListSessionInsightsByThreadParams) ([]sqlc.ListSessionInsightsByThreadRow, error)
	ListRecentSessionInsights(ctx context.Context, arg sqlc.ListRecentSessionInsightsParams) ([]sqlc.ListRecentSessionInsightsRow, error)
	ListObservedApprovalRequests(ctx context.Context, arg sqlc.ListObservedApprovalRequestsParams) ([]sqlc.ListObservedApprovalRequestsRow, error)
	ListObservedTokenTurns(ctx context.Context, arg sqlc.ListObservedTokenTurnsParams) ([]sqlc.ListObservedTokenTurnsRow, error)
}

// store 封装 session insight 的 sqlc 访问。
type store struct{ q querier }

// NewStore 创建基于 sqlc 的 session insight 存储。
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

// normalizeListLimit 统一校验 insight 列表读取窗口。
// 非正值保留既有默认窗口；超过上限直接失败，避免 dashboard 或内部调用放大查询。
func normalizeListLimit(limit int32) (int32, error) {
	if limit <= 0 {
		return defaultListLimit, nil
	}
	if limit > maxListLimit {
		return 0, ErrInvalidLimit
	}
	return limit, nil
}

// ----- Store impl -----

// Upsert 写入或更新一次会话观察结果。
// 时间和状态字段在进入 SQL 前完成默认值处理，避免数据库层承担业务默认值兜底。
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
	insight, err := fromRow(row)
	if err != nil {
		return Insight{}, wrap(err, "upsert")
	}
	return insight, nil
}

// GetByLocalTurn 通过线程 ID 和本地 turn ID 读取单条 insight。
// 两个 ID 都必须存在，未命中时统一映射为 insight.ErrNotFound。
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
	insight, err := fromRow(row)
	if err != nil {
		return Insight{}, wrap(err, "get_by_local_turn")
	}
	return insight, nil
}

// ListByThread 列出指定线程的 insight。
// limit 未传或非正时使用受控默认值，超过上限时失败；线程 ID 必填以避免跨线程扫描。
func (s *store) ListByThread(ctx context.Context, threadID string, limit int32) ([]Insight, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, wrap(ErrEmptyID, "list_by_thread")
	}
	limit, err := normalizeListLimit(limit)
	if err != nil {
		return nil, wrap(err, "list_by_thread")
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
		insight, err := fromRow(r)
		if err != nil {
			return nil, wrap(err, "list_by_thread")
		}
		out[i] = insight
	}
	return out, nil
}

// ListRecent 列出最近的 insight 记录。
// 该查询用于概览页，非正 limit 会收敛到默认窗口，超过上限时直接失败。
func (s *store) ListRecent(ctx context.Context, limit int32) ([]Insight, error) {
	limit, err := normalizeListLimit(limit)
	if err != nil {
		return nil, wrap(err, "list_recent")
	}
	rows, err := s.q.ListRecentSessionInsights(ctx, sqlc.ListRecentSessionInsightsParams{Limit: int64(limit)})
	if err != nil {
		return nil, wrap(err, "list_recent")
	}
	out := make([]Insight, len(rows))
	for i, r := range rows {
		insight, err := fromRow(r)
		if err != nil {
			return nil, wrap(err, "list_recent")
		}
		out[i] = insight
	}
	return out, nil
}

// ListObservedApprovalRequests 列出已观察到审批请求的 turn 摘要。
// threadID 允许为空表示全局视图，limit 仍走统一窗口校验。
func (s *store) ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]ApprovalRow, error) {
	limit, err := normalizeListLimit(limit)
	if err != nil {
		return nil, wrap(err, "list_observed_approval_requests")
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

// ListObservedTokenTurns 列出已采集 token 快照的 turn 摘要。
// 该方法只返回观测行，不推断未上报的 token 数据，并拒绝过大的读取窗口。
func (s *store) ListObservedTokenTurns(ctx context.Context, threadID string, limit int32) ([]TokenRow, error) {
	limit, err := normalizeListLimit(limit)
	if err != nil {
		return nil, wrap(err, "list_observed_token_turns")
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

// fromRow 将不同 sqlc 查询返回的 session insight 行统一映射为领域对象。
// 不支持的行类型返回错误，避免新增查询结果时静默丢字段。
func fromRow(row any) (Insight, error) {
	switch r := row.(type) {
	case sqlc.UpsertSessionInsightRow:
		return insightFromFields(r.ID, r.ThreadID, r.AgentID, r.SessionID, r.Provider, r.LocalTurnID,
			r.ProviderTurnID, r.StartedAt, r.CompletedAt, r.DurationMs, r.Success, r.Status, r.StopReason,
			r.ToolCalls, r.ToolCallsObserved, r.ToolFailures, r.ToolFailuresObserved, r.ApprovalRequests,
			r.ApprovalRequestsObserved, r.TokenInput, r.TokenOutput, r.TokenTotal, r.TokenSnapshotObserved,
			r.ContextWindowTokens, r.UIProjection, r.SkillsSelected, r.CreatedAt, r.UpdatedAt), nil
	case sqlc.GetSessionInsightByLocalTurnRow:
		return insightFromFields(r.ID, r.ThreadID, r.AgentID, r.SessionID, r.Provider, r.LocalTurnID,
			r.ProviderTurnID, r.StartedAt, r.CompletedAt, r.DurationMs, r.Success, r.Status, r.StopReason,
			r.ToolCalls, r.ToolCallsObserved, r.ToolFailures, r.ToolFailuresObserved, r.ApprovalRequests,
			r.ApprovalRequestsObserved, r.TokenInput, r.TokenOutput, r.TokenTotal, r.TokenSnapshotObserved,
			r.ContextWindowTokens, r.UIProjection, r.SkillsSelected, r.CreatedAt, r.UpdatedAt), nil
	case sqlc.ListSessionInsightsByThreadRow:
		return insightFromFields(r.ID, r.ThreadID, r.AgentID, r.SessionID, r.Provider, r.LocalTurnID,
			r.ProviderTurnID, r.StartedAt, r.CompletedAt, r.DurationMs, r.Success, r.Status, r.StopReason,
			r.ToolCalls, r.ToolCallsObserved, r.ToolFailures, r.ToolFailuresObserved, r.ApprovalRequests,
			r.ApprovalRequestsObserved, r.TokenInput, r.TokenOutput, r.TokenTotal, r.TokenSnapshotObserved,
			r.ContextWindowTokens, r.UIProjection, r.SkillsSelected, r.CreatedAt, r.UpdatedAt), nil
	case sqlc.ListRecentSessionInsightsRow:
		return insightFromFields(r.ID, r.ThreadID, r.AgentID, r.SessionID, r.Provider, r.LocalTurnID,
			r.ProviderTurnID, r.StartedAt, r.CompletedAt, r.DurationMs, r.Success, r.Status, r.StopReason,
			r.ToolCalls, r.ToolCallsObserved, r.ToolFailures, r.ToolFailuresObserved, r.ApprovalRequests,
			r.ApprovalRequestsObserved, r.TokenInput, r.TokenOutput, r.TokenTotal, r.TokenSnapshotObserved,
			r.ContextWindowTokens, r.UIProjection, r.SkillsSelected, r.CreatedAt, r.UpdatedAt), nil
	default:
		return Insight{}, errUnsupportedInsightRowType
	}
}

// insightFromFields 汇总各查询行的同构字段并构造 Insight。
// 指针时间与布尔观测位在这里统一转换，确保所有列表和详情接口表现一致。
func insightFromFields(
	id int64,
	threadID, agentID, sessionID, provider, localTurnID, providerTurnID string,
	startedAt, completedAt *int64,
	durationMs int64,
	success *int64,
	status, stopReason string,
	toolCalls, toolCallsObserved, toolFailures, toolFailuresObserved int64,
	approvalRequests, approvalRequestsObserved int64,
	tokenInput, tokenOutput, tokenTotal, tokenSnapshotObserved int64,
	contextWindowTokens int64,
	uiProjection string,
	skillsSelected []byte,
	createdAt, updatedAt int64,
) Insight {
	return Insight{
		ID:                       id,
		ThreadID:                 threadID,
		AgentID:                  agentID,
		SessionID:                sessionID,
		Provider:                 provider,
		LocalTurnID:              localTurnID,
		ProviderTurnID:           providerTurnID,
		StartedAt:                fromTSPtr(startedAt),
		CompletedAt:              fromTSPtr(completedAt),
		DurationMS:               int32(durationMs),
		Success:                  intPtrToBoolPtr(success),
		Status:                   status,
		StopReason:               stopReason,
		ToolCalls:                int32(toolCalls),
		ToolCallsObserved:        toolCallsObserved != 0,
		ToolFailures:             int32(toolFailures),
		ToolFailuresObserved:     toolFailuresObserved != 0,
		ApprovalRequests:         int32(approvalRequests),
		ApprovalRequestsObserved: approvalRequestsObserved != 0,
		TokenInput:               int32(tokenInput),
		TokenOutput:              int32(tokenOutput),
		TokenTotal:               int32(tokenTotal),
		TokenSnapshotObserved:    tokenSnapshotObserved != 0,
		ContextWindowTokens:      int32(contextWindowTokens),
		UIProjection:             uiProjection,
		SkillsSelected:           cloneBytes(skillsSelected),
		CreatedAt:                fromTS(createdAt),
		UpdatedAt:                fromTS(updatedAt),
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
