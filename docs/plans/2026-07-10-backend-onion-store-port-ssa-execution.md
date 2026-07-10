# Backend Onion Store Port and Boundary SSA Execution Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `internal/module` 生产代码中的 26 个 `internal/store` import 降为零，用消费端 Port 和 `internal/app` adapter 完成依赖反转，删除 Thread 的 `any` 端口，并把 backend boundary single-source 的调用连通性从直接 AST 标识符升级为窄 SSA 语义证明。

**Architecture:** 业务模块拥有 Port 与领域 DTO，`internal/app` 作为唯一同时知道 Module 和 Store 的桌面根装配层，负责逐字段 adapter 映射；Store 不反向依赖 Module。现有 AST 继续检测策略字面量、规则 ID 和字符串事实，只把函数调用边交给复用现有 loader 语义的窄 SSA 扫描；共享装载能力下沉到无父包依赖的 `internal/archtest/ssaload`，避免 external test package 访问不可见符号。

**Tech Stack:** Go 1.25、Fx、SQLite/sqlc Store、`go/ast`、`go/types`、`golang.org/x/tools/go/packages`、`golang.org/x/tools/go/ssa`、LSP MCP。

**Verification Surface:** `internal/module/{thread,cron,dashboard,datasource_v2,feedback,insight,memory,personalization,prompt,threadprompt,turn,uistate}`、`internal/app`、`internal/archtest`、洋葱/模块化契约、LSP diagnostics、`make guard`、`git diff --check`。

---

## 0. 冻结事实与非目标

### 当前事实

- 生产代码中共有 12 个 `internal/module/*/module.go` import `internal/store`，合计 26 个 import 点。
- 这 26 个点全部位于装配文件；本计划不重写 Service 业务算法。
- Thread 的 `threadServiceStorePort` 与 `bindingServiceStorePort` 已收窄方法数，但签名仍使用 `threadstore` / `bindingstore` DTO。
- `promptServiceCatalogPort` 当前为 `any`，而 Thread 已有三方法本地 `runtimePromptCatalog`。
- Thread 的分页、loaded-thread 分页和 active-count 是同一 concrete store 上的三个可选动态能力；迁移时必须保留，不能只搬走主接口十个方法。
- Thread `module.go` 直接调用 `threadprompt.NewRuntimeCatalog` / `RegisterProviders`，因此 Thread 与 threadprompt 不是可并行拆开的迁移单元。
- `backend_boundary_single_source_test.go` 当前只把 `*ast.CallExpr.Fun` 为 `*ast.Ident` 的调用记录为本地调用边。
- 当前有两个直接全仓装载点：`priority_ssa_scan.go` 与 `orchestration_service_wide_helpers_test.go`，两者都使用 `packages.LoadSyntax`、overlay 和 package-load fail-fast；本计划把共同装载原语收敛到 `ssaload`，不得保留或新增第三套直接全仓 loader。
- `dependency_direction_module_store_test.go` / `dependency_direction_test.go` 已有 procedural Module→Store allowlist/collector；新增 canonical rule 时必须迁移并删除这套重复事实源。
- pre-commit hook 会刷新并 stage generator-owned codemap/project-map；不得用 `--no-verify` 绕过，最终由主 agent 在集成树统一再生成和裁决。

### 非目标

- 不修改前端。
- 不以文件行数、函数长度或 codemap 生成物作为本计划的修复目标。
- 不把所有 Module Port 上提到 `internal/contract`；只有多个业务模块共同消费的稳定能力才能进入 contract。
- 不允许 `type ModuleDTO = store.DTO` 伪装依赖反转。
- 不把 single-source guard 改成全仓 SSA 扫描。
- 不新增 permanent/temporary exception，也不降低现有 guard baseline。
- 不改变 RPC wire shape、数据库 schema、SQL 查询、错误码或运行时 optional 语义。特别地，builtin-only prompt catalog 的 `CanInsertPromptVersion()==false` 是合法只读能力，不是构造失败。

## 1. 目标依赖与文件结构

```text
internal/module/thread
  ├── module.go                 # 只声明 Fx Module 和领域构造器
  ├── persistence_port.go       # 消费端 Port + 领域 DTO
  └── service / handlers        # 只依赖领域 Port

internal/app
  ├── thread_store_adapters.go   # 同时 import module 与 store，逐字段转换
  └── modules.go                 # 主 agent 串行登记 adapter providers

internal/store/thread
  └── Store 实现                # 不 import internal/module
```

依赖方向固定为：

```text
Store implementation <- App adapter -> Module-owned Port
```

## 2. 多 Agent 执行拓扑

主 agent 先创建干净集成 worktree，在该树串行完成基线、测试守卫拆缝和 owned-file 冻结；守卫拆缝提交 GREEN 后，再从同一 seam SHA fork 三个隔离 worktree：

**本次执行绑定：** 只派发一个执行子 agent，禁止该子 agent 再 spawn/fork 其他 agent。三个 lane worktree 仅用于文件与提交隔离，不代表三个并发 worker；该子 agent 按 Task 0 → A → B1/B2/B3/B4 → C → D → E 串行执行，并在每个 checkpoint 报告 SHA、diff、RED/GREEN 和 LSP 证据后停止等待主 agent 复核/继续指令。主 agent 不在子 agent 的 owned worktree 内并行写代码，只做巡查、纠偏、扩写复核、最终合并与推送裁决。

| Lane | 任务 | 独占范围 | 禁止修改 |
|---|---|---|---|
| A | Thread + threadprompt Port/DTO、移除 `any`、对应 App adapters | `internal/module/{thread,threadprompt}/**`、`internal/app/thread_store_adapters_module.go`、新建 `internal/app/thread*_adapters.go`、Thread/threadprompt focused tests、预拆出的 Thread guard 文件 | `internal/app/modules.go`、canonical registry、契约文档、Lane C SSA 文件 |
| B | 其余 10 个 Module 去 Store import | 对应 10 个 `internal/module/*`、`internal/app/business_store_adapters_module.go`、新建 `internal/app/{cron,dashboard,datasource_v2,feedback,insight,memory,personalization,prompt,turn,uistate}_store_adapters.go`、focused tests、预拆出的 dashboard guard 文件 | `internal/app/modules.go`、canonical registry、Thread/threadprompt、`internal/module/prompt/service_surface_list_test.go`、Lane C SSA 文件、契约文档 |
| C | single-source 窄 SSA 与全仓装载原语收敛 | 新建 `internal/archtest/ssaload/**`、`priority_ssa_scan.go`/helper、`orchestration_service_wide_helpers_test.go`、`backend_boundary_single_source_test.go`、新 SSA helper/fixture | Module、App、canonical registry、契约文档、A/B guard 文件 |

主 agent 独占：

- `internal/app/modules.go`
- `internal/archtest/backend_boundary_registry.go`
- `internal/archtest/backend_boundary_canonical_dependencies_test.go`
- `internal/archtest/dependency_direction_module_store_test.go`
- `internal/archtest/dependency_direction_test.go`
- `docs/契约/modularity-convention.md`
- `docs/契约/onion-architecture-convention.md`
- 最终 merge、冲突裁决、全量验证和 push

以下路径是 hook-owned shared exception，不归任何 worker 独占，也不因 hook 自动 stage 而判定越界：

```text
README.md
docs/doc/codemap/13-archtest-boundaries.md
docs/doc/codemap/README.md
docs/doc/codemap/ai-index.json
docs/doc/codemap/project-map/**
```

worker 必须报告 hook 自动 stage 的精确清单。主 agent 合并 lane 时只对这些生成路径使用固定策略：保留当前 integration parent 的版本，先合入 source/test，再从当前集成源码重新生成；禁止手工拼接 JSON/TSV。任何非上述路径的冲突仍按 owned-file 规则裁决。

本计划不预设额外只读审查 agent。主 agent 必须全量复核每个 lane 的 diff、LSP 证据、RED/GREEN 日志和提交边界。唯一执行子 agent 不得自行合并到 `main` 或推送；Task D/E 中写作“主 agent”的动作由根主 agent在复核通过后执行，或由根主 agent 明确下发单步授权后让该子 agent 在 integration worktree 执行。

Lane B 不得用一个超长 agent turn 一次迁完 10 个模块。主 agent 按以下 checkpoint 发送继续指令，同一 worktree、同一 branch 顺序推进：

```text
B1 feedback + insight + personalization + turn
B2 cron + memory
B3 datasource_v2 + prompt + uistate
B4 dashboard
```

每个 checkpoint 完成后先复核 diff 和 focused tests；未完成 checkpoint 的 agent 必须收到继续任务指令，不能以部分 GREEN 宣称 Lane B 完成。

### 主 agent 巡查规则

- 唯一执行子 agent 每次进入 lane 后立即记录 `agentid`、worktree、branch、base SHA、owned files。
- 每完成一个模块迁移，worker 必须报告当前 `rg internal/store` 剩余数和 focused test。
- 发现 worker 修改主 agent 独占文件，立即停止该 worker，备份 diff，只把越界文件恢复到 lane base，再下发纠正指令。
- adapter 扩写必须由主 agent 检查 DTO 字段完整性、错误映射、nil/optional 语义和 Store 反向依赖。
- worker 不得自行合并；只提交原子 commit 并报告 SHA。

### 双 Agent 全量复核后的主 agent 裁决

两名只读 reviewer 均对整份计划、当前源码和现有 guard 做了全量复核，结论都是“方向正确，但原稿不可直接执行”。主 agent 的裁决如下：

