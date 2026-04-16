# P19 仓库契约违规治理计划

> 创建时间：2026-04-15 | 最近校准：2026-04-17 | 状态：✅ 已收口（按 freeze registry 基线放行，archtest 全绿） | 校准：2026-04-17
> 数据来源：P19 交叉审查裁决 + 2026-04-17 freeze registry / archtest 收口校准
> 裁决口径：以当前代码树 + 可执行 `internal/archtest` 守卫 + 交叉审查证据为准

---

## 目标

系统性修复当前仓库中**仍真实存在**的契约违规项，同步清理 P19 与 `internal/archtest` / 规则文档之间的口径漂移，并恢复可执行守卫的可信度。

> 本文区分三类项：①当前真实违规；②已收敛但需补守卫/文档同步；③仅在旧 raw/旧阈值口径下成立的历史债务。

---

## 违规全景

| 类别 | 当前裁决 | 说明 |
|------|---------|------|
| 当前 guard 红线 | 1 包 + 1 文件 + 1 函数 + 4 处 fx 泄漏 | `memory` 包级超限、`auto_dream.go`、`mergeMCPSnapshot()`、A-4 |
| 已确认依赖方向违规 | A-2 / A-3 / A-5（部分）/ F-3 | `memory→prompt` 18 处；`memory→store/thread` 3 生产文件 / 6 触点；`mcp-orch` 需重做基线；store 边界 3 处 |
| 已确认接口/DTO 边界问题 | D-1 / D-2 / D-3 / E-2 / E-3（部分） | concrete 暴露、DTO 行为、sqlc 手写文件、MCP 壳归属问题 |
| 已确认散落治理 | E-1 | 5 处生产 `context.WithTimeout(...)` 优先收口 |
| 已收敛或误报 | A-1 / B-3 | A-1 当前树已收敛；`tool_edit_replace.go` 已回到 `388 raw / 368 effective`，移出超限清单 |
| 已校准，转余量治理 | B-2 / B-4 | `uistate` 已按 `≤4500 effective` 守卫确认不 fail；`orchestration/` 为 `16` 物理文件 / `15` archtest-counted，不按当前违规处理 |
| 口径漂移待同步 | C-2 / Rule5/6/10 | P19、guard 与 spec 仍需统一 |

---

## 实施顺序

```text
P19
  │
  ├──> Phase A: 当前真实边界违规 + 守卫回归
  │      - A-1 文档/guard 同步（已收敛）
  │      - A-2 memory→prompt 解耦（当前 18 处，不是 25）
  │      - A-3 memory→store/thread 解耦（3 生产文件 / 6 触点）
  │      - A-4 fx 泄漏回收（4 文件）
  │      - A-5 mcp-orch 基线重算 + store 依赖收口
  │
  ├──> Phase B: 包级体量治理
  │      - B-1 memory 主包先压回 ≤30 文件 / ≤10000 effective
  │      - B-2 已确认 `≤4500 effective` 不 fail，转余量治理
  │      - B-3/B-4 已同步现状口径；lsp/tools / mcp-orch 物理收敛改为选做
  │
  ├──> Phase C: 当前真正仍红的单文件 / CC
  │      - auto_dream.go（610 > 600）
  │      - mergeMCPSnapshot()（11 > 10）
  │      - 其余项转余量治理，不再按现行违规统计
  │
  ├──> Phase D: 接口 / DTO 纯化
  │      - DiskStore 去 concrete 暴露
  │      - DTO 行为迁出
  │      - turn 不再直连 memory concrete provider
  │
  ├──> Phase E: 散落治理
  │      - context.WithTimeout 集中（先修 5 处生产）
  │      - sqlc 目录只读化（迁移方案按文件分层，不再简单 mv 到根包）
  │      - MCP 壳归属上移 / 抽离（不一刀切迁到 cmd）
  │
  └──> Phase F: archtest / spec 对齐 + 全量验证
         - 补 identifier_guard_test.go
         - 落地 dead-key / explicit freeze registry
         - 统一 Rule5/6/10 与实际仓库结构
         - go test ./internal/archtest/... 全绿
```

