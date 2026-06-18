# ADR-002：V3 固化 dependency-aware cache 三分法（放弃 Claude name-only）

> 状态：✅ Accepted | 日期：2026-04-17 | 决策者：主线 | 相关：P18.4 §有意偏离项 F-4、P18.2 §A-2

## 1. 背景与决策窗口

P18.2 §A-2 已把 V3 的 prompt section cache 收敛为 `CacheByName` / `Uncached` / `InputScoped` 三分法；P18.4 §F-4 又把 parity 审查焦点重新压缩成一个问题：V3 是否要回退到 Claude 原生的 name-only cache。这个问题现在必须裁决，因为当前 HEAD 已经不是试验性分支，而是**代码实现、测试、失效触发点与会话交接文档都已按三分法闭环**。若继续维持“待决策”，后续维护者极易在“对齐 Claude”名义下把既有正确语义误改回 name-only。

更关键的是，V3 与 Claude 的架构前提不同。Claude 近似 single-provider / single-scope；而 V3 同一 thread 内允许 `claudecli` 与 `codexapp` 共存，Agent Memory 有 scope，Turn 还有 MCP delta、session flags、cwd/worktree 等变量。对 V3 而言，“同 section 名，不同结果”不是例外，而是常态；因此 cache 正确性必须围绕 dependency 建模，而不能只围绕名字建模。

## 2. 候选方案

### 方案 A：Claude 原生 “name-only cache”

机制是 `cache[section.Name] = result`。它依赖一个前提：同一个 section name 永远对应同一个结果。这个前提在 Claude 的单 provider 场景大体成立，但在 V3 中会系统性失效：

- `agent_memory` 会受 `agentMemoryScope` 影响，`project` 与 `local` 不能共 key；
- `memory_context` 依赖 thread/user input，不同 turn 天然应变；
- `env_info_simple` 受 provider、cwd、git root、worktree、language server tools 等影响；
- `session_guidance` 与 `mcp_instructions` 会随 enabled tools、session flags、MCP 连接状态变化。

因此，方案 A 的主要风险不是 cache miss 变多，而是**错命中后静默返回旧值**。

### 方案 B：V3 现有 dependency-aware 三分法

V3 当前实现由 `CachePolicy` + `sectionInputCacheKey()` 驱动：

```text
Uncached    -> 每轮重算
InputScoped -> section.Name + ":" + sha256(json(dependency))
CacheByName -> 有 dependency 时走 name+digest；否则退化为纯 section.Name
```

它的核心价值是把“结果真正依赖什么”写进 key，而不是把所有 section 一律粗暴地按名字缓存。`InputScoped` 解决显式输入依赖；`CacheByName` 保留稳定 section 的低成本路径，同时允许在必要时附着最小 dependency；`Uncached` 则留给高波动数据。除此之外，`inputScopedCacheDependency()` 还把 `agentMemoryScope` 专门并入 Agent Memory 的 dependency，避免跨 scope 复用。额外成本只是少量 `json.Marshal + sha256`，相对 prompt 组装与 provider 调用可忽略。

## 3. 决策

**选择方案 B（dependency-aware 三分法）作为 V3 canonical 方案，正式放弃 Claude 原生 name-only cache。**

理由如下：

1. **V3 架构决定了 name-only 不安全。** multi-provider + multi-scope 下，“同名不同结果”高频存在。
2. **CLI 侧完全透明。** 当前 cache 位于 `internal/module/prompt` 装配层，与 Anthropic/Codex CLI 自带 prompt cache 正交；这不是 CLI 限制问题，而是 V3 内部语义选择。
3. **成本可忽略。** `sha256(marshal(dependency))` 的成本远小于一次静默错命中的排障成本。
4. **已是仓库现状。** 类型、动态 section policy、失效入口、定向 invalidation 与单元测试都已落地；ADR 只是把事实标准升级为书面标准。
5. **更安全。** dependency-aware 至少保证“依赖变 -> key 变”可测试、可审计；name-only 则在错误场景下直接复用旧值。

## 4. 后果

- 新增 section 时，评审必须显式写出 `CachePolicy`，不允许依赖零值默认语义。
- 新增 `InputScoped` section 时，必须同步在 `inputScopedSectionDependency()` 登记决定结果的字段。
- `CacheByName` 的含义固定为“默认按名字缓存，但允许在必要时附着最小 dependency”，不再等同于绝对 name-only。
- 结构性事件继续走 invalidate reason；输入字段变化优先通过 dependency 变化自然换 key，不再鼓励手工改 section 名称做 cache bust。
- 后续相关测试需持续覆盖“依赖变 -> key 变、无关字段变 -> key 不变”。

