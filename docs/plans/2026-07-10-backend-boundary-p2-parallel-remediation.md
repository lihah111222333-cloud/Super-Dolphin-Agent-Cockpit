# Backend Boundary P2 Parallel Remediation Implementation Plan

> **For agentic workers:** 强制使用子代理驱动开发与 TDD；每个 worker 只在分配的 worktree 中修改、验证并提交。主 agent 保留 canonical registry 与最终集成裁决权。

**Goal:** 消除重新评分中的三个 P2：Provider 覆盖缺口、旧 procedural 边界事实双源、Skill 层直接依赖数据库与 Store。

**Architecture:** A 将 Provider 边界改为目录自动覆盖；B 将剩余跨层 import 事实收敛到 typed registry/evaluator；C 按洋葱架构把 Skill 的消费端端口、Store 实现与 App 适配拆开。三个分支允许在 `backend_boundary_registry.go` 出现小范围重叠，由主 agent 串行集成裁决。

**Tech Stack:** Go 1.25、AST/import archtest、Fx、SQLite、LSP MCP。

**Verification Surface:** `internal/archtest`、`internal/module/skill`、`internal/store`、`internal/app`、`make guard`、LSP diagnostics、`git diff --check`。

---

### Task A: Provider 生产树自动覆盖

**Files:**
- Modify: `internal/archtest/backend_boundary_registry.go`
- Modify: `internal/archtest/backend_boundary_guard_coverage_test.go`
- Modify: `internal/archtest/boundary_registry_test.go`
- Modify only provider-related hunks if needed: `internal/archtest/dependency_direction_test.go`

- [ ] 先写失败测试：`internal/provider/shared/leak.go` 导入 `internal/store` 与 `internal/platform/db` 必须被 canonical evaluator 拒绝。
- [ ] 运行测试并确认 RED 原因是 Provider pattern 只枚举 `claudecli/codexapp/unified`。
- [ ] 将 Provider 生产面改为 `internal/provider/**/*.go`，不得新增子目录枚举或静默排除。
- [ ] Provider external dependency 检查改为扫描整个 `internal/provider` 生产树；测试文件可按现有生产规则排除，`module.go` 只能保留明确的 Fx 装配语义。
- [ ] 增加实际目录/文件覆盖对账，新增 Provider 子包不得逃出规则。
- [ ] 跑 LSP 定位、引用、调用层级、精读和 diagnostics；跑 `./scripts/test_with_guard.sh ./internal/archtest -count=1`。
- [ ] 只提交 owned files，中文提交信息，报告 RED/GREEN、SHA、diff 与残余风险。

### Task B: 剩余 procedural 边界事实迁入 typed registry

**Files:**
- Modify: `internal/archtest/backend_boundary_registry.go`
- Modify: `internal/archtest/backend_boundary_evaluator.go`
- Modify: `internal/archtest/dependency_direction_test.go`
- Modify: `internal/archtest/backend_boundary_single_source_test.go`
- Tests may be added under: `internal/archtest/*boundary*_test.go`

- [ ] 先为旧 `storeBoundaryViolations`、`fxImportAllowed`、MCP server family、hooks/mcpcontrol 相互依赖编写 fail-first fixture，证明当前 policy facts 仍由 procedural slices/functions 持有。
- [ ] 用最小 typed rule kind、scope 与 owner 表达这些事实；不得为求 DRY 引入 `any`、字符串兜底或宽泛全局白名单。
- [ ] 消费测试只调用 canonical evaluator/rule ID，不再自行维护 import prefix/file allowlist。
- [ ] 扩展 single-source semantic guard，重命名、移动文件、局部常量和字符串拼接都不能重新引入同义 procedural evaluator。
- [ ] 保持生产行为不变；未知 kind/scope、零候选、语法错误继续 fail-closed。
- [ ] 跑 LSP 全证据、RED/GREEN、`./scripts/test_with_guard.sh ./internal/archtest -count=1`、`make guard`、`git diff --check`。
- [ ] 只提交 owned files，中文提交信息，报告 SHA 与迁移前后事实源清单。

### Task C: Skill 洋葱架构彻底去数据层直连

**Files:**
- Modify: `internal/module/skill/**`
- Create/Modify: `internal/store/skilltool/**`
- Modify: `internal/store/module.go`
- Create/Modify: `internal/app/*skill*adapter*.go`
- Modify: `internal/app/modules.go`
- Modify: `internal/archtest/backend_boundary_registry.go`
- Add focused tests in Skill/Store/App/archtest packages

- [ ] 先写失败架构测试：`internal/module/skill/**` 生产代码不得导入 `database/sql` 或任何 `internal/store`；三个 permanent exception 必须消失。
- [ ] Skill 定义消费端窄端口与领域 DTO；RPC/校验/业务编排留在 Skill。
- [ ] SQLite CRUD、建表与数据库错误处理迁到 `internal/store/skilltool`；Store 不得反向导入 Skill。
- [ ] `internal/app` 负责把 `internal/store/skilltool` 和 `auditlog` 映射为 Skill 消费端端口并通过 Fx 注入；不得在 Skill `module.go` 内适配 Store。
- [ ] 删除 `NewServiceWithDB` 和 `toolstore.New(*sql.DB)` 这类 DB 构造入口，改为端口注入；测试通过真实 Store 或窄 fake 注入。
- [ ] 删除 registry 中三个 Skill DB permanent exceptions，不得改成 temporary 或换路径隐藏。
- [ ] 保持 Skill Tool CRUD、动态工具 surface、RPC 错误映射、审计写入语义不变并补回归测试。
- [ ] 跑 LSP 全证据、RED/GREEN、Skill/Store/App 受影响包、`./scripts/test_with_guard.sh ./internal/archtest -count=1`、`make guard`、`git diff --check`。
- [ ] 只提交 owned files，中文提交信息，报告 SHA、端口映射、测试与残余风险。

### 主 agent 集成验收

- [ ] 逐分支先做规格符合性复核，再做代码质量复核；不使用额外审查 agent。
- [ ] 共享 registry 冲突由主 agent 按 owner、rule ID、scope、exception 生命周期裁决，不接受简单保留双方文本。
- [ ] 串行集成 A、B、C；每次集成后跑受影响包和 LSP diagnostics。
- [ ] 最终确认 Provider 全目录覆盖、旧 procedural facts 归零、Skill 生产代码数据层 import 归零、三个例外归零。
- [ ] 运行 `make guard`、`go vet ./cmd/... ./internal/... ./pkg/... ./scripts/...` 与可执行的仓库级门禁；既有无关失败必须用父提交对照证明。