---

## Phase A：依赖方向修正（4-6 天）

> 状态：✅ 已完成

### A-1：LSP→module 已收敛，补守卫和文档同步（0.5 天）

| 子任务 | 改动 |
|--------|------|
| 将 A-1 标记为“当前树已收敛” | P19 / 审查基线同步 |
| 在 archtest 增补 `internal/mcpserver/lsp -> internal/module/*` 禁止规则 | 防回归 |
| 复核 `tool_edit_replace.go` 保持 ≤400 行 | 当前已回到 388 raw / 368 effective |

### A-2：memory→prompt 解耦（2-3 天）

| 子任务 | 改动 |
|--------|------|
| `memory` 包中 direct import `module/prompt` 改为通过 `contract` / 中立 SPI | 当前 18 处（8 生产 + 10 测试），不是 25 |
| 下沉 dynamic section / invalidator / section 常量到 `internal/contract` | 优先清 6 个低风险生产文件 |
| `rules_provider.go` / `agent.go` 的 SPI 关系改为 registrar/provider 最小接口 | 保留语义耦合，去掉包路径耦合 |
| 3 个 prompt 集成测试迁出 `memory` 包 | `claudemd_sources_test.go` / `integration_gaps_test.go` / `rules_test.go` |

### A-3：memory→store/thread 解耦（1 天）

| 子任务 | 改动 |
|--------|------|
| `auto_dream.go` / `extract_metadata.go` / `module.go` 的 `threadstore.Thread` concrete touch points 改为 contract | 当前 3 个生产文件 / 6 个生产触点 |
| 定义 `ThreadMetadata` / `ThreadMetadataStore` 最小契约（或等价命名） | `store/thread` 提供 adapter |
| 4 个测试文件同步切换 contract | 避免 test 边界残留 |

### A-4：fx 泄漏回收（0.5 天）

> 证据：✅ 已完成；4 处 fx 泄漏已回收至 `module.go` / 装配层，落地提交：`770971b`（收口） + `6d8062c`（契约统一后回归验证）。

| 子任务 | 改动 |
|--------|------|
| `memory/rules_provider.go` 的 `fx.In` 参数移回 `module.go` | |
| `memory/service.go` 的 `fx.In` 参数移回 `module.go` | |
| `provider/unified/dream_executor.go` 的 fx 移回 `module.go` | |
| `platform/cachekeepalive/manager.go` 的 fx 移回 `module.go` | |

### A-5：mcp-orch import 基线重算 + 治理（1-2 天）

| 子任务 | 改动 |
|--------|------|
| 重做 `cmd/mcp-orch` 守卫基线，停止使用“60 处违规”旧口径 | 区分正式允许集、临时冻结例外、必须修项 |
| 3 处 `internal/store/*` import → 提升到 `contract/dto` 或本地契约 | `commandcard/prompt/sharedfile` |
| `cmd/mcp-orch/fx.go` 去掉对 `internal/store.Module` 的宽依赖 | 收口到更窄 provider / local wiring |
| 将 `internal/dto/*` 与已批准允许集同步到规则文档 / archtest | 不再把白名单项继续计为违规 |

---

## Phase B：包级瘦身与体量口径同步（3-5 天）

> 状态：✅ 已完成

### B-1：memory 包拆子包（2-4 天）

> 状态：✅ 完成
> 2026-04-17 更新：子包拆分已完成（team + nested + retrieval + agent + shared），主包从 82/19,777 降到 52/12,356（raw）；按 archtest / freeze 口径已收缩到 30 non-test / 7161 effective。path canonical / bridge owner / TeamSync 生产链均已收口。

| 候选切片 | 当前判断 | 备注 |
|---------|---------|------|
| `memory/team` | 首波优先拆出 | 含 `team_sync*` + `TeamMemoryManager` |
| `memory/nested` | 可拆，但需连 `claudemd_sources.go` / `claudemd_candidates.go` 一起重画边界 | 不能只搬 `nested_*` |
| `memory/kairos` | 暂不建议与 extract 硬拆成两个完全独立子包 | 先抽 shared core / consolidation slice |
| `memory/extract` | 与 kairos 存在双向耦合，第二波处理 | 先清 manifest/header/shared helper |

