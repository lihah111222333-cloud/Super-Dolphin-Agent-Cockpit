package commandcard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// querier 是 commandcard store 依赖的 sqlc 查询子集，测试可用窄接口替身覆盖。
type querier interface {
	ListCommandCards(ctx context.Context, arg sqlc.ListCommandCardsParams) ([]sqlc.ListCommandCardsRow, error)
}

// store 实现命令卡片只读查询，生产入口仍由 *sqlc.Queries 构造。
type store struct {
	q querier
}

// NewStore 使用生产 sqlc 查询对象创建 commandcard Reader。
func NewStore(q *sqlc.Queries) Reader { return &store{q: q} }

// List 按关键字读取启用范围内的命令卡片，并保持 ArgsSchema 原始 JSON。
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

// fromSQLCRow 将 sqlc 查询行转换为命令卡片 JSON wire DTO。
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

// timePtr 兼容 SQLite 和测试替身返回的时间类型，未知类型记录告警并按空值处理。
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
		slog.Warn("commandcard: timePtr received unexpected type, returning nil",
			slog.String("value_type", fmt.Sprintf("%T", value)),
		)
		return nil
	}
}
