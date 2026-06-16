package commandcard

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// Get 读取编排。
func (s *store) Get(ctx context.Context, cardKey string) (*CommandCard, error) {
	row, err := s.q.GetCommandCard(ctx, sqlc.GetCommandCardParams{CardKey: cardKey})
	if err != nil {
		return nil, wrapCommandCardError(err, "get", "command_card")
	}
	mapped := fromCard(row)
	return &mapped, nil
}

// Upsert 新增或更新记录。
func (s *store) Upsert(ctx context.Context, card CommandCard) (*CommandCard, error) {
	row, err := s.q.UpsertCommandCard(ctx, sqlc.UpsertCommandCardParams{
		CardKey:         card.CardKey,
		Title:           card.Title,
		Description:     card.Description,
		CommandTemplate: card.CommandTemplate,
		ArgsSchema:      card.ArgsSchema,
		RiskLevel:       card.RiskLevel,
		Enabled:         boolInt64(card.Enabled),
		CreatedBy:       card.CreatedBy,
		UpdatedBy:       card.UpdatedBy,
	})
	if err != nil {
		return nil, wrapCommandCardError(err, "upsert", "command_card")
	}
	mapped := fromUpsertCard(row)
	return &mapped, nil
}

// List 列出编排。
func (s *store) List(ctx context.Context, filter ListFilter) ([]CommandCard, error) {
	rows, err := s.q.ListCommandCards(ctx, sqlc.ListCommandCardsParams{
		Keyword:    filter.Keyword,
		LimitCount: int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapCommandCardError(err, "list", "command_card")
	}
	cards := make([]CommandCard, 0, len(rows))
	for _, row := range rows {
		cards = append(cards, fromListRow(row))
	}
	return cards, nil
}

// Delete 删除编排。
func (s *store) Delete(ctx context.Context, cardKey string) error {
	_, err := s.q.DeleteCommandCard(ctx, sqlc.DeleteCommandCardParams{CardKey: cardKey})
	return wrapCommandCardError(err, "delete", "command_card")
}

// InsertVersion 插入版本。
func (s *store) InsertVersion(ctx context.Context, version CommandCardVersion) error {
	return wrapCommandCardError(s.q.InsertCommandCardVersion(ctx, sqlc.InsertCommandCardVersionParams{
		CardKey:         version.CardKey,
		Title:           version.Title,
		Description:     version.Description,
		CommandTemplate: version.CommandTemplate,
		ArgsSchema:      version.ArgsSchema,
		RiskLevel:       version.RiskLevel,
		Enabled:         boolInt64(version.Enabled),
		CreatedBy:       version.CreatedBy,
		UpdatedBy:       version.UpdatedBy,
		SourceUpdatedAt: sqlc.TimeValuePtr(version.SourceUpdatedAt),
	}), "insert_version", "command_card_version")
}

// ListVersions 列出versions。
func (s *store) ListVersions(ctx context.Context, cardKey string) ([]CommandCardVersion, error) {
	rows, err := s.q.ListCommandCardVersions(ctx, sqlc.ListCommandCardVersionsParams{CardKey: cardKey})
	if err != nil {
		return nil, wrapCommandCardError(err, "list_versions", "command_card_version")
	}
	versions := make([]CommandCardVersion, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, fromVersion(row))
	}
	return versions, nil
}

func fromCard(row sqlc.GetCommandCardRow) CommandCard {
	return CommandCard{
		ID:              row.ID,
		CardKey:         row.CardKey,
		Title:           row.Title,
		Description:     row.Description,
		CommandTemplate: row.CommandTemplate,
		ArgsSchema:      json.RawMessage(row.ArgsSchema),
		RiskLevel:       row.RiskLevel,
		Enabled:         int64Bool(row.Enabled),
		CreatedBy:       row.CreatedBy,
		UpdatedBy:       row.UpdatedBy,
		CreatedAt:       sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:       sqlc.TimeValue(row.UpdatedAt),
	}
}

func fromListRow(row sqlc.ListCommandCardsRow) CommandCard {
	return CommandCard{
		ID:              row.ID,
		CardKey:         row.CardKey,
		Title:           row.Title,
		Description:     row.Description,
		CommandTemplate: row.CommandTemplate,
		ArgsSchema:      json.RawMessage(row.ArgsSchema),
		RiskLevel:       row.RiskLevel,
		Enabled:         int64Bool(row.Enabled),
		CreatedBy:       row.CreatedBy,
		UpdatedBy:       row.UpdatedBy,
		CreatedAt:       sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:       sqlc.TimeValue(row.UpdatedAt),
		LastRunAt:       timePtr(row.LastRunAt),
		RunCount:        row.RunCount,
	}
}

func fromVersion(row sqlc.ListCommandCardVersionsRow) CommandCardVersion {
	return CommandCardVersion{
		ID:              row.ID,
		CardKey:         row.CardKey,
		Title:           row.Title,
		Description:     row.Description,
		CommandTemplate: row.CommandTemplate,
		ArgsSchema:      json.RawMessage(row.ArgsSchema),
		RiskLevel:       row.RiskLevel,
		Enabled:         int64Bool(row.Enabled),
		CreatedBy:       row.CreatedBy,
		UpdatedBy:       row.UpdatedBy,
		SourceUpdatedAt: sqlc.TimePtr(row.SourceUpdatedAt),
		CreatedAt:       sqlc.TimeValue(row.CreatedAt),
		ArchivedAt:      sqlc.TimeValue(row.ArchivedAt),
	}
}

func timePtr(value any) *time.Time {
	switch ts := value.(type) {
	case nil:
		return nil
	case int64:
		return sqlc.TimePtr(&ts)
	case *int64:
		return sqlc.TimePtr(ts)
	case time.Time:
		return &ts
	case *time.Time:
		return ts
	default:
		return nil
	}
}

func fromUpsertCard(row sqlc.UpsertCommandCardRow) CommandCard {
	return CommandCard{
		ID:              row.ID,
		CardKey:         row.CardKey,
		Title:           row.Title,
		Description:     row.Description,
		CommandTemplate: row.CommandTemplate,
		ArgsSchema:      json.RawMessage(row.ArgsSchema),
		RiskLevel:       row.RiskLevel,
		Enabled:         int64Bool(row.Enabled),
		CreatedBy:       row.CreatedBy,
		UpdatedBy:       row.UpdatedBy,
		CreatedAt:       sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:       sqlc.TimeValue(row.UpdatedAt),
	}
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func int64Bool(value int64) bool {
	return value != 0
}

func wrapCommandCardError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
