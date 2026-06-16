package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/reportstore"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

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

type PersistedThreadStatusUpdate struct {
	ThreadID  string
	Status    string
	UpdatedAt int64
}

type PersistedBindingArchiveUpdate struct {
	AgentID   string
	Archived  bool
	UpdatedAt int64
}

type AgentThreadStore interface {
	ListAll(ctx context.Context) ([]PersistedThread, error)
	GetByThreadID(ctx context.Context, threadID string) (*PersistedThread, error)
	UpdateStatus(ctx context.Context, params PersistedThreadStatusUpdate) error
}

type AgentBindingStore interface {
	GetByAgentID(ctx context.Context, agentID string) (*PersistedBinding, error)
	SetArchived(ctx context.Context, params PersistedBindingArchiveUpdate) error
}

// listPersistedAgentSnapshots 列出persisted代理snapshots。
func (s *service) listPersistedAgentSnapshots(ctx context.Context) ([]AgentSnapshot, error) {
	if s == nil || s.agentThreads == nil {
		return nil, nil
	}
	threads, err := s.agentThreads.ListAll(ctx)
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

// persistedAgentSnapshot 处理persisted代理快照。
func (s *service) persistedAgentSnapshot(ctx context.Context, agentID string) (AgentSnapshot, error) {
	agentID = strings.TrimSpace(agentID)
	if s == nil || s.agentThreads == nil || agentID == "" {
		return AgentSnapshot{}, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
	}
	if snapshot, ok, err := s.persistedAgentSnapshotByThreadID(ctx, agentID); err != nil {
		return AgentSnapshot{}, err
	} else if ok {
		return snapshot, nil
	}
	if snapshot, ok, err := s.persistedAgentSnapshotByList(ctx, agentID); err != nil {
		return AgentSnapshot{}, err
	} else if ok {
		return snapshot, nil
	}
	return AgentSnapshot{}, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
}

// persistedAgentSnapshotByThreadID 按线程ID处理persisted代理快照。
func (s *service) persistedAgentSnapshotByThreadID(ctx context.Context, agentID string) (AgentSnapshot, bool, error) {
	thread, err := s.agentThreads.GetByThreadID(ctx, agentID)
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

func (s *service) persistedAgentSnapshotByList(ctx context.Context, agentID string) (AgentSnapshot, bool, error) {
	threads, listErr := s.agentThreads.ListAll(ctx)
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

// overlayRuntimeSnapshot 处理overlay运行时快照。
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
