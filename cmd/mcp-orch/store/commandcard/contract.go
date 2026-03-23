package commandcard

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	Get(ctx context.Context, cardKey string) (*CommandCard, error)
	Upsert(ctx context.Context, card CommandCard) (*CommandCard, error)
	List(ctx context.Context, filter ListFilter) ([]CommandCard, error)
}

type ListFilter struct {
	Keyword string
	Limit   int32
}

type CommandCard struct {
	ID              int64
	CardKey         string
	Title           string
	Description     string
	CommandTemplate string
	ArgsSchema      json.RawMessage
	RiskLevel       string
	Enabled         bool
	CreatedBy       string
	UpdatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastRunAt       *time.Time
	RunCount        int64
}