| 发现 | 裁决 | 文档修正 |
|---|---|---|
| Thread 与 threadprompt 被拆到 A/B，两个 lane 无法独立收敛 | 接受 | 两者统一归 Lane A；Lane B 改为 10 模块 |
| Thread 被拆成五个必需持久化 Port 过度设计 | 接受 | 保留 `ThreadStore` / `BindingStore` 两个根 Port，另保留三个 typed optional capability |
| 分页、loaded page、active count 三个动态能力可能丢失 | 接受 | adapter concrete 必须实现且测试存在/缺失路径 |
| builtin-only catalog 的 `CanInsertPromptVersion=false` 被误判构造失败 | 接受 | false 视为合法只读能力，仅实际写入时报 capability error |
| 新 canonical rule 与旧 procedural allowlist 会形成双事实源 | 接受 | 改写现有 guard 文件并删除旧 collector/allowlist |
| external `archtest_test` 无法访问父包未导出 loader | 接受 | loader 下沉到独立 `internal/archtest/ssaload` 子包 |
| testdata fixture 未真正进入 `packages.Load` syntax | 接受 | 每个 case 建独立 fixture package，精确加载并验证 syntax 来源 |
| interface invoke 枚举所有实现会误报 | 接受 | 依据 receiver value provenance 收敛，并增加多实现/未解析 fixtures |
| 原计划使用不存在的 priority test 名 | 接受 | 采用真实 `TestPrioritySSAGuardsUseUnifiedFreezeBaseline`，先 `-list` 防零匹配 |
| 共享 guard 文件会导致 lane 越界/冲突 | 接受 | 主 agent fork 前机械拆缝并以 GREEN seam SHA 作为三 lane 共同 base |
| pre-commit 生成物与 worker“不得含 generated”冲突 | 接受 | 不绕 hook，主 agent 集成时统一再生成并把生成物作为独立 evidence |
| A/B 移走 provider 后不能独立通过 App Fx graph/pre-commit | 接受 | fork 前预拆两个 App `fx.Option` bundle，A/B 独占填充并各自跑全图闭合测试 |
| 现有 orchestration wide guard 仍有第二套全仓 `packages.Load` | 接受 | 纳入 Lane C，共用 `ssaload` 并验证候选集合不漂移 |
| exported Port 使用 private DTO/sentinel，App 无法实现 | 接受 | Cron/Prompt/Turn 显式导出完整 Port surface，并增加 App 包编译期接口满足性测试 |
| unresolved call 只检查本地持有 fact 的函数仍可被 bridge helper 绕过 | 接受 | 对 fact-bearing root 的整个本地可达子图 fail-closed，增加函数参数桥接 fixture |
| SSA test-list 管道与单个正则可假绿 | 接受 | 统一使用 guarded test-list，并分别断言两个测试名各恰好一次 |
| worktree 相对路径和未 fetch 的 `origin/main` 证据不稳定 | 接受 | 固定主仓绝对路径；基线、推送前、推送后分别 fetch/远端 SHA 校验 |
| 当前存在 threadprompt 两个 Hint 与 dashboard 一个 Hint | 接受 | 主 agent 在共同 seam 中行为等价修复，dashboard 保持现有 JSON wire shape，fork 前 diagnostics 全零 |
| threadprompt exported Port 仍使用 private `promptListFilter` | 接受 | Lane A 导出 `PromptListFilter`，typed constructor/catalog 全部只使用本地 Port，并增加 App 外包编译断言 |
| 新 canonical rule 未进入 backend governance surface | 接受 | Task D 同步修改 governance source/test，并增加 orphan canonical rule 自守卫 |
| 旧 connected/unrelated 内存 AST 测试无法证明新 SSA | 接受 | 改为真实可加载 fixture package，旧测试名只保留为 SSA analyzer 回归入口 |
| 最终只测 module/app/archtest，不能证明反向消费者可编译 | 接受 | push 前 `make build-plain` 与 `make test` 都是硬门禁 |
| 多命令 shell、零匹配和跨阶段变量可能假绿 | 接受 | controller 使用持久 PTY；每个执行块 fail-closed，`rg` 显式区分 0/1/>=2，关键 SHA 每阶段重算断言 |
| controller block 标注 Bash 却混入 zsh 数组拆分与只读变量名 | 接受 | 全文锁定 macOS Bash 3.2；数组逐行读取、0 下标；扫描退出码变量统一为 `rg_status` |
| worktree add 后未进入 integration tree，相对命令可能落到 dirty `main` | 接受 | 创建后立即 `cd`，并在 Task 0/D/E 每个相对执行块调用 `assert_integration_tree` |
| guard/App seam 只描述未证明 committed | 接受 | 精确 stage、原子提交、clean/parent/required-path/allowlist 证明；lane base 固定为 `SEAM_SHA` |
| Thread prompt adapter 的编译期断言指向错误接口 | 接受 | 分别断言 `threadprompt.PromptStore` 与 `thread.PromptCatalog` |
| `packages.LoadSyntax` 不保证提供 `ForTest` | 接受 | `Tests:true` 显式 OR `packages.NeedForTest`，并增加 external variant/Tests:false 回归 |
| Thread/threadprompt 还有 prompt 与 toolbridge 反向测试消费者 | 接受 | 两个精确测试文件归 Lane A，Lane A GREEN 跑完整 consumer packages |
| dashboard Reader adapter 的动态 Writer capability 可能丢失 | 接受 | B4 增加有/无 Upserter 与真实 `WriteWorkflowMaterial` 三条行为测试 |
| A/C/D RED 只看非零可能把编译或 setup 错误当成功 | 接受 | 先 guarded `-list`，再要求 violation token 并拒绝 build/setup/load 错误；Lane C loader 先 GREEN |
| `test_with_guard.sh` 会先执行未过滤的全量 archtest， intentional RED 无法到达精确 `-list/-run` | 接受 | A/C/D 的 guarded `-list` 与 RED 改用只预跑 CodeSizeGuard、随后透传精确参数的 `go_with_guard.sh test`；实现后的全量 GREEN 仍使用 `test_with_guard.sh` |
| Thread 已占满 30 个被计数生产文件，新增专用 Port 文件触发 package-file guard | 接受 | 保留 `persistence_port.go` 的边界可读性；把同属构造职责且已在 Lane A owned 范围的 `service_constructor.go` 原样并入既有 `factory.go` 后删除前者。`factory.go` 沿用仓库现有不计包文件数规则，不新增豁免；禁止把 Handoff 拼入 Archive 扩大语义范围 |
| Prompt 全包 Fx e2e harness 仍直接提供 Thread/Binding Store，typed Port 迁移后闭图失败 | 接受 | Lane A 同步迁移 `e2e_test.go` 与 `e2e_test_support_test.go` 的测试 Provide/capturing fake 到 Thread-owned Port/DTO；只扩展反向消费者测试，不把 Store adapter 放回业务模块 |
| 26/12、22/10 基线只打印未断言 | 接受 | shell 对行数、唯一文件数与 basename 做硬断言 |
| `git status ??` 不能证明 unrelated 内容未变 | 接受 | 对四个精确文件记录并复核 type/mode/size/SHA256，且证明未 tracked/未与集成 diff 重叠 |
| canonical RED wrapper 被复用于 GREEN 会必然失败 | 接受 | Step 5 仅收 RED，Step 6 使用普通零退出 GREEN 命令 |
| 空 Fx bundle 以 package global 声明会触发 `internal/app` 的 `global_vars=0` 守卫 | 接受 | 使用仓库已有的 function seam：两个无参函数返回 `fx.Option`，`app.Module` 调用函数；不增加豁免 |
| 最终裸 `git diff --check` 看不到已提交结果 | 接受 | 检查 `REMOTE_BASE_SHA..INTEGRATION_SHA` 并单独证明 integration tree clean |
| lane 集成原语不确定 | 接受 | 固定按 A→B 提交序列→C 精确 SHA `cherry-pick`，记录每次集成后 SHA |
| 应继续拆成四个以上 lane | 不采纳 | 用户明确要求 3 个工作树；通过 Lane B checkpoint 串行收敛复杂度 |

任何执行者若要偏离上述裁决，必须先提交源码、测试或 Fx 图证据，由主 agent 修改计划后再继续，不能在 worker 分支自行换架构。

### 执行 shell 与状态契约

- 主 agent 为 Task 0/D/E 启动一个持久 Bash controller session：`/bin/bash --noprofile --norc`。所有 controller Git 命令只在该 session 内执行；全文所有 `bash` block 都必须兼容 macOS 自带 Bash 3.2，禁止 zsh 专用数组展开、只读变量名或下标语义。session 丢失时停止，重新读取当前 repo/branch/HEAD/remote/lane SHA 并与已报告证据逐项相等后才可恢复。
- worker 不继承 controller 环境。每个派发消息必须写入绝对 worktree、branch、`LANE_BASE_SHA`、owned files 和精确 commit 输出要求。
- worker 也必须使用 `/bin/bash --noprofile --norc`。每个 `bash` block 第一行必须是 `set -euo pipefail`。预期 RED 的命令必须显式捕获非零状态，不能让 `set -e` 杀死 controller session；预期零匹配的 `rg` 必须区分 exit 0、1、>=2。
- intentional RED 的 guarded test-list 与精确 RED 统一使用 `./scripts/go_with_guard.sh test ...`，因为它只预跑 CodeSizeGuard 后透传目标参数；实现后的 package/full GREEN 统一恢复为 `./scripts/test_with_guard.sh ...`，不得用 RED wrapper 替代完整守卫。
- Task D 固定使用精确 SHA `cherry-pick`，不用模糊 branch merge，也不混用 merge/cherry-pick。主 agent 记录 `A_SHA`、有序 `B_SHAS`、`C_SHA` 以及每次 cherry-pick 后的新 integration SHA。

---

## Task 0: 主 agent 串行建立干净集成树并拆分共享守卫

**执行前提：** 本计划文件必须先以 docs-only 原子提交进入并推送到 `main`，否则新建 integration/lane worktree 看不到计划。该 docs commit 及 hook-owned 生成物进入基线后，才记录下面的 `BASE_SHA` / `REMOTE_BASE_SHA`；本步骤不授权自动提交或推送文档。

**Files:**
- Verify: all files under current scope
- Modify: `internal/archtest/interface_isolation_guard_test.go`
- Create: `internal/archtest/interface_isolation_thread_guard_test.go`
- Create: `internal/archtest/interface_isolation_dashboard_guard_test.go`
- Modify: `internal/archtest/dependency_optional_boundary_test.go`
- Create: `internal/archtest/dependency_optional_thread_boundary_test.go`
- Modify: `internal/app/modules.go`
- Create: `internal/app/thread_store_adapters_module.go`
- Create: `internal/app/business_store_adapters_module.go`
- Modify: `internal/module/threadprompt/runtime_catalog.go`
- Modify: `internal/module/threadprompt/runtime_catalog_test.go`
- Modify: `internal/module/dashboard/types.go`
- Create: `internal/module/dashboard/types_json_test.go`
- Modify on discovered executable correction: `docs/plans/2026-07-10-backend-onion-store-port-ssa-execution.md`
- Preserve unrelated untracked files exactly as found

- [ ] **Step 1: 记录 Git 与 dirty 边界**

Run:

```bash
set -euo pipefail
export PRIMARY_REPO=/Users/mima0000/Desktop/wj/super-agent-v3
git -C "$PRIMARY_REPO" ls-files --error-unmatch \
  docs/plans/2026-07-10-backend-onion-store-port-ssa-execution.md
git -C "$PRIMARY_REPO" fetch origin main
export BASE_SHA=$(git -C "$PRIMARY_REPO" rev-parse HEAD)
export REMOTE_BASE_SHA=$(git -C "$PRIMARY_REPO" rev-parse origin/main)
printf 'BASE_SHA=%s\nREMOTE_BASE_SHA=%s\n' "$BASE_SHA" "$REMOTE_BASE_SHA"
test "$BASE_SHA" = "$REMOTE_BASE_SHA"
git -C "$PRIMARY_REPO" status --short
git -C "$PRIMARY_REPO" worktree list --porcelain

UNRELATED_PATHS=(
  docs/plans/2026-07-10-generated-artifacts-launchd-refresh.md
  docs/superpowers/specs/2026-07-10-capcontract-launchd-refresh-design.md
  scripts/generated_artifacts_auto_refresh.sh
  scripts/generated_artifacts_auto_refresh_guard_test.go
)
fingerprint_unrelated() {
  local path metadata digest
  for path in "${UNRELATED_PATHS[@]}"; do
    test -f "$PRIMARY_REPO/$path"
    metadata=$(stat -f '%HT|%Lp|%z' "$PRIMARY_REPO/$path")
    digest=$(shasum -a 256 "$PRIMARY_REPO/$path" | awk '{print $1}')
    printf '%s|%s|%s\n' "$path" "$metadata" "$digest"
  done
}
export UNRELATED_FINGERPRINT_BEFORE=$(fingerprint_unrelated)
printf '%s\n' "$UNRELATED_FINGERPRINT_BEFORE"
```

Expected: controller shell 导出并记录 `BASE_SHA`、经过 fetch 的 `REMOTE_BASE_SHA` 和四个 unrelated 文件的 type/mode/size/SHA256 指纹；后续步骤复用这些变量。开始执行时两者必须一致，否则先停止并裁决远端 drift。只看 `git status ??` 不能证明内容未变；已有 generated-artifacts 计划和脚本保持 unrelated，不进入任何 lane commit。

- [ ] **Step 2: 创建专用 integration worktree**

从已记录的 `BASE_SHA` 创建：

```bash
set -euo pipefail
export INTEGRATION_BRANCH=codex/backend-onion-integration-20260710
export INTEGRATION_TREE="$PRIMARY_REPO/.worktrees/backend-onion-integration-20260710"
if git -C "$PRIMARY_REPO" show-ref --verify --quiet "refs/heads/$INTEGRATION_BRANCH"; then
  echo "branch already exists: $INTEGRATION_BRANCH" >&2
  exit 1
fi
if test -e "$INTEGRATION_TREE"; then
  echo "worktree path already exists: $INTEGRATION_TREE" >&2
  exit 1
fi
git -C "$PRIMARY_REPO" worktree add -b "$INTEGRATION_BRANCH" \
  "$INTEGRATION_TREE" "$BASE_SHA"

cd "$INTEGRATION_TREE"
assert_integration_tree() {
  local expected actual
  expected=$(cd "$INTEGRATION_TREE" && pwd -P)
  actual=$(cd "$(git rev-parse --show-toplevel)" && pwd -P)
  test "$(pwd -P)" = "$expected"
  test "$actual" = "$expected"
  test "$(git branch --show-current)" = "$INTEGRATION_BRANCH"
}
assert_integration_tree
test "$(git rev-parse HEAD)" = "$BASE_SHA"
```

后续主 agent 串行修改、lane 合并、生成物刷新和最终验收全部在该干净 worktree 执行；不得直接在带用户 unrelated dirty 的本地 `main` 上集成。Task 0/D/E 每个使用相对路径的执行块开头都先调用 `assert_integration_tree`；恢复 controller session 时先重新定义并调用该 helper。

- [ ] **Step 3: 记录 26 个生产 import 基线**

Run:

```bash
set -euo pipefail
assert_integration_tree
module_store_imports=$(rg -n 'github\.com/anthropic-ai/super-agent-v3/internal/store' \
  internal/module -g '*.go' -g '!**/*_test.go'
)
printf '%s\n' "$module_store_imports"
test "$(printf '%s\n' "$module_store_imports" | sed '/^$/d' | wc -l | tr -d ' ')" -eq 26
test "$(printf '%s\n' "$module_store_imports" | cut -d: -f1 | sort -u | wc -l | tr -d ' ')" -eq 12
test -z "$(printf '%s\n' "$module_store_imports" | cut -d: -f1 | sort -u | awk -F/ '$NF != "module.go" { print }')"
```

Expected: exactly 26 matches in exactly 12 `module.go` files。

- [ ] **Step 4: 运行修改前基线**

Run:

```bash
set -euo pipefail
assert_integration_tree
./scripts/test_with_guard.sh ./internal/archtest -count=1
./scripts/test_with_guard.sh ./internal/module/thread -count=1
make guard
```

Expected: PASS。任何失败必须先与 base SHA 对照，不得把已有失败归因给 lane。

- [ ] **Step 5: 用 LSP 固化关键影响面**

Run through LSP:

