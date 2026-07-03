package dashboard

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

// matchesLogFilter 判断统一日志条目是否满足 source、字段和关键词过滤。
func matchesLogFilter(entry LogEntry, filter LogFilter) bool {
	return matchesLogSource(entry.Source, filter.Source) &&
		matchesLogFields(entry, filter) &&
		matchKeyword(filter.Keyword, entry)
}

// matchesLogSource 兼容 source 与 sourcelog 两种前端传值。
func matchesLogSource(entrySource, filterSource string) bool {
	source := strings.TrimSpace(filterSource)
	if source == "" || strings.EqualFold(source, logSourceAll) {
		return true
	}
	return strings.EqualFold(source, entrySource) ||
		strings.EqualFold(source, entrySource+"log")
}

// matchesLogFields 对 LogFilter 中的精确字段逐项匹配，空字段表示不过滤。
func matchesLogFields(entry LogEntry, filter LogFilter) bool {
	for _, field := range logFilterFields {
		if !matchField(logFilterValue(filter, field), logEntryValue(entry, field)) {
			return false
		}
	}
	return true
}

// matchField 执行大小写不敏感的精确字段匹配。
func matchField(expected, actual string) bool {
	want := strings.TrimSpace(expected)
	return want == "" || strings.EqualFold(want, strings.TrimSpace(actual))
}

// matchKeyword 在日志文本、身份和 extra JSON 中做大小写不敏感关键词匹配。
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
		entry.SpanID,
		entry.ParentSpanID,
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

// containsFold 执行大小写不敏感的子串匹配。
func containsFold(value, needle string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(needle))
}

// sortLogEntries 按时间倒序稳定排序，并用 source/id 打破同时间戳并列。
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

// GetAuditLogs 读取审计日志并规整过滤条件。
// store 缺失时返回空切片，limit 始终受 dashboard 日志上限约束。
func (s *service) GetAuditLogs(ctx context.Context, filter AuditLogFilter) ([]AuditEvent, error) {
	return safeList(s.auditLogs != nil, func() ([]AuditEvent, error) {
		filter.EventType = strings.TrimSpace(filter.EventType)
		filter.Action = strings.TrimSpace(filter.Action)
		filter.Actor = strings.TrimSpace(filter.Actor)
		filter.Keyword = strings.TrimSpace(filter.Keyword)
		filter.Limit = int32(util.ClampLimit(int(filter.Limit), 1, maxLogLimit, defaultLogLimit))
		return s.auditLogs.List(ctx, filter)
	})
}

// GetBusLogs 读取 bus 异常日志并规整过滤条件。
// store 缺失时返回空切片，避免可选 bus 日志能力阻断 dashboard。
func (s *service) GetBusLogs(ctx context.Context, filter BusLogFilter) ([]BusExceptionLog, error) {
	return safeList(s.busLogs != nil, func() ([]BusExceptionLog, error) {
		filter.Category = strings.TrimSpace(filter.Category)
		filter.Severity = strings.TrimSpace(filter.Severity)
		filter.Keyword = strings.TrimSpace(filter.Keyword)
		filter.Limit = int32(util.ClampLimit(int(filter.Limit), 1, maxLogLimit, defaultLogLimit))
		return s.busLogs.List(ctx, filter)
	})
}

// GetBusLog 读取单条 bus 异常日志详情，详情接口才返回 traceback/extra 重字段。
func (s *service) GetBusLog(ctx context.Context, id int64) (BusExceptionLog, error) {
	if s.busLogs == nil {
		return BusExceptionLog{}, errDashboardStoreMissing("bus_logs")
	}
	if id <= 0 {
		return BusExceptionLog{}, errDashboardInvalidID("bus_logs", id)
	}
	return s.busLogs.Get(ctx, id)
}

func errDashboardStoreMissing(name string) error {
	return fmt.Errorf("dashboard: %s store is not configured", strings.TrimSpace(name))
}

func errDashboardInvalidID(name string, id int64) error {
	return fmt.Errorf("dashboard: %s id must be positive, got %d", strings.TrimSpace(name), id)
}
