# 11A Memory 兼容边界代码地图

> 当前树已移除 memory 的持久化、检索、team-sync、auto-dream 及 host-tool 运行时实现。本卷不再把历史实现当作生产架构；历史设计和迁移记录继续留在 docs/plans/、docs/迁移/ 等历史材料中。

<!-- codemap-absent path="internal/module/memory/module.go" -->

## 1. 当前结论

memory 能力在当前版本只剩两类可审计边界：

1. `internal/contract/memory.go` 保留 DTO、错误和兼容接口声明，供旧快照、配置或外部调用方做显式分类；它不代表存在实现或 Fx 装配。
2. `internal/contract/memory_prompt_guard.go` 与 `internal/module/prompt` 保留旧 section 名称的兼容校验。`memory`、`memory_entrypoint`、`memory_context` 一旦出现在新 assembly，必须按 guard 规则拒绝或剥离，不能静默注入 provider 请求。

没有当前生产证据表明 durable memory 文件写入、相关检索、team memory 同步或模型主动 memory 工具仍可用。`memory_read`、`memory_write` 仅作为退役名称保留在测试/过滤规则中；它们不是可注册的 MCP 或 host-tool。

## 2. 保留的契约与 fail-fast 边界

### 2.1 memory contract

`internal/contract/memory.go` 定义以下兼容面：

- `MemoryService`、`AgentMemoryReader`、`AgentMemoryWriter`；
- `MemoryReadRequest/Result`、`AgentMemoryWriteRequest/Result`、`MemoryEntry`；
- memory scope/type 枚举与 `MemoryRuntimeUnsupportedError`；
- `TeamMemoryManager` 只读桥。

这些接口当前没有生产实现。调用方若误把旧实现注入运行时，应收到 `ErrMemoryRuntimeUnsupported` 或带 capability 的 typed error，而不是空结果或默认配置。

`internal/contract/thread.go` 的 `ThreadMetadataStore` 仍是 store/thread 的窄只读适配器，但当前没有 memory runtime consumer；不得据此恢复旧生命周期链。

### 2.2 prompt contract

`internal/contract/prompt.go` 继续定义 `PromptAssemblyService`、`DynamicSectionProvider`、`SectionInvalidator`、`ClaudeMdSourceProvider`、`TurnContextProvider` 等稳定类型。当前实现与接线集中在 `internal/module/prompt`：

- `internal/module/prompt/module.go` 提供 assembly、registry 和 invalidation 服务；
- `internal/module/prompt/service.go` 保留可选 ClaudeMd source registrar，但默认不注入旧 memory provider；
- `internal/module/prompt/assembler.go` 和 `internal/module/prompt/dynamic.go` 对 retired memory sections 执行边界校验；
- `internal/module/datasource`、`internal/module/datasource_v2`、`internal/module/personalization` 通过 `DynamicSectionRegistrar` 注册当前仍存在的动态 sections。

`internal/module/turn/service_helpers.go` 的 `syntheticMemoryContext` 只兼容检测旧 wiring；当检测到旧 `TurnContextProvider` 时返回 typed unsupported，避免把已删除 runtime 数据伪装成成功响应。

## 3. 当前 prompt / provider 组装路径

当前生产 prompt 路径是 prompt service → thread/turn → provider-neutral assembly/snapshot：

- `internal/module/prompt/assembler.go` 生成 `StartAssembly` / `TurnAssembly`；
- `internal/module/thread` 将 assembly 转成 provider DTO 和 resume/fork snapshot；
- `internal/provider/claudecli` 与 `internal/provider/codexapp` 消费同一份 assembly/snapshot 字段。

这条路径不包含 memory 文件扫描、retrieval prefetch、team sync 或 host-direct memory handler。需要新增持久化能力时，必须先建立新的 owner、契约和生产 caller，并同步 archtest、capcontract 与本卷；不能通过恢复历史文件名来绕过边界。

## 4. 退役能力的可验证事实

| 能力 | 当前状态 | 权威证据 |
| --- | --- | --- |
| durable memory 写入 / `MEMORY.md` 刷新 | 已移除 | memory runtime 目录不存在；仅保留 contract 声明 |
| relevant retrieval / transcript fallback | 已移除 | 无当前生产 finder/prefetch caller；旧 turn wiring 由 `syntheticMemoryContext` fail-fast |
| team memory / sync | 已移除 | 无当前生产 team module；`TeamMemoryManager` 仅为兼容 contract |
| auto-dream memory consolidation | 已移除 | dream 执行器边界由 `internal/contract/dream.go` 和 [12-dream-pipeline.md](12-dream-pipeline.md) 单独描述，不再归属于 memory runtime |
| `memory_read` / `memory_write` host tools | 已退役 | `cmd/mcp-orch/runtime_memory_test.go`、`cmd/mcp-orch/tools/memory_tools_test.go` 只验证不得注册 |

## 5. 与相邻卷的边界

- App 根组装和保留契约见 [04-app-contract.md](04-app-contract.md)。
- 当前 prompt、thread、turn assembly 和 snapshot 见 [11-prompt-thread.md](11-prompt-thread.md)。
- dream provider 执行链见 [12-dream-pipeline.md](12-dream-pipeline.md)。
- `cmd/mcp-orch` 工具注册表只记录当前存在的 orchestration/tool surface；不要把退役 memory 名称当作 MCP contract。

## 6. 变更验收

涉及 memory 兼容边界的变更至少运行：

1. `go run scripts/codemap_index.go --check`，确保本卷所有路径/符号仍可解析；
2. `go run ./scripts/archtestmap` 与 focused archtest，确保旧 runtime 没有通过新 import 或 Fx provider 复活；
3. `go run ./scripts/capcontract`，确保 capability manifest 与当前 registry 一致；
4. `go test ./internal/module/prompt ./internal/module/turn ./internal/contract`，验证 prompt guard 与 typed unsupported 行为。
