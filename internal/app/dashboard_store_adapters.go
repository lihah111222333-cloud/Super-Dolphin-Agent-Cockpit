package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/app/internal/storeguard"
	"github.com/anthropic-ai/super-agent-v3/internal/module/dashboard"
	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	dbquerystore "github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	systemlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
)

type dashboardAgentStatusAdapter struct{ store agentstatusstore.Store }
type dashboardAILogAdapter struct{ store ailogstore.Store }
type dashboardAuditLogAdapter struct{ store auditlogstore.Store }
type dashboardBusLogAdapter struct{ store buslogstore.Store }
type dashboardSystemLogAdapter struct{ store systemlogstore.Store }
type dashboardDBQueryAdapter struct{ store dbquerystore.Store }
type dashboardCommandCardAdapter struct{ reader commandcardstore.Reader }
type dashboardPromptTemplateAdapter struct{ reader promptstore.Reader }
type dashboardSharedFileAdapter struct{ reader sharedfilestore.Reader }
type dashboardSharedFileStoreAdapter struct {
	*dashboardSharedFileAdapter
	writer sharedfilestore.Upserter
}

var (
	_ dashboard.AgentStatusReader    = (*dashboardAgentStatusAdapter)(nil)
	_ dashboard.AILogReader          = (*dashboardAILogAdapter)(nil)
	_ dashboard.AuditLogReader       = (*dashboardAuditLogAdapter)(nil)
	_ dashboard.BusLogReader         = (*dashboardBusLogAdapter)(nil)
	_ dashboard.SystemLogReader      = (*dashboardSystemLogAdapter)(nil)
	_ dashboard.DBQueryExecutor      = (*dashboardDBQueryAdapter)(nil)
	_ dashboard.CommandCardReader    = (*dashboardCommandCardAdapter)(nil)
	_ dashboard.PromptTemplateReader = (*dashboardPromptTemplateAdapter)(nil)
	_ dashboard.SharedFileReader     = (*dashboardSharedFileAdapter)(nil)
	_ dashboard.SharedFileReader     = (*dashboardSharedFileStoreAdapter)(nil)
	_ dashboard.SharedFileWriter     = (*dashboardSharedFileStoreAdapter)(nil)
)

// provideDashboardAgentStatusReader 把 agent status Store 收窄为 dashboard 读端口。
func provideDashboardAgentStatusReader(store agentstatusstore.Store) dashboard.AgentStatusReader {
	if storeguard.IsNil(store) {
		return nil
	}
	return &dashboardAgentStatusAdapter{store: store}
}

// provideDashboardAILogReader 把 AI log Store 收窄为 dashboard 读端口。
func provideDashboardAILogReader(store ailogstore.Store) dashboard.AILogReader {
	if storeguard.IsNil(store) {
		return nil
	}
	return &dashboardAILogAdapter{store: store}
}

// provideDashboardAuditLogReader 把 audit log Store 收窄为 dashboard 读端口。
func provideDashboardAuditLogReader(store auditlogstore.Store) dashboard.AuditLogReader {
	if storeguard.IsNil(store) {
		return nil
	}
	return &dashboardAuditLogAdapter{store: store}
}

// provideDashboardBusLogReader 把 bus log Store 收窄为 dashboard 读端口。
func provideDashboardBusLogReader(store buslogstore.Store) dashboard.BusLogReader {
	if storeguard.IsNil(store) {
		return nil
	}
	return &dashboardBusLogAdapter{store: store}
}

// provideDashboardSystemLogReader 把 system log Store 收窄为 dashboard 读端口。
func provideDashboardSystemLogReader(store systemlogstore.Store) dashboard.SystemLogReader {
	if storeguard.IsNil(store) {
		return nil
	}
	return &dashboardSystemLogAdapter{store: store}
}

// provideDashboardDBQueryExecutor 用隔离 adapter 暴露 dashboard 查询端口。
func provideDashboardDBQueryExecutor(store dbquerystore.Store) dashboard.DBQueryExecutor {
	if storeguard.IsNil(store) {
		return nil
	}
	return &dashboardDBQueryAdapter{store: store}
}

// provideDashboardCommandCardReader 把 command card reader 收窄为 dashboard 端口。
func provideDashboardCommandCardReader(reader commandcardstore.Reader) dashboard.CommandCardReader {
	if storeguard.IsNil(reader) {
		return nil
	}
	return &dashboardCommandCardAdapter{reader: reader}
}