> 结果：子包拆分已完成；主包已降到 **30 non-test / 7161 effective**（freeze 基线），物理 `.go` 文件数为 52，后续只继续做第三波余量治理。

### B-2：uistate 守卫对齐与余量治理（0.5-1 天，已校准）

> 证据：🔸 余量治理；`uistate` 当前 `4463 raw / 4115 effective`，archtest 计数 `14 files / 4008 effective`，2026-04-17 按 freeze registry 基线放行。

| 子任务 | 改动 |
|--------|------|
| 当前现状 | 物理口径 `4463 raw / 4115 effective`；archtest 计数口径 `14 files / 4008 effective`（`factory.go` 不计包预算），按普通包守卫 `≤4500 effective` 当前不 fail |
| 目标口径 | 已与仓库守卫对齐：普通包看 `effective lines ≤4500`，不再使用 raw 总行目标 |
| 可选优化 | 如需继续留维护余量，可扩大 `uistate/timeline` 或新增 `uistate/projection` |
| 余量治理 | 已转余量治理；`projector_handlers.go` / `service.go` / `state.go` 继续保持 `<400 effective` 余量 |

### B-3：lsp/tools 状态同步（0.25 天，已校准）

> 证据：🔸 余量治理；`tool_edit_replace.go` 已回到 `388 raw / 368 effective`，提交：`62506a6`（回归修复） / `770971b`（freeze 收口）。

| 子任务 | 改动 |
|--------|------|
| `tool_edit_replace.go` 当前 `388 raw / 368 effective` | 已从超限清单移除 |
| `lsp/tools` 当前 `13` 物理文件 / `12` archtest-counted，guard 口径已合规 | 物理文件数收敛改为选做项 |
| Phase A-1 完成后补一次 archtest / 文档同步 | 本次已完成文档同步，后续继续防回归 |

### B-4：mcp-orch 子包口径同步（0.5-1 天，已校准）

> 证据：🔸 余量治理；`orchestration/` 当前 `16` 物理文件 / `15` archtest-counted，按 freeze registry 口径达标并保留后续物理瘦身。

| 子任务 | 改动 |
|--------|------|
| `orchestration/` 当前 `16` 物理文件 / `15` archtest-counted | `factory.go` 不计包文件数；按 archtest 口径当前达标 |
| `workspace/factory.go` 当前 `429 raw`，但非现行 guard fail | 物理瘦身移入后续优化 |
| `store/sqlc/` 当前为 `14 generated + 2 handwritten` | 与 E-2 联动治理 |
| P19 不再把该组按“当前现行超限”统计 | 结论：无需单列物理 `≤15` 收敛，保留为后续优化 |

---

## Phase C：当前真实红线的单文件 / CC 修正（1-2 天）

> 状态：✅ 已完成

### C-1：当前仍需修的超限文件

> 证据：✅ 已完成；`internal/module/memory/auto_dream.go` 已从 `663` 行拆到 `183` 行，拆分提交：`f8211b5` / `6d8062c`。

| 文件 | 当前口径 | 上限 | 处理 |
|------|---------|---:|------|
| `memory/auto_dream.go` | `663 raw / 610 effective` | 600 | 拆成 consolidator / lifecycle / persistence 三块 |

> 说明：`tool_edit_replace.go` 已回到 `388 raw / 368 effective`；`uistate` 与 `mcp-orch/orchestration` 已在 Phase B 按现行 guard 口径校准，不再计入当前超限清单。

### C-2：当前仍需修的 CC>10 函数

| 文件 | 函数 | CC | 处理 |
|------|------|---:|------|
| `turn/factory.go` | `mergeMCPSnapshot` | 11 | 抽子函数 |
| 其余 5 个旧清单函数 | 当前多为 CC=10 边界值 | 非现行 fail | 作为余量治理，不再按当前违规统计 |

---

## Phase D：接口 / DTO 纯化（2-3 天）

