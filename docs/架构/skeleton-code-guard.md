# skeleton-code-guard.md — 代码守卫框架骨架

> **当前入口**: `scripts/test_with_guard.sh` / `scripts/test_with_guard.ps1`
> **当前规则核心**: `internal/archtest`
> **当前 CLI 适配**: `scripts/code_size_guard.go`
> **定位**: 把架构边界、代码尺寸、冻结基线、字段/协议守卫、前端关键守卫和 AI-maintenance gate 收敛成提交与推送前的准入框架。

---

## 0. 一句话定位

代码守卫框架不是产品运行时模块，也不是测试的替代品。它是仓库级准入层：

```text
guard rule source
  -> CLI / wrapper
  -> local command / hook / AI-maintenance gate
  -> fail-fast report
```

目标是让架构债务、生成物漂移、旧路径回流、字段遗漏和不可维护代码形状在进入提交或推送前暴露，而不是靠人工审查临时发现。

---

## 1. 分层结构

| 层 | 入口 | 职责 | 不负责 |
|---|---|---|---|
| 规则核心 | `internal/archtest` | Go 架构边界、代码尺寸、复杂度、冻结基线、priority SSA、字段/协议守卫 | 产品 runtime 行为、业务状态迁移 |
| CLI 适配 | `scripts/code_size_guard.go` | `check` / `--strict` / `--freeze` / 单文件模式；调用 `archtest.CheckAll` 和 freeze 流程 | 直接跑业务包测试 |
| 验证编排 | `scripts/test_with_guard.sh`、`scripts/test_with_guard.ps1`、`scripts/go_with_guard.sh` | 禁 raw `go test`、运行代码守卫、跑 `internal/archtest`、按需追加 copylocks 和目标包测试 | 自动修复违规 |
| Make 入口 | `make guard`、`make code-size-guard` | 给人工和脚本提供稳定命令 | 替代 hooks |
| Hook 准入 | `.githooks/pre-commit`、`.githooks/pre-push` | 提交前在 exact staged tree 运行代码守卫；推送前统一运行完整 remote ECI 门禁 | 让提交阶段启动完整 CI |
| 前端守卫 | `frontend-app/scripts/*guard*.mjs`、`frontend-app/package.json` | React/Vite 侧 critical skip、silent async failure、contract store、code-size、RPC contract 等校验 | Go 架构边界 |
| 代码地图/能力索引 | `docs/doc/codemap/*`、`docs/doc/codemap/project-map/*`、`docs/doc/codemap/capability-contract/*` | 给人和 AI 提供模块边界、文件索引、能力契约和漂移报告 | 代替源码/LSP 判断 |
| 生成物/AI gate | `scripts/refresh_generated_artifacts.sh`、`scripts/ai_maintenance_gates.sh`、`scripts/ai_maintenance/*` | codemap/project-map/capcontract 刷新与漂移检查、按变更路径推导 gate 和证据 | 代替源代码事实 |

---

## 2. 后端代码守卫主链路

`scripts/test_with_guard.sh --guard-only` 的后端主链路固定为：

```text
scripts/forbid_raw_go_test.sh
  -> go run ./scripts/code_size_guard.go
  -> go test ./internal/archtest -count=1
```

提交 hook 使用更轻的 `scripts/test_with_guard.sh --light-guard-only`。该入口只执行前两项中的入口守卫与 `scripts/code_size_guard.go`，不运行 `go test`、业务测试或其他完整门禁；它专用于 exact staged tree 的快速提交反馈。完整 `--guard-only` 仍属于重型验证，宿主机默认拒绝并要求进入 remote ECI。

普通包测试路径默认使用 `--quick-guard`：代码守卫之后只跑三项规范架构事实测试，再追加直接变更包、一级生产/测试反向依赖和命中范围内的 copylocks：

```text
TestDependencyDirection
TestValidateDefaultBackendBoundaryGovernance
TestBackendBoundaryRuleFactsHaveOneSource
go vet -copylocks <affected provider/platform/thread packages>
go test <direct packages + first-level reverse dependencies>
```

Go module、守卫 wrapper、`internal/archtest` 或代码守卫核心变化仍跑完整 archtest；普通变更在 pre-push 通过 `--archtest-only` 补齐剩余架构测试。`--race-only` 是独立推送 lane，不重复普通包测试。

单文件模式只对传入 `.go` 文件执行 `scripts/code_size_guard.go -- <files>`，用于快速反馈和分区 worker。

---

## 3. `internal/archtest` 规则核心

`internal/archtest/guardlib.go` 是通用代码守卫入口：

- `DefaultScanRoots()` 默认扫描 `internal`、`cmd`、`pkg`、`scripts`。
- `DefaultSkipDirs()` 固定跳过 `.git`、`node_modules`、`vendor`。
- `CheckAll()` 聚合 freeze integrity、源码扫描、包级统计和 post-scan 规则。
- `CheckFiles()` 提供单文件检查。