// provideDashboardPromptTemplateReader 把 prompt reader 收窄为 dashboard 端口。
func provideDashboardPromptTemplateReader(reader promptstore.Reader) dashboard.PromptTemplateReader {
	if storeguard.IsNil(reader) {
		return nil
	}
	return &dashboardPromptTemplateAdapter{reader: reader}
}

// provideDashboardSharedFileReader 保留 concrete reader 的可选写能力。
func provideDashboardSharedFileReader(reader sharedfilestore.Reader) dashboard.SharedFileReader {
	if storeguard.IsNil(reader) {
		return nil
	}
	base := &dashboardSharedFileAdapter{reader: reader}
	if writer, ok := reader.(sharedfilestore.Upserter); ok && !storeguard.IsNil(writer) {
		return &dashboardSharedFileStoreAdapter{dashboardSharedFileAdapter: base, writer: writer}
	}
	return base
}

// List 读取并转换 agent 状态列表。
func (a *dashboardAgentStatusAdapter) List(ctx context.Context, status string) ([]dashboard.AgentStatus, error) {
	items, err := a.store.List(ctx, status)
	if err != nil {
		return nil, err
	}
	return mapDashboardAgentStatuses(items), nil
}

// List 按过滤条件读取 AI 日志。
func (a *dashboardAILogAdapter) List(ctx context.Context, filter dashboard.AILogFilter) ([]dashboard.AILog, error) {
	items, err := a.store.List(ctx, toStoreDashboardAILogFilter(filter))
	if err != nil {
		return nil, err
	}
	return mapDashboardAILogs(items), nil
}

// ListByCategory 按分类读取 AI 日志。
func (a *dashboardAILogAdapter) ListByCategory(ctx context.Context, category, keyword string, limit int32) ([]dashboard.AILog, error) {
	items, err := a.store.ListByCategory(ctx, category, keyword, limit)
	if err != nil {
		return nil, err
	}
	return mapDashboardAILogs(items), nil
}

// CountByStatus 读取 AI 日志状态聚合。
func (a *dashboardAILogAdapter) CountByStatus(ctx context.Context) ([]dashboard.AILogStatusCount, error) {
	items, err := a.store.CountByStatus(ctx)
	if err != nil {
		return nil, err
	}
	return mapDashboardAILogStatusCounts(items), nil
}

// ListRecent 读取最近 AI 日志。
func (a *dashboardAILogAdapter) ListRecent(ctx context.Context, limit int32) ([]dashboard.AILog, error) {
	items, err := a.store.ListRecent(ctx, limit)
	if err != nil {
		return nil, err
	}
	return mapDashboardAILogs(items), nil
}

// List 读取并转换审计事件。
func (a *dashboardAuditLogAdapter) List(ctx context.Context, filter dashboard.AuditLogFilter) ([]dashboard.AuditEvent, error) {
	storeFilter := toStoreDashboardAuditLogFilter(filter)
	items, err := a.store.List(ctx, storeFilter)
	if err != nil {
		return nil, err
	}
	return mapDashboardAuditEvents(items), nil
}

// List 读取并转换 bus 异常日志。
func (a *dashboardBusLogAdapter) List(ctx context.Context, filter dashboard.BusLogFilter) ([]dashboard.BusExceptionLog, error) {
	storeFilter := toStoreDashboardBusLogFilter(filter)
	items, err := a.store.List(ctx, storeFilter)
	if err != nil {
		return nil, err
	}
	return mapDashboardBusLogs(items), nil
}

// Get 读取单条 bus 异常日志。
func (a *dashboardBusLogAdapter) Get(ctx context.Context, id int64) (dashboard.BusExceptionLog, error) {
	item, err := a.store.Get(ctx, id)
	if err != nil {
		return dashboard.BusExceptionLog{}, err
	}
	return mapDashboardBusLog(item), nil
}

// List 读取并转换 system 日志。
func (a *dashboardSystemLogAdapter) List(ctx context.Context, filter dashboard.SystemLogFilter) ([]dashboard.SystemLog, error) {
	storeFilter := toStoreDashboardSystemLogFilter(filter)
	items, err := a.store.List(ctx, storeFilter)
	if err != nil {
		return nil, err
	}
	return mapDashboardSystemLogs(items), nil
}

