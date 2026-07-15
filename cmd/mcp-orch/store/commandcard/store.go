package commandcard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

// store 用 sqlc 查询实现 command card Store 接口。
type store struct {
	db sqlc.DBTX
	q  *sqlc.Queries
}

// NewStore 创建 command card 存储。
func NewStore(db *sql.DB) Store { return &store{db: db, q: sqlc.New(db)} }

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
	var mapped CommandCard
	err := sqlctx.WithImmediateTx(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		rows, err := txq.UpdateCommandCard(ctx, updateCommandCardParams(card))
		if err != nil {
			return err
		}
		if rows == 0 {
			rows, err = txq.InsertCommandCard(ctx, insertCommandCardParams(card))
			if err != nil {
				return err
			}
		}
		if rows != 1 {
			return fmt.Errorf("upsert command card affected %d rows, want 1", rows)
		}
		row, err := txq.GetCommandCard(ctx, sqlc.GetCommandCardParams{CardKey: card.CardKey})
		if err != nil {
			return err
		}
		mapped = fromCard(row)
		return nil
	})
	if err != nil {
		return nil, wrapCommandCardError(err, "upsert", "command_card")
	}
	return &mapped, nil
}

// List 按关键词和上限列出 command card。
func (s *store) List(ctx context.Context, filter ListFilter) ([]CommandCard, error) {
	keyword := filter.Keyword
	if keyword != "" {
		keyword = "%" + keyword + "%"
	}
	rows, err := s.q.ListCommandCards(ctx, sqlc.ListCommandCardsParams{
		Keyword:    keyword,
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

func insertCommandCardParams(card CommandCard) sqlc.InsertCommandCardParams {
	return sqlc.InsertCommandCardParams{
		CardKey: card.CardKey, Title: card.Title, Description: card.Description,
		CommandTemplate: card.CommandTemplate, ArgsSchema: card.ArgsSchema,
		RiskLevel: card.RiskLevel, Enabled: boolInt64(card.Enabled),
		CreatedBy: card.CreatedBy, UpdatedBy: card.UpdatedBy,
	}
}

func updateCommandCardParams(card CommandCard) sqlc.UpdateCommandCardParams {
	return sqlc.UpdateCommandCardParams{
		CardKey: card.CardKey, Title: card.Title, Description: card.Description,
		CommandTemplate: card.CommandTemplate, ArgsSchema: card.ArgsSchema,
		RiskLevel: card.RiskLevel, Enabled: boolInt64(card.Enabled), UpdatedBy: card.UpdatedBy,
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
