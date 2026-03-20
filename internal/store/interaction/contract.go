package interaction

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	Create(ctx context.Context, interaction Interaction) (*Interaction, error)
	Get(ctx context.Context, id int64) (*Interaction, error)
	List(ctx context.Context, filter ListFilter) ([]Interaction, error)
	Review(ctx context.Context, input ReviewInput) (*Interaction, error)
}

type ListFilter struct {
	ThreadID string
	Keyword  string
	Limit    int32
}

type ReviewInput struct {
	ID         int64
	Status     string
	ReviewedBy string
	ReviewNote string
}

type Interaction struct {
	ID             int64
	ThreadID       string
	ParentID       *int64
	Sender         string
	Receiver       string
	MsgType        string
	Status         string
	RequiresReview bool
	ReviewedBy     string
	ReviewNote     string
	ReviewedAt     *time.Time
	Payload        json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
