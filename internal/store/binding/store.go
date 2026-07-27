package binding

import (
	"context"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// querier 是 binding store 依赖的 sqlc 查询集合，包含 session 绑定和 agent-thread 绑定两类写入。
type querier interface {
	BindAgentThread(ctx context.Context, arg sqlc.BindAgentThreadParams) error
	DeleteAgentProviderBindingByAgentID(ctx context.Context, arg sqlc.DeleteAgentProviderBindingByAgentIDParams) error
	GetAgentProviderBindingByAgentID(ctx context.Context, arg sqlc.GetAgentProviderBindingByAgentIDParams) (sqlc.AgentProviderBinding, error)
	GetAgentProviderBindingByProviderThread(ctx context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error)
	GetThreadByAgent(ctx context.Context, arg sqlc.GetThreadByAgentParams) (string, error)
	ListAgentThreadBindings(ctx context.Context) ([]sqlc.AgentProviderBinding, error)
	UnbindAgentThread(ctx context.Context, arg sqlc.UnbindAgentThreadParams) error
	UpdateAgentCwd(ctx context.Context, arg sqlc.UpdateAgentCwdParams) error
	UpdateAgentProviderBindingProviderThreadID(ctx context.Context, arg sqlc.UpdateAgentProviderBindingProviderThreadIDParams) error
	UpdateAgentProviderBindingSessionUUID(ctx context.Context, arg sqlc.UpdateAgentProviderBindingSessionUUIDParams) error
	UpsertAgentProviderBinding(ctx context.Context, arg sqlc.UpsertAgentProviderBindingParams) error
	RebindAgentThreadTx(ctx context.Context, arg sqlc.RebindAgentThreadTxParams) error
}

// store 实现 provider/thread/cwd 绑定持久化，恢复路径依赖这些记录重建 session。
type store struct {
	q querier
}

type bindingQueryStore = store
type bindingCommandStore = store
type bindingMaintenanceStore = store

// NewStore 使用生产 sqlc 查询对象创建 binding Store。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// GetByProviderThread 按 provider thread ID 读取绑定，供 provider 侧恢复路径反查 agent。
func (s *bindingQueryStore) GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*Binding, error) {
	row, err := s.q.GetAgentProviderBindingByProviderThread(ctx, sqlc.GetAgentProviderBindingByProviderThreadParams{
		Provider:         provider,
		ProviderThreadID: providerThreadID,
	})
	if err != nil {
		return nil, wrapBindingError(err, "get_by_provider_thread")
	}
	result := mapBinding(bindingRow{
		AgentID:            row.AgentID,
		Provider:           row.Provider,
		ProviderThreadID:   row.ProviderThreadID,
		CodexThreadID:      row.CodexThreadID,
		RolloutPath:        row.RolloutPath,
		Cwd:                row.CWD,
		ParentAgentID:      row.ParentAgentID,
		AgentType:          row.AgentType,
		AgentMemoryScope:   row.AgentMemoryScope,
		Archived:           row.Archived != 0,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
		SessionUUID:        row.SessionUUID,
		CodexHome:          row.CodexHome,
		CodexInstanceKey:   row.CodexInstanceKey,
		CodexModelProvider: row.CodexModelProvider,
	})
	return &result, nil
}

// Upsert 写入 provider session 绑定；唯一冲突只在同一 agent/thread 元组时视为幂等。
func (s *bindingCommandStore) Upsert(ctx context.Context, params UpsertParams) error {
	err := s.q.UpsertAgentProviderBinding(ctx, sqlc.UpsertAgentProviderBindingParams{
		AgentID:            params.AgentID,
		Provider:           params.Provider,
		ProviderThreadID:   params.ProviderThreadID,
		CodexThreadID:      params.CodexThreadID,
		RolloutPath:        params.RolloutPath,
		CWD:                params.Cwd,
		ParentAgentID:      params.ParentAgentID,
		AgentType:          params.AgentType,
		AgentMemoryScope:   params.AgentMemoryScope,
		CreatedAt:          params.CreatedAt,
		UpdatedAt:          params.UpdatedAt,
		SessionUUID:        params.SessionUUID,
		CodexHome:          params.CodexHome,
		CodexInstanceKey:   params.CodexInstanceKey,
		CodexModelProvider: params.CodexModelProvider,
	})
	if err == nil {
		return nil
	}
	if !platformdb.IsUniqueViolation(err) {
		return wrapBindingError(err, "upsert")
	}
	existing, lookupErr := s.q.GetAgentProviderBindingByProviderThread(ctx, sqlc.GetAgentProviderBindingByProviderThreadParams{
		Provider:         params.Provider,
		ProviderThreadID: params.ProviderThreadID,
	})
	if lookupErr == nil && existing.AgentID == params.AgentID {
		return nil
	}
	if lookupErr != nil {
		return wrapBindingError(lookupErr, "get_by_provider_thread")
	}
	return wrapBindingError(err, "upsert")
}