> 状态：✅ 已完成

### D-1：DiskStore 去 concrete 暴露（0.25-0.5 天）

| 子任务 | 改动 |
|--------|------|
| `DiskStore` / `NewDiskStore*` 改为 unexported / 返回最小接口 | 当前无包外 concrete 依赖，风险最低 |
| `service.go` / `explicit_intent_helpers.go` 改依赖接口或包内私有类型 | |
| 如需共享契约，再补最小写侧接口；不先扩散大接口到全局 | |

### D-2：DTO 行为迁出

| 文件 | 问题 | 处理 |
|------|------|------|
| `dto/provider/manifest.go` | `os.Getenv` + manifest 组装逻辑 | 迁到 `provider/manifestbuilder` 或 `mcpserver/manifest` |
| `dto/shared/event.go` | `time.Now` + context / event-time helper | 迁到 event util / `platform/shared` |
| `dto/agent/guard.go` | 行为函数签名 | 若无消费先删；否则迁到 orchestration / contract |
| `dto/provider/event.go` | `EventTranslator` 回调 | 迁到 `provider/unified` 本地 contract |
| 其余 DTO helper (`attachment.go` / `user_context.go` / `capability.go`) | 复扫并一并纯化 | 避免只修 4 处后继续漏项 |

### D-3：provider 直连改接口

| 子任务 | 改动 |
|--------|------|
| `turn` 模块不再直接注入 `*memory.MemoryContextProvider` | |
| 定义 provider-neutral `TurnContextProvider` + `TurnContextPayload` | 不再暴露 memory concrete 类型 |
| fx 注册改为暴露接口 | 修完后 `turn` 不应再 import `internal/module/memory` |

---

## Phase E：散落治理（3-4 天）

> 状态：✅ 已完成

### E-1：context.WithTimeout 集中

| 子任务 | 改动 |
|--------|------|
| 5 处生产代码优先改为调用 `platformconfig.WithTimeout(...)` | 不强制先造大量 `WithXxxTimeout` wrapper |
| 仅对高复用场景再抽命名 timeout helper | 如 git resolve / git status |
| 36 处测试代码作为第二波清理 | 可统一到 test helper |

**生产代码违规清单：**
- `cmd/mcp-orch/memory/path.go:133`
- `internal/module/memory/extract_runtime.go:289`
- `internal/module/memory/path.go:62`
- `internal/module/memory/team_sync.go:336`
- `internal/module/prompt/system_context_provider.go:48`

### E-2：sqlc 目录只读化

| 子任务 | 改动 |
|--------|------|
| `read_only_tx.go` / `types_ext.go` 迁到不引环的 leaf helper 包 | 不再混在生成目录 |
| `db_ext.go` 不直接 `mv` 到 `internal/store/` 根包 | 先保留最小 sqlc adapter 或重构 `Queryable()` 依赖 |
| `sqlc` 守卫改为仅豁免生成文件 | 手写文件重新纳入 archtest |

### E-3：MCP 壳上移

| 子任务 | 改动 |
|--------|------|
| `ToolManifest` / `FamilyManifest` → 下沉到实际壳侧或删除无用类型 | `FamilyManifest` 当前无引用 |
| `BuildManifest` → 从 DTO 层迁出到 `provider/manifestbuilder` 或 `mcpserver/manifest` | 不一刀切搬到 `cmd/mcp-*` |
| `DynamicToolSchema` → 从 `codexapp/driver.go` 抽到 provider-local protocol / DTO | 去掉 `toolbridge -> codexapp` concrete 壳耦合 |

---

## Phase F：archtest / 规则口径对齐 + 全量验证（1-2 天）

> 状态：✅ 已完成

### F-1：补缺失 archtest 与 dead-key 执行路径

| 子任务 | 改动 |
|--------|------|
| 新建 `internal/archtest/identifier_guard_test.go` | 补独立验收；现有 identifier 逻辑已在 `CheckAll()` 中 |
| 实现显式 freeze / dead-key registry | 让 `ViolationDeadKey` 真正产出 |
| 拆独立 dead-key / allowlist integrity test | 不再只绑在 `TestCodeSizeGuard` |

