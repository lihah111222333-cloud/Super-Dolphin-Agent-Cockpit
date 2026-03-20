package topologyapproval

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	Create(ctx context.Context, approval TopologyApproval) (*TopologyApproval, error)
	Approve(ctx context.Context, reviewer, id string) (int64, error)
	Reject(ctx context.Context, reviewer, id string) (int64, error)
	ListPending(ctx context.Context) ([]TopologyApproval, error)
}

type TopologyApproval struct {
	ID                   string
	Status               string
	RequestedBy          string
	Reason               string
	CreatedAt            time.Time
	ExpireAt             time.Time
	ReviewedAt           *time.Time
	Reviewer             string
	ReviewNote           string
	ArchHash             string
	ProposedArchitecture json.RawMessage
}