```text
grep(text_search, query="internal/store", path="internal/module", glob="module.go")
inspect(definition, pos="internal/module/thread/module.go:78:6")
xref(references, pos="internal/module/thread/factory.go:135:6")
structure(document_symbol, file_path="internal/app/modules.go")
file(diagnostics, file_paths=[
  "internal/module/thread/module.go",
  "internal/module/thread/factory.go",
  "internal/module/thread/service.go",
  "internal/module/threadprompt/runtime_catalog.go",
  "internal/app/modules.go",
  "internal/archtest/backend_boundary_single_source_test.go",
  "internal/archtest/priority_ssa_scan.go",
  "internal/archtest/orchestration_service_wide_helpers_test.go"
])
```

Expected: 26 matches；`runtimePromptCatalog`、`threadPageStore`、`loadedThreadPageStore`、`activeThreadCountStore` 的影响面与当前构造链完整记录。当前基线明确记录 3 个 Hint：`runtime_catalog.go:406 slicescontains`、`:546 minmax`、`dashboard/types.go:85 omitzero`；除此之外为零。

- [ ] **Step 6: 主 agent 消除基线 diagnostics，并预拆共享 guard 与 App 装配 seam**

先做行为等价机械清理：

```text
runtime_catalog.go:406  使用 slices.Contains 替代手写 membership loop
runtime_catalog.go:546  使用 min 替代手写最小值 if
dashboard/types.go:85   删除对 time.Time 实际无效的 omitempty；禁止改为会改变零值输出的 omitzero
```

`types_json_test.go` 必须证明零值时间字段仍按当前实际 wire shape 输出；`runtime_catalog_test.go` 保持同样输入输出。完成后对这两个生产文件重新 `open_file` 再取 diagnostics，Error/Warning/Information/Hint 必须全部为零，才允许继续拆 seam。

只做机械拆分，不改变断言语义：

```text
interface_isolation_guard_test.go              保留通用 helper/预算框架
interface_isolation_thread_guard_test.go       只保留 Thread port/budget/unused-dependency 断言
interface_isolation_dashboard_guard_test.go    只保留 dashboard adapter-location 断言
dependency_optional_thread_boundary_test.go    从 dependency_optional_boundary_test.go 移出 Thread optional surface 断言
```

同时让 `internal/app/modules.go` 固定引用两个可独立填充的 option：

```go
// internal/app/thread_store_adapters_module.go
package app

import "go.uber.org/fx"

func threadStoreAdaptersModule() fx.Option {
	return fx.Options()
}

// internal/app/business_store_adapters_module.go
package app

import "go.uber.org/fx"

func businessStoreAdaptersModule() fx.Option {
	return fx.Options()
}
```

将 `threadStoreAdaptersModule()`、`businessStoreAdaptersModule()` 加入现有 `app.Module` 的 options。必须使用函数而不是 package global，保持 `internal/app` 的 `global_vars=0` 守卫；Lane A/B 后续只修改各自 bundle 函数的返回值，用 `fx.Options` 组合 `fx.Provide` / `fx.Invoke`，不再等待 Task D 修改根清单。

Run:

```bash
set -euo pipefail
assert_integration_tree
./scripts/test_with_guard.sh ./internal/module/threadprompt -count=1
./scripts/test_with_guard.sh ./internal/module/dashboard -count=1
./scripts/test_with_guard.sh ./internal/archtest -count=1
./scripts/test_with_guard.sh ./internal/app -run '^TestAppModuleGraphIsClosed$' -count=1
```

Expected: guard 拆分前后测试集合和结果一致，两个空 function bundle 不改变当前 Fx 图，`global_vars` 守卫仍为零。禁止顺手修改业务断言。

- [ ] **Step 7: 精确提交并证明共同 seam**

只 stage Task 0 owned files；hook 可追加五个 generator-owned surface。提交后用 committed diff 证明没有把 unrelated 或后续 lane 文件带入 seam：

```bash
set -euo pipefail
assert_integration_tree
git add -A -- \
  internal/archtest/interface_isolation_guard_test.go \
  internal/archtest/interface_isolation_thread_guard_test.go \
  internal/archtest/interface_isolation_dashboard_guard_test.go \
  internal/archtest/dependency_optional_boundary_test.go \
  internal/archtest/dependency_optional_thread_boundary_test.go \
  internal/app/modules.go \
  internal/app/thread_store_adapters_module.go \
  internal/app/business_store_adapters_module.go \
  internal/module/threadprompt/runtime_catalog.go \
  internal/module/threadprompt/runtime_catalog_test.go \
  internal/module/dashboard/types.go \
  internal/module/dashboard/types_json_test.go \
  docs/plans/2026-07-10-backend-onion-store-port-ssa-execution.md

git diff --cached --name-only
git commit -m 'refactor(archtest): 预拆后端边界执行接缝'
export SEAM_SHA=$(git rev-parse HEAD)
test "$SEAM_SHA" != "$BASE_SHA"
test "$(git rev-parse "$SEAM_SHA^")" = "$BASE_SHA"
test -z "$(git status --porcelain)"

seam_paths=$(git diff-tree --no-commit-id --name-only -r "$SEAM_SHA")
printf '%s\n' "$seam_paths"
while IFS= read -r path; do
  case "$path" in
    internal/archtest/interface_isolation_guard_test.go|\
    internal/archtest/interface_isolation_thread_guard_test.go|\
    internal/archtest/interface_isolation_dashboard_guard_test.go|\
    internal/archtest/dependency_optional_boundary_test.go|\
    internal/archtest/dependency_optional_thread_boundary_test.go|\
    internal/app/modules.go|\
    internal/app/thread_store_adapters_module.go|\
    internal/app/business_store_adapters_module.go|\
    internal/module/threadprompt/runtime_catalog.go|\
    internal/module/threadprompt/runtime_catalog_test.go|\
    internal/module/dashboard/types.go|\
    internal/module/dashboard/types_json_test.go|\
    docs/plans/2026-07-10-backend-onion-store-port-ssa-execution.md|\
    README.md|\
    docs/doc/codemap/13-archtest-boundaries.md|\
    docs/doc/codemap/README.md|\
    docs/doc/codemap/ai-index.json|\
    docs/doc/codemap/project-map/*) ;;
    *) printf 'unexpected seam path: %s\n' "$path" >&2; exit 1 ;;
  esac
done <<< "$seam_paths"

for required in \
  internal/app/modules.go \
  internal/app/thread_store_adapters_module.go \
  internal/app/business_store_adapters_module.go \
  internal/archtest/interface_isolation_thread_guard_test.go \
  internal/archtest/interface_isolation_dashboard_guard_test.go \
  internal/archtest/dependency_optional_thread_boundary_test.go \
  docs/plans/2026-07-10-backend-onion-store-port-ssa-execution.md; do
  printf '%s\n' "$seam_paths" | grep -Fx "$required" >/dev/null
done
export LANE_BASE_SHA=$SEAM_SHA
```

Expected: seam 是 `BASE_SHA` 的单一子提交，integration tree clean，`LANE_BASE_SHA == SEAM_SHA != BASE_SHA`；Task 0 owned files 已进入 committed diff，且除 hook-owned 五个 surface 外无额外路径。

- [ ] **Step 8: 记录 lane seam SHA 并创建三个 worktree**

Branches must use:

```text
codex/backend-onion-thread-port-20260710
codex/backend-onion-module-store-20260710
codex/backend-boundary-single-source-ssa-20260710
```

创建命令固定为主仓绝对路径；禁止在 integration worktree 中用相对 `.worktrees/...`：

```bash
set -euo pipefail
assert_integration_tree
test "$(git rev-parse HEAD)" = "$SEAM_SHA"
export LANE_BASE_SHA=$SEAM_SHA

LANE_SPECS=(
  "codex/backend-onion-thread-port-20260710|$PRIMARY_REPO/.worktrees/backend-onion-thread-port-20260710"
  "codex/backend-onion-module-store-20260710|$PRIMARY_REPO/.worktrees/backend-onion-module-store-20260710"
  "codex/backend-boundary-single-source-ssa-20260710|$PRIMARY_REPO/.worktrees/backend-boundary-single-source-ssa-20260710"
)

for spec in "${LANE_SPECS[@]}"; do
  branch=${spec%%|*}
  tree=${spec#*|}
  if git -C "$PRIMARY_REPO" show-ref --verify --quiet "refs/heads/$branch"; then
    echo "branch already exists: $branch" >&2
    exit 1
  fi
  if test -e "$tree"; then
    echo "worktree path already exists: $tree" >&2
    exit 1
  fi
done

for spec in "${LANE_SPECS[@]}"; do
  branch=${spec%%|*}
  tree=${spec#*|}
  git -C "$PRIMARY_REPO" worktree add -b "$branch" "$tree" "$LANE_BASE_SHA"
done
```

每个 worktree 必须从 Step 7 的同一 `LANE_BASE_SHA` 创建，不得从随后变化的 integration HEAD 或本地 `main` 创建。唯一执行子 agent 每次进入一个 lane 前记录 `agentid/worktree/branch/LANE_BASE_SHA/owned files`；完成并经主 agent 复核后才进入下一 lane。

---

## Task A: Thread 端口所有权、领域 DTO 与 `any` 清零

**Files:**
- Create: `internal/module/thread/persistence_port.go`
- Modify: `internal/module/thread/module.go`
- Modify: `internal/module/thread/factory.go`
- Modify: `internal/module/thread/service.go`
- Delete after verbatim constructor move: `internal/module/thread/service_constructor.go`
- Modify: `internal/module/thread/lifecycle_fork.go`
- Modify: `internal/module/thread/lifecycle_helpers.go`
- Modify: `internal/module/thread/router_resolve.go`
- Modify: `internal/module/thread/spawn.go`
- Modify: `internal/module/thread/start_session_helpers.go`
- Modify: `internal/module/thread/stop.go`
- Modify: `internal/module/threadprompt/*.go`
- Modify reverse-consumer test: `internal/module/prompt/service_surface_list_test.go`
- Create reverse-consumer test adapter: `internal/module/prompt/threadprompt_port_adapter_test.go`
- Modify reverse-consumer test: `internal/module/prompt/e2e_test.go`
- Modify reverse-consumer test support: `internal/module/prompt/e2e_test_support_test.go`
- Modify reverse-consumer test: `internal/platform/toolbridge/persistent_subagent_flow_test.go`
- Create: `internal/app/thread_store_adapters.go`
- Create: `internal/app/thread_prompt_adapters.go`
- Modify: `internal/app/thread_store_adapters_module.go`
- Modify/Add tests: `internal/module/{thread,threadprompt}/*_test.go`, `internal/app/thread_*_test.go`
- Modify: `internal/archtest/interface_isolation_thread_guard_test.go`
- Modify: `internal/archtest/dependency_optional_thread_boundary_test.go`

### A1. 先锁定失败行为

- [ ] **Step 1: 编写 Thread Port 类型泄漏失败测试**

在预拆出的 `internal/archtest/interface_isolation_thread_guard_test.go` 新增接口形状测试：读取 Thread 持久化 Port 的方法签名，拒绝任何 `threadstore.` / `bindingstore.` qualified type；同时读取 prompt catalog Port，拒绝 underlying type 为 `any`。该测试只守 Thread Port 类型所有权，不复制全局 Module→Store import policy。

Test names:

```go
func TestThreadPersistencePortsOwnTheirDTOs(t *testing.T)
func TestThreadPortsDoNotUseAny(t *testing.T)
```

- [ ] **Step 2: 运行并确认 RED**

Run:

```bash
set -euo pipefail
./scripts/go_with_guard.sh test ./internal/archtest \
  -list '^(TestThreadPersistencePortsOwnTheirDTOs|TestThreadPortsDoNotUseAny)$' \
  > /tmp/thread-port-red-list.txt
test "$(grep -c '^TestThreadPersistencePortsOwnTheirDTOs$' /tmp/thread-port-red-list.txt)" -eq 1
test "$(grep -c '^TestThreadPortsDoNotUseAny$' /tmp/thread-port-red-list.txt)" -eq 1

red_output=$(mktemp)
set +e
./scripts/go_with_guard.sh test ./internal/archtest \
  -run '^(TestThreadPersistencePortsOwnTheirDTOs|TestThreadPortsDoNotUseAny)$' \
  -count=1 >"$red_output" 2>&1
red_status=$?
set -e
test "$red_status" -ne 0
grep -E 'threadstore|bindingstore|promptServiceCatalogPort|underlying type.*any' "$red_output" >/dev/null
if grep -E 'build failed|undefined:|setup failed|syntax error|package load failed' "$red_output" >/dev/null; then
  cat "$red_output" >&2
  exit 1
fi
cat "$red_output"
rm -f "$red_output"
```

Expected: FAIL，明确列出 `threadstore` / `bindingstore` qualified method types 和 `promptServiceCatalogPort any`；不得因测试语法或零候选失败。

### A2. 定义 Thread-owned Port

- [ ] **Step 3: 在 `persistence_port.go` 定义两个持久化根 Port**

不把现有两个窄 Store Port 机械拆成五个必需 Fx 依赖。保留两个消费端持久化根接口，参数/结果改用 Thread 自有结构体，不出现 Store package qualifier：

