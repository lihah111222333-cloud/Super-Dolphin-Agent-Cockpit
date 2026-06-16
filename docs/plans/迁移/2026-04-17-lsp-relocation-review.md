# 2026-04-17 LSP 包搬迁复审记录

> 审查日期：2026-04-17
> 审查对象：`docs/plans/2026-04-17-lsp-package-relocation.md` 第 2 / 第 3 版，以及 Step 5 文档同步项
> 结论：**PASS**（阻塞项已关闭；非阻塞遗留见计划文档《遗留项》）

## 1. 背景与目标

- 背景：LSP 家族历史上沿用旧 `internal` 树，与 `cmd/mcp-orch/*` 的本地包布局不一致，archtest 与契约文档也沿用了旧口径。
- 目标：确认第 3 版计划是否已把守卫口径、family 隔离、fx import 白名单、文档同步与验证链路统一到迁移后的 `cmd/mcp-lsp/*` 布局。
- 范围：本 note 只记录计划审查、复审与文档收口结论，不替代实施计划本体。

## 2. 审查轮次清单

### 2.1 轮 1：第 2 版计划互审

| 审查者 | 视角 | 裁决 | 结论摘要 |
|---|---|---|---|
| `reviewer-A-contract` | 契约 / archtest | 阻塞 | 守卫口径应使用 effective lines；Step 0 强拆属无效工作；rule7/rule7b 的 `internal/tool/*` forbidden 已过时；rule10 对 `cmd/**` 全放行会漏检迁移后的 `cmd/mcp-lsp/*`。 |
| `reviewer-B-execution` | 执行 / 验证 | 需修复 | “42 Go 文件”统计错误，实测应为 68；Step 0 拆分粒度过粗；`go test` 需补 `-p 1`；`sed` / `git mv` / rule7b 兼容性可接受。 |
| `reviewer-C-docsync` | 文档 / 口径同步 | 需修复 | 漏 5 份文档（含 `v3-workflow`、`v3-migration-review-report`、`p9`、`modularity-convention §2.1`）；`ai-index` 应通过 `make codemap-refresh` 生成；不新增 ADR；`session-summary` 需追加，另建独立 review note。 |

### 2.2 轮 2：第 3 版 + 守卫放宽复审

| 审查者 | 视角 | 裁决 | 结论摘要 |
|---|---|---|---|
| `re-reviewer-A-guard` | 守卫 / spec | 需修复 | `v3-code-guard-spec.md`、`modularity-convention.md` 仍残留 `30/7161`、`44/12000` 等旧数字；`guardlib.go` core 分支存在死代码。 |
| `re-reviewer-B-plan` | 计划 / 规则设计 | 需修复 | rule10 文案会误伤 `internal/sidecar/orch/orchestration/service.go:20`；Step 2 分组表存在 `edit=0` 等实测错误；rule7 与 `mcp_family_isolation` 有重复维护面。 |
| `re-reviewer-C-docsync` | 文档 / 会话收口 | 需修复 | Step 5 需明确补 10 份口径说明、`session-summary` 模板与 review note 目录；`v3-code-guard-spec.md:150` 仍是旧数字；两份会话习惯文档第 71 行口径不一致。 |

## 3. 问题清单与修复记录

### 3.1 High

| 问题 | 来源 | 修复记录 |
|---|---|---|
| 守卫口径错误，按 raw lines 推出 Step 0 强拆 | 轮 1-A / 轮 1-B | 第 3 版改用 effective lines 口径，删除 Step 0，直接以前置守卫放宽承接迁移。 |
| family forbidden set 与 fx import 白名单仍按旧布局设计 | 轮 1-A / 轮 2-B | rule7/rule7b 改为禁止 `internal/module/*`、`internal/ui/*`、`internal/app` 与其他 `cmd/mcp-*`；rule10 收窄为仅装配文件及白名单场景可 import `fx`。 |
| 守卫 spec / 契约 / 实现数字不一致 | 轮 2-A / 轮 2-C | 守卫默认值统一到 `25 / 10000 / 600`，并要求同步 `guardlib.go`、spec 与契约说明。 |

### 3.2 Medium

