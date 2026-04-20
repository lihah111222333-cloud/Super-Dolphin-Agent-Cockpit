package prompt

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier 封装在 p20.1 写能力恢复后所依赖的 sqlc 方法，文件仅依赖这个较小接口，
// 便于 Mock 测试 + 支持 WithTx 下的事务级 queries 替换。
type querier interface {
	ListPromptTemplates(ctx context.Context, arg sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error)
	GetPromptTemplate(ctx context.Context, promptKey string) (sqlc.GetPromptTemplateRow, error)
	DeletePromptTemplate(ctx context.Context, promptKey string) (int64, error)
	UpsertPromptTemplate(ctx context.Context, arg sqlc.UpsertPromptTemplateParams) (sqlc.UpsertPromptTemplateRow, error)
	InsertPromptVersion(ctx context.Context, arg sqlc.InsertPromptVersionParams) error
}

type store struct {
	q    querier
	pool *pgxpool.Pool
	// qAll 是指向上层 `*sqlc.Queries` 的实例，仅当非事务 store 时非 nil。
	// WithTx 时用 qAll.WithTx(tx) 构造事务级 queries。
	qAll *sqlc.Queries
}

// NewStore p20.1：返回 `Store`（扩展至写能力）。同时保留向后兼容：
// `Store` 嵌入 `Reader`，因此所有原有消费者（如 `dashboard.service`）仍可以该返回值
// 当作 `Reader` 用。为支持 `WithTx`，新增 `pool` 参数；传 nil 时 WithTx 降级
// 成直接调用 fn（测试时景，不真走事务）。
func NewStore(q *sqlc.Queries, pool *pgxpool.Pool) Store {
	return &store{q: q, pool: pool, qAll: q}
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error) {
	rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{
		Column1: filter.AgentKey,
		Column2: filter.Keyword,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "list", "prompt_template")
	}
	templates := make([]PromptTemplate, 0, len(rows))
	for _, row := range rows {
		templates = append(templates, fromSQLCListRow(row))
	}
	return templates, nil
}

func (s *store) Get(ctx context.Context, promptKey string) (*PromptTemplate, error) {
	row, err := s.q.GetPromptTemplate(ctx, promptKey)
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "get", "prompt_template")
	}
	t := fromSQLCGetRow(row)
	return &t, nil
}

func (s *store) Delete(ctx context.Context, promptKey string) error {
	if _, err := s.q.DeletePromptTemplate(ctx, promptKey); err != nil {
		return platformdb.WrapStoreError(err, "delete", "prompt_template")
	}
	return nil
}

func (s *store) Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error) {
	row, err := s.q.UpsertPromptTemplate(ctx, sqlc.UpsertPromptTemplateParams{
		PromptKey:   template.PromptKey,
		Title:       template.Title,
		AgentKey:    template.AgentKey,
		ToolName:    template.ToolName,
		PromptText:  template.PromptText,
		Column6:     []byte(template.Variables),
		Column7:     []byte(template.Tags),
		Description: template.Description,
		Enabled:     template.Enabled,
		CreatedBy:   template.CreatedBy,
		UpdatedBy:   template.UpdatedBy,
	})
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "upsert", "prompt_template")
	}
	t := fromSQLCUpsertRow(row)
	return &t, nil
}

func (s *store) InsertVersion(ctx context.Context, v PromptTemplateVersion) error {
	if err := s.q.InsertPromptVersion(ctx, sqlc.InsertPromptVersionParams{
		PromptKey:       v.PromptKey,
		Title:           v.Title,
		AgentKey:        v.AgentKey,
		ToolName:        v.ToolName,
		PromptText:      v.PromptText,
		Column6:         []byte(v.Variables),
		Column7:         []byte(v.Tags),
		Description:     v.Description,
		Enabled:         v.Enabled,
		CreatedBy:       v.CreatedBy,
		UpdatedBy:       v.UpdatedBy,
		SourceUpdatedAt: v.SourceUpdatedAt,
	}); err != nil {
		return platformdb.WrapStoreError(err, "insert_version", "prompt_template_versions")
	}
	return nil
}

// WithTx 策略：
//   - pool==nil（测试或构造 store 未传 pool）→ 直接用原 store 执行 fn，
//     语义上没有事务滚回，但满足 "Store.WithTx 如期对外暴露" 的接口约束。
//   - qAll==nil（只提供了一个 mock querier，无法绑 tx）→ 同样降级。
//   - 正常权重路径：用 platformdb.WithTx 开事务，q.WithTx(tx) 派生一个事务级 Store。
func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
	if s.pool == nil || s.qAll == nil {
		return fn(s)
	}
	return platformdb.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		txQ := s.qAll.WithTx(tx)
		return fn(&store{q: txQ, pool: nil, qAll: txQ})
	})
}

func fromSQLCListRow(row sqlc.ListPromptTemplatesRow) PromptTemplate {
	return PromptTemplate{
		ID:          row.ID,
		PromptKey:   row.PromptKey,
		Title:       row.Title,
		AgentKey:    row.AgentKey,
		ToolName:    row.ToolName,
		PromptText:  row.PromptText,
		Variables:   json.RawMessage(row.Variables),
		Tags:        json.RawMessage(row.Tags),
		Enabled:     row.Enabled,
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Description: row.Description,
	}
}

func fromSQLCGetRow(row sqlc.GetPromptTemplateRow) PromptTemplate {
	return PromptTemplate{
		ID:          row.ID,
		PromptKey:   row.PromptKey,
		Title:       row.Title,
		AgentKey:    row.AgentKey,
		ToolName:    row.ToolName,
		PromptText:  row.PromptText,
		Variables:   json.RawMessage(row.Variables),
		Tags:        json.RawMessage(row.Tags),
		Enabled:     row.Enabled,
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Description: row.Description,
	}
}

func fromSQLCUpsertRow(row sqlc.UpsertPromptTemplateRow) PromptTemplate {
	return PromptTemplate{
		ID:          row.ID,
		PromptKey:   row.PromptKey,
		Title:       row.Title,
		AgentKey:    row.AgentKey,
		ToolName:    row.ToolName,
		PromptText:  row.PromptText,
		Variables:   json.RawMessage(row.Variables),
		Tags:        json.RawMessage(row.Tags),
		Enabled:     row.Enabled,
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Description: row.Description,
	}
}
