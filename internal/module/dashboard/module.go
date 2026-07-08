package dashboard

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	dbquerystore "github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	systemlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
	"go.uber.org/fx"
)

// serviceParams 是 fx 注入 dashboard service 的依赖声明。
type serviceParams struct {
	fx.In

	Orchestration contract.OrchestrationService `optional:"true"`
	DAGRuntime    contract.DAGRuntime           `optional:"true"`
	AgentStatuses AgentStatusReader
	SystemLogs    SystemLogReader
	AuditLogs     AuditLogReader
	BusLogs       BusLogReader
	AILogs        AILogReader
	DBQueries     DBQueryExecutor
	CommandCards  CommandCardReader
	Prompts       PromptTemplateReader
	SharedFiles   SharedFileReader
	Skills        contract.SkillLister
}

type agentStatusStoreAdapter struct {
	store agentstatusstore.Store
}

// adaptAgentStatusReader 将 agentstatus store 收窄为 dashboard 只读 port。
func adaptAgentStatusReader(store agentstatusstore.Store) AgentStatusReader {
	if store == nil {
		return nil
	}
	return agentStatusStoreAdapter{store: store}
}

// List 读取 agent status store 并转换为 dashboard wire DTO。
func (a agentStatusStoreAdapter) List(ctx context.Context, status string) ([]AgentStatus, error) {
	items, err := a.store.List(ctx, status)
	if err != nil {
		return nil, err
	}
	return mapAgentStatuses(items), nil
}

type aiLogStoreAdapter struct {
	store ailogstore.Store
}

// adaptAILogReader 将 AI log store 收窄为 dashboard 日志查询 port。
func adaptAILogReader(store ailogstore.Store) AILogReader {
	if store == nil {
		return nil
	}
	return aiLogStoreAdapter{store: store}
}