```go
type ThreadStore interface {
	GetByThreadID(context.Context, string) (*ThreadRecord, error)
	ListAll(context.Context) ([]ThreadRecord, error)
	ListConfigsByIDs(context.Context, []string) ([]ThreadRecord, error)
	Upsert(context.Context, ThreadUpsert) error
	SavePromptSnapshot(context.Context, string, PromptSnapshotRecord) error
	LoadPromptSnapshot(context.Context, string) (*PromptSnapshotRecord, error)
	UpdateStatus(context.Context, ThreadStatusUpdate) error
	DeleteByThreadID(context.Context, string) error
	CountChildren(context.Context, string) (int64, error)
	Exists(context.Context, string) (bool, error)
}

type BindingStore interface {
	GetByProviderThread(context.Context, string, string) (*BindingRecord, error)
	Upsert(context.Context, BindingUpsert) error
	DeleteByAgentID(context.Context, string) error
	UpdateSessionUUID(context.Context, BindingSessionUUIDUpdate) error
	UpdateProviderThreadID(context.Context, BindingProviderThreadIDUpdate) error
	SetArchived(context.Context, BindingArchiveUpdate) error
	GetByAgentID(context.Context, string) (*BindingRecord, error)
	ListAgentThreadBindings(context.Context) ([]BindingRecord, error)
	UpdateAgentCwd(context.Context, BindingCWDUpdate) error
}
```

`ThreadRecord`、`ThreadUpsert`、`ThreadStatusUpdate`、`PromptSnapshotRecord`、`BindingRecord` 和五个 Binding 更新 DTO 必须按当前模块实际读取/写入字段定义为新 struct。禁止使用 Store type alias；JSON tags 只在真实 wire DTO 上保留。

保留当前三个可选能力的行为形状，但把接口改成 Thread-owned 名称：`ThreadPageReader`、`LoadedThreadPageReader`、`ActiveThreadCounter`。同一个 App concrete adapter 同时实现 `ThreadStore` 和这三个可选接口；Service 仍从 `ThreadStore` 的 dynamic concrete value 做类型断言，因此不增加 Fx constructor 参数。必须为 capability 存在和缺失两条路径写测试，确保 `ListPage`、`ListLoadedPage`、`CountActive` 没有在迁移中丢失。

- [ ] **Step 4: 将 Service 字段和构造函数改为新 Port**

`service` 不再保存旧 `threadServiceStorePort`、`bindingServiceStorePort` 或 `promptServiceCatalogPort`。构造函数显式接收 `ThreadStore`、`BindingStore`、`PromptCatalog`；optional 依赖继续通过 Fx tag 表达，不得用 `any` 或 service locator 聚合。

Thread 包在当前基线已占满 30 个被 CodeSizeGuard 计数的生产文件。为保留专用 `persistence_port.go`，把 `service_constructor.go` 的构造函数仅做 import 合并、`gofmt` 和原样移动后并入现有 `factory.go`，随后删除 `service_constructor.go`；两者本就同属 service factory/constructor 责任，且都在 Lane A owned 范围。必须用 LSP references、diagnostics 与 Thread 全包测试证明移动等价；不得改 guard、增加豁免，或把 Handoff/Archive 等无关生命周期文件拼接收口。

### A3. 将 Prompt Catalog 变成真实类型契约

- [ ] **Step 5: 导出精确 PromptCatalog**

把 `factory.go` 中现有三方法本地接口及 DTO 改为 Thread-owned exported Port：

```go
type PromptCatalog interface {
	ListTemplates(context.Context, PromptListFilter) ([]PromptTemplate, error)
	ListSectionsByTemplateID(context.Context, int64) ([]PromptTemplateSection, error)
	InsertVersion(context.Context, PromptTemplateVersion) (int64, error)
	CanInsertPromptVersion() bool
}
```

`PromptListFilter`、`PromptTemplate`、`PromptTemplateSection`、`PromptTemplateVersion` 由现有 `runtimePrompt*` 类型逐字段改名得到；不得引用 `promptstore` 类型。

- [ ] **Step 6: 删除运行时 type assertion 和默认能力兜底**

`service.promptCatalog` 直接使用 `PromptCatalog`。`runtimePromptCatalog()` 只返回已注入的 typed port；删除从 `any` 断言 `promptstore.RuntimePromptCatalog` 的逻辑。`CanInsertPromptVersion` 必须由 adapter 明确实现，禁止缺失 capability 时默认 `true`。返回 `false` 是合法 builtin-only/read-only catalog，构造必须成功；只有实际调用写入时才返回明确的 capability error。

### A4. 将 Store 映射移动到 App

- [ ] **Step 7: 实现 `thread_store_adapters.go`**

App adapter 同时 import `internal/module/thread` 与 `internal/store/{thread,binding}`，逐字段完成以下映射：

```text
threadstore.Thread <-> thread.ThreadRecord
threadstore.UpsertParams <- thread.ThreadUpsert
threadstore.UpdateStatusParams <- thread.ThreadStatusUpdate
threadstore.PromptSnapshot <-> thread.PromptSnapshotRecord
bindingstore.Binding <-> thread.BindingRecord
bindingstore.*Params <- thread.Binding*Update
```

所有 adapter constructor 在必要 Store 为 nil 时返回带领域上下文的 error；只有当前 Fx 图明确标为 optional 的能力可以返回 nil。

跨边界映射必须切断可变引用共享：除 `json.RawMessage`、map 和嵌套指针外，`ThreadRecord.FinishedAt`、`ThreadRecord.PromptVersionID` 等标量指针也必须复制值后返回新地址；字段覆盖测试还要断言修改 domain DTO 不会回写 Store DTO。

- [ ] **Step 8: 实现 `thread_prompt_adapters.go`**

Lane A 同时迁移 threadprompt。先把 private `promptListFilter` 导出为 `threadprompt.PromptListFilter` 并迁移全部消费者，否则包外 App 不可能实现 exported `PromptStore`。`threadprompt.RuntimePromptCatalog` 必须显式包含 `CanInsertPromptVersion() bool`；`NewRuntimeCatalog` 只接收 `threadprompt.PromptStore` 并返回 `threadprompt.RuntimePromptCatalog`，`RegisterProviders` 也只接收本地 typed catalog，不再暴露 `promptstore.Store` / `promptstore.RuntimePromptCatalog`。

App 把 `promptstore.Store` 映射为 `threadprompt.PromptStore`，并在组合根构造 threadprompt runtime catalog；另一个 App adapter 把 `threadprompt.RuntimePromptCatalog` 映射为 `thread.PromptCatalog`。App 文件必须包含：

```go
var _ threadprompt.PromptStore = (*threadPromptStoreAdapter)(nil)
var _ thread.PromptCatalog = (*threadPromptCatalogAdapter)(nil)
```

把 `registerThreadPromptProviders`、`provideRuntimePromptCatalog` 中所有直接需要 `promptstore` 的装配逻辑迁到 App；Thread 不再 import Store，也不再用 Store 构造 threadprompt catalog。禁止臆造不存在的 `threadprompt.Module`。

- [ ] **Step 9: 编写 adapter round-trip/字段覆盖测试**

至少覆盖：

```text
ThreadRecord store->domain
ThreadUpsert domain->store
ThreadStatusUpdate domain->store
PromptSnapshot 双向转换
BindingRecord store->domain
全部 Binding update DTO domain->store
PromptTemplate/Section store->domain
PromptTemplateVersion domain->store
ThreadRecord 指针字段与 Store DTO 不共享地址
nil Store fail-fast
CanInsertPromptVersion=false 的 builtin-only 构造成功
read-only catalog 实际 InsertVersion 明确失败
ThreadPageReader/LoadedThreadPageReader/ActiveThreadCounter capability 存在与缺失
package app_test 可命名 PromptListFilter 并编译实现 threadprompt.PromptStore
threadprompt.RuntimePromptCatalog 强制包含 CanInsertPromptVersion
App 组合根的 fx.Invoke 实际注册 project_default_rules/available_experts/recall_catalog
```

App adapter 测试名称固定为：

```go
func TestThreadStoreAdapterFieldCoverage(t *testing.T)
func TestThreadBindingStoreAdapterFieldCoverage(t *testing.T)
func TestThreadPromptStoreAdapterImplementsPort(t *testing.T)
func TestThreadPromptCatalogAdapterImplementsPort(t *testing.T)
func TestThreadStoreOptionalCapabilities(t *testing.T)
func TestThreadStoreAdaptersModuleRegistersPromptProvidersViaFx(t *testing.T)
```

最后一条测试必须用真实 `threadStoreAdaptersModule()` 创建 `fx.App` 并观察 registrar，证明迁移到 App 后的 `fx.Invoke(registerThreadPromptProvidersFromApp)` 确实执行；`fx.ValidateApp` 只证明 DAG 闭合，不能替代该行为测试。provider 自身的注册顺序与 builtin 渲染继续由 `internal/module/threadprompt` 测试负责，不在 App 重复实现。

反向消费者必须随 Lane A 一起迁移并编译：`internal/module/prompt/service_surface_list_test.go` 不得继续以 `promptstore.Store` 调用 `threadprompt.NewRuntimeCatalog`；为复用该测试既有的 Store-backed fixture，允许在 `threadprompt_port_adapter_test.go` 放置仅测试可见的 typed Port 翻译器，但生产代码不得引用它。`internal/platform/toolbridge/persistent_subagent_flow_test.go` 不得继续把实现 Store DTO 的 fake 直接传入 Thread constructor。Lane B 禁止覆盖这些文件。

不得通过比较 JSON 字符串掩盖字段漏映射。必须使用反射枚举 source/target DTO exported fields 的自动字段覆盖守卫，再配合逐字段值断言；新增 Store DTO 字段未映射时测试必须自动失败。

- [ ] **Step 10: 运行 Thread GREEN**

Run:

```bash
set -euo pipefail
./scripts/test_with_guard.sh ./internal/module/thread -count=1
./scripts/test_with_guard.sh ./internal/module/threadprompt -count=1
./scripts/test_with_guard.sh ./internal/app \
  -list '^(TestThreadStoreAdapterFieldCoverage|TestThreadBindingStoreAdapterFieldCoverage|TestThreadPromptStoreAdapterImplementsPort|TestThreadPromptCatalogAdapterImplementsPort|TestThreadStoreOptionalCapabilities|TestThreadStoreAdaptersModuleRegistersPromptProvidersViaFx)$' \
  > /tmp/thread-app-test-list.txt
for name in \
  TestThreadStoreAdapterFieldCoverage \
  TestThreadBindingStoreAdapterFieldCoverage \
  TestThreadPromptStoreAdapterImplementsPort \
  TestThreadPromptCatalogAdapterImplementsPort \
  TestThreadStoreOptionalCapabilities \
  TestThreadStoreAdaptersModuleRegistersPromptProvidersViaFx; do
  test "$(grep -c "^${name}$" /tmp/thread-app-test-list.txt)" -eq 1
done
./scripts/test_with_guard.sh ./internal/app \
  -run '^(TestThreadStoreAdapterFieldCoverage|TestThreadBindingStoreAdapterFieldCoverage|TestThreadPromptStoreAdapterImplementsPort|TestThreadPromptCatalogAdapterImplementsPort|TestThreadStoreOptionalCapabilities|TestThreadStoreAdaptersModuleRegistersPromptProvidersViaFx)$' \
  -count=1
./scripts/test_with_guard.sh ./internal/app -run '^TestAppModuleGraphIsClosed$' -count=1
./scripts/test_with_guard.sh ./internal/module/prompt -count=1
./scripts/test_with_guard.sh ./internal/platform/toolbridge -count=1
./scripts/test_with_guard.sh ./internal/archtest \
  -run '^(TestThreadPersistencePortsOwnTheirDTOs|TestThreadPortsDoNotUseAny|TestInterfaceIsolationBudgets)$' \
  -count=1
if rg -n 'internal/store|type promptServiceCatalogPort any' \
  internal/module/thread internal/module/threadprompt -g '*.go' -g '!**/*_test.go'; then
  echo "unexpected Thread/threadprompt Store or any dependency" >&2
  exit 1
else
  rg_status=$?
  test "$rg_status" -eq 1
fi
```

Expected: tests PASS；真实 `app.Module` Fx 图闭合；`rg` zero matches。Lane A 必须在自己的 bundle 文件中登记 Thread/threadprompt adapters，不能等待主 agent 集成后才补 provider。

- [ ] **Step 11: Lane A 原子提交**

Commit message:

```text
refactor(thread): 通过领域端口隔离 Store
```

报告 SHA、RED/GREEN、LSP diagnostics、owned-file diff 和剩余风险。

---

## Task B: 其余 10 个 Module 去 Store import

