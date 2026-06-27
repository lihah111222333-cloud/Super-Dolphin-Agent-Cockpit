package dashboard

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

// logFilterField 是 dashboard 统一日志过滤字段的内部枚举。
type logFilterField string

// storedLogRow 约束可被统一映射为 LogEntry 的存储层日志行。
type storedLogRow interface {
	AILog | SystemLog
}

// storedLogFields 是 system/AI 日志行共有字段的中间形态。
// 泛型映射先抽取到这里，再组装为 dashboard wire DTO。
type storedLogFields struct {
	id         int64
	timestamp  time.Time
	level      string
	logger     string
	message    string
	raw        string
	component  string
	agentID    string
	threadID   string
	traceID    string
	eventType  string
	toolName   string
	durationMS *int32
	extra      json.RawMessage
}

// dashboard 日志过滤字段枚举，必须与 LogFilter 字段保持一一对应。
const (
	logFieldLevel     logFilterField = "level"
	logFieldLogger    logFilterField = "logger"
	logFieldComponent logFilterField = "component"
	logFieldAgentID   logFilterField = "agent_id"
	logFieldThreadID  logFilterField = "thread_id"
	logFieldEventType logFilterField = "event_type"
	logFieldToolName  logFilterField = "tool_name"
)

// logFilterFields 是需要对 exact field 执行匹配的字段顺序。
var logFilterFields = []logFilterField{
	logFieldLevel,
	logFieldLogger,
	logFieldComponent,
	logFieldAgentID,
	logFieldThreadID,
	logFieldEventType,
	logFieldToolName,
}

// safeList 执行可选 reader 查询。
// reader 未启用时返回空切片；查询返回 nil 时也规整为空切片以保持 JSON wire 兼容。
func safeList[T any](enabled bool, query func() ([]T, error)) ([]T, error) {
	if !enabled {
		return []T{}, nil
	}
	items, err := query()
	if items == nil {
		items = []T{}
	}
	return items, err
}

// ToFilter 将 system/AI 统一日志 RPC 参数转换为 dashboard LogFilter。
// camelCase 和 snake_case 参数都兼容，避免旧前端字段名失效。
func (p logsParams) ToFilter(source string) LogFilter {
	return LogFilter{
		Source:    strings.TrimSpace(source),
		Keyword:   strings.TrimSpace(p.Keyword),
		Level:     strings.TrimSpace(p.Level),
		Logger:    strings.TrimSpace(p.Logger),
		Component: strings.TrimSpace(p.Component),
		AgentID:   util.FirstNonEmpty(p.AgentID, p.AgentIDSnake),
		ThreadID:  util.FirstNonEmpty(p.ThreadID, p.ThreadIDSnake),
		EventType: util.FirstNonEmpty(p.EventType, p.EventTypeSnake),
		ToolName:  util.FirstNonEmpty(p.ToolName, p.ToolNameSnake),
		Limit:     p.Limit,
	}
}

// ToFilter 将 audit log RPC 参数转换为 dashboard 查询条件。
func (p auditLogsParams) ToFilter() AuditLogFilter {
	return AuditLogFilter{
		EventType: strings.TrimSpace(p.EventType),
		Action:    strings.TrimSpace(p.Action),
		Actor:     strings.TrimSpace(p.Actor),
		Keyword:   strings.TrimSpace(p.Keyword),
		Limit:     int32(p.Limit),
	}
}

// ToFilter 将 bus log RPC 参数转换为 dashboard 查询条件。
func (p busLogsParams) ToFilter() BusLogFilter {
	return BusLogFilter{
		Category: strings.TrimSpace(p.Category),
		Severity: strings.TrimSpace(p.Severity),
		Keyword:  strings.TrimSpace(p.Keyword),
		Limit:    int32(p.Limit),
	}
}

// ToFilter 将 dashboard DAG 列表参数转换为 contract 过滤条件。
func (p dagsParams) ToFilter() contract.ListDAGsFilter {
	return contract.ListDAGsFilter{
		Status:  strings.TrimSpace(p.Status),
		Keyword: strings.TrimSpace(p.Keyword),
		Limit:   p.Limit,
	}
}

