package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skillmodule "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	systemlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
	tasktracestore "github.com/anthropic-ai/super-agent-v3/internal/store/tasktrace"
)

type responseField struct {
	key   string
	value any
}

type logFilterField string

type pageDescriptor struct {
	loaders []dashboardPageLoader
}

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

func wrapResponse(key string, value any) map[string]any {
	return map[string]any{strings.TrimSpace(key): value}
}

func wrapResponses(fields ...responseField) map[string]any {
	out := make(map[string]any, len(fields))
	for _, field := range fields {
		key := strings.TrimSpace(field.key)
		if key == "" {
			continue
		}
		out[key] = field.value
	}
	return out
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

func (p logsParams) ToFilter(source string) LogFilter {
	filter := LogFilter{
		Source:  strings.TrimSpace(source),
		Keyword: strings.TrimSpace(p.Keyword),
		Limit:   p.Limit,
	}
	for _, field := range logFilterFields {
		setLogFilterField(&filter, field, p.fieldValue(field))
	}
	return filter
}

func (p logsParams) fieldValue(field logFilterField) string {
	switch field {
	case logFieldLevel:
		return p.Level
	case logFieldLogger:
		return p.Logger
	case logFieldComponent:
		return p.Component
	case logFieldAgentID:
		return firstNonEmpty(p.AgentID, p.AgentIDSnake)
	case logFieldThreadID:
		return firstNonEmpty(p.ThreadID, p.ThreadIDSnake)
	case logFieldEventType:
		return firstNonEmpty(p.EventType, p.EventTypeSnake)
	case logFieldToolName:
		return firstNonEmpty(p.ToolName, p.ToolNameSnake)
	default:
		return ""
	}
}

func (p auditLogsParams) ToFilter() auditlogstore.ListFilter {
	return auditlogstore.ListFilter{
		EventType: strings.TrimSpace(p.EventType),
		Action:    strings.TrimSpace(p.Action),
		Actor:     strings.TrimSpace(p.Actor),
		Keyword:   strings.TrimSpace(p.Keyword),
		Limit:     int32(p.Limit),
	}
}

func (p busLogsParams) ToFilter() buslogstore.ListFilter {
	return buslogstore.ListFilter{
		Category: strings.TrimSpace(p.Category),
		Severity: strings.TrimSpace(p.Severity),
		Keyword:  strings.TrimSpace(p.Keyword),
		Limit:    int32(p.Limit),
	}
}

func (p dagsParams) ToFilter() contract.ListDAGsFilter {
	return contract.ListDAGsFilter{
		Status:  strings.TrimSpace(p.Status),
		Keyword: strings.TrimSpace(p.Keyword),
		Limit:   p.Limit,
	}
}

func setLogFilterField(filter *LogFilter, field logFilterField, value string) {
	if filter == nil {
		return
	}
	value = strings.TrimSpace(value)
	switch field {
	case logFieldLevel:
		filter.Level = value
	case logFieldLogger:
		filter.Logger = value
	case logFieldComponent:
		filter.Component = value
	case logFieldAgentID:
		filter.AgentID = value
	case logFieldThreadID:
		filter.ThreadID = value
	case logFieldEventType:
		filter.EventType = value
	case logFieldToolName:
		filter.ToolName = value
	}
}

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
	out := systemlogstore.ListFilter{
		Keyword: strings.TrimSpace(filter.Keyword),
		Limit:   int32(filter.Limit),
	}
	for _, field := range logFilterFields {
		setSystemLogFilterField(&out, field, logFilterValue(filter, field))
	}
	return out
}

func setSystemLogFilterField(filter *systemlogstore.ListFilter, field logFilterField, value string) {
	if filter == nil {
		return
	}
	value = strings.TrimSpace(value)
	switch field {
	case logFieldLevel:
		filter.Level = value
	case logFieldLogger:
		filter.Logger = value
	case logFieldComponent:
		filter.Component = value
	case logFieldAgentID:
		filter.AgentID = value
	case logFieldThreadID:
		filter.ThreadID = value
	case logFieldEventType:
		filter.EventType = value
	case logFieldToolName:
		filter.ToolName = value
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

func bindPageLoader[T any](
	out *DashboardPage,
	load func(context.Context) ([]T, error),
	assign func(*DashboardPage, []T),
) dashboardPageLoader {
	return func(ctx context.Context) error {
		items, err := load(ctx)
		assign(out, items)
		return err
	}
}

func (s *service) pageRegistry(out *DashboardPage) map[string]pageDescriptor {
	return map[string]pageDescriptor{
		"agents": {
			loaders: []dashboardPageLoader{
				bindPageLoader(out, s.listAgents, assignDashboardAgents),
			},
		},
		"tasks": {
			loaders: []dashboardPageLoader{
				bindPageLoader(out, s.listDashboardTaskTraces, assignDashboardTaskTraces),
			},
		},
		"skills": {
			loaders: []dashboardPageLoader{
				bindPageLoader(out, s.listDashboardSkills, assignDashboardSkills),
			},
		},
		"commands": {
			loaders: []dashboardPageLoader{
				bindPageLoader(out, s.listDashboardCommandCards, assignDashboardCommandCards),
				bindPageLoader(out, s.listDashboardPrompts, assignDashboardPrompts),
			},
		},
		"memory": {
			loaders: []dashboardPageLoader{
				bindPageLoader(out, s.listDashboardMemory, assignDashboardMemory),
			},
		},
	}
}

func assignDashboardAgents(out *DashboardPage, items []AgentOverview) {
	out.Agents = items
}

func assignDashboardTaskTraces(out *DashboardPage, items []tasktracestore.TaskTrace) {
	out.TaskTraces = items
}

func assignDashboardSkills(out *DashboardPage, items []skillmodule.SkillInfo) {
	out.Skills = items
}

func assignDashboardCommandCards(out *DashboardPage, items []commandcardstore.CommandCard) {
	out.CommandCards = items
}

func assignDashboardPrompts(out *DashboardPage, items []promptstore.PromptTemplate) {
	out.Prompts = items
}

func assignDashboardMemory(out *DashboardPage, items []sharedfilestore.SharedFile) {
	out.Memory = items
}