// Query 复制查询参数与递归结果，隔离 Store 所有权。
func (a *dashboardDBQueryAdapter) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	storeArgs := append([]any(nil), args...)
	rows, err := a.store.Query(ctx, query, storeArgs...)
	if err != nil {
		return nil, err
	}
	return cloneDashboardRows(rows), nil
}

// List 读取并转换命令卡片。
func (a *dashboardCommandCardAdapter) List(ctx context.Context, filter dashboard.CommandCardFilter) ([]dashboard.CommandCard, error) {
	items, err := a.reader.List(ctx, toStoreDashboardCommandCardFilter(filter))
	if err != nil {
		return nil, err
	}
	return mapDashboardCommandCards(items), nil
}

// List 读取并转换 prompt 模板。
func (a *dashboardPromptTemplateAdapter) List(ctx context.Context, filter dashboard.PromptTemplateFilter) ([]dashboard.PromptTemplate, error) {
	items, err := a.reader.List(ctx, toStoreDashboardPromptTemplateFilter(filter))
	if err != nil {
		return nil, err
	}
	return mapDashboardPromptTemplates(items), nil
}

// Get 读取并转换单个 shared file。
func (a *dashboardSharedFileAdapter) Get(ctx context.Context, path string) (*dashboard.SharedFile, error) {
	item, err := a.reader.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	return mapDashboardSharedFilePtr(item), nil
}

// List 读取并转换 shared file 列表。
func (a *dashboardSharedFileAdapter) List(ctx context.Context, filter dashboard.SharedFileFilter) ([]dashboard.SharedFile, error) {
	items, err := a.reader.List(ctx, toStoreDashboardSharedFileFilter(filter))
	if err != nil {
		return nil, err
	}
	return mapDashboardSharedFiles(items), nil
}

// Upsert 逐字段转换并写入 workflow material。
func (a *dashboardSharedFileStoreAdapter) Upsert(ctx context.Context, params dashboard.SharedFileUpsertParams) (*dashboard.SharedFile, error) {
	item, err := a.writer.Upsert(ctx, toStoreDashboardSharedFileUpsert(params))
	if err != nil {
		return nil, err
	}
	return mapDashboardSharedFilePtr(item), nil
}

