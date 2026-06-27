// Package observability 把可观测性 RPC 处理器装配到 Fx 依赖树。
package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/creachadair/jrpc2/handler"

	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const (
	maxFrontendIngestEvents = 100  // 单次前端 ingest 最多接受的事件数。
	defaultQueryLimit       = 100  // trace/thread 查询的默认返回条数。
	defaultListLimit        = 50   // recent/slow/error 列表的默认返回条数。
	maxQueryLimit           = 500  // 所有查询的硬上限。
	recentRawLimitMultiple  = 50   // recent 原始查询量相对显示量的倍数，用于过滤后仍有足够候选。
	maxRecentRawQueryLimit  = 5000 // recent 原始查询的绝对上限，防止单次扫描过大。
	summaryEventLimit       = 10   // slowest/error 摘要最多返回的事件数。
)

// statusParams 是 observability/status 接口的空入参。
type statusParams struct{}

// traceQueryParams 是按 traceID 查询 trace 事件的 JSON-RPC 入参。
// TraceID/TraceIDSnake 兼容前端驼峰和历史下划线字段，IncludeTail 同理。
type traceQueryParams struct {
	TraceID          string `json:"traceId"`
	TraceIDSnake     string `json:"trace_id"`
	Limit            int    `json:"limit"`
	IncludeTail      *bool  `json:"includeTail"`
	IncludeTailSnake *bool  `json:"include_tail"`
}

// threadRecentParams 是按 threadID 查询最近事件的 JSON-RPC 入参。
// 字段同时保留 camelCase 和 snake_case，避免旧前端请求失配。
type threadRecentParams struct {
	ThreadID         string `json:"threadId"`
	ThreadIDSnake    string `json:"thread_id"`
	Limit            int    `json:"limit"`
	IncludeTail      *bool  `json:"includeTail"`
	IncludeTailSnake *bool  `json:"include_tail"`
}

// eventListParams 是 slow/error 列表查询的 JSON-RPC 入参。
type eventListParams struct {
	Limit     int    `json:"limit"`
	Component string `json:"component"`
}

// recentListParams 是 recent 列表查询的 JSON-RPC 入参。
// 同时支持 trace/thread/agent 的 camelCase 与 snake_case 字段，便于新旧 UI 共用同一 handler。
type recentListParams struct {
	TraceID          string `json:"traceId"`
	TraceIDSnake     string `json:"trace_id"`
	ThreadID         string `json:"threadId"`
	ThreadIDSnake    string `json:"thread_id"`
	AgentID          string `json:"agentId"`
	AgentIDSnake     string `json:"agent_id"`
	Limit            int    `json:"limit"`
	Component        string `json:"component"`
	Status           string `json:"status"`
	Method           string `json:"method"`
	Keyword          string `json:"keyword"`
	IncludeTail      *bool  `json:"includeTail"`
	IncludeTailSnake *bool  `json:"include_tail"`
}

// queryResponse 是 trace/thread/recent/slow/error 查询的统一 JSON 响应结构。
// 摘要字段由服务端计算，前端不需要再扫描完整 events 才能展示慢事件和错误概览。
type queryResponse struct {
	Source           platformobs.QuerySource  `json:"source"`
	Events           []platformobs.TraceEvent `json:"events"`
	SlowestEvents    []platformobs.TraceEvent `json:"slowest_events"`
	Errors           []platformobs.TraceEvent `json:"errors"`
	TotalDurationMS  int64                    `json:"total_duration_ms"`
	Truncated        bool                     `json:"truncated"`
	Degraded         bool                     `json:"degraded"`
	TailError        string                   `json:"tailError,omitempty"`
	TailTimedOut     bool                     `json:"tailTimedOut"`
	TailFilesScanned int                      `json:"tailFilesScanned"`
}

// frontendIngestParams 是前端事件批量上报接口的 JSON-RPC 入参。
type frontendIngestParams struct {
	Events []json.RawMessage `json:"events"`
}

