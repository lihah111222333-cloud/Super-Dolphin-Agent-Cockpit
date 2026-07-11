package uistate

import (
	"context"
	"encoding/json"
)

// PreferenceReader 是 uistate 读取单项项目偏好的领域端口。
type PreferenceReader interface {
	GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error)
}

// PreferenceStore 是 uistate 拥有的偏好持久化端口。
type PreferenceStore interface {
	PreferenceReader
	Upsert(ctx context.Context, params PreferenceUpsertParams) error
	List(ctx context.Context, cwd string) ([]PreferenceEntry, error)
}

// PreferenceUpsertParams 是单项偏好写入的领域输入。
type PreferenceUpsertParams struct {
	Cwd   string
	Key   string
	Value json.RawMessage
}

// PreferenceEntry 是 uistate 恢复项目偏好所需的最小领域视图。
type PreferenceEntry struct {
	Cwd   string
	Key   string
	Value json.RawMessage
}

// SharedFileReader 是 uistate 读取 LSP prompt hint 文件的领域端口。
type SharedFileReader interface {
	Get(ctx context.Context, path string) (*SharedFile, error)
}

// SharedFile 是 uistate 使用的共享文件最小领域视图。
type SharedFile struct {
	Path    string
	Content string
}

// BindingLookup 是 uistate 构建 agent/thread 投影所需的绑定查询端口。
type BindingLookup interface {
	ListAgentThreadBindings(ctx context.Context) ([]BindingEntry, error)
}

// BindingEntry 是 uistate 投影所需的绑定身份字段。
type BindingEntry struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	CodexThreadID    string
	RolloutPath      string
	SessionUUID      string
	CodexHome        string
	Cwd              string
}