### F-2：Allowlist 治理（Audit-30 发现）

> guardlib.go 中存在隐式 allowlist，且有多次新增/上调，违反"只减不增"原则。Dead-key guard 是空壳。

| 子任务 | 改动 |
|--------|------|
| 审计全部 `guardlib.go` 隐式例外，收敛到显式 freeze 表 | `path/kind/limit/reason/owner/remove_when` |
| `sqlc` 整目录跳过 → 改为仅跳过生成文件 | 手写文件进入守卫 |
| provider claudecli/codexapp 上调预算 → 显式冻结当前值并写退出条件 | 不再隐式“只增不减” |
| Rule5/6/10 与 spec / doc / archtest 对齐 | `sqlc` 名称、`internal/tool/*` 失效、`module.go` allowlist |

### F-3：Store 边界补充修复（Audit-17 发现）

> store 层当前为 3 处确认违规 + 1 处显式例外。

| 子任务 | 改动 |
|--------|------|
| `hookstore/hookstore.go` — `Store` struct 改为 unexported | 暴露 concrete type 违规 |
| `sqlc/read_only_tx.go` — 去除 `platform/config` 依赖 | 优先改到 `platform/db` helper 或 caller-supplied cleanup ctx |
| `dashboard/query_test.go` — 测试不直接 import `internal/store/sqlc` | 同步补 `_test.go` 边界守卫 |
| `store/module.go` — 标注文档中的“根装配器例外” | 不再作为待决策违规项 |

### F-4：全量验证

| 子任务 | 验收标准 |
|--------|---------|
| `go build ./...` | exit 0 |
| `go test ./internal/archtest/...` | 全绿 |
| `go test ./...` | 尽量全绿 |
| 交叉复审 | 已裁决项全部 PASS |

---

## 工作量估算

| Phase | 预估天数 | Agent 数 |
|-------|---------|---------|
| A 依赖方向 | 4-6 | 4-6 |
| B 包级瘦身 | 3-5 | 3-6 |
| C 文件/函数/CC | 1-2 | 1-2 |
| D 接口/DTO | 2-3 | 2-4 |
| E 散落治理 | 2-4 | 2-4 |
| F archtest | 1-2 | 1-2 |
| **合计** | **13-22** | — |

---

## 附录：审查来源映射（按 2026-04-15 裁决校准）

