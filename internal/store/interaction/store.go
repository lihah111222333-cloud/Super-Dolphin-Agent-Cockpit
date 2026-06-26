package interaction

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier 定义 interaction 存储依赖的 sqlc 最小方法集。
type querier interface {
	CreateInteraction(ctx context.Context, arg sqlc.CreateInteractionParams) (sqlc.AgentInteraction, error)
	GetInteraction(ctx context.Context, arg sqlc.GetInteractionParams) (sqlc.AgentInteraction, error)
	ListInteractions(ctx context.Context, arg sqlc.ListInteractionsParams) ([]sqlc.AgentInteraction, error)
	ReviewInteraction(ctx context.Context, arg sqlc.ReviewInteractionParams) (sqlc.AgentInteraction, error)
}

type store struct {
	q querier
}

// NewStore 创建基于 sqlc 的 agent interaction 存储。
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Create 持久化一条 agent interaction。
// requires_review 在这里转成 SQLite 整数布尔值，payload 原样交给数据库约束校验。
func (s *store) Create(ctx context.Context, interaction Interaction) (*Interaction, error) {
	row, err := s.q.CreateInteraction(ctx, sqlc.CreateInteractionParams{
		ThreadID:       interaction.ThreadID,
		ParentID:       interaction.ParentID,
		Sender:         interaction.Sender,
		Receiver:       interaction.Receiver,
		MsgType:        interaction.MsgType,
		Status:         interaction.Status,
		RequiresReview: boolToInt(interaction.RequiresReview),
		Payload:        interaction.Payload,
	})
	if err != nil {
		return nil, wrapInteractionError(err, "create")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

// Get 按主键读取单条 interaction。
// 底层 not found 和查询错误统一交给 store 错误包装层处理。
func (s *store) Get(ctx context.Context, id int64) (*Interaction, error) {
	row, err := s.q.GetInteraction(ctx, sqlc.GetInteractionParams{ID: id})
	if err != nil {
		return nil, wrapInteractionError(err, "get")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

// List 按线程和关键词列出 interaction。
// keyword 会同时传入多列模糊匹配参数，具体匹配范围由 sqlc 查询定义。
func (s *store) List(ctx context.Context, filter ListFilter) ([]Interaction, error) {
	keyword := filter.Keyword
	rows, err := s.q.ListInteractions(ctx, sqlc.ListInteractionsParams{
		Column1:  filter.ThreadID,
		ThreadID: filter.ThreadID,
		Column3:  keyword,
		Column4:  &keyword,
		Column5:  &keyword,
		Column6:  &keyword,
		Limit:    int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapInteractionError(err, "list")
	}
	interactions := make([]Interaction, 0, len(rows))
	for _, row := range rows {
		interactions = append(interactions, fromSQLC(row))
	}
	return interactions, nil
}

// Review 写入人工评审结果并返回更新后的 interaction。
// SQL 会校验待评审状态，调用方不能把已完成或无需评审的记录当作可重复评审。
func (s *store) Review(ctx context.Context, input ReviewInput) (*Interaction, error) {
	row, err := s.q.ReviewInteraction(ctx, sqlc.ReviewInteractionParams{Status: input.Status, ReviewedBy: input.ReviewedBy, ReviewNote: input.ReviewNote, ID: input.ID})
	if err != nil {
		return nil, wrapInteractionError(err, "review")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func reviewedAtPtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := platformdb.TimeFromMillis(*ms)
	return &t
}

func fromSQLC(row sqlc.AgentInteraction) Interaction {
	return Interaction{
		ID:             row.ID,
		ThreadID:       row.ThreadID,
		ParentID:       row.ParentID,
		Sender:         row.Sender,
		Receiver:       row.Receiver,
		MsgType:        row.MsgType,
		Status:         row.Status,
		RequiresReview: row.RequiresReview != 0,
		ReviewedBy:     row.ReviewedBy,
		ReviewNote:     row.ReviewNote,
		ReviewedAt:     reviewedAtPtr(row.ReviewedAt),
		Payload:        json.RawMessage(row.Payload),
		CreatedAt:      platformdb.TimeFromMillis(row.CreatedAt),
		UpdatedAt:      platformdb.TimeFromMillis(row.UpdatedAt),
	}
}

func wrapInteractionError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "interaction")
}