**Files:**
- Modify: `internal/module/cron/module.go`
- Modify: `internal/module/dashboard/module.go`
- Modify: `internal/module/datasource_v2/module.go`
- Modify: `internal/module/feedback/module.go`
- Modify: `internal/module/insight/module.go`
- Modify: `internal/module/memory/module.go`
- Modify: `internal/module/personalization/module.go`
- Modify: `internal/module/prompt/module.go`
- Modify: `internal/module/turn/module.go`
- Modify: `internal/module/uistate/module.go`
- Modify: `internal/module/cron/contract.go`
- Modify: `internal/module/dashboard/contract.go`, `internal/module/dashboard/types.go`
- Modify: `internal/module/datasource_v2/store_port.go`
- Modify: `internal/module/feedback/contract.go`
- Modify: `internal/module/insight/contract.go`
- Verify unchanged port ownership: `internal/module/memory/sharedfileport/port.go`
- Create: `internal/module/personalization/persistence_port.go`
- Create: `internal/module/prompt/persistence_port.go`
- Modify: `internal/module/turn/contract.go`
- Create: `internal/module/uistate/persistence_port.go`
- Create: `internal/app/{cron,dashboard,datasource_v2,feedback,insight,memory,personalization,prompt,turn,uistate}_store_adapters.go`
- Create: `internal/app/business_store_adapter_nil.go`
- Modify: `internal/app/business_store_adapters_module.go`
- Add focused tests under each affected Module and `internal/app`
- Modify: `internal/archtest/interface_isolation_dashboard_guard_test.go`

### B1. 固定每个模块的迁移责任

- [ ] **Step 1: 记录 10 模块生产 import RED 证据**

运行精确 production scan；不在 Lane B 新建 procedural archtest，避免复制主 agent 后续添加的 canonical policy。

Run:

```bash
set -euo pipefail
lane_b_imports=$(rg -n 'github\.com/anthropic-ai/super-agent-v3/internal/store' \
  internal/module \
  -g '*.go' -g '!**/*_test.go' \
  -g '!internal/module/thread/**' \
  -g '!internal/module/threadprompt/**'
)
printf '%s\n' "$lane_b_imports"
test "$(printf '%s\n' "$lane_b_imports" | sed '/^$/d' | wc -l | tr -d ' ')" -eq 22
test "$(printf '%s\n' "$lane_b_imports" | cut -d: -f1 | sort -u | wc -l | tr -d ' ')" -eq 10
test -z "$(printf '%s\n' "$lane_b_imports" | cut -d: -f1 | sort -u | awk -F/ '$NF != "module.go" { print }')"
```

Expected: exactly 22 matches across 10 `module.go` files；保存输出作为 RED artifact。Lane A 负责的 Thread/threadprompt 不计入 Lane B RED。

- [ ] **Step 2: 运行 Lane B 修改前行为基线**

Run:

```bash
set -euo pipefail
./scripts/test_with_guard.sh \
  ./internal/module/cron \
  ./internal/module/dashboard \
  ./internal/module/datasource_v2 \
  ./internal/module/feedback \
  ./internal/module/insight \
  ./internal/module/memory \
  ./internal/module/personalization \
  ./internal/module/prompt \
  ./internal/module/turn \
  ./internal/module/uistate \
  -count=1
```

Expected: PASS；保存为迁移前行为基线。

每个 checkpoint 内的每个模块都必须先新增一个可观察的 RED：至少包含 adapter constructor/接口满足性测试，以及对有 DTO 转换模块的自动 exported-field 覆盖测试；涉及 not-found、nil、optional、transaction 的模块必须各有行为测试。只运行 `rg` 不算 RED。每完成一个模块就同步把 provider 加入 `businessStoreAdaptersModule()` 的返回 option，并在 Lane B 自己的分支验证真实 App Fx 图，不允许推迟到 Task D。

所有由 `internal/app` 实现的 exported Port 都必须满足“跨包可实现”：方法参数、返回值、callback 参数和需要 `errors.Is` 识别的 sentinel 必须 exported。每个 adapter 文件增加编译期断言，例如 `var _ cron.Store = (*cronStoreAdapter)(nil)`；测试必须从 `package app` 或 `package app_test` 编译这些断言，模块同包 fake 不能替代此证明。

跨层 DTO 映射必须切断可变引用共享：`json.RawMessage`、slice、map 和标量指针都要复制，自动字段测试除 source/target exported field 集合与 one-hot 映射外，还要断言修改 domain DTO 不会回写 Store DTO。Lane B 允许新建一个仅测试可见的 App 字段守卫 helper 供四个 checkpoint 复用，禁止复制四套反射实现。

B1 固定按 `feedback → insight → personalization → turn` 串行推进。Feedback 保留 provider 在 nil Store 时返回 nil、由 `Record` 显式 fail-fast 的既有时机；Personalization 保留 non-nil adapter 的方法级 fail-fast；Turn 保留 optional nil。Insight 的 Store 是 required 依赖，迁到 App 后 constructor 必须在 nil 时返回明确错误，不能留下延迟 panic。Turn 只把 `turndedupe.ErrNotFound` 映射为 `turn.ErrDedupeNotFound`，其他错误保持原始 `errors.Is` 链。

B1 的 App adapter 统一通过 `business_store_adapter_nil.go` 识别 nil interface 与 typed nil Store；共享 helper 只处理 nil 判定，不承载 DTO 映射、错误转换或业务兜底。实现必须覆盖 Go 所有可 nil 的 reflect kind，避免各 adapter 复制不完整的 pointer-only 判断。Turn 的领域端口按既定 owned path 写入 `internal/module/turn/contract.go`，不得另增 turn 顶层生产文件。

### B2. 按现有领域端口迁移

- [ ] **Step 3: 迁移 cron**

保留 `cron.Store`、`cron.SchedulerStore` 和 scheduler 子端口，但先把其完整签名改成跨包可实现的 domain surface。至少将当前 private `jobRecord`、`runRecord`、`createJobParams`、`updateJobScheduleParams`、`claimDueJobsForUpdateParams`、`leaseParams`、`markFinishedParams`、`markFailedParams`、`setActiveTurnParams`、`insertRunParams`、`casRunStatusParams`、`setRunTurnParams` 改为对应 exported 类型，并更新所有消费者。`errStoreJobNotFound`、`errStoreJobRunNotFound`、`errStoreClaimTokenMismatch`、`errStoreStatusTransitionRefused`、`errStoreEmptyID/CWD/Provider/ScheduleExpr` 必须导出为稳定 domain sentinel，或改为 exported domain error 分类；App adapter 返回的错误必须继续支持现有 `errors.Is` 分支。随后把 `cronStoreAdapter`、`cronJobStoreAdapter`、`cronScheduler*Adapter`、`cronSubmitAdapter` 及 Store DTO 映射移动到 `internal/app/cron_store_adapters.go`。`cron.Module` 删除具体 Store provider，只消费 typed ports。

- [ ] **Step 4: 迁移 dashboard**

保留现有 exported `AgentStatusReader`、`AILogReader`、`AuditLogReader`、`BusLogReader`、`SystemLogReader`、`DBQueryExecutor`、`CommandCardReader`、`PromptTemplateReader`、`SharedFileReader`。把九类 Store adapter 和 mapper 从 dashboard `module.go` 移到 `internal/app/dashboard_store_adapters.go`；dashboard 文件不得保留 Store DTO、Store not-found 映射或具体 reader 类型。

必须原样保留 `SharedFileReader` 的动态写能力：当底层 `sharedfilestore.Reader` 同时实现 `sharedfilestore.Upserter` 时，App 返回的同一 concrete adapter 必须同时实现 `dashboard.SharedFileReader` 与 `dashboard.SharedFileWriter`；只有 Reader 时不得虚构 Writer。固定增加 `TestDashboardSharedFileAdapterPreservesWriterCapability`、`TestDashboardSharedFileAdapterOmitsWriterWithoutUpserter` 和 `TestDashboardWriteWorkflowMaterialUsesAppAdapter`，分别证明 capability 存在、缺失以及 `WriteWorkflowMaterial` 经真实 App adapter 调用 `Upsert` 并保持字段/错误语义。这三条是 B4 硬门禁，不能只用编译期接口断言替代动态行为。

- [ ] **Step 5: 迁移 datasource_v2**

将当前 `datasourceV2StorePort`、document/chunk/import adapter 与 `WithTx` 包装移动到 `internal/app/datasource_v2_store_adapters.go`。把 datasource_v2 的消费接口和 DTO 导出为最小 Port surface；事务 callback 接收 Module-owned Port，App 在同一底层事务 Store 上重新构造 adapter。事务 callback 为 nil 时继续 fail-fast。

- [ ] **Step 6: 迁移 feedback 与 insight**

将 `feedbackWriter` 导出为 `feedback.Writer`，将 `feedbackEvent` 导出为 `feedback.Event`；App adapter 保留逐字段 Event 转换。将 `insightReader` / `insightWriter` 导出为 `insight.Reader` / `insight.Writer`，移动 `insightStoreAdapter` 和 DTO/error 映射。nil Store 不得从当前显式错误退化为静默成功。

- [ ] **Step 7: 迁移 memory 与 personalization**

Memory 已使用 `sharedfileport.Reader/Deleter`，只需把 `sharedFileReaderAdapter`、`sharedFileDeleterAdapter` 移到 App 并由 app provider 返回对应接口。Personalization 将本地 preference port/DTO 导出为 `personalization.PreferenceStore` 和领域 DTO，把 `uiPreferenceStoreAdapter` 移到 App。

- [ ] **Step 8: 迁移 prompt**

把 `promptSharedFileReaderAdapter`、`promptStoreAdapter`、`promptIntentStoreAdapter` 和 uipreference 映射移到 `internal/app/prompt_store_adapters.go`。先导出 App 必须实现的 `PreferenceReader`、`SharedFileReader`、`Store` 以及其 `ListFilter`、`Template`、`TemplateSection`、`TemplateVersion`、`IntentDraft`、`IntentDraftListFilter` 等签名 DTO；逐方法检查不得残留 private 参数/返回类型。Prompt 模块保留 prompt-owned Port、intent DTO、runtime catalog DTO 和错误语义；App adapter 逐字段映射，不把 `promptstore`、`sharedfilestore`、`uipreference` 类型暴露回构造函数。

- [ ] **Step 9: 迁移 turn**

Turn 将 `turnDedupeStore` 导出为 `turn.DedupeStore`，将 `turnDedupeEntry`、`turnDedupeUpsertParams`、`turnDedupeBindProviderTurnIDParams` 导出为对应 domain DTO，并把 `errTurnDedupeNotFound` 导出为 `turn.ErrDedupeNotFound`。App adapter 映射 Store not-found 后，现有消费者继续通过 `errors.Is(err, turn.ErrDedupeNotFound)` 识别；optional Store 语义必须保持测试覆盖。Threadprompt 明确属于 Lane A，Lane B 禁止修改。

- [ ] **Step 10: 迁移 uistate**

将 `preferenceStore`、`sharedFileReader`、`bindingLookup` 和对应 DTO 导出为 uistate-owned Port。把 `preferenceStoreAdapter`、`sharedFileReaderAdapter`、`bindingAdapter` 移到 `internal/app/uistate_store_adapters.go`。`serviceParams.Bindings` 不再使用 `bindingstore.Store`，而是 typed `uistate.BindingLookup`；ID trim 行为保留在 App adapter 并增加字段断言测试。

### B3. Lane B 验证与提交

- [ ] **Step 11: 逐模块跑 focused GREEN**

按迁移完成顺序逐条运行：

```bash
set -euo pipefail
./scripts/test_with_guard.sh ./internal/module/cron -count=1
./scripts/test_with_guard.sh ./internal/module/dashboard -count=1
./scripts/test_with_guard.sh ./internal/module/datasource_v2 -count=1
./scripts/test_with_guard.sh ./internal/module/feedback -count=1
./scripts/test_with_guard.sh ./internal/module/insight -count=1
./scripts/test_with_guard.sh ./internal/module/memory -count=1
./scripts/test_with_guard.sh ./internal/module/personalization -count=1
./scripts/test_with_guard.sh ./internal/module/prompt -count=1
./scripts/test_with_guard.sh ./internal/module/turn -count=1
./scripts/test_with_guard.sh ./internal/module/uistate -count=1
```

每个 B1/B2/B3/B4 checkpoint 完成后都额外运行：

```bash
set -euo pipefail
./scripts/test_with_guard.sh ./internal/app -count=1
```

Expected: 每条命令 PASS；App adapter 编译、adapter tests 和 `TestAppModuleGraphIsClosed` 一并 PASS；对应模块 production `rg internal/store` zero matches。前一条失败时停止，不得继续用后续模块的成功掩盖失败。

- [ ] **Step 12: 跑 Lane B 汇总验证**

Run:

```bash
set -euo pipefail
./scripts/test_with_guard.sh \
  ./internal/module/cron \
  ./internal/module/dashboard \
  ./internal/module/datasource_v2 \
  ./internal/module/feedback \
  ./internal/module/insight \
  ./internal/module/memory \
  ./internal/module/personalization \
  ./internal/module/prompt \
  ./internal/module/turn \
  ./internal/module/uistate \
  -count=1

./scripts/test_with_guard.sh ./internal/app -count=1

if rg -n 'github\.com/anthropic-ai/super-agent-v3/internal/store' \
  internal/module \
  -g '*.go' -g '!**/*_test.go' \
  -g '!internal/module/thread/**' \
  -g '!internal/module/threadprompt/**'; then
  echo "unexpected Lane B Store dependency" >&2
  exit 1
else
  rg_status=$?
  test "$rg_status" -eq 1
fi
```

