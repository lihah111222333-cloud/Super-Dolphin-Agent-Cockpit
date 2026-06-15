package binding

import (
	"context"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	BindAgentThread(ctx context.Context, arg sqlc.BindAgentThreadParams) error
	DeleteAgentProviderBindingByAgentID(ctx context.Context, arg sqlc.DeleteAgentProviderBindingByAgentIDParams) error
	GetAgentProviderBindingByAgentID(ctx context.Context, arg sqlc.GetAgentProviderBindingByAgentIDParams) (sqlc.AgentProviderBinding, error)
	GetAgentProviderBindingByProviderThread(ctx context.Context, arg sqlc.GetAgentProviderBindingByProviderThreadParams) (sqlc.AgentProviderBinding, error)
	GetThreadByAgent(ctx context.Context, arg sqlc.GetThreadByAgentParams) (string, error)
	ListAgentThreadBindings(ctx context.Context) ([]sqlc.AgentProviderBinding, error)
	UnbindAgentThread(ctx context.Context, arg sqlc.UnbindAgentThreadParams) error
	UpdateAgentCwd(ctx context.Context, arg sqlc.UpdateAgentCwdParams) error
	UpdateAgentProviderBindingArchived(ctx context.Context, arg sqlc.UpdateAgentProviderBindingArchivedParams) error
	UpdateAgentProviderBindingProviderThreadID(ctx context.Context, arg sqlc.UpdateAgentProviderBindingProviderThreadIDParams) error
	UpdateAgentProviderBindingSessionUUID(ctx context.Context, arg sqlc.UpdateAgentProviderBindingSessionUUIDParams) error
	UpsertAgentProviderBinding(ctx context.Context, arg sqlc.UpsertAgentProviderBindingParams) error
	RebindAgentThreadTx(ctx context.Context, arg sqlc.RebindAgentThreadTxParams) error
}

type store struct {
	q querier
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// GetByProviderThread 按provider线程读取binding存储。
func (s *store) GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*Binding, error) {
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

// Upsert 新增或更新记录。
func (s *store) Upsert(ctx context.Context, params UpsertParams) error {
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

// DeleteByAgentID 按代理ID删除binding存储。
func (s *store) DeleteByAgentID(ctx context.Context, agentID string) error {
	return wrapBindingError(s.q.DeleteAgentProviderBindingByAgentID(ctx, sqlc.DeleteAgentProviderBindingByAgentIDParams{AgentID: agentID}), "delete_by_agent_id")
}

// UpdateSessionUUID 更新会话UUID。
func (s *store) UpdateSessionUUID(ctx context.Context, params UpdateSessionUUIDParams) error {
	return wrapBindingError(s.q.UpdateAgentProviderBindingSessionUUID(ctx, sqlc.UpdateAgentProviderBindingSessionUUIDParams{
		SessionUUID: params.SessionUUID,
		UpdatedAt:   params.UpdatedAt,
		AgentID:     params.AgentID,
	}), "update_session_uuid")
}

// UpdateProviderThreadID 更新provider线程ID。
func (s *store) UpdateProviderThreadID(ctx context.Context, params UpdateProviderThreadIDParams) error {
	return wrapBindingError(s.q.UpdateAgentProviderBindingProviderThreadID(ctx, sqlc.UpdateAgentProviderBindingProviderThreadIDParams{
		ProviderThreadID: params.ProviderThreadID,
		UpdatedAt:        params.UpdatedAt,
		AgentID:          params.AgentID,
	}), "update_provider_thread_id")
}

// SetArchived 设置archived。
func (s *store) SetArchived(ctx context.Context, params SetArchivedParams) error {
	archived := int64(0)
	if params.Archived {
		archived = 1
	}
	return wrapBindingError(s.q.UpdateAgentProviderBindingArchived(ctx, sqlc.UpdateAgentProviderBindingArchivedParams{
		Archived:  archived,
		UpdatedAt: params.UpdatedAt,
		AgentID:   params.AgentID,
	}), "set_archived")
}

// GetByAgentID 按代理ID读取binding存储。
func (s *store) GetByAgentID(ctx context.Context, agentID string) (*Binding, error) {
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

// BindAgentThread 绑定代理线程。
func (s *store) BindAgentThread(ctx context.Context, params BindAgentThreadParams) error {
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

// UnbindAgentThread 解绑代理线程。
func (s *store) UnbindAgentThread(ctx context.Context, agentID string) error {
	return wrapBindingError(s.q.UnbindAgentThread(ctx, sqlc.UnbindAgentThreadParams{AgentID: agentID}), "unbind_agent_thread")
}

// ListAgentThreadBindings 列出代理线程bindings。
func (s *store) ListAgentThreadBindings(ctx context.Context) ([]Binding, error) {
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

// GetThreadByAgent 按代理读取线程。
func (s *store) GetThreadByAgent(ctx context.Context, agentID string) (string, error) {
	threadID, err := s.q.GetThreadByAgent(ctx, sqlc.GetThreadByAgentParams{AgentID: agentID})
	if err != nil {
		return "", wrapBindingError(err, "get_thread_by_agent")
	}
	return threadID, nil
}

// UpdateAgentCwd 更新代理工作目录。
func (s *store) UpdateAgentCwd(ctx context.Context, params UpdateAgentCwdParams) error {
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

// Rebind 处理rebind。
func (s *store) Rebind(ctx context.Context, params RebindParams) error {
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

// ListProviderMap 列出providermap。
func (s *store) ListProviderMap(ctx context.Context) (map[string]string, error) {
	bindings, err := s.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil, wrapBindingError(err, "list_provider_map")
	}
	m := make(map[string]string, len(bindings))
	for _, b := range bindings {
		// Prefer ProviderThreadID, fallback to CodexThreadID if empty
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

// ListCwdMap 列出工作目录map。
func (s *store) ListCwdMap(ctx context.Context) (map[string]string, error) {
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

func mapBinding(row bindingRow) Binding {
	return Binding(row)
}

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

func wrapBindingError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "binding")
}