// DeleteByAgentID 删除 agent 的 provider 绑定，供停止或清理路径释放恢复索引。
func (s *bindingCommandStore) DeleteByAgentID(ctx context.Context, agentID string) error {
	return wrapBindingError(s.q.DeleteAgentProviderBindingByAgentID(ctx, sqlc.DeleteAgentProviderBindingByAgentIDParams{AgentID: agentID}), "delete_by_agent_id")
}

// UpdateSessionUUID 回填 provider session UUID，保持历史文件和绑定记录可互相定位。
func (s *bindingCommandStore) UpdateSessionUUID(ctx context.Context, params UpdateSessionUUIDParams) error {
	return wrapBindingError(s.q.UpdateAgentProviderBindingSessionUUID(ctx, sqlc.UpdateAgentProviderBindingSessionUUIDParams{
		SessionUUID: params.SessionUUID,
		UpdatedAt:   params.UpdatedAt,
		AgentID:     params.AgentID,
	}), "update_session_uuid")
}

// UpdateProviderThreadID 回填 provider thread ID，恢复路径会优先使用该真实线程标识。
func (s *bindingCommandStore) UpdateProviderThreadID(ctx context.Context, params UpdateProviderThreadIDParams) error {
	return wrapBindingError(s.q.UpdateAgentProviderBindingProviderThreadID(ctx, sqlc.UpdateAgentProviderBindingProviderThreadIDParams{
		ProviderThreadID: params.ProviderThreadID,
		UpdatedAt:        params.UpdatedAt,
		AgentID:          params.AgentID,
	}), "update_provider_thread_id")
}

// GetByAgentID 按 agent ID 读取绑定，供公共 thread 路径补齐 provider 恢复信息。
func (s *bindingQueryStore) GetByAgentID(ctx context.Context, agentID string) (*Binding, error) {
	row, err := s.q.GetAgentProviderBindingByAgentID(ctx, sqlc.GetAgentProviderBindingByAgentIDParams{AgentID: agentID})
	if err != nil {
		return nil, wrapBindingError(err, "get_by_agent_id")
	}
	result := mapBinding(bindingRow{
		AgentID:            row.AgentID,
		Provider:           row.Provider,
		ProviderThreadID:   row.ProviderThreadID,
		CodexThreadID:      row.CodexThreadID,
		RolloutPath:        row.RolloutPath,
		Cwd:                row.CWD,
		ParentAgentID:      row.ParentAgentID,
		AgentType:          row.AgentType,
		AgentMemoryScope:   row.AgentMemoryScope,
		Archived:           row.Archived != 0,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
		SessionUUID:        row.SessionUUID,
		CodexHome:          row.CodexHome,
		CodexInstanceKey:   row.CodexInstanceKey,
		CodexModelProvider: row.CodexModelProvider,
	})
	return &result, nil
}

// BindAgentThread 记录 agent 到公共 thread 和 cwd 的绑定，空时间戳由 store 统一补齐。
func (s *bindingCommandStore) BindAgentThread(ctx context.Context, params BindAgentThreadParams) error {
	now := time.Now().Unix()
	if params.CreatedAt == 0 {
		params.CreatedAt = now
	}
	if params.UpdatedAt == 0 {
		params.UpdatedAt = now
	}
	return wrapBindingError(s.q.BindAgentThread(ctx, sqlc.BindAgentThreadParams{
		AgentID:   params.AgentID,
		ThreadID:  params.ThreadID,
		CWD:       params.Cwd,
		CreatedAt: params.CreatedAt,
		UpdatedAt: params.UpdatedAt,
	}), "bind_agent_thread")
}

// UnbindAgentThread 解除 agent 到公共 thread 的绑定，不删除 provider session 绑定。
func (s *bindingCommandStore) UnbindAgentThread(ctx context.Context, agentID string) error {
	return wrapBindingError(s.q.UnbindAgentThread(ctx, sqlc.UnbindAgentThreadParams{AgentID: agentID}), "unbind_agent_thread")
}