Expected: all tests PASS；`rg` zero matches。

- [ ] **Step 13: Lane B 原子提交**

如果 diff 可清晰按低耦合组和复杂组拆分，使用两个提交：

```text
refactor(module): 将简单 Store 适配迁入 App
refactor(module): 将复杂 Store 适配迁入 App
```

每个提交必须独立通过其 owned package tests；不得混入 Thread、SSA、registry 或契约。Hook 自动产生的 generator-owned 输出不得手改，须在报告中单列并由主 agent 最终统一再生成；禁止用 `--no-verify` 隐藏它们。

---

## Task C: Single-source 调用连通性升级为窄 SSA

**Files:**
- Create: `internal/archtest/ssaload/loader.go`
- Create: `internal/archtest/ssaload/loader_test.go`
- Modify: `internal/archtest/priority_ssa_scan.go`
- Modify: `internal/archtest/priority_ssa_util.go`
- Modify: `internal/archtest/orchestration_service_wide_helpers_test.go`
- Verify/modify focused regressions: `internal/archtest/orchestration_service_ssa_boundary_test.go`, `internal/archtest/orchestration_service_type_boundary_test.go`
- Modify: `internal/archtest/backend_boundary_single_source_test.go`
- Create: `internal/archtest/backend_boundary_single_source_ssa_test.go`
- Create: `internal/archtest/testdata/backend_boundary_ssa/<case>/fixture.go`

### C1. 先抽出共享 loader 并取得编译/候选 parity GREEN

- [ ] **Step 1: 创建 `internal/archtest/ssaload`**

`backend_boundary_single_source_test.go` 属于 external `archtest_test` package，不能访问父包未导出 `ssaLoadOptions`。因此把纯装载/构建能力下沉到无父包依赖的子包，暴露最小 API：

```go
type Options struct {
	RepoRoot string
	Patterns []string
	Tests    bool
	Overlay  map[string][]byte
	Include  func(*packages.Package) bool
}

func Load(opts Options) ([]*packages.Package, error)
func Build(pkgs []*packages.Package) (*ssa.Program, []*ssa.Package, error)
```

共享 loader 必须：

```text
默认使用 packages.LoadSyntax
opts.Tests=true 时显式增加 packages.NeedForTest
原样返回 packages.Load error
把 package.Errors 排序后作为 error 返回
保留 overlay
禁止空 package/空 syntax 被当成成功
```

实现中的 load mode 固定为：

```go
mode := packages.LoadSyntax
if opts.Tests {
	mode |= packages.NeedForTest
}
```

不得假设 `packages.LoadSyntax` 自动包含 `ForTest`；当前 `golang.org/x/tools/go/packages` 的 mode 常量没有这个保证。`Tests:false` 时不得无条件请求 `NeedForTest`，以免改变 priority/orchestration production 候选语义。

`ssaload` 不 import 父包 `internal/archtest`。`loadPrioritySSAPackages` 改为调用共享 loader，保持 `Tests:false`、`./cmd/...`、`./internal/...`、overlay 与现有 production filter 不变；priority 的 policy/filter 仍留在父包。必须用现有 priority fixture 和 production baseline 证明抽取前后候选 package 与违规集合完全一致。

当前 `orchestration_service_wide_helpers_test.go` 还有一套 `packages.Load(cfg, "./cmd/...", "./internal/...")`。将 `loadWideOrchestrationTypeGuardPackages` 同步改为调用 `ssaload.Load`，保留 `Tests:false`、`wideOrchestrationGuardOverlay`、`isOrchestrationServiceTypeGuardProductionPackagePath` 和按 package path 排序语义；删除重复的 package-error 聚合逻辑。新增回归测试，记录迁移前 golden package-path 集合，并断言共享 loader 的候选集合和现有 orchestration violations 不漂移。完成后 `internal/archtest` 不得残留第二个直接全仓 `packages.Load` 调用。

- [ ] **Step 2: 先证明 loader 自身、production parity 与 external test variant GREEN**

Boundary SSA 仅加载：

```go
ssaload.Options{
	RepoRoot: root,
	Patterns: []string{"./internal/archtest"},
	Tests:    true,
	Include:  includeBackendBoundaryArchtestVariant,
}
```

production guard 选择 `Name=="archtest_test"`、`ForTest` 指向真实 `internal/archtest` import path、包含 consumer `_test.go` syntax 的唯一 external-test variant，排除 synthetic test main 和 duplicate package；候选不是恰好一个即 fail-fast。

Fixture 测试不依赖 production variant 自动包含 `testdata`，而是逐个精确加载 `./internal/archtest/testdata/backend_boundary_ssa/<case>`，并断言返回 package 的 syntax 中确实含有该 fixture 的 `fixture.go`。load/type-check/SSA build 失败必须使测试失败，不允许退回 AST-only 或用空 syntax 形成假 GREEN。

固定增加以下 loader tests：

```go
func TestLoadIncludesForTestMetadataWhenTestsEnabled(t *testing.T)
func TestLoadDoesNotRequestForTestMetadataWhenTestsDisabled(t *testing.T)
func TestLoadRejectsPackageErrors(t *testing.T)
func TestBuildRejectsEmptySyntax(t *testing.T)
func TestBackendBoundaryLoaderSelectsUniqueExternalTestVariant(t *testing.T)
func TestPrioritySSALoaderExtractionPreservesCandidates(t *testing.T)
func TestWideOrchestrationLoaderExtractionPreservesCandidates(t *testing.T)
```

第一个测试必须证明：唯一 external variant 的 `Name == "archtest_test"`、`ForTest` 等于真实父包 import path、syntax 包含 consumer `_test.go`，并排除 synthetic test main；第二个测试证明 `Tests:false` 不请求 `ForTest` metadata；最后两个父包回归分别证明 priority/orchestration 的 package path 与 violation golden 不漂移。先运行这些 test-list 与 GREEN，只有 loader/harness 可编译且候选正确后才允许进入 behavioral RED：

```bash
set -euo pipefail
./scripts/test_with_guard.sh ./internal/archtest/ssaload \
  -list '^(TestLoadIncludesForTestMetadataWhenTestsEnabled|TestLoadDoesNotRequestForTestMetadataWhenTestsDisabled|TestLoadRejectsPackageErrors|TestBuildRejectsEmptySyntax)$' \
  > /tmp/ssaload-test-list.txt
for name in \
  TestLoadIncludesForTestMetadataWhenTestsEnabled \
  TestLoadDoesNotRequestForTestMetadataWhenTestsDisabled \
  TestLoadRejectsPackageErrors \
  TestBuildRejectsEmptySyntax; do
  test "$(grep -c "^${name}$" /tmp/ssaload-test-list.txt)" -eq 1
done
./scripts/test_with_guard.sh ./internal/archtest/ssaload -count=1
./scripts/test_with_guard.sh ./internal/archtest \
  -run '^(TestBackendBoundaryLoaderSelectsUniqueExternalTestVariant|TestPrioritySSALoaderExtractionPreservesCandidates|TestWideOrchestrationLoaderExtractionPreservesCandidates)$' \
  -count=1
```

Expected: PASS。编译、package load、ForTest 或 candidate selection 失败均在本阶段暴露，不能被下一阶段的预期 RED 吞掉。

### C2. 在可编译 harness 上证明调用边盲点

- [ ] **Step 3: 增加 SSA blind-spot fixtures**

Fixtures 必须分别覆盖：

```text
direct helper call                       -> reject
receiver selector/method value           -> reject
function variable alias                  -> reject
MakeClosure                              -> reject
local function returning closure         -> reject
interface invoke with local implementation -> reject
interface invoke with multiple local implementations -> reject only reachable implementation
unresolved dynamic call from fact-bearing function -> fail-fast
fact-bearing root -> no-fact bridge -> function parameter invoke -> fail-fast
connected helper split from existing regression -> reject via loadable fixture
unrelated helper facts from existing regression -> allow via loadable fixture
two unrelated functions                 -> allow
ordinary import-related string           -> allow
```

每个 reject fixture 把 `parseImportFiles` target 与 Store/Fx/MCP/platform policy facts 分散到真实可达 helper；allow fixture 保证相同字符串位于无调用关系函数时不合并。Fixture 必须是可由 `packages.Load` 精确加载的独立 package，不把 `testdata` 文件名当作已经进入 production archtest syntax。

- [ ] **Step 4: 运行并确认语义 RED**

```bash
set -euo pipefail
./scripts/go_with_guard.sh test ./internal/archtest \
  -list '^TestBackendBoundarySingleSourceSSAFixtures$' \
  > /tmp/backend-boundary-red-list.txt
test "$(grep -c '^TestBackendBoundarySingleSourceSSAFixtures$' /tmp/backend-boundary-red-list.txt)" -eq 1

red_output=$(mktemp)
set +e
./scripts/go_with_guard.sh test ./internal/archtest \
  -run '^TestBackendBoundarySingleSourceSSAFixtures$' -count=1 \
  >"$red_output" 2>&1
red_status=$?
set -e
test "$red_status" -ne 0
grep -E 'method value|function alias|MakeClosure|returned closure|interface invoke|ssa boundary' "$red_output" >/dev/null
if grep -E 'build failed|undefined:|setup failed|syntax error|package load failed|SSA build failed' "$red_output" >/dev/null; then
  cat "$red_output" >&2
  exit 1
fi
cat "$red_output"
rm -f "$red_output"
```

Expected: direct helper 继续被旧 AST 检测；method value、function alias、returned closure 或 interface invoke 至少一个以 fixture case 名/语义 token 证明漏报。只有这种 analyzer 断言失败算 RED；编译、装载、setup、SSA build 或零候选失败都不算。

### C3. 用 SSA 提供真实调用边

- [ ] **Step 5: 将 fact key 从函数名改为 SSA function identity**

现有 `map[string]backendBoundaryDependencyFacts` 改为稳定 source identity（package path + receiver + function + source position）并建立到 `*ssa.Function` 的一一映射。Facts 仍由 AST syntax 收集，但每个 `ssa.Function.Syntax()` 独立收集，避免同名 receiver method 或 closure 混入父函数；闭包使用其 own syntax/position，不并入父函数。

- [ ] **Step 6: 实现窄 callee resolver**

对每个 `ssa.CallInstruction`：

```text
优先 Common().StaticCallee()
解析 *ssa.Function
解析 *ssa.MakeClosure.Fn
递归解析 *ssa.Phi.Edges
递归解析本地函数 Return value
对 interface invoke 枚举当前 archtest test package 内可达实现
只保留同一 archtest test package 的 callee
```

resolver 必须复用或抽取现有 `priority_ssa_util.go` 的 value/call 解析语义，不再写第二套宽泛枚举。Interface invoke 只能沿 receiver value provenance（`MakeInterface`、conversion、`Phi`、local return）确定候选；禁止把“包内所有实现”无条件连边。多实现 fixture 必须证明不可达实现不会产生误报。

从任一持有 `parseTarget` 或 boundary policy fact 的 root 出发，遍历其整个本地可达调用子图；该子图任意函数出现无法解析的动态本地调用，都返回明确 `ssa/unresolved-boundary-call` violation。不能只检查 instruction 所在函数是否本地持有 fact。必须增加“fact-bearing root 静态调用无 fact bridge，bridge 通过函数参数调用 complementary-fact helper”的 fixture；若不实现 call-argument→callee-parameter 跨过程 provenance，该 fixture必须 fail-closed。与任何 fact-bearing root 不连通的普通函数才可忽略 unresolved dynamic call。

- [ ] **Step 7: 保留 AST policy checks**

以下逻辑不得迁到 SSA：

```text
hasBackendBoundaryPolicyLiteralShape
backendBoundaryConsumerGroupFactViolations
rule ID duplicate collection
SQLC/MCP 特殊 policy literal 检查
```

只删除 `collectProceduralBackendFunctionFacts` 中通过 `*ast.Ident` 填充 `callees` 的旧边生成逻辑。

现有 `TestBackendBoundaryRuleFactsRejectsConnectedHelperSplit` 与 `TestBackendBoundaryRuleFactsDoNotJoinUnrelatedFunctions` 不能继续只对内存字符串调用 `parser.ParseFile`，因为新的 `packages.Load`/SSA 看不到该伪 package。保留测试名，但把 source 移入 `testdata/backend_boundary_ssa/connected_helper_split` 与 `.../unrelated_helpers`，由同一个 fixture harness 精确加载、断言 `fixture.go` 进入 syntax，再调用真实 SSA analyzer。旧测试必须分别证明 reject/allow，而不是仅保持名称 GREEN。

- [ ] **Step 8: 跑 SSA GREEN 与 priority 回归**

