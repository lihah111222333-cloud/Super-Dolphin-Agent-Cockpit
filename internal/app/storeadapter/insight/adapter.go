package insightadapter

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/app/internal/storeguard"
	"github.com/anthropic-ai/super-agent-v3/internal/module/insight"
	insightstore "github.com/anthropic-ai/super-agent-v3/internal/store/insight"
)

var errInsightStoreAdapterMissing = errors.New("insight: store adapter missing store")

type insightStoreAdapter struct {
	store insightstore.Store
}

var _ insight.Reader = (*insightStoreAdapter)(nil)
var _ insight.Writer = (*insightStoreAdapter)(nil)

// provideInsightReader 将 required Store 收窄为 insight 读取端口，缺失时立即报错。
func provideInsightReader(store insightstore.Store) (insight.Reader, error) {
	adapter, err := newInsightStoreAdapter(store)
	if err != nil {
		return nil, err
	}
	return adapter, nil
}

// provideInsightWriter 将 required Store 收窄为 insight 写入端口，缺失时立即报错。
func provideInsightWriter(store insightstore.Store) (insight.Writer, error) {
	adapter, err := newInsightStoreAdapter(store)
	if err != nil {
		return nil, err
	}
	return adapter, nil
}

// newInsightStoreAdapter 校验 required Store 并构造同时实现读写端口的适配器。
func newInsightStoreAdapter(store insightstore.Store) (*insightStoreAdapter, error) {
	if storeguard.IsNil(store) {
		return nil, errInsightStoreAdapterMissing
	}
	return &insightStoreAdapter{store: store}, nil
}

// Upsert 将 insight 写入 DTO 转换后写入 Store，并把结果投影回领域 DTO。
func (a *insightStoreAdapter) Upsert(ctx context.Context, params insight.UpsertParams) (insight.Record, error) {
	if err := a.validate(); err != nil {
		return insight.Record{}, err
	}
	row, err := a.store.Upsert(ctx, insightUpsertParamsToStore(params))
	if err != nil {
		return insight.Record{}, err
	}
	return insightRecordFromStore(row), nil
}

// ListByThread 读取指定线程的 Store 行并转换为 insight 领域记录。
func (a *insightStoreAdapter) ListByThread(ctx context.Context, threadID string, limit int32) ([]insight.Record, error) {
	if err := a.validate(); err != nil {
		return nil, err
	}
	rows, err := a.store.ListByThread(ctx, threadID, limit)
	if err != nil {
		return nil, err
	}
	return insightRecordsFromStore(rows), nil
}

// ListRecent 读取最近 Store 行并转换为 insight 领域记录。
func (a *insightStoreAdapter) ListRecent(ctx context.Context, limit int32) ([]insight.Record, error) {
	if err := a.validate(); err != nil {
		return nil, err
	}
	rows, err := a.store.ListRecent(ctx, limit)
	if err != nil {
		return nil, err
	}
	return insightRecordsFromStore(rows), nil
}

// ListObservedApprovalRequests 读取审批观测摘要并转换为 insight 领域投影。
func (a *insightStoreAdapter) ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]insight.ApprovalRow, error) {
	if err := a.validate(); err != nil {
		return nil, err
	}
	rows, err := a.store.ListObservedApprovalRequests(ctx, threadID, limit)
	if err != nil {
		return nil, err
	}
	return insightApprovalRowsFromStore(rows), nil
}

// validate 确保适配器和 required Store 均可调用。
func (a *insightStoreAdapter) validate() error {
	if a == nil || storeguard.IsNil(a.store) {
		return errInsightStoreAdapterMissing
	}
	return nil
}

