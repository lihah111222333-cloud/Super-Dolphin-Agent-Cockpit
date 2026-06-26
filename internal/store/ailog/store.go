package ailog

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// AI 日志解析用正则表达式，复用预编译结果避免列表查询重复编译。
var (
	httpMethodURLRe = regexp.MustCompile(`(?i)(GET|POST|PUT|DELETE|PATCH|HEAD)\s+(https?://\S+)`)
	httpStatusRe    = regexp.MustCompile(`(?i)HTTP/[0-9]\.[0-9]\s+([0-9]{3})\s*(\S*)`)
	httpEndpointRe  = regexp.MustCompile(`^https?://[^/]+`)
	modelRe         = regexp.MustCompile(`(?i)model[=:]\s*([^\s,;"\]]+)`)
)

// querier 是 ailog store 依赖的 sqlc 查询子集，列表和聚合查询共用该窄接口。
type querier interface {
	CountAILogsByStatus(ctx context.Context) ([]string, error)
	ListAILogSystemLogs(ctx context.Context, arg sqlc.ListAILogSystemLogsParams) ([]sqlc.ListAILogSystemLogsRow, error)
	ListAILogsByCategory(ctx context.Context, arg sqlc.ListAILogsByCategoryParams) ([]sqlc.ListAILogsByCategoryRow, error)
	ListRecentAILogs(ctx context.Context, arg sqlc.ListRecentAILogsParams) ([]sqlc.ListRecentAILogsRow, error)
}

// store 实现 AI 日志查询，并在 store 层派生分类、HTTP 字段和模型信息。
type store struct {
	q querier
}

