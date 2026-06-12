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

var (
	httpMethodURLRe = regexp.MustCompile(`(?i)(GET|POST|PUT|DELETE|PATCH|HEAD)\s+(https?://\S+)`)
	httpStatusRe    = regexp.MustCompile(`(?i)HTTP/[0-9]\.[0-9]\s+([0-9]{3})\s*(\S*)`)
	httpEndpointRe  = regexp.MustCompile(`^https?://[^/]+`)
	modelRe         = regexp.MustCompile(`(?i)model[=:]\s*([^\s,;"\]]+)`)
)

type querier interface {
	CountAILogsByStatus(ctx context.Context) ([]string, error)
	ListAILogSystemLogs(ctx context.Context, arg sqlc.ListAILogSystemLogsParams) ([]sqlc.ListAILogSystemLogsRow, error)
	ListAILogsByCategory(ctx context.Context, arg sqlc.ListAILogsByCategoryParams) ([]sqlc.ListAILogsByCategoryRow, error)
	ListRecentAILogs(ctx context.Context, arg sqlc.ListRecentAILogsParams) ([]sqlc.ListRecentAILogsRow, error)
}

type store struct {
	q querier
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

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

func durationMsPtr(v *int64) *int32 {
	if v == nil {
		return nil
	}
	x := int32(*v)
	return &x
}

// extractHTTPMethodURL pulls the HTTP method and URL out of a log message.
func extractHTTPMethodURL(message string) (method string, url string) {
	if m := httpMethodURLRe.FindStringSubmatch(message); m != nil {
		return strings.ToUpper(m[1]), m[2]
	}
	return "", ""
}

// extractEndpoint strips the scheme and host from a URL.
func extractEndpoint(url string) string {
	if url == "" {
		return ""
	}
	return httpEndpointRe.ReplaceAllString(url, "")
}

// extractHTTPStatus pulls the HTTP status code and status text from a message.
func extractHTTPStatus(message string) (status string, statusText string) {
	if m := httpStatusRe.FindStringSubmatch(message); m != nil {
		return m[1], m[2]
	}
	return "", ""
}

// extractModel pulls a model identifier out of a log message.
func extractModel(message string) string {
	if m := modelRe.FindStringSubmatch(message); m != nil {
		return m[1]
	}
	return ""
}

func containsAny(message string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

// deriveCategory classifies an AI log row from its message text.
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

// deriveHTTPFields computes method/url/endpoint/status/status_text/model from a
// raw log message using precompiled expressions.
func deriveHTTPFields(message string) (method, url, endpoint, status, statusText, model string) {
	method, url = extractHTTPMethodURL(message)
	endpoint = extractEndpoint(url)
	status, statusText = extractHTTPStatus(message)
	model = extractModel(message)
	return method, url, endpoint, status, statusText, model
}

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

func wrapAILogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "ai_log")
}