| 问题 | 来源 | 修复记录 |
|---|---|---|
| 文件数统计与分组表误差（42→68，`edit=0` 等） | 轮 1-B / 轮 2-B | 第 3 版按实测刷新总文件数、旧路径命中数与分组表。 |
| 测试命令未串行，codexapp 并发测试抢端口 | 轮 1-B | Step 7 明确 `go test -p 1 ./cmd/mcp-lsp/...` 与 `go test -p 1 ./...`。 |
| `guardlib.go` core package 文件行数分支在守卫放宽后仅保留路由语义，需补明确落点避免误判为遗漏修复 | 轮 2-A | `internal/archtest/guardlib.go:293-295` 已注释说明：`MaxCorePackageFileLines == MaxFileLines == 600` 后该分支继续保留，用于 core package / freeze registry 路由及未来再次调整。 |
| 文档同步范围不足，漏 `v3-workflow`、`v3-migration-review-report`、`p9`、`modularity-convention §2.1` 等 | 轮 1-C / 轮 2-C | Step 5 扩成 10 份文档清单；`session-summary` 与独立 review note 单列；`ai-index` 明确走 `make codemap-refresh`。 |

### 3.3 Low

| 问题 | 来源 | 处理结果 |
|---|---|---|
| 是否新增 ADR | 轮 1-C | 明确不新增 ADR，本次只修订既有计划、契约、review note 与会话摘要。 |
| rule7 与 `TestMCPFamilyIsolation` forbidden set 有重复维护面 | 轮 2-B | 记录为非阻塞遗留；后续可抽共享 helper，但本次不反改规则主体。 |
| `cmd/mcp-orch/**` / `cmd/mcp-ida/**` 的 `fx` import 尚未收窄 | 轮 2-B | 记录为非阻塞遗留，留给各自 binary 的后续搬迁 / 瘦身计划。 |

## 4. 最终裁决

- **最终裁决：PASS — 可开干**。
- 通过依据：
  1. 守卫口径已统一到 effective lines，前置强拆从计划中移除。
  2. family 隔离、fx import 白名单、`TestCodeSizeGuard` / `TestMCPFamilyIsolation` 的设计已按迁移后路径重写。
  3. Step 5 文档同步范围已扩至计划要求的 10 份，并补齐 `session-summary` 模板与独立 review note。
  4. 验证链路明确为 `go build ./...`、`go test -p 1 ./...` 与关键 archtest 全绿。
- 非阻塞遗留：引用 `docs/plans/2026-04-17-lsp-package-relocation.md`《遗留项》，包括 rule7 / family isolation 重叠、`cmd/mcp-orch|mcp-ida` 的 `fx` import 收窄、历史计划文档旧守卫数字三项。

## 5. 落地 commit 清单 + 遗留项

> 主 agent 落盘时 Step -1 按 Agent-1 的实际提交节奏拆成 A/B 两步；最终为 5 个 commit。

- `ff4083d` **Step -1A 守卫常量 + autofix 串入**：`guardlib.go` 默认 400/15/4500 → 600/25/10000；`TestCodeSizeGuard` 前置调用 `AutoRepairFreezeRegistry`；`freeze_registry.go` 自动删 8 条过期 freeze；`freeze_registry_autofix_test.go` fixture 常量化。
- `ec96cab` **Step -1B 配套文档 + 计划本体**：`v3-code-guard-spec.md §1/§1.1`、`modularity-convention.md §2.4`、两份 `会话习惯.md`、`codemap/README.md`、本计划文档、中间态 `ai-index.json` 同步。
- `1bac4c1` **Step 1-3 搬迁**：LSP 10 个子包 `git mv` 迁入 `cmd/mcp-lsp/*`；跨平台 sed 改 import；入口 wiring / schema / tool registry 同步。
- `70bb462` **Step 4 archtest 重设计**：`rule7/rule7b` 路径改 `cmd/mcp-lsp` + forbidden set 换成 `cmd/mcp-orch` / `cmd/mcp-ida` / `internal/app` / `internal/ui/` / `internal/module/`；`rule10_fx_import_scope` 只收窄 `cmd/mcp-lsp/<子包>/`；`mcp_family_isolation_test.go` 三族 forbidden 同步；`guardlib.go` core 分支注释；`freeze_registry.go` RemoveWhen 15→25。
- `f3b228a` **Step 5 剩余文档同步**：`plans/{v3-workflow,v3-migration-review-report,v3-module-migration-details,p9,p19}.md`、`codemap/{03,06}.md`、`modularity-convention.md §2.1` 目录树、本 review note、最终 `ai-index.json`（`make codemap-refresh`）、session-summary `§2.3` 回填。
- 遗留项：引用 `docs/plans/2026-04-17-lsp-package-relocation.md`《遗留项》章节。
  1. archtest rule7 与 `TestMCPFamilyIsolation` forbidden set 重叠。
  2. `cmd/mcp-orch/**` 与 `cmd/mcp-ida/**` 子包的 `fx` import 未收窄。
  3. 历史计划文档残留旧守卫数字，统一由 `v3-code-guard-spec.md §1` 指向当前默认值。