// NewStore 使用生产 sqlc 查询对象创建 ailog Store。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// List 按关键字列出 AI 日志原始记录，并保留 Raw 和 Extra 供排查使用。
func (s *store) List(ctx context.Context, filter ListFilter) ([]AILog, error) {
	rows, err := s.q.ListAILogSystemLogs(ctx, sqlc.ListAILogSystemLogsParams{
		Keyword:        filter.Keyword,
		KeywordPattern: platformdb.LikeContainsFold(filter.Keyword),
		LimitCount:     int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapAILogError(err, "list")
	}
	result := make([]AILog, len(rows))
	for i, row := range rows {
		result[i] = mapAILog(row)
	}
	return result, nil
}

// ListByCategory 按派生分类和关键字读取日志，并在映射后再次校验分类避免 SQL 兼容差异。
func (s *store) ListByCategory(ctx context.Context, category string, keyword string, limit int32) ([]AILog, error) {
	rows, err := s.q.ListAILogsByCategory(ctx, sqlc.ListAILogsByCategoryParams{
		Column1: keyword,
		LOWER:   platformdb.LikeContainsFold(keyword),
		Column3: category,
		Message: category,
		Limit:   int64(limit),
	})
	if err != nil {
		return nil, wrapAILogError(err, "list_by_category")
	}
	result := make([]AILog, 0, len(rows))
	for _, row := range rows {
		item := mapCategoryAILog(row)
		if category == "" || item.Category == category {
			result = append(result, item)
		}
	}
	return result, nil
}

// CountByStatus 按状态统计 AI 日志。
func (s *store) CountByStatus(ctx context.Context) ([]StatusCount, error) {
	messages, err := s.q.CountAILogsByStatus(ctx)
	if err != nil {
		return nil, wrapAILogError(err, "count_by_status")
	}
	counts := make(map[string]int64)
	for _, message := range messages {
		status, _ := extractHTTPStatus(message)
		if status == "" {
			continue
		}
		counts[status]++
	}
	statuses := make([]string, 0, len(counts))
	for status := range counts {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	result := make([]StatusCount, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, StatusCount{Status: status, Count: counts[status]})
	}
	return result, nil
}

// ListRecent 读取最近 AI 日志，并补充分类和 HTTP 派生字段供 UI 快速展示。
func (s *store) ListRecent(ctx context.Context, limit int32) ([]AILog, error) {
	rows, err := s.q.ListRecentAILogs(ctx, sqlc.ListRecentAILogsParams{LimitCount: int64(limit)})
	if err != nil {
		return nil, wrapAILogError(err, "list_recent")
	}
	result := make([]AILog, len(rows))
	for i, row := range rows {
		result[i] = mapRecentAILog(row)
	}
	return result, nil
}

// durationMsPtr 将 sqlc 可空 int64 耗时转换为 UI 使用的 int32 指针。
func durationMsPtr(v *int64) *int32 {
	if v == nil {
		return nil
	}
	x := int32(*v)
	return &x
}

// extractHTTPMethodURL 从日志消息中提取 HTTP 方法和 URL，未匹配时返回空值。
func extractHTTPMethodURL(message string) (method string, url string) {
	if m := httpMethodURLRe.FindStringSubmatch(message); m != nil {
		return strings.ToUpper(m[1]), m[2]
	}
	return "", ""
}

// extractEndpoint 从 URL 中去掉 scheme 和 host，保留接口路径用于聚合。
func extractEndpoint(url string) string {
	if url == "" {
		return ""
	}
	return httpEndpointRe.ReplaceAllString(url, "")
}

// extractHTTPStatus 从日志消息中提取 HTTP 状态码和状态文本，未匹配时返回空值。
func extractHTTPStatus(message string) (status string, statusText string) {
	if m := httpStatusRe.FindStringSubmatch(message); m != nil {
		return m[1], m[2]
	}
	return "", ""
}

// extractModel 从日志消息中提取模型标识，未匹配时返回空值。
func extractModel(message string) string {
	if m := modelRe.FindStringSubmatch(message); m != nil {
		return m[1]
	}
	return ""
}

// containsAny 判断消息是否包含任一候选片段，分类规则共用该辅助函数。
func containsAny(message string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

// deriveCategory 根据日志消息内容推导 AI 日志分类，无法识别时归入通用事件。
func deriveCategory(message string) string {
	lower := strings.ToLower(message)
	if containsAny(lower, "api request", "request to", "http request") {
		return "api_request"
	}
	if containsAny(lower, "api error", "api_error") {
		return "api_error"
	}
	if containsAny(lower, "compat", "fallback", "\u517c\u5bb9") {
		return "compat_fallback"
	}
	if strings.Contains(lower, "runtime") && strings.Contains(lower, "config") {
		return "runtime_config"
	}
	if containsAny(lower, "error", "exception") {
		return "error"
	}
	return "ai_event"
}

// deriveHTTPFields 使用预编译正则从原始日志消息中派生 HTTP 和模型字段。
func deriveHTTPFields(message string) (method, url, endpoint, status, statusText, model string) {
	method, url = extractHTTPMethodURL(message)
	endpoint = extractEndpoint(url)
	status, statusText = extractHTTPStatus(message)
	model = extractModel(message)
	return method, url, endpoint, status, statusText, model
}

// mapAILog 将系统日志查询行转换为基础 AILog DTO，不额外派生分类字段。
func mapAILog(row sqlc.ListAILogSystemLogsRow) AILog {
	return AILog{
		ID:         row.ID,
		Ts:         platformdb.TimeFromMillis(row.Ts),
		Level:      row.Level,
		Logger:     row.Logger,
		Message:    row.Message,
		Raw:        row.Raw,
		Source:     row.Source,
		Component:  row.Component,
		AgentID:    row.AgentID,
		ThreadID:   row.ThreadID,
		TraceID:    row.TraceID,
		EventType:  row.EventType,
		ToolName:   row.ToolName,
		DurationMs: durationMsPtr(row.DurationMs),
		Extra:      json.RawMessage(row.Extra),
	}
}

// mapCategoryAILog 将分类查询行转换为 AILog，并补充分类、HTTP 和模型字段。
func mapCategoryAILog(row sqlc.ListAILogsByCategoryRow) AILog {
	method, url, endpoint, status, statusText, model := deriveHTTPFields(row.Message)
	return AILog{
		ID:         row.ID,
		Ts:         platformdb.TimeFromMillis(row.Ts),
		Level:      row.Level,
		Logger:     row.Logger,
		Message:    row.Message,
		Raw:        row.Raw,
		Source:     row.Source,
		Component:  row.Component,
		AgentID:    row.AgentID,
		ThreadID:   row.ThreadID,
		TraceID:    row.TraceID,
		EventType:  row.EventType,
		ToolName:   row.ToolName,
		DurationMs: durationMsPtr(row.DurationMs),
		Extra:      json.RawMessage(row.Extra),
		Category:   deriveCategory(row.Message),
		Method:     method,
		URL:        url,
		Endpoint:   endpoint,
		Status:     status,
		StatusText: statusText,
		Model:      model,
	}
}

// mapRecentAILog 将最近日志查询行转换为 AILog，并补充 UI 列表需要的派生字段。
func mapRecentAILog(row sqlc.ListRecentAILogsRow) AILog {
	method, url, endpoint, status, statusText, model := deriveHTTPFields(row.Message)
	return AILog{
		ID:         row.ID,
		Ts:         platformdb.TimeFromMillis(row.Ts),
		Level:      row.Level,
		Logger:     row.Logger,
		Message:    row.Message,
		Raw:        row.Raw,
		Source:     row.Source,
		Component:  row.Component,
		AgentID:    row.AgentID,
		ThreadID:   row.ThreadID,
		TraceID:    row.TraceID,
		EventType:  row.EventType,
		ToolName:   row.ToolName,
		DurationMs: durationMsPtr(row.DurationMs),
		Extra:      json.RawMessage(row.Extra),
		Category:   deriveCategory(row.Message),
		Method:     method,
		URL:        url,
		Endpoint:   endpoint,
		Status:     status,
		StatusText: statusText,
		Model:      model,
	}
}

// wrapAILogError 统一包装 AI 日志 store 错误，保留 operation 便于排查。
func wrapAILogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "ai_log")
}
