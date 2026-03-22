package commandcard

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	Get(ctx context.Context, cardKey string) (*CommandCard, error)
	Delete(ctx context.Context, cardKey string) error
	InsertVersion(ctx context.Context, version CommandCardVersion) error
	ListVersions(ctx context.Context, cardKey string) ([]CommandCardVersion, error)
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

type CommandCardVersion struct {
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
	SourceUpdatedAt *time.Time
	CreatedAt       time.Time
	ArchivedAt      time.Time
}

type CommandCardRun struct {
	ID              int64
	CardKey         string
	RequestedBy     string
	Params          json.RawMessage
	RenderedCommand string
	RiskLevel       string
	Status          string
	RequiresReview  bool
	InteractionID   *int64
	Output          string
	Error           string
	ExitCode        *int32
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExecutedAt      *time.Time
}
