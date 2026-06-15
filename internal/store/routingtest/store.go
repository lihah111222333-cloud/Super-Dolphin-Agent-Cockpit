// Package routingtest is the read-only store layer for the
// prompt_routing_tests table (migration 0041). Operators author rows
// manually; router/runTests iterates them and asserts the live RuleRouter
// still maps each input to the expected prompt_key.
package routingtest

import (
	"context"
	"time"

	db "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sqlc "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// Reader is the only interface consumers need. Write/CRUD is intentionally
// left for operators to do via SQL or a future admin UI — keeping the module
// read-only avoids the "CI accidentally mutates production tests" footgun.
type Reader interface {
	ListEnabled(ctx context.Context) ([]RoutingTest, error)
}

type RoutingTest struct {
	ID                int64     `json:"id"`
	Input             string    `json:"input"`
	ExpectedPromptKey string    `json:"expected_prompt_key"`
	Note              string    `json:"note,omitempty"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type store struct {
	q *sqlc.Queries
}

// NewStore returns the sqlc-backed Reader. Pass a *sqlc.Queries backed by
// database/sql; returns a Reader so downstream code can swap in fakes for tests.
func NewStore(q *sqlc.Queries) Reader {
	return &store{q: q}
}

func (s *store) ListEnabled(ctx context.Context) ([]RoutingTest, error) {
	rows, err := s.q.ListEnabledPromptRoutingTests(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RoutingTest, len(rows))
	for i, row := range rows {
		out[i] = RoutingTest{
			ID:                row.ID,
			Input:             row.Input,
			ExpectedPromptKey: row.ExpectedPromptKey,
			Note:              row.Note,
			Enabled:           row.Enabled != 0,
			CreatedAt:         db.TimeFromMillis(row.CreatedAt),
			UpdatedAt:         db.TimeFromMillis(row.UpdatedAt),
		}
	}
	return out, nil
}