// ListAgentThreadBindings 列出全部 agent/thread 绑定，用于恢复索引和管理视图。
func (s *bindingQueryStore) ListAgentThreadBindings(ctx context.Context) ([]Binding, error) {
	rows, err := s.q.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil, wrapBindingError(err, "list_agent_thread_bindings")
	}
	result := make([]Binding, len(rows))
	for i, row := range rows {
		result[i] = mapBinding(bindingRow{
			AgentID:            row.AgentID,
			Provider:           row.Provider,
			ProviderThreadID:   row.ProviderThreadID,
			CodexThreadID:      row.CodexThreadID,
			RolloutPath:        row.RolloutPath,
			Cwd:                row.CWD,
			ParentAgentID:      row.ParentAgentID,
			AgentType:          row.AgentType,
			AgentMemoryScope:   row.AgentMemoryScope,
			Archived:           row.Archived != 0,
			CreatedAt:          row.CreatedAt,
			UpdatedAt:          row.UpdatedAt,
			SessionUUID:        row.SessionUUID,
			CodexHome:          row.CodexHome,
			CodexInstanceKey:   row.CodexInstanceKey,
			CodexModelProvider: row.CodexModelProvider,
		})
	}
	return result, nil
}

// GetThreadByAgent 按 agent ID 读取公共 thread ID，供跨模块路由查询使用。
func (s *bindingQueryStore) GetThreadByAgent(ctx context.Context, agentID string) (string, error) {
	threadID, err := s.q.GetThreadByAgent(ctx, sqlc.GetThreadByAgentParams{AgentID: agentID})
	if err != nil {
		return "", wrapBindingError(err, "get_thread_by_agent")
	}
	return threadID, nil
}

// UpdateAgentCwd 更新 agent 工作目录，空更新时间由 store 统一补齐。
func (s *bindingCommandStore) UpdateAgentCwd(ctx context.Context, params UpdateAgentCwdParams) error {
	updatedAt := params.UpdatedAt
	if updatedAt == 0 {
		updatedAt = time.Now().Unix()
	}
	return wrapBindingError(s.q.UpdateAgentCwd(ctx, sqlc.UpdateAgentCwdParams{
		CWD:       params.Cwd,
		UpdatedAt: updatedAt,
		AgentID:   params.AgentID,
	}), "update_agent_cwd")
}

// Rebind 原子更新 agent 的 thread 和 cwd 绑定，避免先删后插造成短暂不可达。
func (s *bindingCommandStore) Rebind(ctx context.Context, params RebindParams) error {
	now := time.Now().Unix()
	updatedAt := params.UpdatedAt
	if updatedAt == 0 {
		updatedAt = now
	}
	return wrapBindingError(s.q.RebindAgentThreadTx(ctx, sqlc.RebindAgentThreadTxParams{
		AgentID:   params.AgentID,
		ThreadID:  params.ThreadID,
		CWD:       params.Cwd,
		UpdatedAt: updatedAt,
	}), "rebind")
}

// ListProviderMap 返回 agent 到 provider thread 的恢复索引，providerThreadID 缺失时使用 CodexThreadID。
func (s *bindingMaintenanceStore) ListProviderMap(ctx context.Context) (map[string]string, error) {
	bindings, err := s.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil, wrapBindingError(err, "list_provider_map")
	}
	m := make(map[string]string, len(bindings))
	for _, b := range bindings {
		// 旧记录可能只有 CodexThreadID，保留兼容读取以免重启恢复断链。
		threadID := b.ProviderThreadID
		if threadID == "" {
			threadID = b.CodexThreadID
		}
		if b.AgentID != "" && threadID != "" {
			m[b.AgentID] = threadID
		}
	}
	return m, nil
}

// ListCwdMap 返回 agent 到 cwd 的索引，供恢复和工作区定位路径查询。
func (s *bindingMaintenanceStore) ListCwdMap(ctx context.Context) (map[string]string, error) {
	bindings, err := s.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil, wrapBindingError(err, "list_cwd_map")
	}
	m := make(map[string]string, len(bindings))
	for _, b := range bindings {
		if b.AgentID != "" && b.Cwd != "" {
			m[b.AgentID] = b.Cwd
		}
	}
	return m, nil
}

// mapBinding 将内部统一行结构转换为领域 Binding，集中维护字段映射。
func mapBinding(row bindingRow) Binding {
	return Binding(row)
}

// bindingRow 统一 sqlc 多种查询返回形态，避免每个查询重复维护 Binding 字段顺序。
type bindingRow struct {
	AgentID            string
	Provider           string
	ProviderThreadID   string
	CodexThreadID      string
	RolloutPath        string
	Cwd                string
	ParentAgentID      string
	AgentType          string
	AgentMemoryScope   string
	Archived           bool
	CreatedAt          int64
	UpdatedAt          int64
	SessionUUID        string
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

// wrapBindingError 统一包装 binding store 错误，保留 operation 便于排查。
func wrapBindingError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "binding")
}
