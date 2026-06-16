package commandcard

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier is the narrow subset of sqlc.Queries that this store actually uses.
// Accepting an interface in tests keeps the dependency on the generated
// sqlc types loose; NewStore still takes the concrete *sqlc.Queries for
// production wiring.
type querier interface {
	ListCommandCards(ctx context.Context, arg sqlc.ListCommandCardsParams) ([]sqlc.ListCommandCardsRow, error)
}

type store struct {
	q querier
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Reader { return &store{q: q} }

// List 列出commandcard存储。
func (s *store) List(ctx context.Context, filter ListFilter) ([]CommandCard, error) {
	rows, err := s.q.ListCommandCards(ctx, sqlc.ListCommandCardsParams{
		Keyword:    filter.Keyword,
		LimitCount: int64(filter.Limit),
	})
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "list", "command_card")
	}
	cards := make([]CommandCard, 0, len(rows))
	for _, row := range rows {
		cards = append(cards, fromSQLCRow(row))
	}
	return cards, nil
}

func fromSQLCRow(row sqlc.ListCommandCardsRow) CommandCard {
	return CommandCard{
		ID:              row.ID,
		CardKey:         row.CardKey,
		Title:           row.Title,
		Description:     row.Description,
		CommandTemplate: row.CommandTemplate,
		ArgsSchema:      json.RawMessage(row.ArgsSchema),
		RiskLevel:       row.RiskLevel,
		Enabled:         row.Enabled != 0,
		CreatedBy:       row.CreatedBy,
		UpdatedBy:       row.UpdatedBy,
		CreatedAt:       platformdb.TimeFromMillis(row.CreatedAt),
		UpdatedAt:       platformdb.TimeFromMillis(row.UpdatedAt),
		LastRunAt:       timePtr(row.LastRunAt),
		RunCount:        row.RunCount,
	}
}

func timePtr(value any) *time.Time {
	switch ts := value.(type) {
	case nil:
		return nil
	case time.Time:
		return &ts
	case *time.Time:
		return ts
	case int64:
		t := platformdb.TimeFromMillis(ts)
		return &t
	default:
		return nil
	}
}
