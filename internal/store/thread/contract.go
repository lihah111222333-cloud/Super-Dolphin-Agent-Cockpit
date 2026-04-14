package thread

import (
	"context"
	"encoding/json"
	"strings"
)

type Store interface {
	GetByThreadID(ctx context.Context, threadID string) (*Thread, error)
	GetByPort(ctx context.Context, port int32) (*Thread, error)
	ListAll(ctx context.Context) ([]Thread, error)
	ListRunning(ctx context.Context) ([]Thread, error)
	ListRecoverable(ctx context.Context) ([]Thread, error)
	ListRunningAgents(ctx context.Context) ([]RunningAgent, error)
	Upsert(ctx context.Context, params UpsertParams) error
	SavePromptSnapshot(ctx context.Context, threadID string, snapshot PromptSnapshot) error
	LoadPromptSnapshot(ctx context.Context, threadID string) (*PromptSnapshot, error)
	UpdateStatus(ctx context.Context, params UpdateStatusParams) error
	DeleteByThreadID(ctx context.Context, threadID string) error
	ResetRunning(ctx context.Context) error
	ExpireStale(ctx context.Context, params ExpireStaleParams) (int64, error)
	RunningExists(ctx context.Context, threadID string) (bool, error)
	ListCwds(ctx context.Context) ([]ThreadCwd, error)
	ListCwdsByPrefix(ctx context.Context, prefix string) ([]ThreadCwd, error)
}

type UpsertParams struct {
	ThreadID         string
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
}

type UpdateStatusParams struct {
	ThreadID  string
	Status    string
	UpdatedAt int64
}

type ExpireStaleParams struct {
	UpdatedAt int64
	Cutoff    int64
}

type Thread struct {
	ThreadID         string
	AgentID          string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
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
}

type PromptSnapshot struct {
	DisplayName           string            `json:"displayName,omitempty"`
	BaseInstructions      string            `json:"baseInstructions,omitempty"`
	DeveloperInstructions string            `json:"developerInstructions,omitempty"`
	Provider              string            `json:"provider,omitempty"`
	Version               int               `json:"version,omitempty"`
	Hash                  string            `json:"hash,omitempty"`
	SectionSnapshot       map[string]string `json:"sectionSnapshot,omitempty"`
	Generation            uint64            `json:"generation,omitempty"`
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

type RunningAgent struct {
	ThreadID string
	Port     int32
	PID      int32
	Status   string
}

type ThreadCwd struct {
	ThreadID string
	Cwd      string
}
