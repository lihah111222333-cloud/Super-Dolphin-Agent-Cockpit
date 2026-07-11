package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/reportstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// PersistedThread 是 agent thread 表恢复 runtime 快照时使用的只读投影。
// 字段保持接近存储层，service 负责转换为对外 AgentSnapshot。
type PersistedThread struct {
	ThreadID      string
	AgentID       string
	ParentAgentID string
	Name          string
	Prompt        string
	Cwd           string
	Status        string
	Port          int32
	PID           int32
	CreatedAt     int64
	UpdatedAt     int64
	PendingLaunch bool
}

// PersistedBinding 描述 agent 与 provider thread 的持久化绑定。
// archive 和 snapshot fallback 都通过它识别远端线程归属。
type PersistedBinding struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	CodexThreadID    string
	Cwd              string
	Archived         bool
	CreatedAt        int64
	UpdatedAt        int64
}

// PersistedThreadStatusUpdate 是写回持久化 thread 状态的最小更新载荷。
// 只允许更新状态和时间戳，避免 service 状态同步覆盖 thread 身份字段。
type PersistedThreadStatusUpdate struct {
	ThreadID  string
	Status    string
	UpdatedAt int64
}

// PersistedBindingArchiveUpdate 是归档 provider binding 的最小更新载荷。
// 它只切换 archived 标记和更新时间，不重写 provider thread id。
type PersistedBindingArchiveUpdate struct {
	AgentID   string
	Archived  bool
	UpdatedAt int64
}

// AgentThreadStore 是 orchestration 读取和更新持久化 thread 的窄端口。
// service 通过它做 runtime 缺失时的 snapshot fallback 和停止状态写回。
type AgentThreadStore interface {
	ListAll(ctx context.Context) ([]PersistedThread, error)
	GetByThreadID(ctx context.Context, threadID string) (*PersistedThread, error)
	UpdateStatus(ctx context.Context, params PersistedThreadStatusUpdate) error
}

// AgentBindingStore 是 orchestration 读取和归档 provider binding 的窄端口。
// archive 路径依赖它定位远端线程归属，不能在 service 内绕过 store。
type AgentBindingStore interface {
	GetByAgentID(ctx context.Context, agentID string) (*PersistedBinding, error)
	SetArchived(ctx context.Context, params PersistedBindingArchiveUpdate) error
}

// listPersistedAgentSnapshots 从持久化线程表恢复可展示的 agent 快照。
// 同一 agent 只保留第一条快照，runtime 快照会在上层 merge 时覆盖动态字段。
func (s *service) listPersistedAgentSnapshots(ctx context.Context) ([]AgentSnapshot, error) {
	if s == nil {
		return nil, nil
	}
	return listPersistedAgentSnapshotsFromStore(ctx, s.lifecycle.agentThreads)
}