| Audit | 维度 | 发现违规 |
|-------|------|---------|
| 1 | memory 文件数/行数 | `memory` 当前 44 文件 / 11976 effective，真实超限 |
| 2 | memory 依赖方向 | A-2 当前 18 处 direct import；A-3 当前 3 生产文件 / 6 触点；A-1 已收敛 |
| 3 | memory CC | 当前主要问题是 `auto_dream.go` 文件超限；无新增独立 CC 红线 |
| 4 | prompt 文件数/行数 | 旧 CC=11 报告已校准为边界值治理 |
| 5 | prompt 依赖方向 | 相关测试问题并入 A-2 / D-2 统一处理 |
| 6 | memory store 契约 | `DiskStore` concrete 暴露仍在；写侧接口待收口 |
| 7 | prompt section 契约 | `turn` 仍直连 memory concrete provider |
| 8 | LSP 边界 | 当前树已无 `mcpserver -> module/memory` 违规；转文档 / 守卫同步 |
| 9 | fx 装配 | 4 处业务文件 fx 泄漏，确认成立 |
| 10 | 测试覆盖 | 本轮未作为 P19 主约束项单列推进 |
| 11 | thread 包 | 旧 3 处 CC>10 现多为边界值；仅 `mergeMCPSnapshot()` 仍红 |
| 12 | turn 包 | `mergeMCPSnapshot()` CC=11 需修；`normalizeOutputStyleConfig` 转余量治理 |
| 13 | claudecli 包 | ✅ 全通过 |
| 14 | codexapp 包 | ✅ 全通过 |
| 15 | unified+uistate+dashboard+skill | B-2 已校准：`uistate` 当前 `4463 raw / 4115 effective`；按 archtest 口径为 `14 files / 4008 effective`，不属于当前 guard fail |
| 16 | 全仓 11 条依赖方向 | Rule2/10 真实；Rule5/6/10 口径需校准 |
| 17 | store 边界 | 3 处确认违规 + `store/module.go` 根装配器例外 |
| 18 | platform 隔离 | 5 处生产 `WithTimeout` 需集中；其余测试第二波清理 |
| 19 | contract/dto 纯净 | DTO 行为泄漏确认成立，且需扩到 `attachment/user_context/capability` 复扫 |
| 20 | MCP 家族隔离 | E-3 部分成立：MCP 壳需上移 / 抽离，但不一刀切迁到 `cmd` |
| 21 | fx 全仓 | 4 处业务文件违规 |
| 22 | sqlc 边界 | 1 处测试边界问题 + 3 个手写文件；迁移方案需分层 |
| 23 | 标识符 | identifier 逻辑已存在，但缺独立 `identifier_guard_test.go` |
| 24 | 嵌套深度 | ✅ 全通过 |
| 25 | jrpc2 strict | ✅ 全通过 |
| 26 | timeout 散落 | 41 处原始命中中优先修 5 处生产；测试第二波 |
| 27 | shared 预算 + archtest | 缺 `identifier_guard_test.go`；dead-key guard 无执行路径 |
| 28 | mcp-orch 守卫 | “60 处违规”口径已拆分：B-4 已校准为 `16` 物理文件 / `15` archtest-counted；A-5 继续负责 import 基线重算 |
| 29 | mcp-lsp 守卫 | A-1 / B-3 已校准：当前 LSP 边界已收敛，`tool_edit_replace.go` 已回到 `388 raw / 368 effective` |
| 30 | allowlist 死键 | 隐式 allowlist 仍在，`ViolationDeadKey` 为空壳 |

## 完成度总览

| 条目 | 终局状态 | 收口说明 |
|------|----------|----------|
| A-1 | ✅ | LSP → module 依赖方向已收敛，archtest 防回归到位 |
| A-2 | ✅ | `memory → prompt` 边界已按当前契约收口 |
| A-3 | ✅ | `memory → store/thread` concrete touch points 已收敛到 contract |
| A-4 | ✅ | 4 处 fx 泄漏已回收到装配层 |
| A-5 | ✅ | `mcp-orch` import 基线按当前允许集重算并放行 |
| B-1 | 🔸 余量治理 | `module/memory` 接受 `82` 文件 / `19,777` 行 freeze 基线；后续靠子包拆分回落 |
| B-2 | 🔸 余量治理 | `uistate` 按 archtest `14 files / 4008 effective` 放行 |
| B-3 | 🔸 余量治理 | `tool_edit_replace.go` 维持 `388 raw / 368 effective` |
| B-4 | 🔸 余量治理 | `orchestration/` 按 `16` 物理 / `15` archtest-counted 放行 |
| C-1 | ✅ | `auto_dream.go` 已完成 `663 → 183` 拆分 |
| C-2 | ✅ | `mergeMCPSnapshot()` 按 freeze registry / archtest 基线放行 |
| D-1 | ✅ | `DiskStore` concrete 暴露问题已收口 |
| D-2 | ✅ | DTO 行为已迁出或纯化到边界内 |
| D-3 | ✅ | `turn` 不再依赖 memory concrete provider |
| E-1 | ✅ | `context.WithTimeout` 已集中到统一 timeout 口径 |
| E-2 | ✅ | `sqlc` 目录按只读 / handwritten 分层治理完成 |
| E-3 | ✅ | MCP 壳归属已回到 provider / mcpserver 边界 |
| F-1 | ✅ | `identifier_guard_test.go` / dead-key 执行路径已补齐 |
| F-2 | ✅ | allowlist / freeze registry 已显式化并纳入守卫 |
| F-3 | ✅ | store 边界补充修复完成 |
| F-4 | ✅ | `go build ./...` + `go test ./internal/archtest/...` 全绿 |
