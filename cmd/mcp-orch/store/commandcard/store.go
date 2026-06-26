package commandcard

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// store 用 sqlc 查询实现 command card Store 接口。
type store struct {
	q *sqlc.Queries
}

// NewStore 创建 command card 存储。
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// Get 按 card key 读取单张 command card。
func (s *store) Get(ctx context.Context, cardKey string) (*CommandCard, error) {
	row, err := s.q.GetCommandCard(ctx, sqlc.GetCommandCardParams{CardKey: cardKey})
	if err != nil {
		return nil, wrapCommandCardError(err, "get", "command_card")
	}
	mapped := fromCard(row)
	return &mapped, nil
}

// Upsert 新增或更新 command card 当前版本。
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

// List 按关键词和上限列出 command card。
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

// Delete 按 card key 删除 command card。
func (s *store) Delete(ctx context.Context, cardKey string) error {
	_, err := s.q.DeleteCommandCard(ctx, sqlc.DeleteCommandCardParams{CardKey: cardKey})
	return wrapCommandCardError(err, "delete", "command_card")
}

// InsertVersion 插入 command card 历史版本快照。
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

// ListVersions 列出 command card 的历史版本。
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

// fromCard 将详情查询行映射为业务 DTO。
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

// fromListRow 将列表查询行映射为业务 DTO，并保留运行统计字段。
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

// fromVersion 将历史版本查询行映射为业务 DTO。
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

// timePtr 兼容 SQLite int64 时间戳和 time.Time 两类 sqlc 返回值。
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

// fromUpsertCard 将 upsert 返回行映射为业务 DTO。
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

// boolInt64 将 bool 转为 SQLite 使用的 0/1。
func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// int64Bool 将 SQLite 0/1 转为 bool。
func int64Bool(value int64) bool {
	return value != 0
}

// wrapCommandCardError 统一 command card store 错误域。
func wrapCommandCardError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
