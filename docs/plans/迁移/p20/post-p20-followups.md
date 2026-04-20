# P20 收官后隐患跟踪（5 条）

> 创建时间：2026-04-20 | 状态：P20 整套 16 单已合流完成，本文档记录**不阻塞上线**但需后续专单清理的隐患
> 关联文档：`README.md`、`status-checkpoint-2026-04-19.md` 第十四轮
> authoritative 原则：每条隐患**只在此处 single source of truth**；后续开新单时在本文档勾选"已关闭"而非另建重复 checklist

---

## 隐患 1 · 双 SkillCatalogProvider 并存（🔶 中）

### 问题描述

`internal/module/prompt/` 与 `internal/module/skill/` 两侧各自保留一份 `SkillCatalogProvider` 实现，都注册到 `DynamicSectionSkillCatalog` slot。

| 侧 | 文件 | 来源单 | 尺寸 |
|---|---|---|---|
| prompt | `skill_catalog_provider.go` | P20.1 Phase 8 | 17523 B |
| prompt | `skill_catalog_fx.go` | P20.1 Phase 10 | 6895 B |
| prompt | `skill_catalog_provider_test.go` | P20.1 Phase 8/10 | 18599 B |
| prompt | `skill_catalog_fx_test.go` | P20.1 Phase 10 | 5925 B |
| prompt | `skill_catalog_launch_test.go` | P20.1 Phase 10 | 2910 B |
| skill | `skill_catalog_provider.go` | **p20.5（authoritative）** | ~215 行 |

`prompt/dynamic.go:RegisterDynamicProvider` 内部实现是 `s.dynamic[name] = provider` **直接覆盖**，无 duplicate check。

### 触发条件

- **默认生产环境**（`ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=false`）：prompt-side `RegisterSkillCatalogProviderIfEnabled` 直接 no-op → 只有 skill-side 注册 → **无冲突** ✅
- **灰度启用**（flag=true）：两个 `fx.Invoke` 都跑 → 后声明者覆盖 → **行为模糊**（fx 声明顺序可能因 build/env 有差异）

### 修复策略（建议专单 `p21.x-cleanup-prompt-side-catalog`）

1. **删除**：`internal/module/prompt/{skill_catalog_provider.go, skill_catalog_fx.go, skill_catalog_provider_test.go, skill_catalog_fx_test.go, skill_catalog_launch_test.go}` 5 个文件
2. **修改** `internal/module/prompt/module.go`：移除 `NewCompositeNativeSkillDetector` + `NewSkillCatalogProviderFx` + `RegisterSkillCatalogProviderIfEnabled` 的 `fx.Provide`/`fx.Invoke`
3. **迁移** `NativeSkillDetector` / `SkillInjectionPortGroupTag` 相关 fx group 逻辑 → 如果 skill-side 需要消费，迁到 `internal/module/skill/` 或 `internal/contract/`
4. **回收** `internal/archtest/freeze_registry.go` 的 `internal/module/prompt` freeze 从 **28 → 26**
5. **验证**：archtest `TestCodeSizeGuard` 通过；flag=true 灰度下只有 skill-side 注册

### 预算
- 改动 ≤6 文件（含 freeze_registry.go）
- 主要是删除操作，实际新增代码很少
- 1 个 codex agent 可完成

### 优先级
**P1**（灰度 flag=true 前必须完成）

---

## 隐患 2 · 写端未切 v1 marker（🔶 低）

### 问题描述

p20.9 已补齐读端 dual-read + fail-open（`TrimInjectedSkillBlocks` 共享 helper），但 codex/claude 两家 writer 仍保留 **legacy** `[skill:name]\n摘要:\n使用方式:` 格式。

### 锚点

- `internal/provider/codexapp/skill_inject.go:100` `renderLegacySummarySkillBlock()` 保留 legacy 输出
- `internal/provider/claudecli/skill_inject.go:189` `renderLegacySummarySkillBlock()` 保留 legacy 输出

### 触发条件
- 生产环境现在全部走 legacy writer，不会产出 v1 marker
- 新格式 v1 `[skill:<name>::full@v1]\n...\n[/skill:<name>]` 仅被 reader 解析但 writer 不输出

### 修复策略（建议专单 `p21.x-writer-v1-marker-switch`）

**前置 gate**：
- p20.9 helper 在生产观测 **≥2 周**无误裁剪 / 误吞噬告警
- rollout metrics 显示 `skill_injection_decision_total{format="v1"}` 覆盖率准备就绪

**实施**：
1. 在 `internal/module/skill/rollout_markers.go` 添加 `RenderSkillBlockV1(name, body, summary, mode, version)` 纯函数
2. codex `skill_inject.go` + claude `skill_inject.go` 双 writer 在 **feature flag 控制下**切换：
   - `SKILL_WRITER_FORMAT=legacy`（默认）→ 现状
   - `SKILL_WRITER_FORMAT=v1` → 输出 `[skill:<name>::<mode>@v<ver>]...[/skill:<name>]`
