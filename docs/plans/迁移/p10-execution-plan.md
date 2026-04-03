# P10 执行计划 — 两级工厂（Zone A/B）丰满实施

> 生成时间：2026-03-21
> 前提：P0-P7 收官，P8/P9 编排+LSP 工具族待开工
> 来源：10 Agent 1:1 核查 `v3-two-zone-dry-enrichment.md` vs 实际代码
> 优先级：后补（P8/P9 完成后执行）

---

## 1. 核查结论摘要

| 维度 | 完成率 | 说明 |
|---|---:|---|
| Zone A shared 文件 | **3.8%** | 规划 10 文件 1570 行，实际 3 个 stub 59 行 |
| Zone A 框架承接 | **57%** | 3 ✅ + 3 ⚠️ + 1 ❌ |
| Zone B 模块结构 | **72%** | 平均合规度 ~9/12，主要命名漂移 |
| Rule of Two 候选 | **27%** | 1 ✅ + 6 ⚠️ + 4 ❌ |
| 架构守护测试 | **87%** | 7/8 存在但运行 FAIL |

**核心原因**：coderun/ida/tool/registry 三个模块在 P8/P9 才会落地，Zone A 的多项复用前置条件尚不满足。

---

## 2. Zone A — shared 文件实施清单

### 2.1 当前状态

| 文件 | 预算 | 实际 | 率 | 消费者数 | 阻塞项 |
|---|---:|---:|---:|---:|---|
| `retry.go` | 180 | 29 | 16% | 1 (codexapp) | 缺 IsTransient/策略/jitter |
| `validation.go` | 220 | 15 | 7% | 0 | RequireNonEmpty 无人用 |
| `idgen.go` | 80 | 15 | 19% | 2 (claudecli/thread) | dto/shared 有重复实现 |
| `pathscope.go` | 220 | 0 | 0% | — | 文件不存在，只 workspace 有路径逻辑 |
| `fileops.go` | 260 | 0 | 0% | — | 文件不存在，workspace/skill 语义不统一 |
| `jsonutil.go` | 140 | 0 | 0% | — | 文件不存在，uistate/rpc 有散落 clone |
| `cursor.go` | 150 | 0 | 0% | — | 文件不存在，真 cursor 只在 thread |
| `truncate.go` | 120 | 0 | 0% | — | 文件不存在，skill/turn 有截断 |
| `hash.go` | 80 | 0 | 0% | — | 文件不存在，只 workspace 用 sha256 |
| `errors.go` | 120 | 0 | 0% | — | 文件不存在，db/errors 才是真中心 |

### 2.2 散落复用逻辑清单（需收敛到 shared）

| 散落逻辑 | 当前位置 | 目标 shared 文件 | 复用模块数 |
|---|---|---|---:|
| `shouldReconnect(err)` | codexapp/recovery.go:102 | retry.go | 1 → 需 2+ |
| `isRecoverableDispatchErr` | platform/rpc/approval_support.go:100 | retry.go | 1 → 需 2+ |
| `firstNonEmpty` / `firstTrimmed` | thread/lifecycle_helpers + orchestration/rpc_types | validation.go | 2 |
| `normalizeHistoryLimit` | thread/history.go:53 | cursor.go (→ pagination.go) | 1 → 需 2+ |
| `normalizeRelativePath` | workspace/service_helpers.go:347 | pathscope.go | 1 → 需 2+ |
| `cloneMap` / `cloneRawMessage` | rpc/approval_support.go:161,184 | jsonutil.go | 2 (rpc + uistate) |
| `cloneJSONMap` / `cloneJSONValue` | uistate/preferences.go:201,212 | jsonutil.go | 1 → 需 2+ |
| `hashFile` / `hashFileIfExists` | workspace/service_helpers.go:361,374 | hash.go | 1 → 需 2+ |
| `clampString` (UTF-8 截断) | turn/assembler.go:259 | truncate.go | 1 → 需 2+ |
| `truncatePreview` (rune 截断) | skill/skills_meta.go:214 | truncate.go | 1 → 需 2+ |
| `clampLogLimit` | dashboard/service.go:254 | cursor.go (→ pagination.go) | 1 → 需 2+ |
| `NewID` (重复实现) | dto/shared/ids.go:10 | idgen.go (去重) | 3 (thread/turn/claudecli) |

