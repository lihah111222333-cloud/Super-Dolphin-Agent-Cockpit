package dashboard

import (
	"context"
	"sort"
	"strings"

	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
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
	fields := [][2]string{
		{filter.Level, entry.Level},
		{filter.Logger, entry.Logger},
		{filter.Component, entry.Component},
		{filter.AgentID, entry.AgentID},
		{filter.ThreadID, entry.ThreadID},
		{filter.EventType, entry.EventType},
		{filter.ToolName, entry.ToolName},
	}
	for _, field := range fields {
		if !matchField(field[0], field[1]) {
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

func sortLogEntries(entries []LogEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].Timestamp.Equal(entries[j].Timestamp) {
			return entries[i].Timestamp.After(entries[j].Timestamp)
		}
		if entries[i].Source != entries[j].Source {
			return entries[i].Source < entries[j].Source
		}
		return entries[i].ID > entries[j].ID
	})
}

func (s *service) GetAuditLogs(ctx context.Context, filter auditlogstore.ListFilter) ([]auditlogstore.AuditEvent, error) {
	if s.auditLogs == nil {
		return []auditlogstore.AuditEvent{}, nil
	}
	filter.EventType = strings.TrimSpace(filter.EventType)
	filter.Action = strings.TrimSpace(filter.Action)
	filter.Actor = strings.TrimSpace(filter.Actor)
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.Limit = int32(clampLogLimit(int(filter.Limit)))
	return s.auditLogs.List(ctx, filter)
}

func (s *service) GetBusLogs(ctx context.Context, filter buslogstore.ListFilter) ([]buslogstore.BusExceptionLog, error) {
	if s.busLogs == nil {
		return []buslogstore.BusExceptionLog{}, nil
	}
	filter.Category = strings.TrimSpace(filter.Category)
	filter.Severity = strings.TrimSpace(filter.Severity)
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.Limit = int32(clampLogLimit(int(filter.Limit)))
	return s.busLogs.List(ctx, filter)
}