主要规则类型包括：

- 文件/函数有效行数、嵌套深度、圈复杂度、包文件数。
- 标识符下划线、函数注释门槛、dead key。
- onion / dependency direction / root bridge / MCP family / store/sqlc / toolbridge 等架构边界。
- silent fallback、parse fail-closed、naked goroutine、error string match、structured log 等风险模式。
- DTO / wire field registry、protocol、provider/runtime、frontend embed、skill path 等跨层契约守卫。

`internal/archtest/freeze_baseline.json` 是统一冻结文件；默认模式会阻断生产新违规，并收缩已改善的生产/测试 baseline。只有明确接受当前债务时才能用：

```bash
go run ./scripts/code_size_guard.go --freeze \
  --freeze-owner "<accountable-owner>" \
  --freeze-reason "<explicit-acceptance-reason>" \
  --freeze-reviewed-at "YYYY-MM-DDTHH:MM:SSZ" \
  --freeze-review-by "YYYY-MM-DD" \
  --freeze-fail-first "docs/guards/<fail-first-evidence>.txt"
```

`--freeze` 会改写统一冻结文件，五个审批参数缺一即失败。证据必须是仓库内规范相对路径，严格且唯一地保留
`source_head:`、`reviewed_at:`、`snapshot_sha256:`、`working_directory: .`、`command:`、`expected_exit: 1`、`observed_failure:` 七项；
未知、重复、空值或近似字段均会失败。`source_head` 只在执行 freeze 时绑定当时的当前 HEAD，`reviewed_at` 必须与审批参数一致，
`snapshot_sha256` 绑定写入 JSON 的不可变 `approved` 债务上界；不匹配时不会写 baseline。普通 guard 允许当前 baseline 自动收缩，
但新增条目、数值放宽或 priority SSA 内容变化只要超出 `approved` 就会 fail-closed。证据文件内容还由 SHA-256 防漂移。
冻结 JSON 出现未知字段、尾随内容、未来审批时间、超过 90 天的复审周期、已过复审日期、证据摘要漂移或超出审批上界时，
默认 guard、hook 和 CI 都会 fail-closed。

`--strict` 不使用 baseline，适合验证新规则是否已经全仓通过。

---

## 4. 前端守卫链路

当前前端只在 `frontend-app`。`npm test` 会进入 `npm run test:hook`，其关键 guard 链路是：

```text
npm run guard:critical-skip
  -> npm run typecheck:contracts
  -> npm run audit:rpc-contracts
  -> vitest run --maxWorkers=4
```

`guard:critical-skip` 当前包含：

- `scripts/no-critical-skip.mjs`
- `scripts/no-silent-async-failure.mjs`
- `scripts/frontend-contract-store-guard.mjs`
- `scripts/frontend-code-size-guard.mjs`

前端 guard 不写入 Go embed 产物；`npm run build` 通过 `scripts/sync-frontend-dist.mjs` 同步 `cmd/agent-terminal/web-dist`。

---

## 5. Hook 与 AI-maintenance Gate

`pre-commit` 是快速、本地且精确绑定 staged tree 的代码准入：

- 在仓库根目录 `.worktrees/` 下物化最终 exact staged tree，设置 `SUPER_DOLPHIN_GUARD_FAIL_ON_DRIFT=1`，且只执行 `scripts/test_with_guard.sh --light-guard-only`；该入口只运行 `scripts/code_size_guard.go` 静态代码守卫，不执行 `internal/archtest` 测试套件。
- 不读取 remote config、duration ledger 或 agent token，不调用 Gate CLI、生成器或 `remote hook pre-commit`，不运行 closure/map refresh、前后端完整测试、构建、race、SQL 或发布门禁。
- 守卫结束后删除临时 worktree，并复核真实 index tree 未在执行期间改变。

`pre-push` 统一接管完整 remote ECI 门禁，并按 push range 推导变更面：

- Go 代码：`local-fast` 先完成同 tree 的前端 test/build/embed；`pre-push` 只重复轻量前端+LSP、独占内存的完整普通后端与架构契约三分片，高内存 nilness 和独立 race 检查只进入 30 分钟 release profile。
- 前端代码：`local-fast` 运行 `npm run lint`、`npm run test:hook`、build 与 `make frontend-embed-verify`；`pre-push` 只重复 lint，完整测试证据继续由同 tree 回执约束。
- SQL/store：`make sqlc-verify-worktree`。
- project-map 在 pre-commit exact staged tree 上收敛；codemap/capcontract 及其他完整检查由 remote WorkloadCatalog 的对应 owner 执行，任何 drift 都 fail-fast。
- skill/doc skill surface：`python3 scripts/validate_super_agent_skills.py`。
- AI-maintenance 自身或相关路径：`scripts/ai_maintenance_gates.sh`。

