package buslog

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	List(ctx context.Context, filter ListFilter) ([]BusExceptionLog, error)
}

type ListFilter struct {
	Category string
	Severity string
	Keyword  string
	Limit    int32
}

type BusExceptionLog struct {
	ID        int64
	Ts        time.Time
	Category  string
	Severity  string
	Source    string
	ToolName  string
	Message   string
	Traceback string
	Extra     json.RawMessage
}
