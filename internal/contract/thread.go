package contract

import (
	"context"
	"encoding/json"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// ThreadMetadata 分组定义 store/thread 暴露给外部模块的只读线程元数据。

// ThreadMetadata 携带 store/thread 外部消费者需要的只读线程字段。
// 外部模块只能读取该投影，不能借此绕过 thread 服务修改持久化状态。
type ThreadMetadata struct {
	ThreadID         string
	ParentAgentID    string
	AgentMemoryScope string
	Cwd              string
	CreatedAt        int64
	UpdatedAt        int64
	FinishedAt       *int64
	OwnerThreadID    string
	ConfigOverride   json.RawMessage
}

// ThreadMetadataStore 提供 memory 模块读取线程元数据所需的最小查询面。
type ThreadMetadataStore interface {
	GetByThreadID(ctx context.Context, threadID string) (*ThreadMetadata, error)
	ListAll(ctx context.Context) ([]ThreadMetadata, error)
}

// Thread 服务契约分组定义 thread 服务对其他模块暴露的窄接口。

// ThreadRef 是 thread.Ref 的窄投影，用于外部模块列表和摘要场景。
// 放在 contract 层可避免模块之间横向导入 thread 内部类型。
type ThreadRef struct {
	ID        string
	Name      string
	AgentID   string
	Status    string
	CreatedAt int64
	UpdatedAt int64
}

// ThreadLister 是 uistate 构建初始侧边栏所需的 thread 只读列表端口。
type ThreadLister interface {
	List(ctx context.Context) ([]ThreadRef, error)
}

// ThreadConfigReader 读取单个 thread 的有效配置。
// uistate config handler 通过该端口获取 model/approval 等配置，不依赖完整 thread.Service。
type ThreadConfigReader interface {
	GetConfig(ctx context.Context, threadID string) (dto.ThreadConfig, error)
}

// ThreadRuntimeConfigReader 是 ThreadConfigReader 的可选扩展。
// uistate 通过类型断言读取原始 runtime config；实现缺失时调用方必须走显式降级分支。
type ThreadRuntimeConfigReader interface {
	ReadRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error)
	ReadRuntimeConfigs(ctx context.Context, threadIDs []string) (map[string]map[string]any, error)
}

// ThreadStateConfigReader 读取持久化 thread runtime config。
// 该端口不要求存在活跃 provider session，适合启动恢复和 UI 初始状态场景。
type ThreadStateConfigReader interface {
	ReadThreadStateRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error)
}
