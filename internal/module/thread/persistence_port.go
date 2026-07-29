package thread

import (
	"context"
	"encoding/json"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// ThreadStore 是 Thread 业务消费的持久化端口。
// 具体 Store DTO 只能由组合根 adapter 转换后进入本模块。
type ThreadStore interface {
	GetByThreadID(context.Context, string) (*ThreadRecord, error)
	ListAll(context.Context) ([]ThreadRecord, error)
	ListConfigsByIDs(context.Context, []string) ([]ThreadRecord, error)
	Upsert(context.Context, ThreadUpsert) error
	SavePromptSnapshot(context.Context, string, PromptSnapshotRecord) error
	LoadPromptSnapshot(context.Context, string) (*PromptSnapshotRecord, error)
	UpdateStatus(context.Context, ThreadStatusUpdate) error
	DeleteByThreadID(context.Context, string) error
	CountChildren(context.Context, string) (int64, error)
	Exists(context.Context, string) (bool, error)
}

// ArchiveStateStore 原子维护 thread 状态和 binding 归档标记。
type ArchiveStateStore interface {
	SetArchiveState(context.Context, ArchiveStateUpdate) error
}

// BindingStore 是 Thread 业务消费的 session binding 持久化端口。
type BindingStore interface {
	GetByProviderThread(context.Context, string, string) (*BindingRecord, error)
	Upsert(context.Context, BindingUpsert) error
	DeleteByAgentID(context.Context, string) error
	UpdateSessionUUID(context.Context, BindingSessionUUIDUpdate) error
	UpdateProviderThreadID(context.Context, BindingProviderThreadIDUpdate) error
	GetByAgentID(context.Context, string) (*BindingRecord, error)
	ListAgentThreadBindings(context.Context) ([]BindingRecord, error)
	UpdateAgentCwd(context.Context, BindingCWDUpdate) error
}

// ThreadPageReader 是 ThreadStore concrete value 可选提供的全量分页能力。
type ThreadPageReader interface {
	ListPage(context.Context, contract.ThreadListPageParams) (contract.ThreadListPage, error)
}

// LoadedThreadPageReader 是 ThreadStore concrete value 可选提供的已加载分页能力。
type LoadedThreadPageReader interface {
	ListLoadedPage(context.Context, contract.ThreadListPageParams) (contract.ThreadListPage, error)
}

// ActiveThreadCounter 是 ThreadStore concrete value 可选提供的活跃线程统计能力。
type ActiveThreadCounter interface {
	CountActive(context.Context) (int64, error)
}

// ThreadRecord 是 Thread 模块读取的持久化快照。
type ThreadRecord struct {
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

// ThreadUpsert 描述 Thread 模块写入线程记录的完整字段。
type ThreadUpsert struct {
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

// ThreadStatusUpdate 描述线程状态写入。
type ThreadStatusUpdate struct {
	ThreadID  string
	Status    string
	UpdatedAt int64
}

// ArchiveStateUpdate 描述一次不可拆分的 thread/binding 归档状态变更。
type ArchiveStateUpdate struct {
	ThreadID  string
	AgentID   string
	Archived  bool
	UpdatedAt int64
}

// PromptSnapshotRecord 保存 Thread 恢复所需的 prompt assembly 快照。
type PromptSnapshotRecord struct {
	DisplayName           string                `json:"displayName,omitempty"`
	BaseInstructions      string                `json:"baseInstructions,omitempty"`
	Boundary              *PromptBoundaryRecord `json:"boundary,omitempty"`
	DeveloperInstructions string                `json:"developerInstructions,omitempty"`
	Provider              string                `json:"provider,omitempty"`
	Version               int                   `json:"version,omitempty"`
	Hash                  string                `json:"hash,omitempty"`
	SectionSnapshot       map[string]string     `json:"sectionSnapshot,omitempty"`
	Generation            uint64                `json:"generation,omitempty"`
}

// PromptBoundaryRecord 保存 provider prompt 的缓存边界。
type PromptBoundaryRecord struct {
	CachedPrefix string `json:"cachedPrefix,omitempty"`
	UncachedTail string `json:"uncachedTail,omitempty"`
}

// BindingRecord 是 Thread 模块读取的 provider session 绑定快照。
type BindingRecord struct {
	AgentID              string
	Provider             string
	ProviderThreadID     string
	CodexThreadID        string
	RolloutPath          string
	Cwd                  string
	ParentAgentID        string
	AgentType            string
	AgentMemoryScope     string
	Archived             bool
	CreatedAt            int64
	UpdatedAt            int64
	SessionUUID          string
	CodexHome            string
	ProviderRecoveryHome string
	CodexInstanceKey     string
	CodexModelProvider   string
}

// BindingUpsert 描述 provider session 绑定写入。
type BindingUpsert struct {
	AgentID            string
	Provider           string
	ProviderThreadID   string
	CodexThreadID      string
	RolloutPath        string
	SessionUUID        string
	Cwd                string
	ParentAgentID      string
	AgentType          string
	AgentMemoryScope   string
	CreatedAt          int64
	UpdatedAt          int64
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

// BindingSessionUUIDUpdate 更新 provider session UUID。
type BindingSessionUUIDUpdate struct {
	SessionUUID string
	UpdatedAt   int64
	AgentID     string
}

// BindingProviderThreadIDUpdate 更新 provider thread ID。
type BindingProviderThreadIDUpdate struct {
	ProviderThreadID string
	UpdatedAt        int64
	AgentID          string
}

// BindingCWDUpdate 更新绑定的工作目录。
type BindingCWDUpdate struct {
	AgentID   string
	Cwd       string
	UpdatedAt int64
}
