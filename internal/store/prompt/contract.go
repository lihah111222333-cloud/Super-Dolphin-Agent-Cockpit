package prompt

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type Store interface {
	List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error)
}

type ListFilter struct {
	AgentKey string
	Keyword  string
	Limit    int32
}

type PromptTemplate = sqlc.ListPromptTemplatesRow