// logFilterValue 读取 LogFilter 中指定字段的过滤值。
func logFilterValue(filter LogFilter, field logFilterField) string {
	switch field {
	case logFieldLevel:
		return filter.Level
	case logFieldLogger:
		return filter.Logger
	case logFieldComponent:
		return filter.Component
	case logFieldAgentID:
		return filter.AgentID
	case logFieldThreadID:
		return filter.ThreadID
	case logFieldEventType:
		return filter.EventType
	case logFieldToolName:
		return filter.ToolName
	default:
		return ""
	}
}

// logEntryValue 读取 LogEntry 中指定字段的实际值。
func logEntryValue(entry LogEntry, field logFilterField) string {
	switch field {
	case logFieldLevel:
		return entry.Level
	case logFieldLogger:
		return entry.Logger
	case logFieldComponent:
		return entry.Component
	case logFieldAgentID:
		return entry.AgentID
	case logFieldThreadID:
		return entry.ThreadID
	case logFieldEventType:
		return entry.EventType
	case logFieldToolName:
		return entry.ToolName
	default:
		return ""
	}
}

// newSystemLogListFilter 将统一 LogFilter 投影为 system log 查询参数。
func newSystemLogListFilter(filter LogFilter) SystemLogFilter {
	return SystemLogFilter{
		Level:     strings.TrimSpace(filter.Level),
		Logger:    strings.TrimSpace(filter.Logger),
		Component: strings.TrimSpace(filter.Component),
		AgentID:   strings.TrimSpace(filter.AgentID),
		ThreadID:  strings.TrimSpace(filter.ThreadID),
		EventType: strings.TrimSpace(filter.EventType),
		ToolName:  strings.TrimSpace(filter.ToolName),
		Keyword:   strings.TrimSpace(filter.Keyword),
		Limit:     int32(filter.Limit),
	}
}

// appendMappedLogs 将存储层日志映射为 LogEntry 并应用 dashboard 侧二次过滤。
func appendMappedLogs[T any](
	dst []LogEntry,
	rows []T,
	filter LogFilter,
	mapper func(T) LogEntry,
) []LogEntry {
	for _, row := range rows {
		entry := mapper(row)
		if matchesLogFilter(entry, filter) {
			dst = append(dst, entry)
		}
	}
	return dst
}

// mapLogEntry 将 system/AI 存储行转换为统一 dashboard LogEntry。
func mapLogEntry[T storedLogRow](row T, source string) LogEntry {
	fields := readStoredLogFields(row)
	return LogEntry{
		Source:     source,
		ID:         fields.id,
		Timestamp:  fields.timestamp,
		Level:      fields.level,
		Logger:     fields.logger,
		Message:    fields.message,
		Raw:        fields.raw,
		Component:  fields.component,
		AgentID:    fields.agentID,
		ThreadID:   fields.threadID,
		TraceID:    fields.traceID,
		EventType:  fields.eventType,
		ToolName:   fields.toolName,
		DurationMs: fields.durationMS,
		Extra:      fields.extra,
	}
}

// readStoredLogFields 抽取 system/AI 日志行共有字段。
// 未知类型返回零值，调用方的泛型约束正常情况下不会走到该分支。
func readStoredLogFields[T storedLogRow](row T) storedLogFields {
	switch value := any(row).(type) {
	case SystemLog:
		return storedLogFields{
			id:         value.ID,
			timestamp:  value.Ts,
			level:      value.Level,
			logger:     value.Logger,
			message:    value.Message,
			raw:        value.Raw,
			component:  value.Component,
			agentID:    value.AgentID,
			threadID:   value.ThreadID,
			traceID:    value.TraceID,
			eventType:  value.EventType,
			toolName:   value.ToolName,
			durationMS: value.DurationMs,
			extra:      value.Extra,
		}
	case AILog:
		return storedLogFields{
			id:         value.ID,
			timestamp:  value.Ts,
			level:      value.Level,
			logger:     value.Logger,
			message:    value.Message,
			raw:        value.Raw,
			component:  value.Component,
			agentID:    value.AgentID,
			threadID:   value.ThreadID,
			traceID:    value.TraceID,
			eventType:  value.EventType,
			toolName:   value.ToolName,
			durationMS: value.DurationMs,
			extra:      value.Extra,
		}
	default:
		return storedLogFields{}
	}
}
