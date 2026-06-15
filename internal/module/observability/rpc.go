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
	maxFrontendIngestEvents = 100
	defaultQueryLimit       = 100
	defaultListLimit        = 50
	maxQueryLimit           = 500
	recentRawLimitMultiple  = 50
	maxRecentRawQueryLimit  = 5000
	summaryEventLimit       = 10
)

type statusParams struct{}

type traceQueryParams struct {
	TraceID          string `json:"traceId"`
	TraceIDSnake     string `json:"trace_id"`
	Limit            int    `json:"limit"`
	IncludeTail      *bool  `json:"includeTail"`
	IncludeTailSnake *bool  `json:"include_tail"`
}

type threadRecentParams struct {
	ThreadID         string `json:"threadId"`
	ThreadIDSnake    string `json:"thread_id"`
	Limit            int    `json:"limit"`
	IncludeTail      *bool  `json:"includeTail"`
	IncludeTailSnake *bool  `json:"include_tail"`
}

type eventListParams struct {
	Limit     int    `json:"limit"`
	Component string `json:"component"`
}

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

type queryResponse struct {
	Source          platformobs.QuerySource  `json:"source"`
	Events          []platformobs.TraceEvent `json:"events"`
	SlowestEvents   []platformobs.TraceEvent `json:"slowest_events"`
	Errors          []platformobs.TraceEvent `json:"errors"`
	TotalDurationMS int64                    `json:"total_duration_ms"`
	Truncated       bool                     `json:"truncated"`
}

type frontendIngestParams struct {
	Events []json.RawMessage `json:"events"`
}

type frontendIngestResponse struct {
	Enabled        bool   `json:"enabled"`
	Recorded       int    `json:"recorded"`
	Dropped        int    `json:"dropped"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

type recentRowSelection struct {
	traceIDs     map[string]struct{}
	eventIndexes map[int]struct{}
}

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

// NewHandlers 创建处理器。
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

func statusHandler(svc *platformobs.Service) func(context.Context, statusParams) (platformobs.ServiceStatus, error) {
	return func(context.Context, statusParams) (platformobs.ServiceStatus, error) {
		if svc == nil {
			return platformobs.ServiceStatus{}, fmt.Errorf("observability service is not wired")
		}
		return svc.Status(), nil
	}
}

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

func slowListHandler(svc *platformobs.Service) func(context.Context, eventListParams) (queryResponse, error) {
	return func(ctx context.Context, p eventListParams) (queryResponse, error) {
		if svc == nil {
			return queryResponse{}, fmt.Errorf("observability service is not wired")
		}
		query := platformobs.Query{Slow: true, Limit: normalizeLimit(p.Limit, defaultListLimit), IncludeTail: true}
		return responseFromQueryResult(svc.Query(ctx, query), p.Component), nil
	}
}

func errorListHandler(svc *platformobs.Service) func(context.Context, eventListParams) (queryResponse, error) {
	return func(ctx context.Context, p eventListParams) (queryResponse, error) {
		if svc == nil {
			return queryResponse{}, fmt.Errorf("observability service is not wired")
		}
		query := platformobs.Query{Errors: true, Limit: normalizeLimit(p.Limit, defaultListLimit), IncludeTail: true}
		return responseFromQueryResult(svc.Query(ctx, query), p.Component), nil
	}
}

// frontendIngestHandler 处理前端ingest处理器。
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

func responseFromRecentResult(result platformobs.QueryResult, params recentListParams, limit int) queryResponse {
	events := filterRecentEvents(result.Events, params)
	events = latestTraceEventsFirst(events, limit)
	return queryResponse{
		Source:          result.Source,
		Events:          events,
		SlowestEvents:   slowestEvents(events, summaryEventLimit),
		Errors:          errorEvents(events, summaryEventLimit),
		TotalDurationMS: totalDurationMS(events),
		Truncated:       result.Truncated,
	}
}

func responseFromQueryResult(result platformobs.QueryResult, component string) queryResponse {
	events := filterEventsByComponent(result.Events, component)
	return queryResponse{
		Source:          result.Source,
		Events:          events,
		SlowestEvents:   slowestEvents(events, summaryEventLimit),
		Errors:          errorEvents(events, summaryEventLimit),
		TotalDurationMS: totalDurationMS(events),
		Truncated:       result.Truncated,
	}
}

func filterRecentEvents(events []platformobs.TraceEvent, params recentListParams) []platformobs.TraceEvent {
	out := make([]platformobs.TraceEvent, 0, len(events))
	for _, event := range events {
		if recentEventMatches(event, params) {
			out = append(out, event)
		}
	}
	return out
}

// recentEventMatches 判断recent事件是否匹配。
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

// internalRecentNoise 处理internalrecentnoise。
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

// explicitInternalRecentSearch 处理explicitinternalrecentsearch。
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

func eventMatchesComponent(event platformobs.TraceEvent, component string) bool {
	component = strings.ToLower(strings.TrimSpace(component))
	if component == "" {
		return true
	}
	return strings.ToLower(strings.TrimSpace(event.Kind)) == component || strings.ToLower(strings.TrimSpace(event.ClientKind)) == component || strings.ToLower(strings.TrimSpace(event.Method)) == component
}

func eventMatchesStatus(event platformobs.TraceEvent, status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" || status == "all" {
		return true
	}
	return strings.ToLower(strings.TrimSpace(string(event.Status))) == status
}

func eventMatchesText(value, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(value)), query)
}

// eventMatchesKeyword 处理事件matcheskeyword。
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

func slowestEvents(events []platformobs.TraceEvent, limit int) []platformobs.TraceEvent {
	out := append([]platformobs.TraceEvent(nil), events...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].DurationMS > out[j].DurationMS })
	return limitEvents(out, limit)
}

func errorEvents(events []platformobs.TraceEvent, limit int) []platformobs.TraceEvent {
	out := make([]platformobs.TraceEvent, 0, len(events))
	for _, event := range events {
		if event.Status == platformobs.StatusError || event.Status == platformobs.StatusPanic {
			out = append(out, event)
		}
	}
	return limitEvents(out, limit)
}

func limitEvents(events []platformobs.TraceEvent, limit int) []platformobs.TraceEvent {
	if limit > 0 && len(events) > limit {
		return events[:limit]
	}
	return events
}

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

// selectRecentRows 选择recentrows。
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

func recentRowSelected(index int, event platformobs.TraceEvent, selected recentRowSelection) bool {
	traceID := strings.TrimSpace(event.TraceID)
	if traceID == "" {
		_, ok := selected.eventIndexes[index]
		return ok
	}
	_, ok := selected.traceIDs[traceID]
	return ok
}

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

// totalDurationMS 处理totaldurationms。
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

func firstTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func includeTail(values ...*bool) bool {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return true
}

func normalizeLimit(limit int, defaultLimit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxQueryLimit {
		return maxQueryLimit
	}
	return limit
}

// frontendEventFromRaw 从原始处理前端事件。
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

func allowedFrontendTraceField(key string) bool {
	switch key {
	case "ts", "trace_id", "span_id", "parent_span_id", "kind", "phase", "method", "thread_id", "agent_id", "turn_id", "call_id", "client_kind", "client_route", "duration_ms", "status", "error", "metadata":
		return true
	default:
		return false
	}
}