// listPersistedAgentSnapshotsFromStore 从 thread store 恢复可展示快照，供 service 与 report fallback 共用。
func listPersistedAgentSnapshotsFromStore(ctx context.Context, agentThreads AgentThreadStore) ([]AgentSnapshot, error) {
	if agentThreads == nil {
		return nil, nil
	}
	threads, err := agentThreads.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	snapshots := make([]AgentSnapshot, 0, len(threads))
	seen := map[string]struct{}{}
	for _, thread := range threads {
		snapshot, ok := snapshotFromPersistedThread(thread)
		if !ok {
			continue
		}
		if _, exists := seen[snapshot.AgentID]; exists {
			continue
		}
		seen[snapshot.AgentID] = struct{}{}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

// persistedAgentSnapshot 按 agentID 或 threadID 查找持久化快照。
// 先走 threadID 精确查询，失败后再列表扫描兼容旧绑定。
func (s *service) persistedAgentSnapshot(ctx context.Context, agentID string) (AgentSnapshot, error) {
	if s == nil {
		return AgentSnapshot{}, fmt.Errorf("%w: %s", errAgentNotFound, strings.TrimSpace(agentID))
	}
	return persistedAgentSnapshotFromStore(ctx, s.lifecycle.agentThreads, agentID)
}

// persistedAgentSnapshotFromStore 按 agent_id 精确恢复持久化快照，禁止用 display name 误命中旧 report。
func persistedAgentSnapshotFromStore(ctx context.Context, agentThreads AgentThreadStore, agentID string) (AgentSnapshot, error) {
	agentID = strings.TrimSpace(agentID)
	if agentThreads == nil || agentID == "" {
		return AgentSnapshot{}, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
	}
	if snapshot, ok, err := persistedAgentSnapshotByThreadIDFromStore(ctx, agentThreads, agentID); err != nil {
		return AgentSnapshot{}, err
	} else if ok {
		return snapshot, nil
	}
	if snapshot, ok, err := persistedAgentSnapshotByListFromStore(ctx, agentThreads, agentID); err != nil {
		return AgentSnapshot{}, err
	} else if ok {
		return snapshot, nil
	}
	return AgentSnapshot{}, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
}

// persistedAgentSnapshotByThreadIDFromStore 先用 thread_id 快路径查找，agent_id 不匹配时交给列表兼容路径。
func persistedAgentSnapshotByThreadIDFromStore(ctx context.Context, agentThreads AgentThreadStore, agentID string) (AgentSnapshot, bool, error) {
	if agentThreads == nil {
		return AgentSnapshot{}, false, nil
	}
	thread, err := agentThreads.GetByThreadID(ctx, agentID)
	if err != nil {
		if errors.Is(err, errAgentNotFound) || platformdb.IsNotFound(err) {
			return AgentSnapshot{}, false, nil
		}
		return AgentSnapshot{}, false, err
	}
	if thread == nil {
		return AgentSnapshot{}, false, nil
	}
	snapshot, ok := snapshotFromPersistedThread(*thread)
	if ok && sameAgentID(snapshot.AgentID, agentID) {
		attachPersistedAgentReport(&snapshot)
		return snapshot, true, nil
	}
	return AgentSnapshot{}, false, nil
}

// persistedAgentSnapshotByListFromStore 扫描 thread store 兼容旧绑定，只接受 agent_id 相同的快照。
func persistedAgentSnapshotByListFromStore(ctx context.Context, agentThreads AgentThreadStore, agentID string) (AgentSnapshot, bool, error) {
	if agentThreads == nil {
		return AgentSnapshot{}, false, nil
	}
	threads, listErr := agentThreads.ListAll(ctx)
	if listErr != nil {
		return AgentSnapshot{}, false, listErr
	}
	for _, thread := range threads {
		snapshot, ok := snapshotFromPersistedThread(thread)
		if ok && sameAgentID(snapshot.AgentID, agentID) {
			attachPersistedAgentReport(&snapshot)
			return snapshot, true, nil
		}
	}
	return AgentSnapshot{}, false, nil
}

func snapshotFromPersistedThread(thread PersistedThread) (AgentSnapshot, bool) {
	agentID := persistedThreadAgentID(thread)
	if agentID == "" {
		return AgentSnapshot{}, false
	}
	name := strings.TrimSpace(platformshared.FirstNonEmpty(thread.Name, thread.Prompt, agentID))
	return AgentSnapshot{ID: agentID, AgentID: agentID, Name: name, ParentID: strings.TrimSpace(thread.ParentAgentID), Port: int(thread.Port), PID: int(thread.PID), ThreadID: strings.TrimSpace(thread.ThreadID), Cwd: strings.TrimSpace(thread.Cwd), State: persistedThreadAgentState(thread), CreatedAt: contract.NormalizeUnixTime(thread.CreatedAt), UpdatedAt: contract.NormalizeUnixTime(thread.UpdatedAt, thread.CreatedAt)}, true
}

func persistedThreadAgentID(thread PersistedThread) string {
	if agentID := strings.TrimSpace(thread.AgentID); agentID != "" {
		return agentID
	}
	return strings.TrimSpace(thread.ThreadID)
}

func persistedThreadAgentState(thread PersistedThread) string {
	switch strings.ToLower(strings.TrimSpace(thread.Status)) {
	case "", "created", "running":
		if thread.PendingLaunch {
			return string(agentdto.StateProvisioning)
		}
		return string(agentdto.StateIdle)
	case "stopped", "archived":
		return string(agentdto.StateStopped)
	case "expired":
		return string(agentdto.StateFailed)
	default:
		return strings.TrimSpace(thread.Status)
	}
}

// mergeAgentSnapshots 合并代理snapshots。
func mergeAgentSnapshots(persisted, runtime []AgentSnapshot) []AgentSnapshot {
	merged := make([]AgentSnapshot, 0, len(persisted)+len(runtime))
	index := make(map[string]int, len(persisted)+len(runtime))
	for _, snapshot := range persisted {
		key := snapshotKey(snapshot)
		if key == "" {
			continue
		}
		index[key] = len(merged)
		merged = append(merged, snapshot)
	}
	for _, snapshot := range runtime {
		key := snapshotKey(snapshot)
		if key == "" {
			continue
		}
		if pos, ok := index[key]; ok {
			merged[pos] = overlayRuntimeSnapshot(merged[pos], snapshot)
			continue
		}
		index[key] = len(merged)
		merged = append(merged, snapshot)
	}
	return merged
}

// overlayRuntimeSnapshot 用持久化字段覆盖 runtime 快照的展示身份。
// runtime 的状态和动态字段保留，持久化名称、线程和创建时间用于跨重启展示稳定性。
func overlayRuntimeSnapshot(persisted, runtime AgentSnapshot) AgentSnapshot {
	if persisted.Name != "" {
		runtime.Name = persisted.Name
	}
	if persisted.ThreadID != "" {
		runtime.ThreadID = persisted.ThreadID
	}
	if persisted.Cwd != "" {
		runtime.Cwd = persisted.Cwd
	}
	if persisted.ParentID != "" {
		runtime.ParentID = persisted.ParentID
	}
	if !persisted.CreatedAt.IsZero() {
		runtime.CreatedAt = persisted.CreatedAt
	}
	if runtime.AgentID == "" {
		runtime.AgentID = runtime.ID
	}
	return runtime
}

func sortAgentSnapshots(snapshots []AgentSnapshot) {
	sort.SliceStable(snapshots, func(i, j int) bool {
		left, right := snapshots[i].CreatedAt, snapshots[j].CreatedAt
		if left.IsZero() {
			left = snapshots[i].UpdatedAt
		}
		if right.IsZero() {
			right = snapshots[j].UpdatedAt
		}
		if !left.Equal(right) {
			return left.After(right)
		}
		return snapshots[i].ID < snapshots[j].ID
	})
}

func snapshotKey(snapshot AgentSnapshot) string {
	return strings.TrimSpace(platformshared.FirstNonEmpty(snapshot.AgentID, snapshot.ID))
}

func sameAgentID(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func attachPersistedAgentReport(snapshot *AgentSnapshot) {
	if snapshot == nil {
		return
	}
	report, err := reportstore.ReadPersisted(agentReportFileRecordFromSnapshot(*snapshot))
	if err == nil {
		snapshot.LastReport = normalizeDisplayReportText(report)
	}
}
