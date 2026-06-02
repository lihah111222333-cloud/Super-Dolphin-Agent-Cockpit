package observability

type QuerySource string

const (
	QuerySourceMemory    QuerySource = "memory"
	QuerySourceJSONLTail QuerySource = "jsonl_tail"
	QuerySourceMixed     QuerySource = "mixed"
)

type Query struct {
	TraceID     string
	ThreadID    string
	Slow        bool
	Errors      bool
	Limit       int
	IncludeTail bool
}

type QueryResult struct {
	Source    QuerySource
	Events    []TraceEvent
	Truncated bool
}