3. 写端切换后，reader dual-read 自动兼容新旧 rollout
4. 全量切 v1 后，后续迭代可删 legacy `renderLegacySummarySkillBlock` 分支

### 预算
- 改动 ≤4 文件
- 1 个 codex agent

### 优先级
**P2**（shadow 2 周之后，非紧急）

---

## 隐患 3 · metrics 只落 no-op 骨架（🔶 中）

### 问题描述

p20.12 按 §8.3 审查口径落 backend-agnostic no-op recorder：**有常量 + 接口 + 默认 no-op 实现**，但无真实指标后端。

### 锚点

`internal/module/skill/policy_metrics.go`:
- L16-22: 7 个 snake_case 常量 `SkillMetricL1Tokens` / `SkillMetricExpandTotal` / `SkillMetricExpandErrorTotal` / `SkillMetricCacheHitTotal` / `SkillMetricCacheMissTotal` / `SkillMetricInjectionDecision` / `SkillMetricContextTokensSaved`
- L35: `type SkillMetrics interface { ... }`
- L43: `type noopSkillMetrics struct{}`
- L49: `func NewNoopSkillMetrics() SkillMetrics`

### 触发条件
- 灰度上线后**观测面板无数据**：无法验证 skill L1 token 实际消耗 / expand hit rate / injection decision 分布
- 对默认 flag=false 产品无影响

### 修复策略（建议专单 `p21.x-skill-metrics-wire`）

**前置 gate**：Prometheus/OTel 基础设施就绪

**实施**：
1. 选型：Prometheus（推荐，仓库已熟悉）或 OTel
2. 在 `internal/platform/metrics/` 新建（或扩展）counter/histogram recorder
3. 实现 `SkillMetrics` 的真实后端：`type prometheusSkillMetrics struct{ ... }`
4. 埋点：
   - `skill.Service.Expand()` → `skill_expand_total` + `skill_expand_error_total`
   - `skill.Service.ApprovalLookup()` → `skill_cache_hit_total` / `skill_cache_miss_total`
   - `turn.service.PrepareTurn()` resolve 后 → `skill_injection_decision_total{mode=Full|Summary|None}` + `skill_context_tokens_saved_total`
   - `prompt/skill_catalog_provider.go` 渲染后 → `skill_l1_tokens`
5. 在 `fx.Provide` 按 env 分流：`SKILL_METRICS_BACKEND=noop`（默认）/ `prometheus` / `otel`
6. 面板模板：Grafana JSON 至少覆盖 5 项核心指标

### 预算
- 改动 ≤8 文件（跨 skill + turn + prompt + 新 platform/metrics 子包）
- 1 个 codex agent

### 优先级
**P1**（灰度观测必需）

---

## 隐患 4 · orchestration agent report 通道异常（🔶 低）

### 问题描述

P20 施工期间 **p20.12** 和 **p20.16** 两个 agent 出现相同模式异常：
- agent 状态变为 `idle` 或 `thinking` 持续 >1 小时
- `orchestration_get_agent_report` 返回 **空字符串**
- 但 **代码已全部写盘**，经独立 go build / go test 验证合规

### 影响
- 不影响功能交付（代码已落）
- 但 parent 无法从 agent 自身确认完成，必须依赖独立验证
- 可能卡住 wakeup 循环（我本轮不得不主动 `orchestration_stop_agent` 清理 p20.16）

### 可能原因
- Agent 跑长 shell 命令（`npm test` 全量 / `go test` 全仓库）导致 stdout buffer 阻塞
- orchestration 层 report 持久化机制对超长运行任务有 timeout 未正确 fallback
- 两次都是 **skill 模块相关 shell 测试**（p20.12 跑 skill config 测试；p20.16 跑 guarded wrapper 跨 9 包）

### 修复策略（建议专单 `infra.orchestration-report-timeout`）

1. 复现：设计压测用例（让 agent 跑 `npm test` 全量 + `go test ./... -count=1`）
2. 检查 orchestration 层的 report persist 路径 + stdout pipe 处理
3. 补 **强制 timeout fallback**：agent 超过 N 分钟未出 report → 自动 flush 或标 `state=failed` 附 partial report
4. 增加 `orchestration_list_agents` 返回的诊断字段（`last_stdout_ts` / `stdout_buffer_bytes`）

### 预算
- 基础设施修复，不在 P20 范围内
- 跟踪到下次 orchestration 相关 P-plan

### 优先级
**P3**（不阻塞 P20 上线；下次用到 orchestration 并行跑长命令时处理）

---

## 隐患 5 · HEAD 8 条 archtest 历史债（🔶 低）

### 问题描述