// insightUpsertParamsToStore 逐字段转换写入 DTO，并复制可变字段。
func insightUpsertParamsToStore(params insight.UpsertParams) insightstore.UpsertParams {
	return insightstore.UpsertParams{
		ThreadID:                 params.ThreadID,
		AgentID:                  params.AgentID,
		SessionID:                params.SessionID,
		Provider:                 params.Provider,
		LocalTurnID:              params.LocalTurnID,
		ProviderTurnID:           params.ProviderTurnID,
		StartedAt:                params.StartedAt,
		CompletedAt:              params.CompletedAt,
		DurationMS:               params.DurationMS,
		Success:                  copyInsightSuccess(params.Success),
		Status:                   params.Status,
		StopReason:               params.StopReason,
		ToolCalls:                params.ToolCalls,
		ToolCallsObserved:        params.ToolCallsObserved,
		ToolFailures:             params.ToolFailures,
		ToolFailuresObserved:     params.ToolFailuresObserved,
		ApprovalRequests:         params.ApprovalRequests,
		ApprovalRequestsObserved: params.ApprovalRequestsObserved,
		TokenInput:               params.TokenInput,
		TokenOutput:              params.TokenOutput,
		TokenTotal:               params.TokenTotal,
		TokenSnapshotObserved:    params.TokenSnapshotObserved,
		ContextWindowTokens:      params.ContextWindowTokens,
		UIProjection:             params.UIProjection,
		SkillsSelected:           copyInsightSkillsSelected(params.SkillsSelected),
		CreatedAt:                params.CreatedAt,
		UpdatedAt:                params.UpdatedAt,
	}
}

// insightRecordsFromStore 批量转换 Store 行。
func insightRecordsFromStore(rows []insightstore.Insight) []insight.Record {
	out := make([]insight.Record, len(rows))
	for index, row := range rows {
		out[index] = insightRecordFromStore(row)
	}
	return out
}

// insightRecordFromStore 逐字段转换 Store 行，并复制可变字段。
func insightRecordFromStore(row insightstore.Insight) insight.Record {
	return insight.Record{
		ID:                       row.ID,
		ThreadID:                 row.ThreadID,
		AgentID:                  row.AgentID,
		SessionID:                row.SessionID,
		Provider:                 row.Provider,
		LocalTurnID:              row.LocalTurnID,
		ProviderTurnID:           row.ProviderTurnID,
		StartedAt:                row.StartedAt,
		CompletedAt:              row.CompletedAt,
		DurationMS:               row.DurationMS,
		Success:                  copyInsightSuccess(row.Success),
		Status:                   row.Status,
		StopReason:               row.StopReason,
		ToolCalls:                row.ToolCalls,
		ToolCallsObserved:        row.ToolCallsObserved,
		ToolFailures:             row.ToolFailures,
		ToolFailuresObserved:     row.ToolFailuresObserved,
		ApprovalRequests:         row.ApprovalRequests,
		ApprovalRequestsObserved: row.ApprovalRequestsObserved,
		TokenInput:               row.TokenInput,
		TokenOutput:              row.TokenOutput,
		TokenTotal:               row.TokenTotal,
		TokenSnapshotObserved:    row.TokenSnapshotObserved,
		ContextWindowTokens:      row.ContextWindowTokens,
		UIProjection:             row.UIProjection,
		SkillsSelected:           copyInsightSkillsSelected(row.SkillsSelected),
		CreatedAt:                row.CreatedAt,
		UpdatedAt:                row.UpdatedAt,
	}
}

// insightApprovalRowsFromStore 转换审批观测摘要。
func insightApprovalRowsFromStore(rows []insightstore.ApprovalRow) []insight.ApprovalRow {
	out := make([]insight.ApprovalRow, len(rows))
	for index, row := range rows {
		out[index] = insight.ApprovalRow{
			ID:               row.ID,
			ThreadID:         row.ThreadID,
			AgentID:          row.AgentID,
			LocalTurnID:      row.LocalTurnID,
			ProviderTurnID:   row.ProviderTurnID,
			ApprovalRequests: row.ApprovalRequests,
			CreatedAt:        row.CreatedAt,
		}
	}
	return out
}

// copyInsightSuccess 复制可空成功状态，避免跨边界共享指针。
func copyInsightSuccess(value *bool) *bool {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

// copyInsightSkillsSelected 复制技能 JSON，避免跨边界共享底层数组。
func copyInsightSkillsSelected(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
