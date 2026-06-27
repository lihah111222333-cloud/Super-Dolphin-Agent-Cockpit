package insight

import (
	"context"
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	insightstore "github.com/anthropic-ai/super-agent-v3/internal/store/insight"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Module 将 insight subscriber、flusher、service 和 RPC handler 注入 Fx 树。
// subscriber 注入 BusModule 的 bus.subscribers 组；flusher 注入 runners 组由 platformrunner.RunGroup 驱动。
// 该模块不导入 turn 写入路径，保证 observation 到 insight 的单向依赖不被反向打破。
var Module = fx.Module("insight",
	fx.Provide(
		provideCollector,
		provideInsightReader,
		provideInsightWriter,
		NewFlusher,
		NewService,
		NewInsightSubscribers,
	),
	fx.Provide(
		fx.Annotate(flusherAsRunner, fx.ResultTags(`group:"runners"`)),
	),
)

// provideCollector 用包默认容量创建 collector。
func provideCollector(logger *slog.Logger) *collector {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return newCollector(logger, defaultQueueCapacity)
}

// flusherAsRunner 将 *Flusher 收窄为 contract.Runner 接口，用于 `group:"runners"` 收集器。
func flusherAsRunner(f *Flusher) contract.Runner { return f }

// insightStoreAdapter 是 module.go 装配边界的防腐层。
// 它把 store/insight 的 DTO 转成模块局部 store port，避免业务文件直接依赖 store 包。
type insightStoreAdapter struct {
	store insightstore.Store
}

// provideInsightReader 向只读 service 暴露最小读取端口。
func provideInsightReader(store insightstore.Store) insightReader {
	return insightStoreAdapter{store: store}
}

// provideInsightWriter 向 flusher 暴露最小写入端口。
func provideInsightWriter(store insightstore.Store) insightWriter {
	return insightStoreAdapter{store: store}
}

// Upsert 将 flusher 的模块写入参数转换后写入真实 insight store。
func (a insightStoreAdapter) Upsert(ctx context.Context, params insightUpsertParams) (insightRecord, error) {
	row, err := a.store.Upsert(ctx, toStoreInsightUpsertParams(params))
	if err != nil {
		return insightRecord{}, err
	}
	return toInsightRecord(row), nil
}

// ListByThread 读取指定线程的 insight 行并转换为模块端口 DTO。
func (a insightStoreAdapter) ListByThread(ctx context.Context, threadID string, limit int32) ([]insightRecord, error) {
	rows, err := a.store.ListByThread(ctx, threadID, limit)
	if err != nil {
		return nil, err
	}
	return toInsightRecords(rows), nil
}

// ListRecent 读取最近 insight 行并转换为模块端口 DTO。
func (a insightStoreAdapter) ListRecent(ctx context.Context, limit int32) ([]insightRecord, error) {
	rows, err := a.store.ListRecent(ctx, limit)
	if err != nil {
		return nil, err
	}
	return toInsightRecords(rows), nil
}

// ListObservedApprovalRequests 读取审批观测摘要，并只暴露 dashboard service 需要的字段。
func (a insightStoreAdapter) ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]insightApprovalRow, error) {
	rows, err := a.store.ListObservedApprovalRequests(ctx, threadID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]insightApprovalRow, len(rows))
	for i, row := range rows {
		out[i] = insightApprovalRow{
			ID:               row.ID,
			ThreadID:         row.ThreadID,
			AgentID:          row.AgentID,
			LocalTurnID:      row.LocalTurnID,
			ProviderTurnID:   row.ProviderTurnID,
			ApprovalRequests: row.ApprovalRequests,
			CreatedAt:        row.CreatedAt,
		}
	}
	return out, nil
}

// toStoreInsightUpsertParams 将模块写入 DTO 转为 store DTO。
// 字段逐项透传，避免在装配边界加入新的默认值或兜底逻辑。
func toStoreInsightUpsertParams(p insightUpsertParams) insightstore.UpsertParams {
	return insightstore.UpsertParams{
		ThreadID:                 p.ThreadID,
		AgentID:                  p.AgentID,
		SessionID:                p.SessionID,
		Provider:                 p.Provider,
		LocalTurnID:              p.LocalTurnID,
		ProviderTurnID:           p.ProviderTurnID,
		StartedAt:                p.StartedAt,
		CompletedAt:              p.CompletedAt,
		DurationMS:               p.DurationMS,
		Success:                  p.Success,
		Status:                   p.Status,
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
		SkillsSelected:           p.SkillsSelected,
		CreatedAt:                p.CreatedAt,
		UpdatedAt:                p.UpdatedAt,
	}
}

func toInsightRecords(rows []insightstore.Insight) []insightRecord {
	out := make([]insightRecord, len(rows))
	for i, row := range rows {
		out[i] = toInsightRecord(row)
	}
	return out
}

// toInsightRecord 将 store 行转为模块端口 DTO。
// 这里不解析 JSON 或格式化时间，保持持久化端口与 UI 快照投影职责分离。
func toInsightRecord(row insightstore.Insight) insightRecord {
	return insightRecord{
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
		Success:                  row.Success,
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
		SkillsSelected:           row.SkillsSelected,
		CreatedAt:                row.CreatedAt,
		UpdatedAt:                row.UpdatedAt,
	}
}