// List 读取 AI 日志列表并转换为 dashboard wire DTO。
func (a aiLogStoreAdapter) List(ctx context.Context, filter AILogFilter) ([]AILog, error) {
	items, err := a.store.List(ctx, ailogstore.ListFilter{
		Keyword: filter.Keyword,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return mapAILogs(items), nil
}

// ListByCategory 按分类读取 AI 日志并转换为 dashboard wire DTO。
func (a aiLogStoreAdapter) ListByCategory(ctx context.Context, category string, keyword string, limit int32) ([]AILog, error) {
	items, err := a.store.ListByCategory(ctx, category, keyword, limit)
	if err != nil {
		return nil, err
	}
	return mapAILogs(items), nil
}

// CountByStatus 读取 AI 日志状态统计并转换为 dashboard wire DTO。
func (a aiLogStoreAdapter) CountByStatus(ctx context.Context) ([]AILogStatusCount, error) {
	items, err := a.store.CountByStatus(ctx)
	if err != nil {
		return nil, err
	}
	return mapAILogStatusCounts(items), nil
}

// ListRecent 读取最近 AI 日志并转换为 dashboard wire DTO。
func (a aiLogStoreAdapter) ListRecent(ctx context.Context, limit int32) ([]AILog, error) {
	items, err := a.store.ListRecent(ctx, limit)
	if err != nil {
		return nil, err
	}
	return mapAILogs(items), nil
}

type auditLogStoreAdapter struct {
	store auditlogstore.Store
}

// adaptAuditLogReader 将 audit log store 收窄为 dashboard 审计日志 port。
func adaptAuditLogReader(store auditlogstore.Store) AuditLogReader {
	if store == nil {
		return nil
	}
	return auditLogStoreAdapter{store: store}
}

// List 读取审计日志并转换为 dashboard wire DTO。
func (a auditLogStoreAdapter) List(ctx context.Context, filter AuditLogFilter) ([]AuditEvent, error) {
	items, err := a.store.List(ctx, auditlogstore.ListFilter{
		EventType: filter.EventType,
		Action:    filter.Action,
		Actor:     filter.Actor,
		Keyword:   filter.Keyword,
		Limit:     filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return mapAuditEvents(items), nil
}

type busLogStoreAdapter struct {
	store buslogstore.Store
}

// adaptBusLogReader 将 bus log store 收窄为 dashboard bus 日志 port。
func adaptBusLogReader(store buslogstore.Store) BusLogReader {
	if store == nil {
		return nil
	}
	return busLogStoreAdapter{store: store}
}

// List 读取 bus 异常日志并转换为 dashboard wire DTO。
func (a busLogStoreAdapter) List(ctx context.Context, filter BusLogFilter) ([]BusExceptionLog, error) {
	items, err := a.store.List(ctx, buslogstore.ListFilter{
		Category: filter.Category,
		Severity: filter.Severity,
		Keyword:  filter.Keyword,
		Limit:    filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return mapBusExceptionLogs(items), nil
}

// Get 读取 bus 异常日志详情并转换为 dashboard wire DTO。
func (a busLogStoreAdapter) Get(ctx context.Context, id int64) (BusExceptionLog, error) {
	item, err := a.store.Get(ctx, id)
	if err != nil {
		return BusExceptionLog{}, err
	}
	return mapBusExceptionLogs([]buslogstore.BusExceptionLog{item})[0], nil
}

type systemLogStoreAdapter struct {
	store systemlogstore.Store
}

// adaptSystemLogReader 将 system log store 收窄为 dashboard system 日志 port。
func adaptSystemLogReader(store systemlogstore.Store) SystemLogReader {
	if store == nil {
		return nil
	}
	return systemLogStoreAdapter{store: store}
}

// List 读取 system log 并转换为 dashboard 内部 DTO。
func (a systemLogStoreAdapter) List(ctx context.Context, filter SystemLogFilter) ([]SystemLog, error) {
	items, err := a.store.List(ctx, systemlogstore.ListFilter{
		Level:        filter.Level,
		Logger:       filter.Logger,
		Source:       filter.Source,
		Component:    filter.Component,
		AgentID:      filter.AgentID,
		ThreadID:     filter.ThreadID,
		TraceID:      filter.TraceID,
		SpanID:       filter.SpanID,
		ParentSpanID: filter.ParentSpanID,
		EventType:    filter.EventType,
		ToolName:     filter.ToolName,
		Keyword:      filter.Keyword,
		Limit:        filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return mapSystemLogs(items), nil
}

// adaptDBQueryExecutor 将 dbquery store 收窄为 dashboard 查询执行 port。
func adaptDBQueryExecutor(store dbquerystore.Store) DBQueryExecutor {
	if store == nil {
		return nil
	}
	return store
}

type commandCardReaderAdapter struct {
	reader commandcardstore.Reader
}

// adaptCommandCardReader 将 commandcard reader 收窄为 dashboard 命令卡片 port。
func adaptCommandCardReader(reader commandcardstore.Reader) CommandCardReader {
	if reader == nil {
		return nil
	}
	return commandCardReaderAdapter{reader: reader}
}

// List 读取命令卡片并转换为 dashboard wire DTO。
func (a commandCardReaderAdapter) List(ctx context.Context, filter CommandCardFilter) ([]CommandCard, error) {
	items, err := a.reader.List(ctx, commandcardstore.ListFilter{
		Keyword: filter.Keyword,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return mapCommandCards(items), nil
}

type promptTemplateReaderAdapter struct {
	reader promptstore.Reader
}

// adaptPromptTemplateReader 将 prompt reader 收窄为 dashboard prompt 模板 port。
func adaptPromptTemplateReader(reader promptstore.Reader) PromptTemplateReader {
	if reader == nil {
		return nil
	}
	return promptTemplateReaderAdapter{reader: reader}
}

// List 读取 prompt 模板并转换为 dashboard wire DTO。
func (a promptTemplateReaderAdapter) List(ctx context.Context, filter PromptTemplateFilter) ([]PromptTemplate, error) {
	items, err := a.reader.List(ctx, promptstore.ListFilter{
		AgentKey: filter.AgentKey,
		Keyword:  filter.Keyword,
		CWD:      filter.CWD,
		Limit:    filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return mapPromptTemplates(items), nil
}

type sharedFileReaderAdapter struct {
	reader sharedfilestore.Reader
}

type sharedFileStoreAdapter struct {
	sharedFileReaderAdapter
	writer sharedfilestore.Upserter
}

// adaptSharedFileReader 将 sharedfile reader 收窄为 dashboard 读 port。
// 当 concrete reader 同时支持 Upserter 时返回读写 adapter，供 workflow material 写入使用。
func adaptSharedFileReader(reader sharedfilestore.Reader) SharedFileReader {
	if reader == nil {
		return nil
	}
	if writer, ok := reader.(sharedfilestore.Upserter); ok {
		return sharedFileStoreAdapter{
			sharedFileReaderAdapter: sharedFileReaderAdapter{reader: reader},
			writer:                  writer,
		}
	}
	return sharedFileReaderAdapter{reader: reader}
}

// Get 读取单个 sharedfile 并转换为 dashboard wire DTO。
func (a sharedFileReaderAdapter) Get(ctx context.Context, path string) (*SharedFile, error) {
	item, err := a.reader.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	return mapSharedFilePtr(item), nil
}

// List 读取 sharedfile 列表并转换为 dashboard wire DTO。
func (a sharedFileReaderAdapter) List(ctx context.Context, filter SharedFileFilter) ([]SharedFile, error) {
	items, err := a.reader.List(ctx, sharedfilestore.ListFilter{
		Prefix: filter.Prefix,
		Limit:  filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return mapSharedFiles(items), nil
}

// Upsert 写入 workflow material sharedfile 并转换返回 DTO。
func (a sharedFileStoreAdapter) Upsert(ctx context.Context, params SharedFileUpsertParams) (*SharedFile, error) {
	item, err := a.writer.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      params.Path,
		Content:   params.Content,
		UpdatedBy: params.UpdatedBy,
	})
	if err != nil {
		return nil, err
	}
	return mapSharedFilePtr(item), nil
}

// mapAgentStatuses 将 store agent 状态逐字段转换为 dashboard wire DTO。
func mapAgentStatuses(items []agentstatusstore.AgentStatus) []AgentStatus {
	out := make([]AgentStatus, 0, len(items))
	for _, item := range items {
		out = append(out, AgentStatus{
			AgentID:     item.AgentID,
			AgentName:   item.AgentName,
			SessionID:   item.SessionID,
			Status:      item.Status,
			StagnantSec: item.StagnantSec,
			Error:       item.Error,
			OutputTail:  item.OutputTail,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
		})
	}
	return out
}

// mapAILogs 将 store AI 日志逐字段转换为 dashboard wire DTO。
func mapAILogs(items []ailogstore.AILog) []AILog {
	out := make([]AILog, 0, len(items))
	for _, item := range items {
		out = append(out, AILog{
			ID:           item.ID,
			Ts:           item.Ts,
			Level:        item.Level,
			Logger:       item.Logger,
			Message:      item.Message,
			Raw:          item.Raw,
			Source:       item.Source,
			Component:    item.Component,
			AgentID:      item.AgentID,
			ThreadID:     item.ThreadID,
			TraceID:      item.TraceID,
			SpanID:       item.SpanID,
			ParentSpanID: item.ParentSpanID,
			EventType:    item.EventType,
			ToolName:     item.ToolName,
			DurationMs:   item.DurationMs,
			Extra:        item.Extra,
			Category:     item.Category,
			Method:       item.Method,
			URL:          item.URL,
			Endpoint:     item.Endpoint,
			Status:       item.Status,
			StatusText:   item.StatusText,
			Model:        item.Model,
		})
	}
	return out
}

// mapAILogStatusCounts 将 store 状态统计逐字段转换为 dashboard wire DTO。
func mapAILogStatusCounts(items []ailogstore.StatusCount) []AILogStatusCount {
	out := make([]AILogStatusCount, 0, len(items))
	for _, item := range items {
		out = append(out, AILogStatusCount{
			Status: item.Status,
			Count:  item.Count,
		})
	}
	return out
}

// mapAuditEvents 将 store 审计事件逐字段转换为 dashboard wire DTO。
func mapAuditEvents(items []auditlogstore.AuditEvent) []AuditEvent {
	out := make([]AuditEvent, 0, len(items))
	for _, item := range items {
		out = append(out, AuditEvent{
			ID:        item.ID,
			Ts:        item.Ts,
			EventType: item.EventType,
			Action:    item.Action,
			Result:    item.Result,
			Actor:     item.Actor,
			Target:    item.Target,
			Detail:    item.Detail,
			Level:     item.Level,
			Extra:     item.Extra,
		})
	}
	return out
}

// mapBusExceptionLogs 将 store bus 异常日志逐字段转换为 dashboard wire DTO。
func mapBusExceptionLogs(items []buslogstore.BusExceptionLog) []BusExceptionLog {
	out := make([]BusExceptionLog, 0, len(items))
	for _, item := range items {
		out = append(out, BusExceptionLog{
			ID:           item.ID,
			Ts:           item.Ts,
			Category:     item.Category,
			Severity:     item.Severity,
			Source:       item.Source,
			ToolName:     item.ToolName,
			Message:      item.Message,
			Traceback:    item.Traceback,
			Extra:        item.Extra,
			HasTraceback: item.HasTraceback,
			HasExtra:     item.HasExtra,
		})
	}
	return out
}

// mapSystemLogs 将 store system log 逐字段转换为 dashboard 内部 DTO。
func mapSystemLogs(items []systemlogstore.SystemLog) []SystemLog {
	out := make([]SystemLog, 0, len(items))
	for _, item := range items {
		out = append(out, SystemLog{
			ID:           item.ID,
			Ts:           item.Ts,
			Level:        item.Level,
			Logger:       item.Logger,
			Message:      item.Message,
			Raw:          item.Raw,
			Source:       item.Source,
			Component:    item.Component,
			AgentID:      item.AgentID,
			ThreadID:     item.ThreadID,
			TraceID:      item.TraceID,
			SpanID:       item.SpanID,
			ParentSpanID: item.ParentSpanID,
			EventType:    item.EventType,
			ToolName:     item.ToolName,
			DurationMs:   item.DurationMs,
			Extra:        item.Extra,
		})
	}
	return out
}

// mapCommandCards 将 store 命令卡片逐字段转换为 dashboard wire DTO。
func mapCommandCards(items []commandcardstore.CommandCard) []CommandCard {
	out := make([]CommandCard, 0, len(items))
	for _, item := range items {
		out = append(out, CommandCard{
			ID:              item.ID,
			CardKey:         item.CardKey,
			Title:           item.Title,
			Description:     item.Description,
			CommandTemplate: item.CommandTemplate,
			ArgsSchema:      item.ArgsSchema,
			RiskLevel:       item.RiskLevel,
			Enabled:         item.Enabled,
			CreatedBy:       item.CreatedBy,
			UpdatedBy:       item.UpdatedBy,
			CreatedAt:       item.CreatedAt,
			UpdatedAt:       item.UpdatedAt,
			LastRunAt:       item.LastRunAt,
			RunCount:        item.RunCount,
		})
	}
	return out
}

// mapPromptTemplates 将 store prompt 模板逐字段转换为 dashboard wire DTO。
func mapPromptTemplates(items []promptstore.PromptTemplate) []PromptTemplate {
	out := make([]PromptTemplate, 0, len(items))
	for _, item := range items {
		out = append(out, PromptTemplate{
			ID:             item.ID,
			PromptKey:      item.PromptKey,
			Title:          item.Title,
			AgentKey:       item.AgentKey,
			ToolName:       item.ToolName,
			PromptText:     item.PromptText,
			WhenToUse:      item.WhenToUse,
			Variables:      item.Variables,
			Tags:           item.Tags,
			Enabled:        item.Enabled,
			ManuallyEdited: item.ManuallyEdited,
			MatchWhen:      item.MatchWhen,
			Priority:       item.Priority,
			CreatedBy:      item.CreatedBy,
			UpdatedBy:      item.UpdatedBy,
			CreatedAt:      item.CreatedAt,
			UpdatedAt:      item.UpdatedAt,
			Description:    item.Description,
		})
	}
	return out
}

// mapSharedFilePtr 将 store sharedfile 指针转换为 dashboard wire DTO 指针。
func mapSharedFilePtr(item *sharedfilestore.SharedFile) *SharedFile {
	if item == nil {
		return nil
	}
	mapped := mapSharedFile(*item)
	return &mapped
}

// mapSharedFiles 将 store sharedfile 列表逐字段转换为 dashboard wire DTO。
func mapSharedFiles(items []sharedfilestore.SharedFile) []SharedFile {
	out := make([]SharedFile, 0, len(items))
	for _, item := range items {
		out = append(out, mapSharedFile(item))
	}
	return out
}

// mapSharedFile 将单个 store sharedfile 逐字段转换为 dashboard wire DTO。
func mapSharedFile(item sharedfilestore.SharedFile) SharedFile {
	return SharedFile{
		Path:      item.Path,
		Content:   item.Content,
		UpdatedBy: item.UpdatedBy,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}

// dashboardHandlersParams 是 fx 注入 dashboard handler 的依赖声明。
type dashboardHandlersParams struct {
	fx.In

	Service  Service
	Insights InsightReader `optional:"true"`
}

// NewDashboardHandlersWithInsights 注册 dashboard RPC handler 并附加 insights 子路由。
// insights reader 是可选依赖，未配置时只注册 dashboard 核心 handler。
func NewDashboardHandlersWithInsights(p dashboardHandlersParams) platformrpc.HandlerMapResult {
	result := NewDashboardHandlers(p.Service)
	addDashboardInsightHandlers(result.Handlers, p.Insights)
	return result
}

// Module 组装 dashboard service、handler 和可选 insights RPC。
var Module = fx.Module("dashboard",
	fx.Provide(
		adaptDBQueryExecutor,
		adaptAgentStatusReader,
		adaptSystemLogReader,
		adaptAuditLogReader,
		adaptBusLogReader,
		adaptAILogReader,
		adaptCommandCardReader,
		adaptPromptTemplateReader,
		adaptSharedFileReader,
	),
	fx.Provide(func(p serviceParams) Service {
		return newServiceWithDAGRuntime(
			p.Orchestration,
			p.DAGRuntime,
			p.AgentStatuses,
			p.SystemLogs,
			p.AuditLogs,
			p.BusLogs,
			p.AILogs,
			p.DBQueries,
			p.CommandCards,
			p.Prompts,
			p.SharedFiles,
			p.Skills,
		)
	}),
	fx.Provide(NewDashboardHandlersWithInsights),
)
