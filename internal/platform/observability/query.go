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

func matchesQuery(event TraceEvent, query Query) bool {
	return matchesTraceID(event, query) && matchesThreadID(event, query) && matchesSlow(event, query) && matchesError(event, query)
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
