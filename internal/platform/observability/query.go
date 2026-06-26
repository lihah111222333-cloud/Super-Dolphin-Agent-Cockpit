package observability

import (
	"fmt"
	"strings"
)

// QuerySource 是 trace 查询结果的来源标记，作为诊断字段返回给调用方。
type QuerySource string

// trace 查询来源枚举。
const (
	QuerySourceMemory    QuerySource = "memory"
	QuerySourceJSONLTail QuerySource = "jsonl_tail"
	QuerySourceMixed     QuerySource = "mixed"
)

// Query 是 observability 查询入口的过滤条件。
// 字段保持可比较，Service 用它作为 inflight tail 查询去重键。
type Query struct {
	TraceID     string // 精确匹配 trace_id。
	ThreadID    string // 精确匹配 thread_id。
	Component   string // 匹配 kind、client kind 或 method。
	Status      Status // 状态过滤，空值和 all 表示不过滤。
	Method      string // method 模糊匹配。
	AgentID     string // agent_id 模糊匹配。
	Keyword     string // 跨关键字段与 metadata 搜索。
	Slow        bool   // 只返回慢事件。
	Errors      bool   // 只返回 error/panic 事件。
	Limit       int    // 返回事件上限，0 表示不上限。
	IncludeTail bool   // 是否补查 JSONL tail。
}

// QueryResult 是 observability 查询返回值。
// Tail* 字段不进入 JSON，供后端诊断 tail 文件读取、截断和超时情况。
type QueryResult struct {
	Source           QuerySource
	Events           []TraceEvent
	Truncated        bool
	TailDecodeErrors []TailDecodeError `json:"-"`
	TailFilesScanned int
	TailBytesRead    int
	TailDurationMS   int64
	TailTimedOut     bool
	TailTruncated    bool
}

// matchesQuery 检查事件是否满足所有查询过滤条件。
func matchesQuery(event TraceEvent, query Query) bool {
	return matchesTraceID(event, query) &&
		matchesThreadID(event, query) &&
		matchesComponent(event, query) &&
		matchesStatus(event, query) &&
		matchesMethod(event, query) &&
		matchesAgentID(event, query) &&
		matchesKeyword(event, query) &&
		matchesSlow(event, query) &&
		matchesError(event, query)
}

// matchesTraceID 判断 trace_id 精确过滤是否命中。
func matchesTraceID(event TraceEvent, query Query) bool {
	return query.TraceID == "" || event.TraceID == query.TraceID
}

// matchesThreadID 判断 thread_id 精确过滤是否命中。
func matchesThreadID(event TraceEvent, query Query) bool {
	return query.ThreadID == "" || event.ThreadID == query.ThreadID
}

// matchesSlow 判断慢请求过滤是否命中。
func matchesSlow(event TraceEvent, query Query) bool {
	return !query.Slow || event.Status == StatusSlow
}

// matchesError 判断错误或 panic 状态过滤是否命中。
func matchesError(event TraceEvent, query Query) bool {
	return !query.Errors || event.Status == StatusError || event.Status == StatusPanic
}

// matchesComponent 在 kind、client kind 和 method 上匹配组件过滤。
func matchesComponent(event TraceEvent, query Query) bool {
	component := normalizedQueryText(query.Component)
	if component == "" {
		return true
	}
	return normalizedQueryText(event.Kind) == component ||
		normalizedQueryText(event.ClientKind) == component ||
		normalizedQueryText(event.Method) == component
}

// matchesStatus 判断状态过滤是否命中，空值和 all 表示不过滤。
func matchesStatus(event TraceEvent, query Query) bool {
	status := normalizedQueryText(string(query.Status))
	if status == "" || status == "all" {
		return true
	}
	return normalizedQueryText(string(event.Status)) == status
}

// matchesMethod 判断 method 模糊过滤是否命中。
func matchesMethod(event TraceEvent, query Query) bool {
	return matchesText(event.Method, query.Method)
}

// matchesAgentID 判断 agent_id 模糊过滤是否命中。
func matchesAgentID(event TraceEvent, query Query) bool {
	return matchesText(event.AgentID, query.AgentID)
}

// matchesKeyword 在事件关键字段和 metadata 中执行大小写无关搜索。
func matchesKeyword(event TraceEvent, query Query) bool {
	keyword := normalizedQueryText(query.Keyword)
	if keyword == "" {
		return true
	}
	values := []string{
		event.TraceID, event.SpanID, event.ParentSpanID, event.Kind, event.Phase, event.Method,
		event.ThreadID, event.AgentID, event.TurnID, event.CallID, event.ToolName, event.ClientKind,
		event.ClientRoute, event.Error, event.Code.File, event.Code.Function,
	}
	for _, value := range values {
		if strings.Contains(normalizedQueryText(value), keyword) {
			return true
		}
	}
	for key, value := range event.Metadata {
		if strings.Contains(normalizedQueryText(key), keyword) || strings.Contains(normalizedQueryText(fmt.Sprint(value)), keyword) {
			return true
		}
	}
	return false
}

// matchesText 执行统一的大小写无关包含匹配。
func matchesText(value string, query string) bool {
	query = normalizedQueryText(query)
	return query == "" || strings.Contains(normalizedQueryText(value), query)
}

// normalizedQueryText 标准化查询和候选文本。
func normalizedQueryText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