Run:

```bash
set -euo pipefail
./scripts/test_with_guard.sh ./internal/archtest \
  -list 'Test(BackendBoundary|PrioritySSA)' \
  > /tmp/backend-boundary-test-list.txt
test "$(rg -c '^TestBackendBoundarySingleSourceSSAFixtures$' /tmp/backend-boundary-test-list.txt)" = 1
test "$(rg -c '^TestPrioritySSAGuardsUseUnifiedFreezeBaseline$' /tmp/backend-boundary-test-list.txt)" = 1

./scripts/test_with_guard.sh ./internal/archtest \
  -run '^(TestBackendBoundarySingleSourceSSAFixtures|TestBackendBoundaryRuleFactsDoNotJoinUnrelatedFunctions|TestBackendBoundaryRuleFactsRejectsConnectedHelperSplit|TestPrioritySSAGuardFixtures|TestPrioritySSAGuardsUseUnifiedFreezeBaseline)$' \
  -count=1
```

Expected: guarded test-list 成功，两个精确名称各命中一次，随后 all PASS；旧 priority SSA 和 orchestration-wide production scan 与 fixtures 不发生行为漂移。禁止使用 raw `go test | tee` 掩盖左侧编译失败，也禁止一个宽正则只命中其中一个测试形成假 GREEN。

- [ ] **Step 9: Lane C 原子提交**

Commit message:

```text
test(archtest): 用窄 SSA 加固边界事实连通性
```

报告 loader 复用点、fixture RED/GREEN、SSA load 时间、diagnostics 和 unresolved-call 策略。

---

## Task D: 主 agent 串行集成 canonical rule、App 图和契约

**Files:**
- Modify: `internal/app/modules.go`
- Modify: `internal/app/modules_graph_test.go`
- Modify: `internal/archtest/backend_boundary_registry.go`
- Modify: `internal/archtest/backend_boundary_canonical_dependencies_test.go`
- Modify: `internal/archtest/backend_boundary_governance.go`
- Modify: `internal/archtest/backend_boundary_governance_test.go`
- Rewrite: `internal/archtest/dependency_direction_module_store_test.go`
- Modify: `internal/archtest/dependency_direction_test.go`
- Modify: `docs/契约/modularity-convention.md`
- Modify: `docs/契约/onion-architecture-convention.md`
- Generator-owned refresh: `docs/doc/codemap/capability-contract/capability_manifest.json`、`README.md`、`docs/doc/codemap/13-archtest-boundaries.md`、`docs/doc/codemap/README.md`、`docs/doc/codemap/ai-index.json`、`docs/doc/codemap/project-map/**`

先在持久 controller session 从三个固定 branch 与 `LANE_BASE_SHA` 计算有序提交集合，并与唯一执行子 agent 的 lane 报告逐项相等。A/C 各要求一个原子提交；B 允许计划中的一个或两个提交：

```bash
set -euo pipefail
assert_integration_tree
declare -a A_SHAS B_SHAS C_SHAS
A_RAW=$(git -C "$PRIMARY_REPO" rev-list --reverse \
  "$LANE_BASE_SHA..codex/backend-onion-thread-port-20260710")
B_RAW=$(git -C "$PRIMARY_REPO" rev-list --reverse \
  "$LANE_BASE_SHA..codex/backend-onion-module-store-20260710")
C_RAW=$(git -C "$PRIMARY_REPO" rev-list --reverse \
  "$LANE_BASE_SHA..codex/backend-boundary-single-source-ssa-20260710")
test -n "$A_RAW"
test -n "$B_RAW"
test -n "$C_RAW"
A_SHAS=()
while IFS= read -r sha; do
  test -n "$sha" && A_SHAS+=("$sha")
done <<< "$A_RAW"
B_SHAS=()
while IFS= read -r sha; do
  test -n "$sha" && B_SHAS+=("$sha")
done <<< "$B_RAW"
C_SHAS=()
while IFS= read -r sha; do
  test -n "$sha" && C_SHAS+=("$sha")
done <<< "$C_RAW"
test "${#A_SHAS[@]}" -eq 1
test "${#B_SHAS[@]}" -ge 1
test "${#B_SHAS[@]}" -le 2
test "${#C_SHAS[@]}" -eq 1
export A_SHA=${A_SHAS[0]}
export C_SHA=${C_SHAS[0]}
printf 'A_SHA=%s\nB_SHAS=%s\nC_SHA=%s\n' "$A_SHA" "${B_SHAS[*]}" "$C_SHA"
```

主 agent 确认输出与 lane 报告完全一致后，定义唯一集成原语；helper 会再次通过 `git cat-file -e <sha>^{commit}` 验证：

```bash
set -euo pipefail
assert_integration_tree
cherry_pick_exact() {
  local sha=$1
  git -C "$INTEGRATION_TREE" cat-file -e "$sha^{commit}"
  if git -C "$INTEGRATION_TREE" cherry-pick "$sha"; then
    git -C "$INTEGRATION_TREE" rev-parse HEAD
    return 0
  fi

  local unmerged non_generated
  unmerged=$(git -C "$INTEGRATION_TREE" diff --name-only --diff-filter=U)
  non_generated=$(printf '%s\n' "$unmerged" | awk '
    $0 != "README.md" &&
    $0 != "docs/doc/codemap/13-archtest-boundaries.md" &&
    $0 != "docs/doc/codemap/README.md" &&
    $0 != "docs/doc/codemap/ai-index.json" &&
    $0 !~ /^docs\/doc\/codemap\/project-map\// { print }
  ')
  if test -n "$non_generated"; then
    printf 'non-generated conflicts require owner adjudication:\n%s\n' "$non_generated" >&2
    return 2
  fi
  test -n "$unmerged"

  git -C "$INTEGRATION_TREE" restore --ours -- \
    README.md \
    docs/doc/codemap/13-archtest-boundaries.md \
    docs/doc/codemap/README.md \
    docs/doc/codemap/ai-index.json \
    docs/doc/codemap/project-map
  (
    cd "$INTEGRATION_TREE"
    ./scripts/refresh_generated_artifacts.sh codemap
    ./scripts/refresh_generated_artifacts.sh project-map -- --filesystem-scan
  )
  git -C "$INTEGRATION_TREE" add -A -- \
    README.md \
    docs/doc/codemap/13-archtest-boundaries.md \
    docs/doc/codemap/README.md \
    docs/doc/codemap/ai-index.json \
    docs/doc/codemap/project-map
  git -C "$INTEGRATION_TREE" cherry-pick --continue
  git -C "$INTEGRATION_TREE" rev-parse HEAD
}
```

若 helper 返回 2，保持冲突现场并停止；主 agent 复核 source/test diff、取得 owner 修复后再继续，禁止自动选择 ours/theirs。`git restore --ours` 仅允许 helper 对上述五个 generator-owned surface 使用；capability manifest 仍归主 agent 最终集成提交。

- [ ] **Step 1: 复核并合并 Lane A**

检查：Thread-owned DTO 无 Store alias、app adapter 字段完整、`any` 归零、optional/fail-fast 语义不变，`threadStoreAdaptersModule()` 已让 Lane A 自身 App 图闭合。合并后运行 Thread、App focused tests 和 LSP diagnostics。

```bash
set -euo pipefail
assert_integration_tree
: "${A_SHA:?A_SHA is required}"
export INTEGRATION_AFTER_A=$(cherry_pick_exact "$A_SHA" | tail -n 1)
test "$(git -C "$INTEGRATION_TREE" rev-parse HEAD)" = "$INTEGRATION_AFTER_A"
```

- [ ] **Step 2: 复核并合并 Lane B**

检查每个 Module production import 为零、adapter 只位于 App、Store 未反向 import Module、`businessStoreAdaptersModule()` 在 Lane B 分支已闭合、所有 exported Port 可由 `package app` 实现、错误映射与 nil 语义有测试。发现 private DTO/sentinel、字段扩写或跨模块 Port 上提时必须回到真实消费者引用裁决。按 Step 1 的固定协议处理并刷新 hook-owned 生成物。

```bash
set -euo pipefail
assert_integration_tree
test "${#B_SHAS[@]}" -ge 1
for sha in "${B_SHAS[@]}"; do
  cherry_pick_exact "$sha"
done
export INTEGRATION_AFTER_B=$(git -C "$INTEGRATION_TREE" rev-parse HEAD)
```

- [ ] **Step 3: 复核 App 两个 bundle 的最终 provider 集合**

`internal/app/modules.go` 在 Task 0 后只调用两个 bundle function，不在集成阶段重新复制 provider 清单。主 agent 复核 `threadStoreAdaptersModule()`、`businessStoreAdaptersModule()` 的最终 provider/invoke 集合，确保每个 Module 收到原有必需/optional 能力；未知或缺失的必需 Store 让 Fx 启动失败。

登记完成后立即运行：

```bash
set -euo pipefail
assert_integration_tree
./scripts/test_with_guard.sh ./internal/app -run '^TestAppModuleGraphIsClosed$' -count=1
```

并为 Thread/threadprompt catalog 链及每个迁移模块新增/更新 graph ownership 断言；不能只证明 adapter 文件编译而未进入 Fx 图。

- [ ] **Step 4: 先把旧 procedural guard 改写为 canonical RED**

在现有 `dependency_direction_module_store_test.go` 中删除 `moduleStoreImportAllowlist`、`moduleStoreImportCollection`、`collectModuleStoreImports*`、`skipModuleStoreImportFile` 和 stale-allowlist 测试，避免与 registry 并存为第二事实源。把 `dependency_direction_test.go` 的 `rule17b` 改为调用 `assertCanonicalBoundaryRule(t, root, "module_no_store_imports")`，并在同一 canonical 测试文件添加：

```go
func TestModuleNoStoreImportsUsesCanonicalRule(t *testing.T)
func TestModuleNoStoreImportsRejectsFixture(t *testing.T)
```

第一个测试从 `DefaultBackendBoundaryRegistry` 查找 `module_no_store_imports`，并断言 owner、kind、production patterns、deny prefix 与 zero exceptions。第二个测试把临时 Go 文件的逻辑相对路径设为 `internal/module/example/leak.go`，内容导入 `internal/store/thread`，调用 `EvaluateBackendBoundaryFile` 并要求得到 violation。改写后仓库中不得残留 `moduleStoreImportAllowlist` 或 procedural collector 符号。

- [ ] **Step 5: 运行并确认 canonical RED**

Run:

```bash
set -euo pipefail
assert_integration_tree
./scripts/go_with_guard.sh test ./internal/archtest \
  -list '^(TestModuleNoStoreImportsUsesCanonicalRule|TestModuleNoStoreImportsRejectsFixture)$' \
  > /tmp/module-store-canonical-red-list.txt
test "$(grep -c '^TestModuleNoStoreImportsUsesCanonicalRule$' /tmp/module-store-canonical-red-list.txt)" -eq 1
test "$(grep -c '^TestModuleNoStoreImportsRejectsFixture$' /tmp/module-store-canonical-red-list.txt)" -eq 1

red_output=$(mktemp)
set +e
./scripts/go_with_guard.sh test ./internal/archtest \
  -run '^(TestModuleNoStoreImportsUsesCanonicalRule|TestModuleNoStoreImportsRejectsFixture)$' \
  -count=1 >"$red_output" 2>&1
red_status=$?
set -e
test "$red_status" -ne 0
grep -E 'module_no_store_imports|canonical rule.*missing|missing canonical rule|no violation' "$red_output" >/dev/null
if grep -E 'build failed|undefined:|setup failed|syntax error|package load failed' "$red_output" >/dev/null; then
  cat "$red_output" >&2
  exit 1
fi
cat "$red_output"
rm -f "$red_output"
```

Expected: FAIL because `module_no_store_imports` 尚不存在；不得因 fixture 语法、文件写入或零候选失败。

- [ ] **Step 6: 实现 canonical `module_no_store_imports` rule**

在 `defaultBackendBoundaryRules` 新增独立规则，不能扩写名字仍为 DB-only 的旧规则：

```go
BackendBoundaryRule{
	ID:           "module_no_store_imports",
	Owner:        "module_boundary",
	Reason:       "business modules own persistence ports and receive Store adapters from internal/app",
	Kind:         BoundaryRuleDenyImports,
	FilePatterns: patterns.module,
	Deny: boundaryPolicies(
		"module_boundary",
		patterns.module,
		[]string{"internal/store"},
		"module production code must not import Store implementations",
	),
	SkipTestFiles: true,
}
```

实现后用普通 GREEN 命令运行，禁止再次使用 Step 5 的“必须非零”wrapper：

```bash
set -euo pipefail
assert_integration_tree
./scripts/test_with_guard.sh ./internal/archtest \
  -run '^(TestModuleNoStoreImportsUsesCanonicalRule|TestModuleNoStoreImportsRejectsFixture)$' \
  -count=1
```

Expected: PASS。随后运行真实 production tree，必须零违规、零 exception、每条规则命中非零候选。

- [ ] **Step 7: 补齐 production coverage 自守卫**

