package commandcard

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

type store struct {
	q *sqlc.Queries
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// Get 读取编排。
func (s *store) Get(ctx context.Context, cardKey string) (*CommandCard, error) {
	row, err := s.q.GetCommandCard(ctx, cardKey)
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
		Column5:         card.ArgsSchema,
		RiskLevel:       card.RiskLevel,
		Enabled:         card.Enabled,
		CreatedBy:       card.CreatedBy,
		UpdatedBy:       card.UpdatedBy,
	})
	if err != nil {
		return nil, wrapCommandCardError(err, "upsert", "command_card")
	}
	mapped := fromCard(row)
	return &mapped, nil
}

// List 列出编排。
func (s *store) List(ctx context.Context, filter ListFilter) ([]CommandCard, error) {
	rows, err := s.q.ListCommandCards(ctx, sqlc.ListCommandCardsParams{Column1: filter.Keyword, Limit: filter.Limit})
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
	_, err := s.q.DeleteCommandCard(ctx, cardKey)
	return wrapCommandCardError(err, "delete", "command_card")
}

// InsertVersion 插入版本。
func (s *store) InsertVersion(ctx context.Context, version CommandCardVersion) error {
	return wrapCommandCardError(s.q.InsertCommandCardVersion(ctx, sqlc.InsertCommandCardVersionParams{
		CardKey:         version.CardKey,
		Title:           version.Title,
		Description:     version.Description,
		CommandTemplate: version.CommandTemplate,
		Column5:         version.ArgsSchema,
		RiskLevel:       version.RiskLevel,
		Enabled:         version.Enabled,
		CreatedBy:       version.CreatedBy,
		UpdatedBy:       version.UpdatedBy,
		SourceUpdatedAt: sqlc.TimeValuePtr(version.SourceUpdatedAt),
	}), "insert_version", "command_card_version")
}

// ListVersions 列出versions。
func (s *store) ListVersions(ctx context.Context, cardKey string) ([]CommandCardVersion, error) {
	rows, err := s.q.ListCommandCardVersions(ctx, cardKey)
	if err != nil {
		return nil, wrapCommandCardError(err, "list_versions", "command_card_version")
	}
	versions := make([]CommandCardVersion, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, fromVersion(row))
	}
	return versions, nil
}

func fromCard(row sqlc.CommandCard) CommandCard {
	return CommandCard{
		ID:              row.ID,
		CardKey:         row.CardKey,
		Title:           row.Title,
		Description:     row.Description,
		CommandTemplate: row.CommandTemplate,
		ArgsSchema:      json.RawMessage(row.ArgsSchema),
		RiskLevel:       row.RiskLevel,
		Enabled:         row.Enabled,
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
		Enabled:         row.Enabled,
		CreatedBy:       row.CreatedBy,
		UpdatedBy:       row.UpdatedBy,
		CreatedAt:       sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:       sqlc.TimeValue(row.UpdatedAt),
		LastRunAt:       timePtr(row.LastRunAt),
		RunCount:        row.RunCount,
	}
}

func fromVersion(row sqlc.CommandCardVersion) CommandCardVersion {
	return CommandCardVersion{
		ID:              row.ID,
		CardKey:         row.CardKey,
		Title:           row.Title,
		Description:     row.Description,
		CommandTemplate: row.CommandTemplate,
		ArgsSchema:      json.RawMessage(row.ArgsSchema),
		RiskLevel:       row.RiskLevel,
		Enabled:         row.Enabled,
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
	case time.Time:
		return &ts
	case *time.Time:
		return ts
	case sqlc.Timestamptz:
		return sqlc.TimePtr(ts)
	default:
		return nil
	}
}

func wrapCommandCardError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
