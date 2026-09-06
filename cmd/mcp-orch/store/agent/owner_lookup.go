package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
)

// ErrTerminalOwnerNotFound 表示没有持久化绑定拥有该线程；调用方不得回退到任意内存候选。
var ErrTerminalOwnerNotFound = errors.New("terminal owner not found")

// ErrTerminalOwnerAmbiguous 表示线程的持久化绑定存在冲突，包括跨 provider 冲突。
var ErrTerminalOwnerAmbiguous = errors.New("terminal owner is ambiguous")

var _ orchestration.TerminalOwnerLookup = (*threadStore)(nil)

const lookupOwnerByThreadSQL = `
SELECT agent_id, provider, provider_thread_id, codex_thread_id, archived
FROM agent_provider_binding
WHERE (provider_thread_id = ? AND provider_thread_id <> '')
   OR (codex_thread_id = ? AND codex_thread_id <> '')
`

type ownerLookupState struct {
	activeOwners      map[string]struct{}
	activeProviders   map[string]struct{}
	archivedOwners    map[string]struct{}
	archivedProviders map[string]struct{}
}

type ownerBindingRow struct {
	agentID          string
	provider         string
	providerThreadID string
	codexThreadID    string
	archived         bool
}

// LookupOwnerByThread 查询所有匹配的持久化绑定，并在零匹配或身份冲突时 fail-fast。
// 查询不使用排序或 LIMIT，避免错误地选择“最新”行掩盖 owner 冲突。
func (s *threadStore) LookupOwnerByThread(ctx context.Context, threadID string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("%w: store is not configured", ErrTerminalOwnerNotFound)
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", fmt.Errorf("%w: thread id is required", ErrTerminalOwnerNotFound)
	}
	rows, err := s.db.QueryContext(ctx, lookupOwnerByThreadSQL, threadID, threadID)
	if err != nil {
		return "", wrapBinding(err, "lookup_owner_by_thread")
	}
	defer rows.Close()
	state := newOwnerLookupState()
	if err := collectOwnerBindings(rows, threadID, state); err != nil {
		return "", err
	}
	return state.resolve(threadID)
}

// newOwnerLookupState 创建区分 active 与 archived 证据的 owner 累加器。
func newOwnerLookupState() *ownerLookupState {
	return &ownerLookupState{
		activeOwners:      make(map[string]struct{}),
		activeProviders:   make(map[string]struct{}),
		archivedOwners:    make(map[string]struct{}),
		archivedProviders: make(map[string]struct{}),
	}
}

// collectOwnerBindings 扫描 SQL 结果并保留原有的行级身份与匹配复核。
func collectOwnerBindings(rows *sql.Rows, threadID string, state *ownerLookupState) error {
	for rows.Next() {
		row, err := scanOwnerBinding(rows)
		if err != nil {
			return wrapBinding(err, "lookup_owner_by_thread")
		}
		if err := state.add(threadID, row); err != nil {
			return err
		}
	}
	return wrapBinding(rows.Err(), "lookup_owner_by_thread")
}

// scanOwnerBinding 将一行绑定记录解码为 owner 判定所需的原始字段。
func scanOwnerBinding(rows *sql.Rows) (ownerBindingRow, error) {
	var row ownerBindingRow
	err := rows.Scan(&row.agentID, &row.provider, &row.providerThreadID, &row.codexThreadID, &row.archived)
	return row, err
}

// add 校验并累加一条绑定；SQL 谓词之外的行不会贡献 owner。
func (s *ownerLookupState) add(threadID string, row ownerBindingRow) error {
	agentID := strings.TrimSpace(row.agentID)
	provider := strings.ToLower(strings.TrimSpace(row.provider))
	if err := validateTerminalOwnerBinding(agentID, provider, row.providerThreadID, row.codexThreadID); err != nil {
		return fmt.Errorf("%w: thread %q: %v", ErrTerminalOwnerAmbiguous, threadID, err)
	}
	if strings.TrimSpace(row.providerThreadID) != threadID && strings.TrimSpace(row.codexThreadID) != threadID {
		return nil
	}
	owners, providers := s.activeOwners, s.activeProviders
	if row.archived {
		owners, providers = s.archivedOwners, s.archivedProviders
	}
	owners[agentID] = struct{}{}
	providers[provider] = struct{}{}
	return nil
}

// resolve 根据 active owner 唯一性裁决结果，并用 archived 记录检测历史碰撞。
func (s *ownerLookupState) resolve(threadID string) (string, error) {
	if len(s.activeOwners) == 0 {
		return "", fmt.Errorf("%w: thread %q", ErrTerminalOwnerNotFound, threadID)
	}
	if len(s.activeOwners) != 1 || len(s.activeProviders) != 1 {
		return "", fmt.Errorf("%w: thread %q has %d active owners across %d providers", ErrTerminalOwnerAmbiguous, threadID, len(s.activeOwners), len(s.activeProviders))
	}
	ownerID := soleOwner(s.activeOwners)
	provider := soleOwner(s.activeProviders)
	if err := s.checkArchivedOwners(threadID, ownerID, provider); err != nil {
		return "", err
	}
	return ownerID, nil
}

// soleOwner 读取已确认只有一个元素的集合。
func soleOwner(values map[string]struct{}) string {
	for value := range values {
		return value
	}
	return ""
}

// checkArchivedOwners 确保历史绑定不会隐藏与 active owner/provider 的冲突。
func (s *ownerLookupState) checkArchivedOwners(threadID, ownerID, provider string) error {
	for archivedOwner := range s.archivedOwners {
		if archivedOwner != ownerID {
			return fmt.Errorf("%w: thread %q has archived owner %q and active owner %q", ErrTerminalOwnerAmbiguous, threadID, archivedOwner, ownerID)
		}
	}
	for archivedProvider := range s.archivedProviders {
		if archivedProvider != provider {
			return fmt.Errorf("%w: thread %q has archived provider %q and active provider %q", ErrTerminalOwnerAmbiguous, threadID, archivedProvider, provider)
		}
	}
	return nil
}

// validateTerminalOwnerBinding 在路由终态事件前拒绝损坏的 agent/provider/线程三元组。
// provider-only 或 public-only 记录仍兼容 legacy；provider_thread_id 等于 agent id 时始终只是占位符。
func validateTerminalOwnerBinding(agentID, provider, providerThreadID, codexThreadID string) error {
	if agentID == "" || provider == "" {
		return errors.New("binding agent/provider identity is empty")
	}
	if provider != "codex" && provider != "claude" {
		return fmt.Errorf("unsupported provider %q", provider)
	}
	providerThreadID = strings.TrimSpace(providerThreadID)
	codexThreadID = strings.TrimSpace(codexThreadID)
	if providerThreadID == "" && codexThreadID == "" {
		return errors.New("binding provider/public thread identity is empty")
	}
	if providerThreadID == agentID {
		return errors.New("binding provider thread identity is an agent placeholder")
	}
	return nil
}