`dependency_direction_module_store_test.go` 必须证明：

```text
module_no_store_imports 存在于 canonical registry
规则 owner/kind/pattern/deny prefix 精确
无 Exceptions
真实 production tree 零违规
新 module 子目录自动进入候选
internal/storex sibling prefix 不被误判为 internal/store
```

把新 rule ID 加入 `OnionBoundaryRuleIDs()`。它描述洋葱依赖方向，不重复加入 `CrossDomainBoundaryRuleIDs()`；若实现者主张跨域集合也需要，必须先给出现有消费者与非重复语义证据，由主 agent 裁决后再改。

同时把 `module_no_store_imports` 登记到 `defaultBackendBoundarySurfaces()` 的 `internal/module` surface。新增：

```go
func TestBackendBoundaryGovernanceRejectsOrphanCanonicalRule(t *testing.T)
func TestBackendBoundaryModuleSurfaceIncludesNoStoreRule(t *testing.T)
```

第一个测试向 fixture registry 增加未被任何 surface 引用的 canonical rule，要求 governance validator 返回 violation；默认 registry 中每条 canonical rule 必须至少被一个适用 surface 引用，不允许新 rule 只存在于 registry/Onion ID 集合却缺席生成治理 surface。第二个测试精确断言 `internal/module` surface 包含新 rule。

- [ ] **Step 8: 更新契约**

将模块化契约中“`module/*` 可以依赖 `store/*`”改为：

```text
internal/module/* 只能依赖 contract、dto、允许的 platform 能力、provider 语义端口和模块自有 Port；
internal/store/* 的具体实现只能在 internal/app 或独立 cmd 组合根中通过 adapter 注入。
```

洋葱契约明确：Module 拥有 Port/DTO；App adapter 负责 Module DTO 与 Store DTO 转换；Store 不 import Module；`internal/contract` 不承接单一 Module 私有持久化接口。

- [ ] **Step 9: 合并 Lane C**

主 agent 确认只有 `ssaload` 提供公共全仓装载/构建原语，`priority_ssa_scan.go` 和 `orchestration_service_wide_helpers_test.go` 都不再直接调用全仓 `packages.Load`；两者的 production filter、overlay、候选集合与违规集合未变。Boundary production SSA 只加载唯一 external archtest test variant，fixture 精确加载真实 case package，load 失败不降级。

```bash
set -euo pipefail
assert_integration_tree
: "${C_SHA:?C_SHA is required}"
export INTEGRATION_AFTER_C=$(cherry_pick_exact "$C_SHA" | tail -n 1)
test "$(git -C "$INTEGRATION_TREE" rev-parse HEAD)" = "$INTEGRATION_AFTER_C"
```

- [ ] **Step 10: 统一刷新 generator-owned 输出**

导出 Port、Fx provider 或能力 source map 可能改变 `capcontract` manifest；pre-commit 也会刷新 codemap/project-map。`docs/doc/codemap/capability-contract/capability_manifest.json` 只归主 agent 的最终集成提交，不由 lane 手工修改。主 agent 在 integration worktree 统一运行仓库现有 generator/check 命令，以最终集成源码再生成一次；这些输出必须单独标记为 generator-owned evidence，不作为评分或修复理由，但若 hook/guard 要求则必须进入正确提交。禁止 worker 手工编辑生成物，禁止 `--no-verify`。

Run:

```bash
set -euo pipefail
assert_integration_tree
make capcontract-refresh
make capcontract-check
./scripts/refresh_generated_artifacts.sh codemap
./scripts/refresh_generated_artifacts.sh project-map -- --filesystem-scan
make codemap-check
make project-map-check
```

若 `--filesystem-scan` 与 hook snapshot 的 tracked/untracked 边界造成差异，停止并按 `.githooks/pre-commit` 的 staged-index 语义复现，不得手工改生成文件骗过 check。

- [ ] **Step 11: 主 agent 集成提交**

建议提交：

```text
refactor(app): 统一装配 Module Store 适配器
test(archtest): 禁止 Module 直接依赖 Store
docs(architecture): 收紧 Module 持久化依赖方向
```

每个提交只 stage owned files，并在提交前检查 `git diff --cached --name-only`。

---

## Task E: 最终验收、评分门槛与推送条件

在持久 controller session 定义 fail-closed 零匹配断言。只有 `rg` exit 1 表示成功；exit 0 是发现违规，exit >=2 是扫描错误：

```bash
set -euo pipefail
assert_integration_tree
assert_no_rg_matches() {
  local output rg_status
  if output=$(rg "$@" 2>&1); then
    printf 'unexpected matches:\n%s\n' "$output" >&2
    return 1
  else
    rg_status=$?
  fi
  if test "$rg_status" -eq 1; then
    return 0
  fi
  printf 'rg scan failed (exit %s):\n%s\n' "$rg_status" "$output" >&2
  return "$rg_status"
}
```

- [ ] **Step 1: 证明生产 import 为零**

Run:

```bash
set -euo pipefail
assert_integration_tree
assert_no_rg_matches -l \
  'github\.com/anthropic-ai/super-agent-v3/internal/store' \
  internal/module -g '*.go' -g '!**/*_test.go'
```

Expected: exit 0，no output。

- [ ] **Step 2: 证明 Store 不反向依赖 Module**

Run:

```bash
set -euo pipefail
assert_integration_tree
assert_no_rg_matches -l \
  'github\.com/anthropic-ai/super-agent-v3/internal/module' \
  internal/store -g '*.go' -g '!**/*_test.go'
```

Expected: exit 0，no output。若当前 base 已有命中，必须在 Task 0 记录并由 canonical boundary 裁决，不能新增。

- [ ] **Step 3: 跑受影响包**

Run:

```bash
set -euo pipefail
assert_integration_tree
./scripts/test_with_guard.sh ./internal/module/... -count=1
./scripts/test_with_guard.sh ./internal/app -count=1
./scripts/test_with_guard.sh ./internal/archtest -count=1
make build-plain
make test
```

Expected: PASS。`make build-plain` 是所有生产反向消费者的硬编译门禁，`make test` 覆盖全仓外部 `_test.go` 消费者；任一失败都阻断 push，不得只记录为可接受风险。

- [ ] **Step 4: 跑仓库架构门禁**

Run:

```bash
set -euo pipefail
assert_integration_tree
make guard
: "${REMOTE_BASE_SHA:?REMOTE_BASE_SHA is required}"
export INTEGRATION_SHA=$(git -C "$INTEGRATION_TREE" rev-parse HEAD)
git -C "$INTEGRATION_TREE" diff --check "$REMOTE_BASE_SHA" "$INTEGRATION_SHA"
test -z "$(git -C "$INTEGRATION_TREE" status --porcelain)"
```

Expected: PASS。Range diff 覆盖已经提交的 lane/integration 变更；裸 `git diff --check` 不能替代该证明。

- [ ] **Step 5: LSP 全量变更诊断**

对每个变更 Go 文件运行 `file(diagnostics)`；Error、Warning、Information、Hint 必须全部为零。对所有导出/重命名 Port 用 `xref(references)` 证明调用方已迁移，无旧定义残留。

- [ ] **Step 6: 主 agent 检查 diff 合格**

必须逐项确认：

```text
26 -> 0 production Module Store imports
promptServiceCatalogPort any 已删除
Thread Port/DTO 不引用 Store package
所有 App 实现的 exported Port 签名不含 private DTO/sentinel
Lane A/B 各自通过真实 TestAppModuleGraphIsClosed
module_no_store_imports 为 canonical typed rule，zero exception
module_no_store_imports 已进入 internal/module governance surface，且 canonical rule 无 orphan
single-source AST policy checks 保留
SSA 只替换调用边，支持 alias/method/closure/invoke
fact-bearing root 的整个本地可达子图对 unresolved call fail-closed
SSA load 失败 fail-fast
priority 与 orchestration-wide 共用 ssaload，无第二套全仓 packages.Load
priority SSA 原行为未漂移
make build-plain 与 make test 全仓通过
契约与源码依赖方向一致
无 unrelated 文件混入；generator-owned 输出与最终源码一致且来源可追踪
```

- [ ] **Step 7: integration 分支 pre-push 条件**

只有以下条件同时成立才允许推送 integration branch：

```bash
set -euo pipefail
assert_integration_tree
: "${PRIMARY_REPO:?PRIMARY_REPO is required}"
: "${INTEGRATION_TREE:?INTEGRATION_TREE is required}"
: "${REMOTE_BASE_SHA:?REMOTE_BASE_SHA is required}"
git -C "$PRIMARY_REPO" fetch origin main
test "$(git -C "$PRIMARY_REPO" rev-parse origin/main)" = "$REMOTE_BASE_SHA"
export INTEGRATION_SHA=$(git -C "$INTEGRATION_TREE" rev-parse HEAD)
test -z "$(git -C "$INTEGRATION_TREE" status --porcelain)"
test "$(fingerprint_unrelated)" = "$UNRELATED_FINGERPRINT_BEFORE"
for path in "${UNRELATED_PATHS[@]}"; do
  test -z "$(git -C "$PRIMARY_REPO" ls-files -- "$path")"
  if git -C "$INTEGRATION_TREE" diff --name-only "$REMOTE_BASE_SHA" "$INTEGRATION_SHA" | grep -Fx "$path" >/dev/null; then
    printf 'integration overlaps unrelated file: %s\n' "$path" >&2
    exit 1
  fi
done
```

```text
所有 lane commit 已合入 codex/backend-onion-integration-20260710
integration worktree clean
HEAD 与预期 integration SHA 一致
pre-push hook PASS
fetch 后的 origin/main 仍等于 Task 0 的 REMOTE_BASE_SHA
```

若远端已推进，停止 push；不得仅凭过期 remote-tracking ref 继续。主 agent 必须重新裁决新提交、集成方式和需要重跑的 gates。

- [ ] **Step 8: 安全推进 main 与 post-push 证明**

先推送 integration branch：

```bash
set -euo pipefail
assert_integration_tree
git -C "$INTEGRATION_TREE" push -u origin "$INTEGRATION_BRANCH"
```

再次 `git fetch origin main` 并确认远端仍未漂移。确认本地 `main` 仍只包含用户原有 unrelated dirty，且 fast-forward 不会覆盖这些路径；若 dirty 与集成 diff 重叠则停止并报告 blocker，不得 stash/reset 用户文件。安全时执行：

```bash
set -euo pipefail
assert_integration_tree
test "$(fingerprint_unrelated)" = "$UNRELATED_FINGERPRINT_BEFORE"
git -C "$PRIMARY_REPO" fetch origin main
test "$(git -C "$PRIMARY_REPO" rev-parse origin/main)" = "$REMOTE_BASE_SHA"
git -C "$PRIMARY_REPO" merge --ff-only "$INTEGRATION_SHA"
git -C "$PRIMARY_REPO" push origin main
```

push 完成后读取真实远端引用：

```bash
set -euo pipefail
assert_integration_tree
git -C "$PRIMARY_REPO" fetch origin main
test "$(git -C "$PRIMARY_REPO" rev-parse HEAD)" = "$INTEGRATION_SHA"
test "$(git -C "$PRIMARY_REPO" rev-parse origin/main)" = "$INTEGRATION_SHA"
test "$(git -C "$PRIMARY_REPO" ls-remote origin refs/heads/main | awk '{print $1}')" = "$INTEGRATION_SHA"
git -C "$PRIMARY_REPO" status --short
test "$(fingerprint_unrelated)" = "$UNRELATED_FINGERPRINT_BEFORE"
for path in "${UNRELATED_PATHS[@]}"; do
  test -z "$(git -C "$PRIMARY_REPO" ls-files -- "$path")"
done
```

Expected: `HEAD == origin/main == INTEGRATION_SHA`；用户原有 unrelated dirty 状态逐项保持。`origin/main` 到达 integration SHA 是 post-push 结果，不能写成 push 前提。

## 3. 完成定义

本计划只有在以下事实同时成立时才算完成：

1. `internal/module` 生产代码对 `internal/store` import 为零。
2. Thread 的所有持久化与 prompt catalog 依赖都是 Module-owned typed Port；不存在 `*Port any`。
3. `internal/app` 是桌面进程唯一 Store→Module adapter 装配层。
4. canonical registry 对 Module→Store fail-closed，zero exception，新增 Module 自动进入覆盖；新规则进入 `internal/module` governance surface，canonical rules 无 orphan。
5. single-source guard 的 policy facts 仍由 AST 精确识别，调用连通性由窄 SSA 证明。
6. A/B 分支在各自 bundle 已登记 provider 的前提下独立通过真实 App Fx graph；所有跨包 Port 可由 App 编译实现。
7. priority、orchestration-wide 与 boundary SSA 共用 `ssaload` 装载原语，候选与违规集合无漂移。
8. 所有 focused tests、`internal/archtest`、`make guard`、`make build-plain`、`make test`、LSP diagnostics 和 committed-range `git diff --check` 通过。
9. 当前用户 unrelated dirty 文件未被修改、stage、commit 或删除。