### 2.3 实施波次

#### 波次 1 — 立即可做（P8 前置或并行，3 Agent）

| 任务 | 文件 | 预估行数 | 理由 |
|---|---|---:|---|
| idgen 去重 | idgen.go | +30 | dto/shared.NewID → shared.NewID 统一，加 ShortID/TraceID |
| jsonutil 新建 | jsonutil.go | +90 | CloneRawMessage/CloneJSONMap/TrimRawJSON，uistate+rpc 2 消费者 |
| retry 补能力 | retry.go | +100 | IsTransient/Policy/MaxDelay/Jitter/OnRetry |
| validation 补能力 | validation.go | +60 | ClampLimit/RequireNonEmpty 扩展，接入 thread/dashboard/workspace |

预估总量：+280 行，shared 目录从 59 → ~340 行（17% → 22%）

#### 波次 2 — P8/P9 后（等 coderun/ida 落地，4 Agent）

| 任务 | 文件 | 预估行数 | 触发条件 |
|---|---|---:|---|
| pathscope 新建 | pathscope.go | +150 | coderun/ida 需要路径校验时 |
| truncate 新建 | truncate.go | +80 | coderun 审计截断 + ida shell 输出截断 |
| pagination 新建 | pagination.go | +70 | tool/orch 分页复用后（改名自 cursor.go） |
| errors 新建 | errors.go | +60 | 跨域哨兵收敛，不动 db/errors |

预估总量：+360 行，shared 目录 → ~700 行（45%）

#### 波次 3 — 延后（等第二消费者，2 Agent）

| 任务 | 文件 | 预估行数 | 触发条件 |
|---|---|---:|---|
| fileops 新建 | fileops.go | +120 | ida 或 dashboard 需要原子写 |
| hash 新建 | hash.go | +50 | skill/sharedfile 需要内容指纹 |

预估总量：+170 行，shared 目录 → ~870 行（55%）

---

## 3. Zone A — 框架承接补完

| 项 | 当前状态 | 补完内容 | 归期 |
|---|---|---|---|
| typed request Validate() | ❌ 未落地 | 设计 `rpc.ValidatedHandler` 自动调用 params.Validate()，逐模块补 Validate 实现 | P10 |
| middleware.go 命名 | ⚠️ 落在 handler.go | 拆分：handler.go 只保留 ThreadHandler/StrictHandler，middleware 逻辑独立文件 | P10 |
| typed event 替代 | ⚠️ 部分 | 原计划推广 `NewTypedEmitter`（已在 P13 W1 删除）；如需替代 raw dispatcher，应改走现有 bus API | P10 |
| sqlc WithTx 收口 | ⚠️ 部分 | store 接口不再暴露 WithTx，改用 platform/db 事务闭包 | P10 |
| Zone A stub 清理 | ✅ 已完成 | 删除 legacy factory stub 全部占位文件 | P13 |

---

## 4. Zone B — 模块结构整改

### 4.1 命名漂移修正（低风险，可批量）

| 模块 | 现有文件 | 应改为 | 类型 |
|---|---|---|---|
| skill | skills_fs.go | loader.go | 重命名 |
| skill | skills_match.go | matcher.go | 重命名 |
| workspace | service_merge.go | merge.go | 重命名 |
| workspace | service_helpers.go | helpers.go | 重命名 |
| uistate | projector.go | projection.go | 重命名 |
| dashboard | ui_page.go | projection.go | 重命名 |

### 4.2 缺失文件补建（中风险，需设计）

| 模块 | 缺失文件 | 职责 | 优先级 |
|---|---|---|---|
| thread | events.go | 事件桥（当前散在 service.go） | P10 |
| thread | config.go | 配置逻辑（当前散在 command.go） | P10 |
| workspace | events.go | 事件桥（当前散在 service.go + helpers.go） | P10 |
| uistate | runtime.go | 运行时投影（当前混在 service.go） | P10 |
| orchestration | phase1_watcher.go | 阶段监控（完全缺失） | P10 |

### 4.3 module.go 纯化（需重构）

