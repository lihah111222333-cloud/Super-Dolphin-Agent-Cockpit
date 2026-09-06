# Orch 子 agent 升级当前快照（2026-09-03）

这是截至 2026-09-03 的状态锚点，用来区分当前工作树事实与本目录中较早的历史观测。对象是
`/Volumes/hello/wj/super-agent-v3/.worktrees/20260831-orch-subagent-upgrade`；本快照不改变
任何 DAG、blocker 或发布状态。

## 当前决策

- 总计划仍为 `Active`；十四份 S0 子规格仍为 `Draft`；`current-blocker-registry.json`
  的 B01–B18 仍全部为 `needs_revalidation`。
- 所有新增实现、守卫、E2E 和 standalone 入口仍是
  `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` 的本地候选。strict `unified_v2`、Room、
  canonical cross-task send、managed provider、OUT/receipt/custody 生产接线、跨进程
  recovery，以及远程/原生 Windows/发布验收继续 `NO-GO`。

## 本次复核（2026-09-03）

| 检查 | 结果 | 解释 |
| --- | --- | --- |
| `make orch-upgrade-check` | `EXIT=0`（Python 29 + 9 + 6 tests，dependency-map check） | 仅证明规划输入、索引和派生物一致；仍是 planning-only `NO-GO`。 |
| `make project-map-refresh PROJECT_MAP_ARGS=--full && make project-map-check` | `EXIT=0`（5461 files，8 domains，11 generated files up to date，drift=OK） | 机器项目地图已按当前稳定源码刷新；只证明索引一致，不是发布验收。 |
| `make codemap-check` | `EXIT=0`（393 files，1474 refs，20 codemaps） | 代码地图派生物一致；不改变能力或发布状态。 |
| `./scripts/test_with_guard.sh --light-guard-only` | `EXIT=1`（production-ratchet findings=25：max_complexity 16、lines 1、missing_docs 3、max_struct_methods 1、naked_goroutines 2、raw_goroutines 2） | 棘轮阶段 fail-fast；本次没有修改冻结基线，也没有使用 `--no-verify`。冻结基线仍有 1 个未提交生成文件路径漂移，但本次命令尚未进入 drift 阶段。 |
| `GOWORK=off go test ./cmd/mcp-orch/... ./cmd/mcp-recovery-custody -count=1 -p=1` | `EXIT=0` | 当前源码普通包矩阵通过；仍是本机候选证据，不是发布验收。 |
| `GOWORK=off go vet ./...` / `GOWORK=off go build ./...` | `EXIT=0` / `EXIT=0` | 全仓静态检查和构建通过；不等于真实 provider、远程或发布验收。 |
| `GOWORK=off go test -race ./cmd/mcp-orch/... -count=1 -p=1` | `EXIT=0`（所有包 `ok` 或 `[no test files]`，无 `FAIL`/`WARNING: DATA RACE`） | 使用独立 `GOTMPDIR/GOCACHE` 的干净直接命令完成；此前单包 recovery-custody race 的 `query=2/finalize=1/effects=1` 已定位为调度敏感的测试计数假设，测试现只固定一份物理 effect。早先外层 zsh 包装因使用只读变量 `status` 自身退出 1，不是测试失败。 |
| LSP diagnostics（关键新增源文件） | 无 error/warning；仅现代化/样式 hints | 诊断不构成编译或合同失败。 |

旧文档中更早的 host-light、wrapper、full-archtest 耗时只保留作历史观测。尤其是“统一守卫
因 `freeze_baseline.json` drift 阻断”的描述不能替代本次输出：当前可复现的首要失败是上述
25 项棘轮恶化；`freeze_baseline.json` 本身仍处于工作树 dirty 状态。本次项目地图已在源码稳定
时刷新并通过 check；若后续再修改源文件，必须重新 refresh/check。

## 工作树与交付边界

- 当前分支为 `codex/20260831-orch-subagent-upgrade`，HEAD 与 `main` 同为
  `d95c63798`；该分支没有 configured upstream。
- 本次审查只新增/修改文档和候选守卫/测试，没有创建提交，也没有推送；工作树在审查前已有大量
  用户/代理变更（`git status --short` 约 297 条路径），均未在本快照中归属或清理。

本文件是状态说明，不是验收签名、receipt、授权或发布许可。后续若验证结果变化，应新增带
日期的快照或更新本文件，并同时保留可复查命令与失败输出。
