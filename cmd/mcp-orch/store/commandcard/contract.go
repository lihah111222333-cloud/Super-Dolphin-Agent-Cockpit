package commandcard

import (
	"context"
	"encoding/json"
	"time"

	cc "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
)

// Re-export shared types from internal/store/commandcard.
type CommandCard = cc.CommandCard
type ListFilter = cc.ListFilter

// Store extends the shared Reader with write operations.
type Store interface {
	cc.Reader
	Get(ctx context.Context, cardKey string) (*CommandCard, error)
	Delete(ctx context.Context, cardKey string) error
	InsertVersion(ctx context.Context, version CommandCardVersion) error
	ListVersions(ctx context.Context, cardKey string) ([]CommandCardVersion, error)
	Upsert(ctx context.Context, card CommandCard) (*CommandCard, error)
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