P20 合流后 `go test ./internal/archtest -run 'TestCodeSizeGuard|TestDependencyDirection|TestFreezeRegistryIntegrity'` 始终报 **8 条违规**。逐条确认**全部**来自 P20 之前的历史债：

### 清单

#### CodeSizeGuard（5 条）

1. **`internal/module/prompt: 28 个 > 上限 26`**
   - 源：P20.1 Phase 8+10 新增 `skill_catalog_provider.go` + `skill_catalog_fx.go`
   - **归属：隐患 1 同步解决**

2. **`internal/module/skill/approval.go:106 NewApprovalCache(): CC 11 > 10`**
   - 源：P20 之前（commit 追溯：`approval.go` 在 p20.13 仅扩展消费，未改构造）
   - 修复：拆分 `NewApprovalCache` 成 load+validate 两段

3. **`internal/module/skill/skills_expand.go:160 ReadResource(): CC 11 > 10`**
   - 源：P20 之前（p20.10 引入 `Expand` 入口，未改 `ReadResource`）
   - 修复：提取 `normalizeReadResourceParams` helper

4. **`internal/module/skill/skills_expand.go:238 sliceMarkdownSection(): CC 11 > 10`**
   - 源：P20 之前
   - 修复：提取 `splitMarkdownHeadings` helper

5. **`internal/module/skill/trust.go:98 NormalizeArtifactLocator(): CC 20 > 10`**
   - 源：P20 之前（显著超标，CC 20）
   - 修复：拆 `NormalizeArtifactLocator` 成 3 段：kind 判定 / path 清洗 / alias 解析

#### DependencyDirection（3 条）

6. **`internal/module/prompt/skill_catalog_fx.go imports go.uber.org/fx outside module.go` (rule2)**
   - 源：P20.1 Phase 10
   - **归属：隐患 1 同步解决（删除此文件）**

7. **`internal/store/prompt/store.go imports github.com/jackc/pgx/v5 outside store boundary` (rule5)**
   - 源：P20.1（bug-prompts-list-handler 恢复时）
   - 修复：将 pgx 具体类型隔离到 `internal/store/prompt/pgx_adapter.go`，`store.go` 只暴露 `Reader/Store` interface

8. **`internal/module/prompt/skill_catalog_fx.go imports go.uber.org/fx outside an assembly entry` (rule10)**
   - 源：P20.1 Phase 10（与 #6 同文件）
   - **归属：隐患 1 同步解决**

### 修复策略（按归属分拆）

- **隐患 1 完成后**：自动解 3 条（#1, #6, #8）
- **`skill 模块 CC 清理`专单**：解 4 条（#2, #3, #4, #5）
- **`store/prompt 边界清理`专单**：解 1 条（#7）

### 总预算
- 2 个跟踪专单（skill CC + store boundary）各 ≤3 文件
- 隐患 1 专单解 3 条

### 优先级
**P2**（随时机方便清理，不阻塞上线）

---

## Follow-up 派单次序建议

| 次序 | 专单 | 闭合隐患 | 时机 |
|---|---|---|---|
| 1 | `p21.x-cleanup-prompt-side-catalog` | 1（+ 5 的 #1/#6/#8）| **灰度 flag=true 前必做（P1）** |
| 2 | `p21.x-skill-metrics-wire` | 3 | **灰度观测前必做（P1）** |
| 3 | `p21.x-skill-cc-cleanup` | 5 的 #2/#3/#4/#5 | 随时（P2） |
| 4 | `p21.x-store-prompt-boundary` | 5 的 #7 | 随时（P2） |
| 5 | `p21.x-writer-v1-marker-switch` | 2 | **shadow 2 周后（P2）** |
| 6 | `infra.orchestration-report-timeout` | 4 | 下次 orchestration 场景触发（P3） |

---

## 跟踪状态表

| # | 隐患 | 状态 | 专单 | 关闭日期 |
|---|---|---|---|---|
| 1 | 双 SkillCatalogProvider 并存 | ⚠️ 部分关闭 | 远程 `0b4ad39` 已删 `prompt/skill_catalog_fx.go`；`skill_catalog_provider.go` 仍双存 | 2026-04-20 |
| 2 | 写端未切 v1 marker | ⏳ 未开工 | - | - |
| 3 | metrics 只落 no-op 骨架 | ⏳ 未开工 | `policy_metrics.go` 骨架已落 (本分支)；真实 backend 接线仍待专单 | - |
| 4 | orchestration agent report 通道异常 | ⏳ 未开工 | - | - |
| 5 | HEAD 8 条 archtest 历史债 | ✅ **已全部关闭** | 远程 `72d3300` refactor 修 skill CC；prompt/fx scope + store/prompt pgx boundary 同步已清；archtest 全绿 | 2026-04-20 |

关闭本表记录时规则：在"状态"列改 ✅ + 填专单 commit hash + 日期；禁止删除条目（保留审计轨迹）。
