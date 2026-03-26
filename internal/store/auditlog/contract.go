package auditlog

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	List(ctx context.Context, filter ListFilter) ([]AuditEvent, error)
	Insert(ctx context.Context, params InsertParams) error
}

type ListFilter struct {
	EventType string
	Action    string
	Actor     string
	Keyword   string
	Limit     int32
}

type InsertParams struct {
	EventType string
	Action    string
	Result    string
	Actor     string
	Target    string
	Detail    string
	Level     string
	Extra     json.RawMessage
}

type AuditEvent struct {
	ID        int64           `json:"id"`
	Ts        time.Time       `json:"ts"`
	EventType string          `json:"event_type"`
	Action    string          `json:"action"`
	Result    string          `json:"result"`
	Actor     string          `json:"actor"`
	Target    string          `json:"target"`
	Detail    string          `json:"detail"`
	Level     string          `json:"level"`
	Extra     json.RawMessage `json:"extra"`
}
