package dashboard

import (
	"context"
	"strings"

	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

func matchesLogFilter(entry LogEntry, filter LogFilter) bool {
	return matchesLogSource(entry.Source, filter.Source) &&
		matchesLogFields(entry, filter) &&
		matchKeyword(filter.Keyword, entry)
}

func matchesLogSource(entrySource, filterSource string) bool {
	source := strings.TrimSpace(filterSource)
	if source == "" || strings.EqualFold(source, logSourceAll) {
		return true
	}
	return strings.EqualFold(source, entrySource) ||
		strings.EqualFold(source, entrySource+"log")
}

func matchesLogFields(entry LogEntry, filter LogFilter) bool {
	for _, field := range logFilterFields {
		if !matchField(logFilterValue(filter, field), logEntryValue(entry, field)) {
			return false
		}
	}
	return true
}

func matchField(expected, actual string) bool {
	want := strings.TrimSpace(expected)
	return want == "" || strings.EqualFold(want, strings.TrimSpace(actual))
}

func matchKeyword(keyword string, entry LogEntry) bool {
	needle := strings.TrimSpace(keyword)
	if needle == "" {
		return true
	}
	fields := []string{
		entry.Message,
		entry.Raw,
		entry.Logger,
		entry.Component,
		entry.AgentID,
		entry.ThreadID,
		entry.TraceID,
		entry.EventType,
		entry.ToolName,
		string(entry.Extra),
	}
	for _, field := range fields {
		if containsFold(field, needle) {
			return true
		}
	}
	return false
}

func containsFold(value, needle string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(needle))
}

// GetAuditLogs 读取auditlogs。
func (s *service) GetAuditLogs(ctx context.Context, filter auditlogstore.ListFilter) ([]auditlogstore.AuditEvent, error) {
	return safeList(s.auditLogs != nil, func() ([]auditlogstore.AuditEvent, error) {
		filter.EventType = strings.TrimSpace(filter.EventType)
		filter.Action = strings.TrimSpace(filter.Action)
		filter.Actor = strings.TrimSpace(filter.Actor)
		filter.Keyword = strings.TrimSpace(filter.Keyword)
		filter.Limit = int32(util.ClampLimit(int(filter.Limit), 1, maxLogLimit, defaultLogLimit))
		return s.auditLogs.List(ctx, filter)
	})
}

// GetBusLogs 读取buslogs。
func (s *service) GetBusLogs(ctx context.Context, filter buslogstore.ListFilter) ([]buslogstore.BusExceptionLog, error) {
	return safeList(s.busLogs != nil, func() ([]buslogstore.BusExceptionLog, error) {
		filter.Category = strings.TrimSpace(filter.Category)
		filter.Severity = strings.TrimSpace(filter.Severity)
		filter.Keyword = strings.TrimSpace(filter.Keyword)
		filter.Limit = int32(util.ClampLimit(int(filter.Limit), 1, maxLogLimit, defaultLogLimit))
		return s.busLogs.List(ctx, filter)
	})
}
