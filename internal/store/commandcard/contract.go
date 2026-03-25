package commandcard

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type Store interface {
	List(ctx context.Context, filter ListFilter) ([]CommandCard, error)
}

type ListFilter struct {
	Keyword string
	Limit   int32
}

type CommandCard = sqlc.ListCommandCardsRow
