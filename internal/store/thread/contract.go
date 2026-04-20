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
	Boundary              *PromptBoundary   `json:"boundary,omitempty"`
	DeveloperInstructions string            `json:"developerInstructions,omitempty"`
	Provider              string            `json:"provider,omitempty"`
	Version               int               `json:"version,omitempty"`
	Hash                  string            `json:"hash,omitempty"`
	SectionSnapshot       map[string]string `json:"sectionSnapshot,omitempty"`
	Generation            uint64            `json:"generation,omitempty"`
	// LaunchSkillNames p20.4 §4.4：launch skill 名称列表（stored snapshot 端）。
	// resume/fork/recover 时优先读取该字段恢复 launch skill 选择；nil 表示未显式选择。
	LaunchSkillNames []string `json:"launchSkillNames,omitempty"`
	// ForceLaunchSkills p20.4 §4.4：对应 UI manualSkillSelection 持久化端。
	ForceLaunchSkills bool `json:"forceLaunchSkills,omitempty"`
}

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
	// p20.4 §4.4：legacy snake_case tag，兼容旧 snapshot 序列化输出。
	LaunchSkillNames  []string `json:"launch_skill_names,omitempty"`
	ForceLaunchSkills bool     `json:"force_launch_skills,omitempty"`
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
	// p20.4 §4.4：legacy snake_case launch skill 回落；旧 snapshot 缺省
	// 时保持 nil/false，与 modern 零值语义一致（分支抽到 helper 以压 CC）。
	snapshot.LaunchSkillNames, snapshot.ForceLaunchSkills = mergeLegacyLaunchSkill(
		snapshot.LaunchSkillNames, snapshot.ForceLaunchSkills,
		old.LaunchSkillNames, old.ForceLaunchSkills,
	)
	return snapshot
}

// mergeLegacyLaunchSkill p20.4 §4.4：把 modern + legacy launch skill 字段按
// "modern 优先 / legacy 回落" 的规则合并。抽出独立函数避免 mergeLegacy
// PromptSnapshot 的 CC 超标。
func mergeLegacyLaunchSkill(
	modernNames []string, modernForce bool,
	legacyNames []string, legacyForce bool,
) ([]string, bool) {
	names := modernNames
	if len(names) == 0 {
		names = legacyNames
	}
	if len(names) == 0 {
		names = nil
	} else {
		names = append([]string(nil), names...)
	}
	return names, modernForce || legacyForce
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