`scripts/ai_maintenance/*` 只提供开发期 changed-file 计划与显式本地执行，推导 `backend:test_with_guard`、`backend:archtest`、`sqlc:verify`、`codemap:check`、`project-map:check`、按 registry 收窄的 `frontend:*` 和 `diff:whitespace` 等现行 owner。它不再提供本地 push profile、staged gate cache、prevalidated gate 或 closure 旁路；pre-commit 不运行该计划，pre-push/release 的完整门禁、PASS 复用、race、nilness、性能与 E2E 均以 remote WorkloadCatalog 和权威 SQLite receipt 为唯一事实源。

---

## 6. Codemap / Project Map / Capability Contract

代码地图是守卫框架的一部分，不是普通说明文档：

- `docs/doc/codemap/README.md` 定义阅读边界和分卷入口。
- `docs/doc/codemap/*.md` 提供人工审查和 AI 定位用的模块地图。
- `docs/doc/codemap/project-map/AI_PROJECT_MAP.md`、`AI_PROJECT_MANIFEST.json`、`AI_PROJECT_DRIFT.md` 和 `index/*.tsv` 提供文件级索引、能力标签和漂移证据。
- `docs/doc/codemap/capability-contract/capability_manifest.json` 提供核心 Go 领域的符号级能力契约。

这些索引的定位是“导航和漂移探针”：

```text
source tree / generator
  -> codemap / project-map / capability-contract
  -> guard or review routing
  -> LSP + source truth confirmation
```

规则：

1. 回答路径、影响面或架构边界问题时，先用 codemap/project-map 缩小范围，再用源码、测试和 LSP 确认。
2. codemap/project-map/capcontract 看起来过期时，优先修生成器或运行统一刷新入口，不手改生成内容。
3. pre-commit 在 exact staged tree 上检查 project-map；仅当输出区干净且发现漂移时，才由受信 Gate 刷新、暂存并对新 tree 复验。pre-push 的 codemap/capcontract 等完整检查由 remote WorkloadCatalog 唯一 owner 执行，任何 drift 都会阻断，不能以 warning 形成假绿。
4. AI-maintenance gate 把 `codemap:check`、`project-map:check` 和 `generated:source` 当成证据要求，避免 stale map 继续指导修复。

---

## 7. 设计规则

1. 守卫事实源必须靠源码、AST、schema、registry 或生成器反查；禁止把手写清单当唯一真值。
2. 守卫失败必须 fail-fast，输出可定位的文件、行号、规则和修复方向；禁止把超时或工具异常记为 PASS。
3. baseline/freeze 只能冻结已接受债务；新文件和已改善文件不能靠 baseline 绕过硬阈值。
4. 守卫不得导入产品 runtime 以制造副作用；`internal/archtest` 可读全仓，但不应成为业务依赖。
5. 生成物漂移要修生成器或刷新入口，不手改 codemap/project-map/capcontract 输出。
6. 前端、Go、SQL、skill、codemap 的守卫各管各面；不能用一个面的绿色结果替代另一个面的验证。
7. Hook 可以选择按路径收窄测试，但不能收窄掉全仓代码守卫和 diff whitespace 这类准入底线。

---

## 8. 新增守卫流程

新增或调整守卫时按这个顺序：

1. 在 `internal/archtest`、`frontend-app/scripts` 或 `scripts/ai_maintenance` 选择正确事实源位置。
2. 写 fail-first 测试，证明旧问题会被拦住。
3. 接入 `scripts/test_with_guard.sh`、`frontend-app/package.json`、Makefile 或 hook，避免只成为手动脚本。
4. 如果需要 baseline，先修能修的生产违规；剩余债务用 `--freeze` 显式记录，并解释原因。
5. 更新对应契约或架构文档，说明 owner、入口、失败语义和验证命令。

---

## 9. 验证命令

只改本文档：

```bash
git diff --check -- docs/架构/README.md docs/架构/skeleton-code-guard.md
```

改 Go 守卫规则、baseline、`scripts/code_size_guard.go` 或 `scripts/test_with_guard.*`：

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
make guard
```

改前端守卫：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

改 AI-maintenance / hook gate：

```bash
./scripts/test_with_guard.sh --host-test light ./scripts/ai_maintenance -run '^TestGatePlanProducerMatchesRunnerAndEvidenceRegistries$' -timeout=120s -count=1
./scripts/test_with_guard.sh --host-test light ./scripts -run '^TestAIMaintenanceGateSelectsMcpLSPResourceCohortE2E$' -timeout=120s -count=1
git diff --check
```

本地 host-test 只用于单个具名测试的非权威红绿；完整包与交付结论仍由正常 `git remote-ci push` 的 ECI receipt 给出。
