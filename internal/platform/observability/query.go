package observability

import (
	"fmt"
	"strings"
)

type QuerySource string

const (
	QuerySourceMemory    QuerySource = "memory"
	QuerySourceJSONLTail QuerySource = "jsonl_tail"
	QuerySourceMixed     QuerySource = "mixed"
)

type Query struct {
	TraceID     string
	ThreadID    string
	Component   string
	Status      Status
	Method      string
	AgentID     string
	Keyword     string
	Slow        bool
	Errors      bool
	Limit       int
	IncludeTail bool
}

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

// matchesQuery 判断查询是否匹配。
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

func matchesTraceID(event TraceEvent, query Query) bool {
	return query.TraceID == "" || event.TraceID == query.TraceID
}

func matchesThreadID(event TraceEvent, query Query) bool {
	return query.ThreadID == "" || event.ThreadID == query.ThreadID
}

func matchesSlow(event TraceEvent, query Query) bool {
	return !query.Slow || event.Status == StatusSlow
}

func matchesError(event TraceEvent, query Query) bool {
	return !query.Errors || event.Status == StatusError || event.Status == StatusPanic
}

func matchesComponent(event TraceEvent, query Query) bool {
	component := normalizedQueryText(query.Component)
	if component == "" {
		return true
	}
	return normalizedQueryText(event.Kind) == component ||
		normalizedQueryText(event.ClientKind) == component ||
		normalizedQueryText(event.Method) == component
}

func matchesStatus(event TraceEvent, query Query) bool {
	status := normalizedQueryText(string(query.Status))
	if status == "" || status == "all" {
		return true
	}
	return normalizedQueryText(string(event.Status)) == status
}

func matchesMethod(event TraceEvent, query Query) bool {
	return matchesText(event.Method, query.Method)
}

func matchesAgentID(event TraceEvent, query Query) bool {
	return matchesText(event.AgentID, query.AgentID)
}

// matchesKeyword 判断keyword是否匹配。
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

func matchesText(value string, query string) bool {
	query = normalizedQueryText(query)
	return query == "" || strings.Contains(normalizedQueryText(value), query)
}

func normalizedQueryText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
