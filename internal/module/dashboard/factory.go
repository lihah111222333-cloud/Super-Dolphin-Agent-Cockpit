package dashboard

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	systemlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

type logFilterField string

type storedLogRow interface {
	ailogstore.AILog | systemlogstore.SystemLog
}

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

const (
	logFieldLevel     logFilterField = "level"
	logFieldLogger    logFilterField = "logger"
	logFieldComponent logFilterField = "component"
	logFieldAgentID   logFilterField = "agent_id"
	logFieldThreadID  logFilterField = "thread_id"
	logFieldEventType logFilterField = "event_type"
	logFieldToolName  logFilterField = "tool_name"
)

var logFilterFields = []logFilterField{
	logFieldLevel,
	logFieldLogger,
	logFieldComponent,
	logFieldAgentID,
	logFieldThreadID,
	logFieldEventType,
	logFieldToolName,
}

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

// ToFilter 把dashboard处理为过滤条件。
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

// ToFilter 把dashboard处理为过滤条件。
func (p auditLogsParams) ToFilter() auditlogstore.ListFilter {
	return auditlogstore.ListFilter{
		EventType: strings.TrimSpace(p.EventType),
		Action:    strings.TrimSpace(p.Action),
		Actor:     strings.TrimSpace(p.Actor),
		Keyword:   strings.TrimSpace(p.Keyword),
		Limit:     int32(p.Limit),
	}
}

// ToFilter 把dashboard处理为过滤条件。
func (p busLogsParams) ToFilter() buslogstore.ListFilter {
	return buslogstore.ListFilter{
		Category: strings.TrimSpace(p.Category),
		Severity: strings.TrimSpace(p.Severity),
		Keyword:  strings.TrimSpace(p.Keyword),
		Limit:    int32(p.Limit),
	}
}

// ToFilter 把dashboard处理为过滤条件。
func (p dagsParams) ToFilter() contract.ListDAGsFilter {
	return contract.ListDAGsFilter{
		Status:  strings.TrimSpace(p.Status),
		Keyword: strings.TrimSpace(p.Keyword),
		Limit:   p.Limit,
	}
}

// logFilterValue 处理日志过滤条件值。
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

// logEntryValue 处理日志条目值。
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

func newSystemLogListFilter(filter LogFilter) systemlogstore.ListFilter {
	return systemlogstore.ListFilter{
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

// readStoredLogFields 读取stored日志字段。
func readStoredLogFields[T storedLogRow](row T) storedLogFields {
	switch value := any(row).(type) {
	case systemlogstore.SystemLog:
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
	case ailogstore.AILog:
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
