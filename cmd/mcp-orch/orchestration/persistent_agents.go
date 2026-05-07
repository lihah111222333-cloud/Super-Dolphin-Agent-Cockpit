package orchestration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

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
		attachPersistedAgentReport(&snapshot)
		seen[snapshot.AgentID] = struct{}{}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

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

func (s *service) persistedAgentSnapshotByThreadID(ctx context.Context, agentID string) (AgentSnapshot, bool, error) {
	thread, err := s.agentThreads.GetByThreadID(ctx, agentID)
	if err != nil || thread == nil {
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
	updatedAt := persistedThreadTime(thread.UpdatedAt, thread.CreatedAt)
	return AgentSnapshot{
		ID:        agentID,
		AgentID:   agentID,
		Name:      name,
		ParentID:  strings.TrimSpace(thread.ParentAgentID),
		Port:      int(thread.Port),
		PID:       int(thread.PID),
		ThreadID:  strings.TrimSpace(thread.ThreadID),
		Cwd:       strings.TrimSpace(thread.Cwd),
		State:     persistedThreadAgentState(thread),
		UpdatedAt: updatedAt,
	}, true
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

func persistedThreadTime(values ...int64) time.Time {
	for _, value := range values {
		if value > 0 {
			return time.Unix(value, 0)
		}
	}
	return time.Time{}
}

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
			snapshot = overlayRuntimeSnapshot(merged[pos], snapshot)
			merged[pos] = snapshot
			continue
		}
		index[key] = len(merged)
		merged = append(merged, snapshot)
	}
	return merged
}

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
	if runtime.AgentID == "" {
		runtime.AgentID = runtime.ID
	}
	return runtime
}

func sortAgentSnapshots(snapshots []AgentSnapshot) {
	sort.SliceStable(snapshots, func(i, j int) bool {
		if snapshots[i].ID != snapshots[j].ID {
			return snapshots[i].ID < snapshots[j].ID
		}
		if snapshots[i].Name != snapshots[j].Name {
			return snapshots[i].Name < snapshots[j].Name
		}
		return snapshots[i].Port < snapshots[j].Port
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
	report, err := readPersistedAgentReportFile(agentReportFileRecordFromSnapshot(*snapshot))
	if err != nil {
		return
	}
	snapshot.LastReport = normalizeDisplayReportText(report)
}
