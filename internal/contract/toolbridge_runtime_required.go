package contract

import "errors"

// ErrThreadRuntimeRequired is the sentinel returned when a toolbridge
// tool call cannot resolve a thread identity to apply per-thread policy
// (e.g. spawn_agent policy enforcement). Callers must propagate it
// verbatim so the proxy layer can map it to a JSON-RPC invalid-params
// response instead of silently degrading to the global default.
//
// P22 P4 §276 / §fallback 缺失硬报错口径: 缺 thread / runtime / identity
// 时统一走 ErrXxxRequired / fail-closed; 不再通过 silent fallback 放宽
// trust-domain. Pre-P4 this sentinel lived in
// internal/platform/toolbridge/types.go (owner package); moving it to
// contract lets other consumers (e.g. mcp-orch RPC layer, future
// test harnesses) errors.Is-check the same authoritative sentinel
// without reaching back into the platform package.
var ErrThreadRuntimeRequired = errors.New("toolbridge: thread runtime is required")

// ErrPersistentSubagentRuntimeRequired is the sibling sentinel for the
// specific case where thread identity resolves but the thread store has
// no runtime config (so persistent_subagent_default cannot be
// evaluated). Same semantics and same ownership rules as
// ErrThreadRuntimeRequired.
//
// P22 P4 §99: PersistentSubagentDefault 只有在 thread 已成功解析且
// runtime 明确无本地配置位时才允许读取; thread/runtime 解析失败本身
// 就是 fail-closed, 不得借 "无配置" 名义偷回全局默认.
var ErrPersistentSubagentRuntimeRequired = errors.New("toolbridge: persistent subagent runtime is required")

// ErrPersistentSubagentFlagRequired is returned when a toolbridge
// spawn_agent policy check has a runtime config, but that runtime does
// not explicitly carry the persistent-subagent session flag. This keeps
// the runtime flag as the single source of truth; callers must not
// silently fall back to Agent.PersistentSubagentDefault unless the
// temporary toolbridge compatibility env gate is explicitly enabled.
var ErrPersistentSubagentFlagRequired = errors.New("toolbridge: persistent subagent flag is required")
