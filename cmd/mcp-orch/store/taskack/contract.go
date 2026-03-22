package taskack

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	Upsert(ctx context.Context, ack TaskAck) (*TaskAck, error)
	List(ctx context.Context, filter ListFilter) ([]TaskAck, error)
}

type ListFilter struct {
	Status     string
	Priority   string
	AssignedTo string
	Keyword    string
	Limit      int32
}

type TaskAck struct {
	ID            int64
	AckKey        string
	Title         string
	Description   string
	AssignedTo    string
	RequestedBy   string
	Priority      string
	Status        string
	Progress      int32
	AckMessage    string
	ResultSummary string
	Metadata      json.RawMessage
	DueAt         *time.Time
	AckedAt       *time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