// mapDashboardAgentStatuses 转换 agent status 列表并复制 JSON。
func mapDashboardAgentStatuses(items []agentstatusstore.AgentStatus) []dashboard.AgentStatus {
	result := make([]dashboard.AgentStatus, len(items))
	for i, v := range items {
		result[i] = dashboard.AgentStatus{AgentID: v.AgentID, AgentName: v.AgentName, SessionID: v.SessionID, Status: v.Status, StagnantSec: v.StagnantSec, Error: v.Error, OutputTail: cloneDashboardJSON(v.OutputTail), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	}
	return result
}

// mapDashboardAILogs 转换 AI 日志列表并复制指针与 JSON。
func mapDashboardAILogs(items []ailogstore.AILog) []dashboard.AILog {
	result := make([]dashboard.AILog, len(items))
	for i, v := range items {
		result[i] = dashboard.AILog{ID: v.ID, Ts: v.Ts, Level: v.Level, Logger: v.Logger, Message: v.Message, Raw: v.Raw, Source: v.Source, Component: v.Component, AgentID: v.AgentID, ThreadID: v.ThreadID, TraceID: v.TraceID, SpanID: v.SpanID, ParentSpanID: v.ParentSpanID, EventType: v.EventType, ToolName: v.ToolName, DurationMs: cloneDashboardInt32(v.DurationMs), Extra: cloneDashboardJSON(v.Extra), Category: v.Category, Method: v.Method, URL: v.URL, Endpoint: v.Endpoint, Status: v.Status, StatusText: v.StatusText, Model: v.Model}
	}
	return result
}

// mapDashboardAILogStatusCounts 转换 AI 日志状态聚合列表。
func mapDashboardAILogStatusCounts(items []ailogstore.StatusCount) []dashboard.AILogStatusCount {
	result := make([]dashboard.AILogStatusCount, len(items))
	for i, item := range items {
		result[i] = dashboard.AILogStatusCount{Status: item.Status, Count: item.Count}
	}
	return result
}

// mapDashboardAuditEvents 转换审计事件并复制 JSON。
func mapDashboardAuditEvents(items []auditlogstore.AuditEvent) []dashboard.AuditEvent {
	result := make([]dashboard.AuditEvent, len(items))
	for i, v := range items {
		result[i] = dashboard.AuditEvent{ID: v.ID, Ts: v.Ts, EventType: v.EventType, Action: v.Action, Result: v.Result, Actor: v.Actor, Target: v.Target, Detail: v.Detail, Level: v.Level, Extra: cloneDashboardJSON(v.Extra)}
	}
	return result
}

// mapDashboardBusLogs 转换 bus 异常日志列表。
func mapDashboardBusLogs(items []buslogstore.BusExceptionLog) []dashboard.BusExceptionLog {
	result := make([]dashboard.BusExceptionLog, len(items))
	for i, v := range items {
		result[i] = mapDashboardBusLog(v)
	}
	return result
}

// mapDashboardBusLog 转换单条 bus 异常日志并复制 JSON。
func mapDashboardBusLog(v buslogstore.BusExceptionLog) dashboard.BusExceptionLog {
	return dashboard.BusExceptionLog{ID: v.ID, Ts: v.Ts, Category: v.Category, Severity: v.Severity, Source: v.Source, ToolName: v.ToolName, Message: v.Message, Traceback: v.Traceback, Extra: cloneDashboardJSON(v.Extra), HasTraceback: v.HasTraceback, HasExtra: v.HasExtra}
}

// mapDashboardSystemLogs 转换 system 日志并复制指针与 JSON。
func mapDashboardSystemLogs(items []systemlogstore.SystemLog) []dashboard.SystemLog {
	result := make([]dashboard.SystemLog, len(items))
	for i, v := range items {
		result[i] = dashboard.SystemLog{ID: v.ID, Ts: v.Ts, Level: v.Level, Logger: v.Logger, Message: v.Message, Raw: v.Raw, Source: v.Source, Component: v.Component, AgentID: v.AgentID, ThreadID: v.ThreadID, TraceID: v.TraceID, SpanID: v.SpanID, ParentSpanID: v.ParentSpanID, EventType: v.EventType, ToolName: v.ToolName, DurationMs: cloneDashboardInt32(v.DurationMs), Extra: cloneDashboardJSON(v.Extra)}
	}
	return result
}

// mapDashboardCommandCards 转换命令卡片并复制可变字段。
func mapDashboardCommandCards(items []commandcardstore.CommandCard) []dashboard.CommandCard {
	result := make([]dashboard.CommandCard, len(items))
	for i, v := range items {
		result[i] = dashboard.CommandCard{ID: v.ID, CardKey: v.CardKey, Title: v.Title, Description: v.Description, CommandTemplate: v.CommandTemplate, ArgsSchema: cloneDashboardJSON(v.ArgsSchema), RiskLevel: v.RiskLevel, Enabled: v.Enabled, CreatedBy: v.CreatedBy, UpdatedBy: v.UpdatedBy, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, LastRunAt: cloneDashboardTime(v.LastRunAt), RunCount: v.RunCount}
	}
	return result
}

// mapDashboardPromptTemplates 转换 prompt 模板并复制 JSON。
func mapDashboardPromptTemplates(items []promptstore.PromptTemplate) []dashboard.PromptTemplate {
	result := make([]dashboard.PromptTemplate, len(items))
	for i, v := range items {
		result[i] = dashboard.PromptTemplate{ID: v.ID, PromptKey: v.PromptKey, Title: v.Title, AgentKey: v.AgentKey, ToolName: v.ToolName, PromptText: v.PromptText, WhenToUse: v.WhenToUse, Variables: cloneDashboardJSON(v.Variables), Tags: cloneDashboardJSON(v.Tags), Enabled: v.Enabled, ManuallyEdited: v.ManuallyEdited, MatchWhen: cloneDashboardJSON(v.MatchWhen), Priority: v.Priority, CreatedBy: v.CreatedBy, UpdatedBy: v.UpdatedBy, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Description: v.Description}
	}
	return result
}

// mapDashboardSharedFilePtr 保持 nil 指针结果语义。
func mapDashboardSharedFilePtr(item *sharedfilestore.SharedFile) *dashboard.SharedFile {
	if item == nil {
		return nil
	}
	v := mapDashboardSharedFile(*item)
	return &v
}

// mapDashboardSharedFiles 转换 shared file 列表并分配新切片。
func mapDashboardSharedFiles(items []sharedfilestore.SharedFile) []dashboard.SharedFile {
	result := make([]dashboard.SharedFile, len(items))
	for i, v := range items {
		result[i] = mapDashboardSharedFile(v)
	}
	return result
}

// mapDashboardSharedFile 逐字段转换 shared file。
func mapDashboardSharedFile(v sharedfilestore.SharedFile) dashboard.SharedFile {
	return dashboard.SharedFile{Path: v.Path, Content: v.Content, UpdatedBy: v.UpdatedBy, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

// toStoreDashboardAILogFilter 转换 AI 日志过滤条件。
func toStoreDashboardAILogFilter(v dashboard.AILogFilter) ailogstore.ListFilter {
	return ailogstore.ListFilter{Keyword: v.Keyword, Limit: v.Limit}
}

// toStoreDashboardAuditLogFilter 转换审计日志过滤条件。
func toStoreDashboardAuditLogFilter(v dashboard.AuditLogFilter) auditlogstore.ListFilter {
	return auditlogstore.ListFilter{EventType: v.EventType, Action: v.Action, Actor: v.Actor, Keyword: v.Keyword, Limit: v.Limit}
}

// toStoreDashboardBusLogFilter 转换 bus 日志过滤条件。
func toStoreDashboardBusLogFilter(v dashboard.BusLogFilter) buslogstore.ListFilter {
	return buslogstore.ListFilter{Category: v.Category, Severity: v.Severity, Keyword: v.Keyword, Limit: v.Limit}
}

// toStoreDashboardSystemLogFilter 转换 system 日志过滤条件。
func toStoreDashboardSystemLogFilter(v dashboard.SystemLogFilter) systemlogstore.ListFilter {
	return systemlogstore.ListFilter{Level: v.Level, Logger: v.Logger, Source: v.Source, Component: v.Component, AgentID: v.AgentID, ThreadID: v.ThreadID, TraceID: v.TraceID, SpanID: v.SpanID, ParentSpanID: v.ParentSpanID, EventType: v.EventType, ToolName: v.ToolName, Keyword: v.Keyword, Limit: v.Limit}
}

// toStoreDashboardCommandCardFilter 转换命令卡片过滤条件。
func toStoreDashboardCommandCardFilter(v dashboard.CommandCardFilter) commandcardstore.ListFilter {
	return commandcardstore.ListFilter{Keyword: v.Keyword, Limit: v.Limit}
}

// toStoreDashboardPromptTemplateFilter 转换 prompt 模板过滤条件。
func toStoreDashboardPromptTemplateFilter(v dashboard.PromptTemplateFilter) promptstore.ListFilter {
	return promptstore.ListFilter{AgentKey: v.AgentKey, Keyword: v.Keyword, CWD: v.CWD, Limit: v.Limit}
}

// toStoreDashboardSharedFileFilter 转换 shared file 过滤条件。
func toStoreDashboardSharedFileFilter(v dashboard.SharedFileFilter) sharedfilestore.ListFilter {
	return sharedfilestore.ListFilter{Prefix: v.Prefix, Limit: v.Limit}
}

// toStoreDashboardSharedFileUpsert 转换 shared file 写入参数。
func toStoreDashboardSharedFileUpsert(v dashboard.SharedFileUpsertParams) sharedfilestore.UpsertParams {
	return sharedfilestore.UpsertParams{Path: v.Path, Content: v.Content, UpdatedBy: v.UpdatedBy}
}

// cloneDashboardJSON 复制 JSON backing array。
func cloneDashboardJSON(v json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), v...) }

// cloneDashboardInt32 复制可选耗时指针。
func cloneDashboardInt32(v *int32) *int32 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// cloneDashboardTime 复制可选时间指针。
func cloneDashboardTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// cloneDashboardRows 深复制数据库查询结果行。
func cloneDashboardRows(rows []map[string]any) []map[string]any {
	result := make([]map[string]any, len(rows))
	for i, row := range rows {
		result[i] = cloneDashboardMap(row)
	}
	return result
}

// cloneDashboardMap 深复制单条数据库查询结果。
func cloneDashboardMap(row map[string]any) map[string]any {
	if row == nil {
		return nil
	}
	result := make(map[string]any, len(row))
	for k, v := range row {
		result[k] = cloneDashboardValue(v)
	}
	return result
}

// cloneDashboardValue 递归复制已知 SQL/JSON 可变值。
func cloneDashboardValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		return cloneDashboardMap(value)
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = cloneDashboardValue(item)
		}
		return out
	case json.RawMessage:
		return cloneDashboardJSON(value)
	case []byte:
		return append([]byte(nil), value...)
	default:
		return value
	}
}
