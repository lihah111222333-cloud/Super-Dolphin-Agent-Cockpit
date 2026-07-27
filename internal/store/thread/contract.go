package thread

import (
	"context"
	"encoding/json"
	"strings"
)

// Store 定义 thread 运行态、prompt 快照和恢复查询的持久化边界。
// 该接口被 app、module 和 adapter 共用，新增方法需要同时评估恢复与 UI 调用面。
type Store interface {
	GetByThreadID(ctx context.Context, threadID string) (*Thread, error)
	GetByPort(ctx context.Context, port int32) (*Thread, error)
	ListAll(ctx context.Context) ([]Thread, error)
	ListConfigsByIDs(ctx context.Context, threadIDs []string) ([]Thread, error)
	ListRunning(ctx context.Context) ([]Thread, error)
	ListRecoverable(ctx context.Context) ([]Thread, error)
	ListRunningAgents(ctx context.Context) ([]RunningAgent, error)
	Upsert(ctx context.Context, params UpsertParams) error
	SavePromptSnapshot(ctx context.Context, threadID string, snapshot PromptSnapshot) error
	LoadPromptSnapshot(ctx context.Context, threadID string) (*PromptSnapshot, error)
	UpdateStatus(ctx context.Context, params UpdateStatusParams) error
	UpdateLaunchResult(ctx context.Context, params UpdateLaunchResultParams) error
	DeleteByThreadID(ctx context.Context, threadID string) error
	ResetRunning(ctx context.Context) error
	ExpireStale(ctx context.Context, params ExpireStaleParams) (int64, error)
	RunningExists(ctx context.Context, threadID string) (bool, error)
	ListCwds(ctx context.Context) ([]ThreadCwd, error)
	ListCwdsByPrefix(ctx context.Context, prefix string) ([]ThreadCwd, error)
	CountChildren(ctx context.Context, parentAgentID string) (int64, error)
	Exists(ctx context.Context, threadID string) (bool, error)
	CountAll(ctx context.Context) (int64, error)
}

// ArchiveStateStore 原子维护 thread 状态和 provider binding 的归档标记。
// 它是 Store concrete value 的附加能力，组合根通过 Thread-owned adapter 暴露给业务层。
type ArchiveStateStore interface {
	SetArchiveState(ctx context.Context, params ArchiveStateParams) error
}

// ArchiveStateParams 是跨 agent_threads 与 agent_provider_binding 的原子写入输入。
type ArchiveStateParams struct {
	ThreadID  string
	AgentID   string
	Archived  bool
	UpdatedAt int64
}

// UpsertParams 汇总创建或更新 thread 记录时需要写入的字段。
// ConfigOverride 和 PromptVersionID 保留运行时配置快照，不能在 store 层静默补默认值。
type UpsertParams struct {
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

// UpdateStatusParams 描述 thread 状态变更写入。
// UpdatedAt 由调用方提供，便于恢复逻辑保持事件时间和存储时间一致。
type UpdateStatusParams struct {
	ThreadID  string
	Status    string
	UpdatedAt int64
}

// UpdateLaunchResultParams 描述 provider 启动结果回写字段。
// AgentKey 和 PromptVersionID 会影响后续恢复与 prompt 快照选择。
type UpdateLaunchResultParams struct {
	ThreadID        string
	AgentKey        string
	PromptVersionID *int64
	UpdatedAt       int64
}

// ExpireStaleParams 限定过期运行中 thread 的批量收口窗口。
// Cutoff 是判定陈旧的时间线，UpdatedAt 是写回 expired 状态的时间戳。
type ExpireStaleParams struct {
	UpdatedAt int64
	Cutoff    int64
}

// Thread 是 thread 表的运行态快照 DTO。
// 它承载 provider 会话、父子 agent、prompt 版本和恢复状态，供 UI 与模块共同读取。
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

// PromptSnapshot 保存一次 thread 启动或恢复时使用的 prompt 组装结果。
// 结构同时兼容旧字段解码，读取时会合并 legacy snapshot 后再返回。
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

// PromptBoundary 拆分 provider prompt 的缓存前缀和非缓存尾部。
// 空字段表示该快照没有可复用边界，调用方需要重新组装。
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

// UnmarshalJSON 解码JSON。
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

// mergeLegacyPromptSnapshot 合并legacyprompt快照。
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

// clonePromptSnapshotSectionMap 复制prompt快照sectionmap。
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

// RunningAgent 是 ListRunningAgents 返回的轻量运行中 agent 快照。
// 它只携带连接和状态字段，避免列表查询读取完整 thread 配置。
type RunningAgent struct {
	ThreadID string
	Port     int32
	PID      int32
	Status   string
}

// ThreadCwd 表示 thread 与工作目录的最小映射。
// cachekeepalive、选择器和 UI 可以用它按 cwd 聚合，而不需要读取完整 Thread。
type ThreadCwd struct {
	ThreadID string
	Cwd      string
}