| 模块 | 问题 | 修复 |
|---|---|---|
| orchestration | module.go 混入订阅绑定 (line 29,56) | 提取到 events.go |
| uistate | module.go 混入 lifecycle/projection 注册 | 提取到 projection.go |
| skill | module.go 保留 newService wiring helper | 改用 fx.Annotate |

### 4.4 rpc.go 边界收紧

| 模块 | 问题 | 修复 |
|---|---|---|
| thread | rpc.go line 122+ 超出 handler.Map 装配 | 提取辅助到 rpc_helpers.go |
| orchestration | rpc.go line 98+ 做 submission 构造 | 提取到 rpc_helpers.go |

---

## 5. Rule of Two — 候选状态修正

| 候选 | 文档状态 | 实际状态 | 修正 |
|---|---|---|---|
| 路径 containment | "立即提升,3+消费者" | 只 workspace 1 个 | → "暂留，等 coderun/ida" |
| retry/backoff | "立即提升,3+消费者" | 只 codexapp 1 个 | → "波次1补能力，等第2消费者" |
| 文件原子写/hash | "暂留模块内" | 匹配，无原子写 | → 保持 |
| 审计截断 | "暂留 coderun" | coderun 不存在 | → "暂留，等 P8 coderun" |
| JSON clone | "候选" | uistate+rpc 2 消费者 | → "波次1 立即提升" |
| 分页 cursor | "候选" | 只 thread 1 处 | → "暂留，改名 pagination" |
| 状态机 graph/matrix | "立即放平台专包" | 只有轻量 factory | → "波次2 补 graph/matrix" |
| Tool schema enum | "不提升" | tool/registry 不存在 | → "等 P9" |
| 危险命令 gate | "暂留 coderun" | coderun 不存在 | → "暂留，在 dbquery/skill" |
| 线程别名归一化 | "候选" | ✅ 三模块本地兼容 | → 保持 |
| Event→RPC bridge | "立即放 bus" | 落在 platform/rpc | → 修正文档位置 |

---

## 6. 架构守护测试修复

| 项 | 状态 | 修复 |
|---|---|---|
| identifier_guard_test.go | ❌ 缺失 | 新建，检查命名规范 |
| TestCodeSizeGuard | ❌ FAIL (6 项违规) | 修复违规文件或调整 allowlist |
| TestDependencyDirection/rule5 | ❌ FAIL (6 项 store 边界) | 修复 store 导入边界 |
| dependency_direction_test.go | ⚠️ 10 条 vs 文档 11 条 | 文档修正（第 11 条已拆到 timeout） |

---

## 7. Agent 拆分方案

### P10 波次 1（P8 并行，3 Agent）
- Agent 1: idgen 去重 + jsonutil 新建
- Agent 2: retry 补能力 + validation 补能力
- Agent 3: archtest 修复（identifier_guard + CodeSizeGuard + DependencyDirection）

### P10 波次 2（P9 后，4 Agent）
- Agent 4: pathscope + truncate 新建
- Agent 5: pagination + errors 新建
- Agent 6: Zone B 命名漂移批量修正（6 文件重命名）
- Agent 7: Zone B 缺失文件补建（events.go ×2 + config.go + runtime.go + phase1_watcher.go）

### P10 波次 3（延后，3 Agent）
- Agent 8: fileops + hash 新建
- Agent 9: module.go 纯化 + rpc.go 边界收紧
- Agent 10: 框架承接补完（Validate/TypedEmitter/WithTx/Zone A stub 清理）

---

## 8. 验收标准

| 维度 | 目标 |
|---|---|
| Zone A shared 完成率 | ≥ 60%（波次1+2 后） |
| Zone A 框架承接 | 7/7 ✅ |
| Zone B 模块合规度 | 平均 ≥ 11/13 |
| Rule of Two 匹配度 | ≥ 70% |
| 架构守护测试 | 8/8 存在 + 全绿 |
| `go test ./internal/archtest/` | PASS |

---

## 9. 关键依赖

| P10 任务 | 依赖 |
|---|---|
| pathscope.go | P8 coderun 模块落地 |
| truncate.go | P8 coderun 审计截断需求 |
| fileops.go | P9 ida 文件操作需求 |
| Tool schema enum | P9 tool/registry 落地 |
| pagination.go | P8 tool 分页需求 |