// frontendIngestResponse 是前端事件批量上报的 JSON 响应。
// Dropped 包含服务禁用或单批超过上限导致未记录的事件数。
type frontendIngestResponse struct {
	Enabled        bool   `json:"enabled"`
	Recorded       int    `json:"recorded"`
	Dropped        int    `json:"dropped"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

// recentRowSelection 记录 recent 列表最终选中的 traceID 和无 trace 行号。
// 有 traceID 的事件按 trace 聚合，无 traceID 的前端事件只能用原始行号保留。
type recentRowSelection struct {
	traceIDs     map[string]struct{}
	eventIndexes map[int]struct{}
}

// frontendTraceEvent 是前端上报的单条 trace 事件结构，仅允许白名单字段。
type frontendTraceEvent struct {
	Timestamp    time.Time            `json:"ts,omitzero"`
	TraceID      string               `json:"trace_id,omitempty"`
	SpanID       string               `json:"span_id,omitempty"`
	ParentSpanID string               `json:"parent_span_id,omitempty"`
	Kind         string               `json:"kind,omitempty"`
	Phase        string               `json:"phase,omitempty"`
	Method       string               `json:"method,omitempty"`
	ThreadID     string               `json:"thread_id,omitempty"`
	AgentID      string               `json:"agent_id,omitempty"`
	TurnID       string               `json:"turn_id,omitempty"`
	CallID       string               `json:"call_id,omitempty"`
	ClientKind   string               `json:"client_kind,omitempty"`
	ClientRoute  string               `json:"client_route,omitempty"`
	DurationMS   int64                `json:"duration_ms,omitempty"`
	Status       platformobs.Status   `json:"status"`
	Error        string               `json:"error,omitempty"`
	Metadata     platformobs.Metadata `json:"metadata,omitempty"`
}

// NewHandlers 注册 observability 对外 JSON-RPC 接口。
func NewHandlers(svc *platformobs.Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"observability/trace/get":       platformrpc.StrictHandler(traceGetHandler(svc)),
		"observability/thread/recent":   platformrpc.StrictHandler(threadRecentHandler(svc)),
		"observability/recent/list":     platformrpc.StrictHandler(recentListHandler(svc)),
		"observability/slow/list":       platformrpc.StrictHandler(slowListHandler(svc)),
		"observability/error/list":      platformrpc.StrictHandler(errorListHandler(svc)),
		"observability/status":          platformrpc.StrictHandler(statusHandler(svc)),
		"observability/frontend/ingest": platformrpc.StrictHandler(frontendIngestHandler(svc)),
	}}
}

// statusHandler 返回 observability 服务的当前状态（是否启用、禁用原因等）。
func statusHandler(svc *platformobs.Service) func(context.Context, statusParams) (platformobs.ServiceStatus, error) {
	return func(context.Context, statusParams) (platformobs.ServiceStatus, error) {
		if svc == nil {
			return platformobs.ServiceStatus{}, fmt.Errorf("observability service is not wired")
		}
		return svc.Status(), nil
	}
}

// traceGetHandler 按 traceID 查询 trace 下的所有事件。
func traceGetHandler(svc *platformobs.Service) func(context.Context, traceQueryParams) (queryResponse, error) {
	return func(ctx context.Context, p traceQueryParams) (queryResponse, error) {
		if svc == nil {
			return queryResponse{}, fmt.Errorf("observability service is not wired")
		}
		traceID := firstTrimmed(p.TraceID, p.TraceIDSnake)
		if traceID == "" {
			return queryResponse{}, fmt.Errorf("traceId is required")
		}
		query := platformobs.Query{TraceID: traceID, Limit: normalizeLimit(p.Limit, defaultQueryLimit), IncludeTail: includeTail(p.IncludeTail, p.IncludeTailSnake)}
		return responseFromQueryResult(svc.Query(ctx, query), ""), nil
	}
}

// threadRecentHandler 按 threadID 查询该 thread 下的最近事件。
func threadRecentHandler(svc *platformobs.Service) func(context.Context, threadRecentParams) (queryResponse, error) {
	return func(ctx context.Context, p threadRecentParams) (queryResponse, error) {
		if svc == nil {
			return queryResponse{}, fmt.Errorf("observability service is not wired")
		}
		threadID := firstTrimmed(p.ThreadID, p.ThreadIDSnake)
		if threadID == "" {
			return queryResponse{}, fmt.Errorf("threadId is required")
		}
		query := platformobs.Query{ThreadID: threadID, Limit: normalizeLimit(p.Limit, defaultQueryLimit), IncludeTail: includeTail(p.IncludeTail, p.IncludeTailSnake)}
		return responseFromQueryResult(svc.Query(ctx, query), ""), nil
	}
}

// recentListHandler 按多维过滤条件（trace/thread/agent/component/status/keyword）查询最近事件列表。
func recentListHandler(svc *platformobs.Service) func(context.Context, recentListParams) (queryResponse, error) {
	return func(ctx context.Context, p recentListParams) (queryResponse, error) {
		if svc == nil {
			return queryResponse{}, fmt.Errorf("observability service is not wired")
		}
		limit := normalizeLimit(p.Limit, defaultListLimit)
		query := platformobs.Query{
			Component:   p.Component,
			Status:      platformobs.Status(p.Status),
			Method:      p.Method,
			AgentID:     firstTrimmed(p.AgentID, p.AgentIDSnake),
			Keyword:     p.Keyword,
			Limit:       recentRawQueryLimit(limit),
			IncludeTail: includeTail(p.IncludeTail, p.IncludeTailSnake),
		}
		if traceID := firstTrimmed(p.TraceID, p.TraceIDSnake); traceID != "" {
			query.TraceID = traceID
		} else if threadID := firstTrimmed(p.ThreadID, p.ThreadIDSnake); threadID != "" {
			query.ThreadID = threadID
		}
		return responseFromRecentResult(svc.Query(ctx, query), p, limit), nil
	}
}

// slowListHandler 查询最慢的事件列表。
func slowListHandler(svc *platformobs.Service) func(context.Context, eventListParams) (queryResponse, error) {
	return func(ctx context.Context, p eventListParams) (queryResponse, error) {
		if svc == nil {
			return queryResponse{}, fmt.Errorf("observability service is not wired")
		}
		query := platformobs.Query{Slow: true, Limit: normalizeLimit(p.Limit, defaultListLimit), IncludeTail: true}
		return responseFromQueryResult(svc.Query(ctx, query), p.Component), nil
	}
}

// errorListHandler 查询发生错误或 panic 的事件列表。
func errorListHandler(svc *platformobs.Service) func(context.Context, eventListParams) (queryResponse, error) {
	return func(ctx context.Context, p eventListParams) (queryResponse, error) {
		if svc == nil {
			return queryResponse{}, fmt.Errorf("observability service is not wired")
		}
		query := platformobs.Query{Errors: true, Limit: normalizeLimit(p.Limit, defaultListLimit), IncludeTail: true}
		return responseFromQueryResult(svc.Query(ctx, query), p.Component), nil
	}
}

// frontendIngestHandler 接收前端批量 trace 事件。
// 服务禁用时返回 dropped 计数；单批超过上限时截断，非法字段直接报错而不是静默丢弃。
func frontendIngestHandler(svc *platformobs.Service) func(context.Context, frontendIngestParams) (frontendIngestResponse, error) {
	return func(ctx context.Context, p frontendIngestParams) (frontendIngestResponse, error) {
		if svc == nil {
			return frontendIngestResponse{}, fmt.Errorf("observability service is not wired")
		}
		status := svc.Status()
		if !status.Enabled {
			return frontendIngestResponse{Enabled: false, Dropped: len(p.Events), DisabledReason: status.DisabledReason}, nil
		}
		limit := len(p.Events)
		dropped := 0
		if limit > maxFrontendIngestEvents {
			dropped = limit - maxFrontendIngestEvents
			limit = maxFrontendIngestEvents
		}
		recorded := 0
		for i := 0; i < limit; i++ {
			event, err := frontendEventFromRaw(p.Events[i])
			if err != nil {
				return frontendIngestResponse{}, fmt.Errorf("frontend trace event %d: %w", i, err)
			}
			if err := svc.Record(ctx, event); err != nil {
				return frontendIngestResponse{}, err
			}
			recorded++
		}
		return frontendIngestResponse{Enabled: true, Recorded: recorded, Dropped: dropped}, nil
	}
}

// responseFromRecentResult 对 recent 查询结果做过滤、排序和摘要汇总。
func responseFromRecentResult(result platformobs.QueryResult, params recentListParams, limit int) queryResponse {
	events := filterRecentEvents(result.Events, params)
	events = latestTraceEventsFirst(events, limit)
	response := queryResponse{
		Source:          result.Source,
		Events:          events,
		SlowestEvents:   slowestEvents(events, summaryEventLimit),
		Errors:          errorEvents(events, summaryEventLimit),
		TotalDurationMS: totalDurationMS(events),
		Truncated:       result.Truncated,
	}
	response.applyTailDiagnostics(result)
	return response
}

// responseFromQueryResult 对 trace/slow/error 查询结果做组件过滤和摘要汇总。
func responseFromQueryResult(result platformobs.QueryResult, component string) queryResponse {
	events := filterEventsByComponent(result.Events, component)
	response := queryResponse{
		Source:          result.Source,
		Events:          events,
		SlowestEvents:   slowestEvents(events, summaryEventLimit),
		Errors:          errorEvents(events, summaryEventLimit),
		TotalDurationMS: totalDurationMS(events),
		Truncated:       result.Truncated,
	}
	response.applyTailDiagnostics(result)
	return response
}

// applyTailDiagnostics 把落盘 tail 读取状态加入响应，避免 UI 把查询失败误判为空数据。
func (r *queryResponse) applyTailDiagnostics(result platformobs.QueryResult) {
	r.TailFilesScanned = result.TailFilesScanned
	r.TailTimedOut = result.TailTimedOut
	if len(result.TailDecodeErrors) == 0 && !result.TailTimedOut {
		return
	}
	r.Degraded = true
	if len(result.TailDecodeErrors) > 0 {
		r.TailError = strings.TrimSpace(result.TailDecodeErrors[0].Error)
	}
}

// filterRecentEvents 过滤事件列表，只保留符合 recent 查询参数的事件。
func filterRecentEvents(events []platformobs.TraceEvent, params recentListParams) []platformobs.TraceEvent {
	out := make([]platformobs.TraceEvent, 0, len(events))
	for _, event := range events {
		if recentEventMatches(event, params) {
			out = append(out, event)
		}
	}
	return out
}

// recentEventMatches 判断事件是否符合 recent 查询条件。
// 默认隐藏框架内部噪声，只有用户显式搜索内部关键词时才放行。
func recentEventMatches(event platformobs.TraceEvent, params recentListParams) bool {
	if internalRecentNoise(event) && !explicitInternalRecentSearch(params) {
		return false
	}
	return eventMatchesComponent(event, params.Component) &&
		eventMatchesStatus(event, params.Status) &&
		eventMatchesText(event.Method, params.Method) &&
		eventMatchesText(event.TraceID, firstTrimmed(params.TraceID, params.TraceIDSnake)) &&
		eventMatchesText(event.ThreadID, firstTrimmed(params.ThreadID, params.ThreadIDSnake)) &&
		eventMatchesText(event.AgentID, firstTrimmed(params.AgentID, params.AgentIDSnake)) &&
		eventMatchesKeyword(event, params.Keyword)
}

// internalRecentNoise 识别 recent 列表默认应隐藏的内部生命周期事件。
func internalRecentNoise(event platformobs.TraceEvent) bool {
	values := []string{event.Kind, event.Phase, event.Method, event.ClientKind}
	for _, value := range values {
		text := strings.ToLower(strings.TrimSpace(value))
		switch {
		case text == "":
			continue
		case strings.Contains(text, "lifecycle"):
			return true
		case strings.HasPrefix(text, "bus.event."):
			return true
		case strings.HasPrefix(text, "uistate."):
			return true
		case strings.HasPrefix(text, "turn.ready_"):
			return true
		case strings.Contains(text, "observability/"):
			return true
		}
	}
	return false
}

// explicitInternalRecentSearch 判断用户是否明确在找内部事件。
// 只有 component/method/keyword 命中内部关键词时才覆盖默认噪声过滤。
func explicitInternalRecentSearch(params recentListParams) bool {
	values := []string{params.Component, params.Method, params.Keyword}
	for _, value := range values {
		text := strings.ToLower(strings.TrimSpace(value))
		if text == "" {
			continue
		}
		if strings.Contains(text, "lifecycle") ||
			strings.Contains(text, "bus.event") ||
			strings.Contains(text, "uistate") ||
			strings.Contains(text, "patch") ||
			strings.Contains(text, "turn.ready") ||
			strings.Contains(text, "observability") {
			return true
		}
	}
	return false
}

// filterEventsByComponent 按组件名过滤事件，空组件名返回全部。
func filterEventsByComponent(events []platformobs.TraceEvent, component string) []platformobs.TraceEvent {
	component = strings.ToLower(strings.TrimSpace(component))
	if component == "" {
		return events
	}
	out := make([]platformobs.TraceEvent, 0, len(events))
	for _, event := range events {
		if eventMatchesComponent(event, component) {
			out = append(out, event)
		}
	}
	return out
}

// eventMatchesComponent 判断事件是否匹配指定组件名，空组件名始终匹配。
func eventMatchesComponent(event platformobs.TraceEvent, component string) bool {
	component = strings.ToLower(strings.TrimSpace(component))
	if component == "" {
		return true
	}
	return strings.ToLower(strings.TrimSpace(event.Kind)) == component || strings.ToLower(strings.TrimSpace(event.ClientKind)) == component || strings.ToLower(strings.TrimSpace(event.Method)) == component
}

// eventMatchesStatus 判断事件状态是否匹配，空或 "all" 始终匹配。
func eventMatchesStatus(event platformobs.TraceEvent, status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" || status == "all" {
		return true
	}
	return strings.ToLower(strings.TrimSpace(string(event.Status))) == status
}

// eventMatchesText 判断字段值是否包含查询子串，空查询始终匹配。
func eventMatchesText(value, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(value)), query)
}

// eventMatchesKeyword 在事件核心字段和 metadata 中执行关键词匹配。
func eventMatchesKeyword(event platformobs.TraceEvent, keyword string) bool {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return true
	}
	values := []string{
		event.TraceID, event.SpanID, event.ParentSpanID, event.Kind, event.Phase, event.Method,
		event.ThreadID, event.AgentID, event.TurnID, event.CallID, event.ToolName, event.ClientKind,
		event.ClientRoute, event.Error, event.Code.File, event.Code.Function,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(strings.TrimSpace(value)), keyword) {
			return true
		}
	}
	for key, value := range event.Metadata {
		if strings.Contains(strings.ToLower(strings.TrimSpace(key)), keyword) || strings.Contains(strings.ToLower(fmt.Sprint(value)), keyword) {
			return true
		}
	}
	return false
}

// slowestEvents 返回耗时最长的前 limit 个事件，按 DurationMS 降序。
func slowestEvents(events []platformobs.TraceEvent, limit int) []platformobs.TraceEvent {
	out := append([]platformobs.TraceEvent(nil), events...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].DurationMS > out[j].DurationMS })
	return limitEvents(out, limit)
}

// errorEvents 从事件列表中提取状态为 error 或 panic 的事件，最多返回 limit 条。
func errorEvents(events []platformobs.TraceEvent, limit int) []platformobs.TraceEvent {
	out := make([]platformobs.TraceEvent, 0, len(events))
	for _, event := range events {
		if event.Status == platformobs.StatusError || event.Status == platformobs.StatusPanic {
			out = append(out, event)
		}
	}
	return limitEvents(out, limit)
}

// limitEvents 截断事件列表到 limit 条，limit <= 0 时不截断。
func limitEvents(events []platformobs.TraceEvent, limit int) []platformobs.TraceEvent {
	if limit > 0 && len(events) > limit {
		return events[:limit]
	}
	return events
}

// latestEventsFirst 按时间戳降序排列事件，截取最新的 limit 条后再排序。
func latestEventsFirst(events []platformobs.TraceEvent, limit int) []platformobs.TraceEvent {
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	out := append([]platformobs.TraceEvent(nil), events...)
	sort.SliceStable(out, func(i, j int) bool {
		left, right := out[i].Timestamp, out[j].Timestamp
		if left.IsZero() || right.IsZero() {
			return i > j
		}
		return left.After(right)
	})
	return out
}

// latestTraceEventsFirst 按时间降序排列，并按 traceID 去重后取最近 limit 组 trace。
func latestTraceEventsFirst(events []platformobs.TraceEvent, limit int) []platformobs.TraceEvent {
	ordered := latestEventsFirst(events, 0)
	if limit <= 0 {
		return ordered
	}
	selected := selectRecentRows(ordered, limit)
	out := make([]platformobs.TraceEvent, 0, len(ordered))
	for index, event := range ordered {
		if recentRowSelected(index, event, selected) {
			out = append(out, event)
		}
	}
	return out
}

// selectRecentRows 选择 recent 列表展示行。
// 有 traceID 的事件按 trace 去重；没有 traceID 的事件按原始行号保留，避免无 trace 前端事件互相覆盖。
func selectRecentRows(events []platformobs.TraceEvent, limit int) recentRowSelection {
	selected := recentRowSelection{traceIDs: make(map[string]struct{}, limit), eventIndexes: make(map[int]struct{}, limit)}
	rows := 0
	for index, event := range events {
		traceID := strings.TrimSpace(event.TraceID)
		if traceID == "" {
			selected.eventIndexes[index] = struct{}{}
		} else {
			if _, ok := selected.traceIDs[traceID]; ok {
				continue
			}
			selected.traceIDs[traceID] = struct{}{}
		}
		rows++
		if rows >= limit {
			break
		}
	}
	return selected
}

// recentRowSelected 判断某行事件是否在选中集合中。
func recentRowSelected(index int, event platformobs.TraceEvent, selected recentRowSelection) bool {
	traceID := strings.TrimSpace(event.TraceID)
	if traceID == "" {
		_, ok := selected.eventIndexes[index]
		return ok
	}
	_, ok := selected.traceIDs[traceID]
	return ok
}

// recentRawQueryLimit 计算 recent 接口原始查询量，确保过滤后有足够的候选行。
func recentRawQueryLimit(displayLimit int) int {
	if displayLimit <= 0 {
		displayLimit = defaultListLimit
	}
	rawLimit := displayLimit * recentRawLimitMultiple
	if rawLimit < maxQueryLimit {
		rawLimit = maxQueryLimit
	}
	if rawLimit > maxRecentRawQueryLimit {
		return maxRecentRawQueryLimit
	}
	return rawLimit
}

// totalDurationMS 计算响应摘要的总耗时。
// 有时间戳时使用最早开始到最晚结束的跨度；缺少时间戳时退回单事件最大耗时。
func totalDurationMS(events []platformobs.TraceEvent) int64 {
	var minStart time.Time
	var maxEnd time.Time
	var maxDuration int64
	for _, event := range events {
		if event.DurationMS > maxDuration {
			maxDuration = event.DurationMS
		}
		if event.Timestamp.IsZero() {
			continue
		}
		end := event.Timestamp.Add(time.Duration(event.DurationMS) * time.Millisecond)
		if minStart.IsZero() || event.Timestamp.Before(minStart) {
			minStart = event.Timestamp
		}
		if maxEnd.IsZero() || end.After(maxEnd) {
			maxEnd = end
		}
	}
	if !minStart.IsZero() && maxEnd.After(minStart) {
		return maxEnd.Sub(minStart).Milliseconds()
	}
	return maxDuration
}

// firstTrimmed 返回第一个非空 TrimSpace 后的字符串。
func firstTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// includeTail 返回第一个非 nil 的 bool 指针值，默认返回 true。
func includeTail(values ...*bool) bool {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return true
}

// normalizeLimit 将 limit 收敛到合法范围，超出 maxQueryLimit 时截断。
func normalizeLimit(limit int, defaultLimit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxQueryLimit {
		return maxQueryLimit
	}
	return limit
}

// frontendEventFromRaw 校验并转换前端上报的原始事件。
// 只接受白名单字段，防止前端把任意 JSON 注入 trace 存储。
func frontendEventFromRaw(raw json.RawMessage) (platformobs.TraceEvent, error) {
	if len(raw) == 0 {
		return platformobs.TraceEvent{}, fmt.Errorf("event must be an object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return platformobs.TraceEvent{}, err
	}
	for key := range fields {
		if !allowedFrontendTraceField(key) {
			return platformobs.TraceEvent{}, fmt.Errorf("field %q is not allowed", key)
		}
	}
	var in frontendTraceEvent
	if err := json.Unmarshal(raw, &in); err != nil {
		return platformobs.TraceEvent{}, err
	}
	if in.Status == "" {
		in.Status = platformobs.StatusOK
	}
	if in.Timestamp.IsZero() {
		in.Timestamp = time.Now().UTC()
	}
	return platformobs.TraceEvent{
		SchemaVersion: platformobs.SchemaVersion,
		Timestamp:     in.Timestamp,
		TraceID:       in.TraceID,
		SpanID:        in.SpanID,
		ParentSpanID:  in.ParentSpanID,
		Kind:          "frontend",
		Phase:         in.Phase,
		Method:        in.Method,
		ThreadID:      in.ThreadID,
		AgentID:       in.AgentID,
		TurnID:        in.TurnID,
		CallID:        in.CallID,
		ClientKind:    in.ClientKind,
		ClientRoute:   in.ClientRoute,
		DurationMS:    in.DurationMS,
		Status:        in.Status,
		Error:         in.Error,
		Metadata:      in.Metadata,
	}, nil
}

// allowedFrontendTraceField 判断字段名是否在前端事件的白名单中。
func allowedFrontendTraceField(key string) bool {
	switch key {
	case "ts", "trace_id", "span_id", "parent_span_id", "kind", "phase", "method", "thread_id", "agent_id", "turn_id", "call_id", "client_kind", "client_route", "duration_ms", "status", "error", "metadata":
		return true
	default:
		return false
	}
}
