package contract

import (
	"context"
	"encoding/json"
	"strings"
)

// ThreadStore defines the thread persistence port consumed by the thread module.
type ThreadStore interface {
	GetByThreadID(ctx context.Context, threadID string) (*Thread, error)
	GetByPort(ctx context.Context, port int32) (*Thread, error)
	ListAll(ctx context.Context) ([]Thread, error)
	ListConfigsByIDs(ctx context.Context, threadIDs []string) ([]Thread, error)
	ListRunning(ctx context.Context) ([]Thread, error)
	ListRecoverable(ctx context.Context) ([]Thread, error)
	ListRunningAgents(ctx context.Context) ([]RunningAgent, error)
	Upsert(ctx context.Context, params ThreadUpsertParams) error
	SavePromptSnapshot(ctx context.Context, threadID string, snapshot PromptSnapshot) error
	LoadPromptSnapshot(ctx context.Context, threadID string) (*PromptSnapshot, error)
	UpdateStatus(ctx context.Context, params ThreadUpdateStatusParams) error
	UpdateLaunchResult(ctx context.Context, params ThreadUpdateLaunchResultParams) error
	DeleteByThreadID(ctx context.Context, threadID string) error
	ResetRunning(ctx context.Context) error
	ExpireStale(ctx context.Context, params ThreadExpireStaleParams) (int64, error)
	RunningExists(ctx context.Context, threadID string) (bool, error)
	ListCwds(ctx context.Context) ([]ThreadCwd, error)
	ListCwdsByPrefix(ctx context.Context, prefix string) ([]ThreadCwd, error)
	CountChildren(ctx context.Context, parentAgentID string) (int64, error)
	Exists(ctx context.Context, threadID string) (bool, error)
	CountAll(ctx context.Context) (int64, error)
}

// ThreadUpsertParams carries fields persisted for a thread row.
type ThreadUpsertParams struct {
	ThreadID         string
	Name             string
	Prompt           string
	Model            string
	Cwd              string
	Status           string
	Port             int32
	PID              int32
	CreatedAt        int64
	UpdatedAt        int64
	OwnerThreadID    string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	ConfigOverride   json.RawMessage
	AgentKey         string
	PromptVersionID  *int64
	PendingLaunch    bool
	ManuallyRenamed  bool
}

// ThreadUpdateStatusParams carries a status transition update.
type ThreadUpdateStatusParams struct {
	ThreadID  string
	Status    string
	UpdatedAt int64
}

// ThreadUpdateLaunchResultParams carries launch metadata for a thread.
type ThreadUpdateLaunchResultParams struct {
	ThreadID        string
	AgentKey        string
	PromptVersionID *int64
	UpdatedAt       int64
}

// ThreadExpireStaleParams carries stale-thread expiration bounds.
type ThreadExpireStaleParams struct {
	UpdatedAt int64
	Cutoff    int64
}

// Thread is the persisted thread read model.
type Thread struct {
	ThreadID         string
	AgentID          string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	Name             string
	Prompt           string
	Model            string
	Cwd              string
	Status           string
	Port             int32
	PID              int32
	CreatedAt        int64
	UpdatedAt        int64
	FinishedAt       *int64
	LastEventType    string
	ErrorMessage     string
	WorkspaceRunKey  string
	OwnerThreadID    string
	ConfigOverride   json.RawMessage
	AgentKey         string
	PromptVersionID  *int64
	PendingLaunch    bool
	ManuallyRenamed  bool
}

// PromptSnapshot is the persisted prompt assembly snapshot for a thread.
type PromptSnapshot struct {
	DisplayName           string            `json:"displayName,omitempty"`
	BaseInstructions      string            `json:"baseInstructions,omitempty"`
	Boundary              *PromptBoundary   `json:"boundary,omitempty"`
	DeveloperInstructions string            `json:"developerInstructions,omitempty"`
	Provider              string            `json:"provider,omitempty"`
	Version               int               `json:"version,omitempty"`
	Hash                  string            `json:"hash,omitempty"`
	SectionSnapshot       map[string]string `json:"sectionSnapshot,omitempty"`
	Generation            uint64            `json:"generation,omitempty"`
}

// PromptBoundary splits cached and uncached prompt content.
type PromptBoundary struct {
	CachedPrefix string `json:"cachedPrefix,omitempty"`
	UncachedTail string `json:"uncachedTail,omitempty"`
}

type legacyPromptSnapshot struct {
	DisplayName           string            `json:"display_name,omitempty"`
	BaseInstructions      string            `json:"base_instructions,omitempty"`
	DeveloperInstructions string            `json:"developer_instructions,omitempty"`
	Provider              string            `json:"provider,omitempty"`
	Version               int               `json:"version,omitempty"`
	Hash                  string            `json:"hash,omitempty"`
	SectionSnapshot       map[string]string `json:"section_snapshot,omitempty"`
	Generation            int64             `json:"generation,omitempty"`
}

// UnmarshalJSON decodes modern and legacy prompt snapshot JSON shapes.
func (p *PromptSnapshot) UnmarshalJSON(data []byte) error {
	snapshot, err := unmarshalPromptSnapshot(data)
	if err != nil {
		return err
	}
	*p = snapshot
	return nil
}

func unmarshalPromptSnapshot(data []byte) (PromptSnapshot, error) {
	type modern PromptSnapshot
	var current modern
	if err := json.Unmarshal(data, &current); err != nil {
		return PromptSnapshot{}, err
	}
	var old legacyPromptSnapshot
	if err := json.Unmarshal(data, &old); err != nil {
		return PromptSnapshot{}, err
	}
	return mergeLegacyPromptSnapshot(PromptSnapshot(current), old), nil
}

func mergeLegacyPromptSnapshot(snapshot PromptSnapshot, old legacyPromptSnapshot) PromptSnapshot {
	if snapshot.DisplayName == "" {
		snapshot.DisplayName = strings.TrimSpace(old.DisplayName)
	}
	if snapshot.BaseInstructions == "" {
		snapshot.BaseInstructions = strings.TrimSpace(old.BaseInstructions)
	}
	if snapshot.DeveloperInstructions == "" {
		snapshot.DeveloperInstructions = strings.TrimSpace(old.DeveloperInstructions)
	}
	if snapshot.Provider == "" {
		snapshot.Provider = strings.TrimSpace(old.Provider)
	}
	if snapshot.Version == 0 {
		snapshot.Version = old.Version
	}
	if snapshot.Hash == "" {
		snapshot.Hash = strings.TrimSpace(old.Hash)
	}
	snapshot.SectionSnapshot = resolvePromptSnapshotSections(snapshot.SectionSnapshot, old.SectionSnapshot)
	if snapshot.Generation == 0 && old.Generation > 0 {
		snapshot.Generation = uint64(old.Generation)
	}
	return snapshot
}

func resolvePromptSnapshotSections(current, legacy map[string]string) map[string]string {
	if len(current) == 0 {
		return clonePromptSnapshotSectionMap(legacy)
	}
	return clonePromptSnapshotSectionMap(current)
}

func clonePromptSnapshotSectionMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RunningAgent describes a thread row with a live agent process.
type RunningAgent struct {
	ThreadID string
	Port     int32
	PID      int32
	Status   string
}

// ThreadCwd is a persisted thread working-directory projection.
type ThreadCwd struct {
	ThreadID string
	Cwd      string
}
