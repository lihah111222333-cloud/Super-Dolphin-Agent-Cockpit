package skilladapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/toolstore"
	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	skilltoolstore "github.com/anthropic-ai/super-agent-v3/internal/store/skilltool"
)

type skillMutationAuditStoreAdapter struct {
	store auditstore.Store
}

// provideSkillMutationAuditStore 将 auditlog Store 适配为 Skill 自有写入端口。
func provideSkillMutationAuditStore(store auditstore.Store) (skill.MutationAuditStore, error) {
	if store == nil {
		return nil, errors.New("skill audit log store is required")
	}
	return skillMutationAuditStoreAdapter{store: store}, nil
}

// Insert 把 Skill 审计 DTO 显式映射为 auditlog Store DTO。
func (a skillMutationAuditStoreAdapter) Insert(ctx context.Context, entry skill.MutationAuditEntry) error {
	if a.store == nil {
		return errors.New("skill audit log store adapter is not configured")
	}
	return a.store.Insert(ctx, auditstore.InsertParams{
		EventType: entry.EventType,
		Action:    entry.Action,
		Result:    entry.Result,
		Actor:     entry.Actor,
		Target:    entry.Target,
		Detail:    entry.Detail,
		Level:     entry.Level,
		Extra:     json.RawMessage(entry.Extra),
	})
}

type skillToolPersistenceAdapter struct {
	store *skilltoolstore.Store
}

// provideSkillToolPersistence 将 SQLite Store 适配为 Skill 自有持久化端口。
func provideSkillToolPersistence(store *skilltoolstore.Store) (toolstore.Persistence, error) {
	if store == nil {
		return nil, errors.New("skill tool store is required")
	}
	return skillToolPersistenceAdapter{store: store}, nil
}

// Create 把已校验的 Skill 写入参数映射到 Store DTO。
func (a skillToolPersistenceAdapter) Create(ctx context.Context, p toolstore.MutationParams) (toolstore.Result, error) {
	if err := a.ensureConfigured(); err != nil {
		return toolstore.Result{}, err
	}
	if p.Enabled == nil {
		return toolstore.Result{}, toolstore.ErrEnabledRequired
	}
	row, err := a.store.Create(ctx, skilltoolstore.Mutation{
		CWD: p.CWD, MethodName: p.MethodName, Description: p.Description, Enabled: *p.Enabled,
	})
	return mapSkillToolResult(row), mapSkillToolStoreError(err)
}

// List 映射 Skill 列表查询，并只向内层返回 Skill 自有 DTO。
func (a skillToolPersistenceAdapter) List(ctx context.Context, p toolstore.ListParams) (toolstore.ListResult, error) {
	if err := a.ensureConfigured(); err != nil {
		return toolstore.ListResult{}, err
	}
	rows, err := a.store.List(ctx, skilltoolstore.ListQuery{CWD: p.CWD, Keyword: p.Keyword, Limit: p.Limit})
	if err != nil {
		return toolstore.ListResult{}, mapSkillToolStoreError(err)
	}
	tools := make([]toolstore.Result, len(rows))
	for i, row := range rows {
		tools[i] = mapSkillToolResult(row)
	}
	return toolstore.ListResult{Tools: tools}, nil
}

// Get 把 Skill ID 查询映射到 Store DTO。
func (a skillToolPersistenceAdapter) Get(ctx context.Context, p toolstore.IDParams) (toolstore.Result, error) {
	if err := a.ensureConfigured(); err != nil {
		return toolstore.Result{}, err
	}
	row, err := a.store.Get(ctx, skilltoolstore.ByID{CWD: p.CWD, ID: p.ID})
	return mapSkillToolResult(row), mapSkillToolStoreError(err)
}

// GetByMethod 把 Skill 方法名查询映射到 Store DTO。
func (a skillToolPersistenceAdapter) GetByMethod(ctx context.Context, p toolstore.MethodParams) (toolstore.Result, error) {
	if err := a.ensureConfigured(); err != nil {
		return toolstore.Result{}, err
	}
	row, err := a.store.GetByMethod(ctx, skilltoolstore.ByMethod{CWD: p.CWD, MethodName: p.MethodName})
	return mapSkillToolResult(row), mapSkillToolStoreError(err)
}

// Update 把已校验的 Skill 更新参数映射到 Store DTO。
func (a skillToolPersistenceAdapter) Update(ctx context.Context, p toolstore.UpdateParams) (toolstore.Result, error) {
	if err := a.ensureConfigured(); err != nil {
		return toolstore.Result{}, err
	}
	if p.Enabled == nil {
		return toolstore.Result{}, toolstore.ErrEnabledRequired
	}
	row, err := a.store.Update(ctx, skilltoolstore.Update{
		ID: p.ID,
		Mutation: skilltoolstore.Mutation{
			CWD: p.CWD, MethodName: p.MethodName, Description: p.Description, Enabled: *p.Enabled,
		},
	})
	return mapSkillToolResult(row), mapSkillToolStoreError(err)
}

// Delete 把 Skill 删除请求映射到 Store DTO。
func (a skillToolPersistenceAdapter) Delete(ctx context.Context, p toolstore.IDParams) error {
	if err := a.ensureConfigured(); err != nil {
		return err
	}
	return mapSkillToolStoreError(a.store.Delete(ctx, skilltoolstore.ByID{CWD: p.CWD, ID: p.ID}))
}

// ensureConfigured 在任何 Store 调用前统一阻断零值 adapter，避免 nil pointer panic。
func (a skillToolPersistenceAdapter) ensureConfigured() error {
	if a.store == nil {
		return toolstore.ErrStoreNotConfigured
	}
	return nil
}

// mapSkillToolResult 将 Store 行转换为 Skill 自有 DTO。
func mapSkillToolResult(row skilltoolstore.Tool) toolstore.Result {
	return toolstore.Result{
		ID: row.ID, CWD: row.CWD, MethodName: row.MethodName, Description: row.Description,
		Enabled: row.Enabled, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// mapSkillToolStoreError 跨适配层保留 Skill 对外错误契约。
func mapSkillToolStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, skilltoolstore.ErrStoreNotConfigured):
		return toolstore.ErrStoreNotConfigured
	case errors.Is(err, skilltoolstore.ErrNotFound):
		return toolstore.ErrNotFound
	default:
		return fmt.Errorf("skill tool persistence: %w", err)
	}
}
