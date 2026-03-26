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
	ID        int64           `json:"id"`
	Ts        time.Time       `json:"ts"`
	Category  string          `json:"category"`
	Severity  string          `json:"severity"`
	Source    string          `json:"source"`
	ToolName  string          `json:"tool_name"`
	Message   string          `json:"message"`
	Traceback string          `json:"traceback"`
	Extra     json.RawMessage `json:"extra"`
}