## 5. 约束与边界

- `Uncached` 仅用于 volatile 数据；当前典型对象是 `mcp_instructions`。
- `CacheByName` 只有在 `cacheByNameSectionDependency()` 返回 `nil` 时才可退化为纯 name-only；一旦存在真实 dependency，就必须升级为 name+digest。
- `InputScoped` 的 dependency 必须保持“最小但充分”，禁止重新退化成全量输入 hash。
- 本 ADR 不引入 Claude 的 global cache block marker，也不改变 provider adapter；它只裁决 V3 自身的 section assembly cache 语义。

## 6. 源码锚点

- `internal/contract/prompt.go:169-175`：`CachePolicy` 类型与三个常量 `CacheByName` / `Uncached` / `InputScoped`。
- `internal/module/prompt/cache_keys.go:10-31`：`sectionInputCacheKey()`；其中 `Uncached` 分支在 `11-13`，`InputScoped` 分支在 `14-20`，`CacheByName/default` 分支在 `21-29`。
- `internal/module/prompt/cache_keys.go:33-49`：`inputScopedCacheDependency()`；`35-47` 对 `DynamicSectionAgentMemory` 额外拼入 `agentMemoryScope`。
- `internal/module/prompt/dynamic.go:53-69`：动态 section 与 policy 清单；`session_guidance`、`memory`、`agent_memory`、`memory_context`、`env_info_simple`、`language` 为 `InputScoped`，`mcp_instructions` 为 `Uncached`，`output_style` 为 `CacheByName`。
- `internal/module/prompt/dynamic.go:332-339`：`Uncached` section 被标记为 `Volatile`，走每轮重算路径。
- `internal/module/prompt/dynamic.go:365-442`：`inputScopedSectionDependency()`，登记 InputScoped section 的依赖字段，是新增 InputScoped section 的必改点。
- `internal/module/prompt/dynamic.go:444-506`：`cacheByNameSectionDependency()`，定义 `CacheByName` 在有 dependency 时升级为 name+digest、无 dependency 时退化为纯 name 的边界。
- `internal/module/prompt/invalidation.go:15-22`：`InvalidateSections()`，定向 section cache 失效入口；`24-46` 向实现了 `InvalidationAwareProvider` 的 provider 广播失效原因。
- `internal/module/thread/service.go:73-82`：thread 层统一的 `invalidatePromptAssembly()` 入口；P18.2-A3 登记的触发点经此进入 prompt cache。
- P18.2-A3 失效接线现状：`internal/module/thread/command.go:108-112`（`/clear`）、`internal/module/thread/history.go:313-316`（`/compact`）、`internal/module/thread/events.go:85-88`（worktree 切换）、`internal/module/thread/lifecycle.go:289-292` 与 `internal/module/thread/lifecycle_fork.go:157-160`（resume restore）、`internal/module/thread/command.go:233-235,270-272,309-311`（provider/setup flip）、`internal/module/memory/extract_runtime.go:358-366`（memory write 定向失效）。
- `internal/module/prompt/phasef_providers_test.go:76-96`、`98-112`、`114-140`：CachePolicy 单元测试，分别覆盖 `output_style`、`scratchpad` 与 `agent_memory scope` 的 key 语义。

## 7. 参考

- `docs/plans/迁移/p18/p18.2-core-alignment.md` §A-2（三分法由来）
- `docs/plans/迁移/p18/p18.4-claude-parity-gap-closure.md` §F-4、§有意偏离项
- `docs/plans/迁移/session-summary.md`（当前交接真值源，已把 CachePolicy 三分法写入交接建议）
- Claude 对照文档（沿用 P18.4 头部引用）：
  - `/Users/mac/Desktop/agnet/cluade/claude-code-sourcemap/docs/claude_memory_system_mapping.md`
  - `/Users/mac/Desktop/agnet/cluade/claude-code-sourcemap/docs/claude_memory_system_source_refs.md`
  - `/Users/mac/Desktop/agnet/cluade/claude-code-sourcemap/docs/claude_system_prompts_mapping.md`
  - `/Users/mac/Desktop/agnet/cluade/claude-code-sourcemap/docs/claude_system_prompts_source_refs.md`
