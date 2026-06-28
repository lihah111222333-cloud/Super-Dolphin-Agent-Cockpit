package toolbridge

import (
	"context"
	"encoding/json"
)

// 本文件定义 toolbridge 消费 store/app 能力时使用的窄接口。
// handler.go 和 proxy.go 只依赖这些端口，具体 store adapter 由 app 装配层注入，
// 这样平台包不会反向导入 store 或 provider 实现。

// AgentThreadLookup 是 toolbridge 绑定 agent 到 thread 时需要的最小查询接口。
// 调用方只关心当前绑定关系，持久化结构和错误细节留给 adapter 处理。
type AgentThreadLookup interface {
	GetThreadByAgent(ctx context.Context, agentID string) (string, error)
}

// agentThreadLookup 是 Handler 字段使用的内部别名，保持生产端口和内部命名解耦。
type agentThreadLookup = AgentThreadLookup

// ToolCallBinding 是 toolbridge managed launch 注入上下文所需的绑定投影。
// 字段面向跨模块 wire 组装，避免 handler 依赖完整 store 行结构。
type ToolCallBinding struct {
	AgentID            string
	Provider           string
	ProviderThreadID   string
	CodexThreadID      string
	CWD                string
	ParentAgentID      string
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

// toolCallBinding 是 toolbridge 内部使用的别名，避免把导出类型名散落到私有实现里。
type toolCallBinding = ToolCallBinding

// ToolCallBindingLookup 扩展 agent/thread 查询，支持按 agent 或 provider thread 定位绑定。
// managed launch 依赖它恢复父子会话上下文，查询失败时由 handler 走 fail-fast 错误返回。
type ToolCallBindingLookup interface {
	GetBindingByAgent(ctx context.Context, agentID string) (ToolCallBinding, error)
	GetBindingByProviderThread(ctx context.Context, provider, providerThreadID string) (ToolCallBinding, error)
}

// toolCallBindingLookup 是 Handler 内部字段别名，用于收窄 app adapter 注入面。
type toolCallBindingLookup = ToolCallBindingLookup

// ThreadConfigOverrideStore 是 toolbridge 读取 thread 运行时配置覆盖的最小接口。
// 返回值保持 json.RawMessage，让 handler 自己解码运行时片段并在坏数据上阻断。
type ThreadConfigOverrideStore interface {
	GetConfigOverride(ctx context.Context, threadID string) (json.RawMessage, error)
}

// threadConfigOverrideStore 是 Handler 内部使用的配置覆盖端口别名。
type threadConfigOverrideStore = ThreadConfigOverrideStore

// UIPreferenceReader 是 managed child-agent launch 读取默认偏好的最小接口。
// 生产 adapter 返回全局偏好与 cwd 作用域偏好的合并视图，handler 不直接接触 UI store。
type UIPreferenceReader interface {
	GetMergedPreferences(ctx context.Context, cwd string) (map[string]any, error)
}

// uiPreferenceReader 是 Handler 内部使用的 UI 偏好端口别名。
type uiPreferenceReader = UIPreferenceReader
