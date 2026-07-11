# Internal App Store Adapter Package Split Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不改变运行时行为、DTO、Store 语义和 Fx 图的前提下，把 `internal/app` 中跨领域聚集的 Store adapter 拆成可独立理解、编译和测试的领域包，同时升级 Archtest，使新的包边界与导入方向成为机器可执行约束。

**Architecture:** `internal/app` 继续作为唯一桌面组合根，只保留进程入口、生命周期、orchestration/runtime bridge 和根 Fx 图。业务持久化转换迁入 `internal/app/storeadapter/<domain>`；跨业务的 platform/provider Store 消费迁入 `internal/app/runtimeadapter/<consumer>`。两个 aggregator 只导出 `Module`，根 `internal/app/modules.go` 只组合这些模块，不再持有领域 adapter 构造器。

**Tech Stack:** Go 1.25.7、Uber Fx 1.24.0、Go AST/LSP、`internal/archtest` canonical backend boundary registry、仓库原生 guard/codemap generators。

**Verification Surface:** `./internal/app/...`、`./internal/archtest`、受影响的 `internal/module/*` 与 `internal/store/*` 编译面、`make guard`、`make codemap-check`、`make project-map-check`、LSP diagnostics。

---

## 1. 已锁定的当前基线

基线日期：2026-07-11；基线分支：`main`；初次取证时 `HEAD=ae9282e40`，文档复核锚点为 `HEAD=6b8f80ffe`。两者之间未改动 `internal/app`、`internal/archtest` 或 04/13 codemap，指标复算一致。执行时必须重新记录实际 `HEAD`，不得把本节当作未来工作树的当前状态。

| 指标 | 当前值 | 证据 |
|---|---:|---|
| `internal/app` Go 文件 | 74 | 29 个生产文件 + 45 个测试文件 |
| 总行数 | 14,161 | 生产 5,534 + 测试 8,627 |
| 直接 import `internal/store/*` 的生产文件 | 16 | 13 个业务域 adapter、`mcpcontrol_log_sink.go`、`toolbridge_adapters.go`、`modules.go` |
| 根业务 Store provider | 26 | `businessStoreAdaptersModule()` 的 `fx.Provide` 清单 |
| 当前基线验证 | PASS | `./scripts/test_with_guard.sh ./internal/app ./internal/archtest -count=1`；guard 0 违规，两个包通过 |

LSP 已确认：

- `businessStoreAdaptersModule()` 定义在 `internal/app/business_store_adapters_module.go:6-40`。
- 它被 `internal/app/modules.go` 和 dashboard/prompt/uistate 的图测试直接引用。
- `internal/archtest/interface_isolation_dashboard_guard_test.go` 把 `internal/app/dashboard_store_adapters.go` 写成常量。
- optional dependency 守卫的 evidence key 还绑定 `business_store_adapters_module.go`、`thread_prompt_adapters.go` 等具体路径。
- `fx_assembly_scope` 当前允许 `internal/app/**/*.go` 使用 Fx；拆成 `internal/app` 子包不会破坏该规则，但新子包仍需要自己的窄导入规则。

## 2. 成功标准与非目标

### 2.1 必须全部满足的成功标准

1. `internal/app` 根 package 的生产文件不超过 12 个、生产代码不超过 2,200 行；统计只看 `internal/app/*.go`，不递归子包。
2. 根 package 除 `modules.go` 对 canonical `internal/store` 聚合模块的 import 外，不再 import任何 `internal/store/<leaf>`；root 只允许 import `internal/app/storeadapter` 与 `internal/app/runtimeadapter` 两个 aggregator，禁止直接 import child domain/consumer。
3. 12 个业务域分别位于独立包：`cron`、`dashboard`、`datasourcev2`、`feedback`、`insight`、`memory`、`personalization`、`prompt`、`skill`、`thread`、`turn`、`uistate`。
4. `mcpcontrol`、`toolbridge`、`cachekeepalive`、`builtintools` 四个跨层 Store 消费面不再由根 package 实现。
5. 每个领域包有自己的 `Module`、adapter 实现、同包行为测试；端口可外部实现性测试与对应领域放在一起。
6. `businessStoreAdaptersModule()`、`threadStoreAdaptersModule()` 和旧根级 adapter 文件被删除，不保留 compat wrapper、type alias 或双注册。
7. Fx 图中的 provider 数量、类型、optional tag、invoke/decorate 顺序和错误语义不变；任何缺依赖仍按当前行为 fail-fast。
8. Archtest 不只接受新文件路径，还新增 canonical package boundary rule，阻止领域 adapter 横向 import 其它 adapter 域或无登记的 module/store。
9. dashboard 的 owner-local interface 守卫改为按 package + function symbol 定位 adapter，不再依赖单个文件名。
10. `rg 'internal/app/.*(store_adapters|toolbridge_adapters|mcpcontrol_log_sink).*\.go' internal/archtest` 只允许保留明确解释过的精确 evidence key；不存在指向已删除文件的路径。
11. focused package test、`internal/app/...`、Archtest、guard、codemap/project-map check 与 LSP diagnostics 全部通过。
12. 变更按阶段原子提交；任一阶段失败就停止，不通过 fallback、旧新双跑或降低 baseline 继续。

### 2.2 明确非目标

- 不修改 module-owned Store port 的方法签名。
- 不修改 Store DTO、module DTO、字段映射、错误文本、JSON 形状、事务或 SQL。
- 不移动 `app.go`、`runner.go`、`dashboard_adapter.go`、`thread_orchestration_adapter.go`、`orchestration_dag_runtime_adapter.go`、`runtime_reporter_adapter.go`。
- 不把 adapter 下沉到 `internal/store`，也不让 `internal/module` import `internal/store`。
- 不引入共享 service locator、全局 registry、生成式 DI 或新的兼容层。
- 不顺手处理无关 optional/noop 行为；若现有行为存在问题，单独立项并先锁回归测试。

## 3. 目标文件结构与所有权

```text
internal/app/
├── app.go
├── modules.go
├── runner.go
├── ...保留的 root/runtime bridge 文件
├── storeadapter/
│   ├── module.go
│   ├── cron/{module.go,adapter.go,adapter_test.go,port_external_test.go}
│   ├── dashboard/{module.go,adapter.go,adapter_test.go,port_external_test.go}
│   ├── datasourcev2/{module.go,adapter.go,adapter_test.go,port_external_test.go}
│   ├── feedback/{module.go,adapter.go,adapter_test.go,port_external_test.go}
│   ├── insight/{module.go,adapter.go,adapter_test.go,port_external_test.go}
│   ├── memory/{module.go,adapter.go,adapter_test.go,port_external_test.go}
│   ├── personalization/{module.go,adapter.go,adapter_test.go,port_external_test.go}
│   ├── prompt/{module.go,adapter.go,adapter_test.go,port_external_test.go}
│   ├── skill/{module.go,adapter.go,adapter_test.go,tool_crud_test.go}
│   ├── thread/{module.go,store.go,prompt.go,adapter_test.go,port_external_test.go}
│   ├── turn/{module.go,adapter.go,adapter_test.go,port_external_test.go}
│   └── uistate/{module.go,adapter.go,adapter_test.go,port_external_test.go}
└── runtimeadapter/
    ├── module.go
    ├── mcpcontrol/{module.go,log_sink.go,log_sink_test.go}
    ├── toolbridge/{module.go,adapter.go,adapter_test.go}
    ├── cachekeepalive/{module.go,adapter.go,adapter_test.go}
    └── builtintools/{module.go,adapter.go,adapter_test.go}

internal/app/internal/storeguard/{nil.go,nil_test.go}
internal/testutil/storeadapter/{assert.go,assert_test.go}
```

所有权规则：

- integration owner 是 `internal/app/modules.go`、两个 aggregator、`internal/archtest/**`、当前 code map 与最终提交的唯一写 owner。
- 子代理可并行做领域源码/LSP 调查与只读复核，但只把 domain-only patch 交给 integration owner；不得直接提交或修改 shared seams。
- integration owner 按 Task 3→7 串行应用 domain patch，并在同一原子提交内同步 root wiring 与对应 Archtest 路径证据。
- pre-commit 会从 staged snapshot 刷新并 stage README、13 卷、codemap index 与 project-map；每次提交都由 integration owner审阅这些派生文件，不能把它们假定为最后一次提交才出现。

## 4. 迁移映射

| 旧文件/符号 | 新包 | 新 Module provider |
|---|---|---|
| `cron_store_adapters.go` | `storeadapter/cron` | `newCronStoreAdapter`、`provideCronStore`、`provideCronSchedulerStore` |
| `dashboard_store_adapters.go` | `storeadapter/dashboard` | 9 个 `provideDashboard*Reader/Executor` |
| `datasource_v2_store_adapters.go` | `storeadapter/datasourcev2` | `provideDatasourceV2Store` |
| `feedback_store_adapters.go` | `storeadapter/feedback` | `provideFeedbackWriter` |
| `insight_store_adapters.go` | `storeadapter/insight` | `provideInsightReader`、`provideInsightWriter` |
| `memory_store_adapters.go` | `storeadapter/memory` | shared-file reader/deleter 两个 provider |
| `personalization_store_adapters.go` | `storeadapter/personalization` | `providePersonalizationPreferenceStore` |
| `prompt_store_adapters.go` | `storeadapter/prompt` | prompt store/preference/shared-file 三个 provider |
| `skill_store_adapters.go` | `storeadapter/skill` | mutation audit + tool persistence 两个 provider |
| `thread_store_adapters.go` + `thread_prompt_adapters.go` | `storeadapter/thread` | 5 个 provider + `registerThreadPromptProvidersFromApp` invoke |
| `turn_store_adapters.go` | `storeadapter/turn` | optional `provideTurnDedupeStore` |
| `uistate_store_adapters.go` | `storeadapter/uistate` | preference/shared-file/binding 三个 provider |
| `mcpcontrol_log_sink.go` | `runtimeadapter/mcpcontrol` | `provideMCPControlSystemLogSink` |
| `toolbridge_adapters.go` | `runtimeadapter/toolbridge` | 原 `toolbridgeAdaptersModule` + `toolbridgeCodexBindingModule` |
| `modules.go` keepalive helpers | `runtimeadapter/cachekeepalive` | binding/thread lookup 两个 provider |
| `modules.go` native/builtin tool helpers | `runtimeadapter/builtintools` | native descriptors + disabled tools function |

## 5. 执行 DAG

```text
T1 temporary RED contract -> clean worktree
  └─> T2 single canonical helpers
       └─> T3 simple domains + atomic root/Archtest integration
            └─> T4 prompt/skill + atomic root integration
                 └─> T5 cron/dashboard + atomic root/Archtest integration
                      └─> T6 thread + atomic root/Archtest integration
                           └─> T7 runtime consumers + atomic root/Archtest integration
T7 ───────────────────────> T8 final graph/metric contract
T8 ────────────────────────> T9 canonical package-boundary Archtest
T9 ────────────────────────> T10 generated docs + full verification
```

T3-T10 必须串行，因为每步都要同步 root aggregator 或路径型 Archtest，强行并发会把冲突集中到同一 shared seam。执行前先用 `使用git工作区` 创建 integration worktree；领域子代理只做可丢弃的准备 worktree/patch，integration owner 不直接 cherry-pick 可能破坏 root 图的 lane commit，而是把 domain patch 与 shared-seam 更新组成一个可构建提交。

每个 Commit step 都遵循同一 hook 合同：提交标题必须含中文；提交前 generated paths 必须无未暂存/未跟踪漂移；pre-commit 会从 staged snapshot 刷新并 stage `README.md`、13 卷、codemap README/index 与整个 project-map。提交成功后立即运行：

```bash
git show --stat --oneline HEAD
git status --short
```

integration owner 必须把 hook 生成物逐项视为本提交派生 owned files，并确认没有 unrelated 文件进入 `git show --name-only HEAD^..HEAD`。hook 失败时修根因，不使用旁路。

## 6. 详细任务

### Task 1: 用临时 RED 测试冻结目标包边界

**Files:**
- Temporary create then delete: `internal/archtest/app_adapter_package_boundary_red_test.go`

- [ ] **Step 1: 写目标边界的失败测试**

测试必须扫描 `internal/app/*.go` 的 production imports，只允许 `modules.go` import 精确路径 `github.com/anthropic-ai/super-agent-v3/internal/store`，拒绝任何 `internal/store/` leaf；root 只允许两个精确 aggregator import，拒绝 adapter child import；同时要求 12 个 storeadapter 目录和 4 个 runtimeadapter 目录存在。

```go
func TestAppRootDoesNotOwnLeafStoreAdapters(t *testing.T) {
	root := repoRoot(t)
	violations := leafStoreImportsInDir(t, root, "internal/app", map[string]string{
		"modules.go": "github.com/anthropic-ai/super-agent-v3/internal/store",
	})
	failIfViolations(t, violations)
}
```

- [ ] **Step 2: 运行测试并确认自然 RED**

Run: `./scripts/test_with_guard.sh ./internal/archtest -run TestAppRootDoesNotOwnLeafStoreAdapters -count=1`

Expected: FAIL，列出当前 15 个 root leaf-store import 文件；`modules.go` 的 canonical root import 不计违规。

- [ ] **Step 3: 记录 RED 与基线 PASS**

Run: `./scripts/test_with_guard.sh ./internal/app ./internal/archtest -count=1`

Expected: 只有新目标边界测试失败；既有 app/archtest 测试仍通过。把失败摘要保留在执行记录，不提交日志文件。

- [ ] **Step 4: 删除临时 RED 文件并恢复干净工作树**

使用 `apply_patch` 删除本任务创建的 `internal/archtest/app_adapter_package_boundary_red_test.go`，然后运行：

```bash
git status --short
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

Expected: 临时文件不再存在，Archtest PASS，Task 2 开始前 worktree 只保留执行前已有的 unrelated dirty。RED 的命令、exit code 和违规摘要写入执行 handoff/EVIDENCE，不提交日志文件。

### Task 2: 原子迁移到唯一 canonical helper

**Files:**
- Create: `internal/app/internal/storeguard/nil.go`
- Create: `internal/app/internal/storeguard/nil_test.go`
- Create: `internal/testutil/storeadapter/assert.go`
- Create: `internal/testutil/storeadapter/assert_test.go`
- Modify: the 10 production consumers returned by LSP xref (`cron`、`dashboard`、`datasourcev2`、`feedback`、`insight`、`memory`、`personalization`、`prompt`、`turn`、`uistate` adapter files)
- Modify: the 10 matching `*_store_adapters_test.go` consumers of the one-hot assertion
- Delete in this task: `internal/app/business_store_adapter_nil.go`
- Delete in this task: `internal/app/business_store_adapter_nil_test.go`
- Delete in this task: `internal/app/business_store_adapter_test_helpers_test.go`

- [ ] **Step 1: 搬移 nil 识别实现并保留全部 nil-capable kind**

```go
package storeguard

import "reflect"

// IsNil 识别 nil interface 与其中承载的 typed nil Store。
func IsNil[Store any](store Store) bool {
	value := reflect.ValueOf(store)
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return value.IsNil()
	default:
		return false
	}
}
```

- [ ] **Step 2: 一次性切换全部 production 引用并删除旧 owner**

用 LSP `xref(references)` 重新取得 `isNilBusinessStore` 的完整引用集，把每个调用改成 `storeguard.IsNil`；同一未提交 diff 中删除旧 root helper。新 helper 位于 `internal/app/internal/storeguard`，因此 root package 与所有 `internal/app/**` 子包都能导入，且仓库其它顶层包不能导入。

Run: `rg -n 'isNilBusinessStore' internal/app`

Expected: 0 matches；生产中只有 `internal/app/internal/storeguard/nil.go` 持有 nil-capable kind 清单。

- [ ] **Step 3: 把 one-hot DTO 映射断言导出为测试工具并切换全部测试**

`internal/testutil/storeadapter` 声明 package `storeadaptertest`，提供两种入口并共享同一个反射 engine：

```go
func AssertFieldsMap[Source, Target any](t *testing.T, mapField func(Source) Target)
func AssertFieldsMapE[Source, Target any](t *testing.T, mapField func(Source) (Target, error))
```

engine 必须覆盖双向字段集合、逐字段 one-hot、嵌套 struct、pointer、slice、map、标量与 `time.Time`；`AssertFieldsMap` 用无错误 mapper，`AssertFieldsMapE` 保留当前 10 个业务 adapter 的错误返回断言。更新 LSP xref 返回的 10 个测试文件使用 `AssertFieldsMapE`，在同一 diff 删除旧 test helper。Task 6 再把 Thread 的纯 mapper 切到 `AssertFieldsMap` 并删除第二套反射 engine。不得把 test helper 用于 production provider。

- [ ] **Step 4: 运行 helper 与当前 root 包测试**

Run: `./scripts/test_with_guard.sh ./internal/app/internal/storeguard ./internal/testutil/storeadapter ./internal/app -count=1`

Expected: PASS；LSP diagnostics 对全部 changed Go files 为 0。

- [ ] **Step 5: Commit**

```bash
git add internal/app/internal/storeguard internal/testutil/storeadapter
git add -u -- internal/app/business_store_adapter_nil.go internal/app/business_store_adapter_nil_test.go internal/app/business_store_adapter_test_helpers_test.go
git add \
  internal/app/cron_store_adapters.go internal/app/cron_store_adapters_test.go \
  internal/app/dashboard_store_adapters.go internal/app/dashboard_store_adapters_test.go \
  internal/app/datasource_v2_store_adapters.go internal/app/datasource_v2_store_adapters_test.go \
  internal/app/feedback_store_adapters.go internal/app/feedback_store_adapters_test.go \
  internal/app/insight_store_adapters.go internal/app/insight_store_adapters_test.go \
  internal/app/memory_store_adapters.go internal/app/memory_store_adapters_test.go \
  internal/app/personalization_store_adapters.go internal/app/personalization_store_adapters_test.go \
  internal/app/prompt_store_adapters.go internal/app/prompt_store_adapters_test.go \
  internal/app/turn_store_adapters.go internal/app/turn_store_adapters_test.go \
  internal/app/uistate_store_adapters.go internal/app/uistate_store_adapters_test.go
git diff --cached --name-only
git diff --cached --check
git commit -m "refactor(app): 收敛 Store adapter 守卫"
```

Expected: 提交中从旧 helper 到新 helper 是一次原子 owner 转移；不存在两个提交间可独立修改的重复规则。

### Task 3: 拆分简单业务域 adapter 包

**Files:**
- Create/move: `internal/app/storeadapter/{datasourcev2,feedback,insight,memory,personalization,turn,uistate}/**`
- Create: `internal/app/storeadapter/module.go`（此阶段只聚合已迁移的 7 个域）
- Modify: `internal/app/business_store_adapters_module.go`（只保留未迁移 provider）
- Modify: `internal/app/modules.go`（首次接入 `storeadapter.Module`）
- Modify: `internal/archtest/dependency_optional_boundary_test.go`（turn optional evidence 原子迁移）
- Move matching tests from `internal/app/*_store_adapters_test.go` and `*_port_external_test.go`

- [ ] **Step 1: 每个域先只改 package/import，并保持函数体与错误文本不变**

包名依次为 `datasourcev2adapter`、`feedbackadapter`、`insightadapter`、`memoryadapter`、`personalizationadapter`、`turnadapter`、`uistateadapter`。Task 2 已把实现切到 `storeguard.IsNil`、错误返回 mapper 测试切到 `storeadaptertest.AssertFieldsMapE`；本任务只移动这些已统一的调用，不再改 helper 语义。

- [ ] **Step 2: 为每个域写显式 Module**

```go
// turn/module.go
var Module = fx.Module("turnadapter",
	fx.Provide(fx.Annotate(
		provideTurnDedupeStore,
		fx.ParamTags(`optional:"true"`),
	)),
)
```

其余域的 provider 清单必须严格等于第 4 节映射，不增加 invoke/decorate。

- [ ] **Step 3: 在同一 diff 完成 root 与 optional evidence 切换**

`storeadapter.Module` 先只列出本任务 7 个域；从 `businessStoreAdaptersModule()` 删除对应 provider，并在 root 中把新 aggregator 放到 `threadStoreAdaptersModule()` 之后、`businessStoreAdaptersModule()` 之前。Task 6 删除旧 thread option 后，aggregator 自然占据同一相对位置，保证 Thread root-flat invoke 相对其它 invoke 的顺序不变。`provideTurnDedupeStore` 移动的同一 diff 中，把 optional scanner key 从 `internal/app/business_store_adapters_module.go` 改为 `internal/app/storeadapter/turn/module.go` 的真实扫描结果；禁止同时接受新旧路径。

把现有 `TestBusinessStoreAdaptersModuleOwnsUIStatePorts` 改为在 `uistateadapter` 包内验证 `uistateadapter.Module`；本任务后不允许新包测试继续引用 root business aggregator。

- [ ] **Step 4: 逐域单文件与单包验证**

Run for each changed `.go`: `./scripts/test_with_guard.sh <changed-file.go>`

Run:

```bash
./scripts/test_with_guard.sh \
  ./internal/app/storeadapter/datasourcev2 \
  ./internal/app/storeadapter/feedback \
  ./internal/app/storeadapter/insight \
  ./internal/app/storeadapter/memory \
  ./internal/app/storeadapter/personalization \
  ./internal/app/storeadapter/turn \
  ./internal/app/storeadapter/uistate -count=1
```

Expected: PASS；端口外部实现性测试仍为 compile-time assertions。

- [ ] **Step 5: 验证 root/Archtest 仍为 GREEN**

Run: `./scripts/test_with_guard.sh ./internal/app/... ./internal/archtest -count=1`

Expected: PASS；不存在删除旧文件后仍指向旧路径的 guard。

- [ ] **Step 6: Commit**

```bash
git add internal/app/storeadapter internal/app/modules.go internal/app/business_store_adapters_module.go internal/archtest/dependency_optional_boundary_test.go
git add -u -- \
  internal/app/datasource_v2_store_adapters.go internal/app/datasource_v2_store_adapters_test.go internal/app/datasource_v2_port_external_test.go \
  internal/app/feedback_store_adapters.go internal/app/feedback_store_adapters_test.go internal/app/feedback_port_external_test.go \
  internal/app/insight_store_adapters.go internal/app/insight_store_adapters_test.go internal/app/insight_port_external_test.go \
  internal/app/memory_store_adapters.go internal/app/memory_store_adapters_test.go internal/app/memory_port_external_test.go \
  internal/app/personalization_store_adapters.go internal/app/personalization_store_adapters_test.go internal/app/personalization_port_external_test.go \
  internal/app/turn_store_adapters.go internal/app/turn_store_adapters_test.go internal/app/turn_port_external_test.go \
  internal/app/uistate_store_adapters.go internal/app/uistate_store_adapters_test.go internal/app/uistate_port_external_test.go
git diff --cached --name-only
git diff --cached --check
git commit -m "refactor(app): 拆分简单 Store adapter 领域"
```

### Task 4: 拆分 prompt 与 skill adapter 包

**Files:**
- Create/move: `internal/app/storeadapter/prompt/**`
- Create/move: `internal/app/storeadapter/skill/**`
- Move: `internal/app/prompt_store_adapters_test.go`
- Move: `internal/app/prompt_port_external_test.go`
- Move: `internal/app/skill_store_adapters_test.go`
- Move: `internal/app/skill_tool_crud_test.go`
- Modify: `internal/app/storeadapter/module.go`
- Modify: `internal/app/business_store_adapters_module.go`
- Modify: `internal/app/modules.go`

- [ ] **Step 1: 迁移 prompt 并固定三个 provider**

```go
var Module = fx.Module("promptadapter",
	fx.Provide(
		providePromptStore,
		providePromptPreferenceReader,
		providePromptSharedFileReader,
	),
)
```

- [ ] **Step 2: 迁移 skill 并固定两个 provider**

```go
var Module = fx.Module("skilladapter",
	fx.Provide(
		provideSkillMutationAuditStore,
		provideSkillToolPersistence,
	),
)
```

- [ ] **Step 3: 验证字段映射、错误传播和 CRUD 测试**

Run: `./scripts/test_with_guard.sh ./internal/app/storeadapter/prompt ./internal/app/storeadapter/skill -count=1`

Expected: PASS；skill mutation audit 和 tool persistence 的错误必须原样向上返回。

- [ ] **Step 4: 原子切换 root wiring 并验证完整 App 图**

把 prompt/skill Module 加入 aggregator；从 `businessStoreAdaptersModule()` 删除 prompt provider，从 root `fx.Provide` 删除 skill 两个 provider。迁移后的 prompt/skill module test 直接验证自己的 `Module`，不再引用 root business aggregator。

Run: `./scripts/test_with_guard.sh ./internal/app/... ./internal/archtest -count=1`

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/app/storeadapter/prompt internal/app/storeadapter/skill internal/app/storeadapter/module.go internal/app/business_store_adapters_module.go internal/app/modules.go
git add -u -- internal/app/prompt_store_adapters.go internal/app/prompt_store_adapters_test.go internal/app/prompt_port_external_test.go internal/app/skill_store_adapters.go internal/app/skill_store_adapters_test.go internal/app/skill_tool_crud_test.go
git diff --cached --check
git commit -m "refactor(app): 拆分 Prompt 与 Skill Store adapter"
```

### Task 5: 拆分 cron 与 dashboard adapter 包

**Files:**
- Create/move: `internal/app/storeadapter/cron/**`
- Create/move: `internal/app/storeadapter/dashboard/**`
- Modify: `internal/app/storeadapter/module.go`
- Modify: `internal/app/modules.go`
- Delete after its final providers move: `internal/app/business_store_adapters_module.go`
- Modify: `internal/archtest/interface_isolation_dashboard_guard_test.go`
- Modify: `internal/archtest/interface_isolation_guard_test.go`

- [ ] **Step 1: 迁移 cron，保留 shared root adapter 投影语义**

```go
var Module = fx.Module("cronadapter",
	fx.Provide(
		newCronStoreAdapter,
		provideCronStore,
		provideCronSchedulerStore,
	),
)
```

`cron.Store` 和 `cron.SchedulerStore` 必须继续来自同一个 required `cronStoreAdapter`，typed nil 继续返回 `errCronStoreAdapterMissing`。

- [ ] **Step 2: 迁移 dashboard 的 9 个 reader/executor provider**

Module 必须注册第 4 节列出的全部 9 个 provider，不在 root aggregator 展开函数清单。

把 `TestBusinessStoreAdaptersModuleOwnsDashboardPorts` 改为在 `dashboardadapter` 包内验证 `dashboardadapter.Module`，确保删除 business aggregator 后没有测试靠旧 owner 才能编译。

- [ ] **Step 3: 把 dashboard 守卫从文件定位升级为 package + symbol 定位**

新增 helper：在 `internal/app/storeadapter/dashboard` 目录 AST 扫描 production Go 文件，要求每个 `provideDashboard*` symbol 恰好出现一次，再读取参数类型。0 个或多个定义都 fail-fast。

```go
adapterRelPath := singleFunctionFileInPackage(
	t, root, "internal/app/storeadapter/dashboard", check.funcName,
)
actual, ok := functionParamType(t, root, adapterRelPath, check.funcName, check.paramName)
```

这两个 Archtest 文件只由 integration owner 在本任务写一次；Task 9 不再重复修改 dashboard specialized guard。把 cron/dashboard Module 加入 aggregator，在 `modules.go` 同时删除 `businessStoreAdaptersModule()` 调用，再删除已经清空的定义文件；三者处于同一 staged diff。

- [ ] **Step 4: 验证**

Run:

```bash
./scripts/test_with_guard.sh ./internal/app/storeadapter/cron ./internal/app/storeadapter/dashboard -count=1
./scripts/test_with_guard.sh ./internal/app/... ./internal/archtest -count=1
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/app/storeadapter/cron internal/app/storeadapter/dashboard internal/app/storeadapter/module.go internal/app/modules.go internal/archtest/interface_isolation_dashboard_guard_test.go internal/archtest/interface_isolation_guard_test.go
git add -u -- internal/app/business_store_adapters_module.go internal/app/cron_store_adapters.go internal/app/cron_store_adapters_test.go internal/app/cron_port_external_test.go internal/app/dashboard_store_adapters.go internal/app/dashboard_store_adapters_test.go internal/app/dashboard_port_external_test.go
git diff --cached --check
git commit -m "refactor(app): 拆分 Cron 与 Dashboard Store adapter"
```

提交前用 `git diff --cached --name-only` 确认没有带入其它 Archtest 文件。

### Task 6: 拆分 thread Store 与 prompt adapter 包

**Files:**
- Create/move: `internal/app/storeadapter/thread/store.go`
- Create/move: `internal/app/storeadapter/thread/prompt.go`
- Create: `internal/app/storeadapter/thread/module.go`
- Modify: `internal/app/storeadapter/module.go`
- Modify: `internal/app/modules.go`
- Move/split: `internal/app/thread_adapters_test.go`
- Move: `internal/app/thread_prompt_port_external_test.go`
- Modify in this task: `internal/archtest/dependency_optional_thread_boundary_test.go`

- [ ] **Step 1: 迁移两个实现文件并保持 thread-owned port 不变**

不得把 `thread.ThreadStore`、`thread.BindingStore`、`threadprompt.PromptStore` 移到 `internal/contract`。

- [ ] **Step 2: 固定原有 provide/invoke 顺序**

```go
var Module = fx.Options(
	fx.Module("threadadapter",
		fx.Provide(
			provideThreadStoreAdapter,
			provideThreadBindingStoreAdapter,
			provideThreadPromptStoreAdapter,
			provideThreadPromptRuntimeCatalog,
			provideThreadPromptCatalog,
		),
	),
	fx.Invoke(registerThreadPromptProvidersFromApp),
)
```

provider 归属内部 child `fx.Module`；外层 `fx.Options` 让 registration invoke 在 aggregator 展开时保持 root-flat。`storeadapter.Module` 只聚合 `threadadapter.Module`，root 不直接 import thread child。这样既保持当前 root invoke scope，又不增加第二个公开 assembly option。

- [ ] **Step 3: 拆分混合测试**

只把 Store/prompt adapter 测试迁入新包；orchestration facade 测试继续留在 `internal/app`。如果单个测试同时依赖两者，先按被测职责拆成两个测试，不共享 root 私有类型。

把 `thread_adapters_test.go` 的 19 个纯 mapper 调用切到 `storeadaptertest.AssertFieldsMap`；删除 `assertAdapterMappingByField`、`populateAdapterReflectValue`、`assertOnlyAdapterFieldSet`、字段枚举/递归比较等 Thread-local 通用反射 engine。pointer clone 和共享 backing-map 等 Thread 特有语义测试保留为领域测试，不塞进通用 helper。

- [ ] **Step 4: 原子切换 root 与 optional evidence**

从 root 删除 `threadStoreAdaptersModule()` 调用；Task 3 已紧邻其后接入的唯一 `storeadapter.Module` aggregator 保留原位，并在内部加入 `threadadapter.Module`，因此 root 不新增 child import。随后删除旧 module 文件。4 个 thread prompt optional key 在同一 staged diff 改到 `internal/app/storeadapter/thread/prompt.go` 的真实 scanner key；禁止保留迁移前路径。

- [ ] **Step 5: 验证**

Run: `./scripts/test_with_guard.sh ./internal/app/storeadapter/thread ./internal/app/... ./internal/archtest -count=1`

Expected: 新包、root orchestration 与 optional dependency guard 全部 PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/app/storeadapter/thread internal/app/storeadapter/module.go internal/app/modules.go internal/app/thread_adapters_test.go internal/archtest/dependency_optional_thread_boundary_test.go
git add -u -- internal/app/thread_prompt_adapters.go internal/app/thread_store_adapters.go internal/app/thread_store_adapters_module.go internal/app/thread_prompt_port_external_test.go
git diff --cached --check
git commit -m "refactor(app): 拆分 Thread Store adapter"
```

### Task 7: 拆分 runtime Store 消费 adapter

**Files:**
- Create/move: `internal/app/runtimeadapter/mcpcontrol/**`
- Create/move: `internal/app/runtimeadapter/toolbridge/**`
- Create: `internal/app/runtimeadapter/cachekeepalive/**`
- Create: `internal/app/runtimeadapter/builtintools/**`
- Create: `internal/app/runtimeadapter/module.go`
- Modify: `internal/app/modules.go`
- Modify: `internal/app/modules_graph_test.go`
- Modify in this task: `internal/archtest/skill_fbsd_boundary_guard_test.go`
- Move/split matching tests from `internal/app`

- [ ] **Step 1: 迁移 mcpcontrol log sink**

```go
var Module = fx.Module("mcpcontroladapter",
	fx.Provide(provideMCPControlSystemLogSink),
)
```

保留 nil/错误语义；不得让 `internal/platform/mcpcontrol` import Store。

- [ ] **Step 2: 用单一 Module 迁移 toolbridge provider 与 root-flat binding**

`toolbridgeadapter` 只导出一个 `Module`：外层使用 `fx.Options`，内部 child `fx.Module("toolbridgeadapter", ...)` 持有 6 个窄端口 provider，readiness provider、decorate、invoke 则与 child module 并列，保持在调用者 root scope 展开。保持原顺序，不得把 decorator 包进 child `fx.Module`。不要移动 `toolbridgeHandlerRef`；它属于 root orchestration facade 的构造环断开点。

- [ ] **Step 3: 从 modules.go 抽出 keepalive 与 builtin-tools provider**

`cachekeepalive.Module` 本身仍由 root 加载；新 adapter module 只提供 `contract.CacheKeepaliveBindingLookup` 和 `contract.CacheKeepaliveThreadLookup`。`builtintools` 继续调用 `uistate.ResolveExplicitSoftFilteredBuiltinTools`，保留 registry nil 时返回 nil 的当前语义。

- [ ] **Step 4: 聚合 runtime module**

```go
var Module = fx.Options(
	mcpcontroladapter.Module,
	toolbridgeadapter.Module,
	cachekeepaliveadapter.Module,
	builtintoolsadapter.Module,
)
```

root 只 import `runtimeadapter` aggregator，并在原 `toolbridgeAdaptersModule()` / `toolbridgeCodexBindingModule()` 位置放置唯一 `runtimeadapter.Module`。aggregator 展开 child `toolbridgeadapter.Module` 时，binding options 仍处于 root scope；不再暴露第二个 aggregator option。

- [ ] **Step 5: 原子切换 root wiring 与路径守卫**

在同一 staged diff 中让 root 只使用 `runtimeadapter.Module`，删除 root mcpcontrol/toolbridge/keepalive/builtintools provider 与实现。把 `modules_graph_test.go` 中两个直接测试 `toolbridgeCodexBindingModule()` 的用例迁到 `internal/app/runtimeadapter/toolbridge/adapter_test.go`，改为验证 `toolbridgeadapter.Module`；root graph test 不 import runtime child。把 `skill_fbsd_boundary_guard_test.go` 的 toolbridge 路径改为 `internal/app/runtimeadapter/toolbridge/adapter.go`。禁止先删除旧文件、下一提交才改测试或守卫。

- [ ] **Step 6: 验证**

Run: `./scripts/test_with_guard.sh ./internal/app/runtimeadapter/... ./internal/app/... ./internal/archtest -count=1`

Expected: PASS；readiness 缺依赖仍失败，不能变成可选或 noop。

- [ ] **Step 7: Commit**

```bash
git add internal/app/runtimeadapter internal/app/modules.go internal/app/modules_graph_test.go internal/archtest/skill_fbsd_boundary_guard_test.go
git add -u -- internal/app/mcpcontrol_log_sink.go internal/app/toolbridge_adapters.go internal/app/toolbridge_adapters_test.go internal/app/modules_builtin_tools_test.go
git diff --cached --check
git commit -m "refactor(app): 拆分 Runtime Store 消费适配器"
```

### Task 8: 固化最终 root/Fx 结构合同

**Files:**
- Create: `internal/archtest/app_adapter_package_boundary_test.go`
- Modify: `internal/app/modules_graph_test.go`

- [ ] **Step 1: 把 Task 1 的临时 RED 合同恢复为永久 GREEN 守卫**

创建同名 `TestAppRootDoesNotOwnLeafStoreAdapters`，扫描 root production files，只允许 `modules.go` import canonical `internal/store` root，拒绝全部 leaf store import；同时只允许 root import `internal/app/storeadapter` 与 `internal/app/runtimeadapter` 两个精确 aggregator 路径，拒绝 `internal/app/storeadapter/` 和 `internal/app/runtimeadapter/` child imports；并要求 12 个 storeadapter 与 4 个 runtimeadapter 目录存在。测试实现与 Task 1 的 RED 版本一致，不引入 baseline 数值豁免。

- [ ] **Step 2: 扩充最终 Fx 图断言**

在现有 `fx.ValidateApp(Module)` / populate 测试旁增加 table，固定 cron 两端口、dashboard readers、prompt Store、thread Store/Binding/PromptCatalog、turn DedupeStore、uistate ports、toolbridge readiness probe。只断言图可构造与接口可 populate，不复制 provider 文件路径。

- [ ] **Step 3: 验证旧符号与旧路径都已退出**

Run:

```bash
rg -n 'businessStoreAdaptersModule|threadStoreAdaptersModule|toolbridgeAdaptersModule|toolbridgeCodexBindingModule' internal/app
rg -n 'internal/app/(business_store|cron_store|dashboard_store|datasource_v2_store|feedback_store|insight_store|memory_store|personalization_store|prompt_store|skill_store|thread_prompt|thread_store|toolbridge_adapters|turn_store|uistate_store|mcpcontrol_log_sink)' internal/archtest
```

Expected: 0 matches。

- [ ] **Step 4: 量化目标并运行 GREEN**

```bash
rg --files internal/app -g '*.go' -g '!**/*_test.go' | rg '^internal/app/[^/]+\.go$' | xargs wc -l
root_store_importers=$(rg --files internal/app -g '*.go' -g '!**/*_test.go' | rg '^internal/app/[^/]+\.go$' | xargs rg -l '"github\.com/anthropic-ai/super-agent-v3/internal/store"' || true)
leaf_store_importers=$(rg --files internal/app -g '*.go' -g '!**/*_test.go' | rg '^internal/app/[^/]+\.go$' | xargs rg -l '"github\.com/anthropic-ai/super-agent-v3/internal/store/' || true)
test "$root_store_importers" = "internal/app/modules.go"
test -z "$leaf_store_importers"
go list ./internal/app/... | sort
./scripts/test_with_guard.sh ./internal/app/... ./internal/archtest -count=1
```

Expected: root production ≤12 files、≤2,200 行；canonical root Store import 恰好只在 `internal/app/modules.go`，leaf Store import 为 0；12 个业务 adapter 包和 4 个 runtime adapter 包存在；测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/archtest/app_adapter_package_boundary_test.go internal/app/modules_graph_test.go
git diff --cached --check
git commit -m "test(app): 固化 adapter 分包后的 Fx 与导入边界"
```

### Task 9: 用 canonical registry 守卫新包边界

**Files:**
- Modify: `internal/archtest/backend_boundary_registry.go`
- Modify: `internal/archtest/backend_boundary_governance_test.go`
- Modify: `internal/archtest/backend_boundary_guard_coverage_test.go`

- [ ] **Step 1: 注册 app adapter canonical owner/rule**

新增 owner `app_adapter_boundary` 和 rule `app_adapter_narrow_import_surface`。规则使用 `BoundaryRuleAllowInternalImports`，production file patterns 覆盖 `internal/app/storeadapter/**/*.go` 与 `internal/app/runtimeadapter/**/*.go`。allow policy 必须分三层显式登记：

- `storeadapter/module.go` 与 `runtimeadapter/module.go` 只允许 import 自己列出的 child packages 和 Fx。
- 每个 domain `module.go` 只允许 Fx 及该域 Module 构造器实际需要的精确路径；没有真实 helper import 就不登记 helper。
- adapter implementation 只允许同域精确 module/store/contract/dto；cron、dashboard、datasourcev2、feedback、insight、memory、personalization、prompt、turn、uistate 的精确 implementation pattern 额外允许唯一 helper `internal/app/internal/storeguard`。skill/thread 未使用该 helper，不获得许可。runtime consumer 只允许其当前真实 platform/provider/store 路径。

不得允许整个 `internal/module`、`internal/store` 或 `internal/app/storeadapter` 前缀；非 aggregator domain 不得 import sibling adapter package。

该 rule 必须显式设置 `SkipTestFiles: true`。在 `backend_boundary_governance_test.go` 增加断言：rule kind 为 `BoundaryRuleAllowInternalImports`、`SkipTestFiles` 为 true、production candidate count 非零；`_test.go` 的 `internal/testutil/storeadapter`/`fxtest` imports 不得进入 production allow registry。

- [ ] **Step 2: 增加横向污染失败用例**

至少构造三种临时 fixture：`storeadapter/cron` import `store/prompt`、`storeadapter/prompt` import `storeadapter/thread`、非 aggregator domain import `runtimeadapter/toolbridge`。三者必须被同一 canonical evaluator 拒绝。

- [ ] **Step 3: 纳入 onion/cross-domain rule sets 与 surface**

把新 rule 加到 `OnionBoundaryRuleIDs()`、`CrossDomainBoundaryRuleIDs()` 和 `internal/app` governed surface；更新 governance 期望数量应由 registry 事实推导，不手写第二份业务清单。

- [ ] **Step 4: 扫描全部 stale path**

Run:

```bash
rg -n 'internal/app/(business_store|cron_store|dashboard_store|datasource_v2_store|feedback_store|insight_store|memory_store|personalization_store|prompt_store|skill_store|thread_prompt|thread_store|toolbridge_adapters|turn_store|uistate_store|mcpcontrol_log_sink)' internal/archtest docs/契约 docs/架构
```

Expected: 0 个指向已删除生产文件的有效引用；历史文档不在本任务修改范围，但当前契约/架构文档必须无 stale path。

- [ ] **Step 5: 验证 Archtest**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
make guard
```

Expected: PASS，且临时污染 fixture 有明确 RED 断言。

- [ ] **Step 6: Commit**

```bash
git add \
  internal/archtest/backend_boundary_registry.go \
  internal/archtest/backend_boundary_governance_test.go \
  internal/archtest/backend_boundary_guard_coverage_test.go
git diff --cached --check
git commit -m "archtest(app): 守卫拆分后的 adapter 包"
```

### Task 10: 更新当前代码地图并完成全量验证

**Files:**
- Modify manually from current source truth: `docs/doc/codemap/04-app-contract.md`
- Generated by `make codemap-refresh`: `README.md`、`docs/doc/codemap/13-archtest-boundaries.md`、`docs/doc/codemap/README.md`、`docs/doc/codemap/ai-index.json`
- Generated by `make project-map-refresh`: `docs/doc/codemap/project-map/**`

- [ ] **Step 1: 手工更新 04 当前架构说明**

`04-app-contract.md` 当前没有专属 generator；根据最终源码/LSP 证据手工更新 root Fx 图、adapter package 表、how-to 与测试入口。不要声称 `codemap-refresh` 会生成 04。

- [ ] **Step 2: 刷新生成物，禁止手改 generated facts**

Run `make codemap-refresh` and `make project-map-refresh`。pre-commit 在此前每个代码提交都可能已从 staged snapshot 更新这些文件；本步骤接受当前 generator 的幂等结果，不假设所有生成物会留到最后才变化。若 target 在执行 HEAD 改名，先检查 Makefile 和 hook 再调整。

- [ ] **Step 3: LSP 全闭环**

对 root `Module`、两个 aggregators、每个 domain `Module` 做 `structure`/`inspect`；对 root Module 和 representative providers 做 `xref`；用 `file(read_file)` 精读最终装配；对全部 changed Go files 执行 `file(diagnostics)`。

Expected: Error/Warning/Information/Hint 均为 0。任何非零 diagnostics 都必须修复或记录 blocker，不能写成 PASS。

- [ ] **Step 4: focused + broad Go 验证**

Run:

```bash
./scripts/test_with_guard.sh ./internal/app/... ./internal/archtest -count=1
make guard
make codemap-check
make project-map-check
make test
make build-plain
git diff --check
```

Expected: 全部 exit 0。若 `make test`/`make build-plain` 需要 frontend embed，使用 Makefile 自带准备步骤，不裸跑替代命令。

- [ ] **Step 5: 最终结构验收**

```bash
test "$(rg --files internal/app -g '*.go' -g '!**/*_test.go' -g '!internal/app/*/*' | wc -l | tr -d ' ')" -le 12
test "$(rg --files internal/app -g '*.go' -g '!**/*_test.go' -g '!internal/app/*/*' | xargs wc -l | tail -1 | awk '{print $1}')" -le 2200
root_store_importers=$(rg -l '"github\.com/anthropic-ai/super-agent-v3/internal/store"' internal/app/*.go || true)
leaf_store_importers=$(rg -l '"github\.com/anthropic-ai/super-agent-v3/internal/store/' internal/app/*.go || true)
test "$root_store_importers" = "internal/app/modules.go"
test -z "$leaf_store_importers"
```

Expected: 四条结构断言均 exit 0。

- [ ] **Step 6: Commit 当前代码地图**

```bash
git add \
  README.md \
  docs/doc/codemap/04-app-contract.md \
  docs/doc/codemap/13-archtest-boundaries.md \
  docs/doc/codemap/README.md \
  docs/doc/codemap/ai-index.json \
  docs/doc/codemap/project-map
git diff --cached --check
git commit -m "docs(codemap): 更新 App adapter 分包地图"
```

- [ ] **Step 7: 对最终 HEAD 重跑生成检查和状态检查**

```bash
make codemap-check
make project-map-check
git diff --check "$(git merge-base main HEAD)"..HEAD
git status --short
```

Expected: checks exit 0；完整 integration range 无 whitespace error；除执行前已有 unrelated dirty 外无新增未提交文件；`git show --name-only HEAD^..HEAD` 包含手工 04 和真实 generator/hook 派生文件。

- [ ] **Step 8: 独立双审最终 HEAD**

锁定最终 `HEAD`、`git merge-base main HEAD`、`git diff "$(git merge-base main HEAD)"..HEAD --name-status` 和完整验证输出，分别交给两名 read-only reviewer：

- Reviewer A：D01/D18/D19，重点审 package 边界、Fx 图、SSOT、是否存在 compat wrapper 和跨域 import。
- Reviewer B：D02/D12/D16，重点审 fail-fast、测试有效性、Archtest path migration、生成物、提交边界。

两者都必须返回 `PASS` 或带文件/行号的 findings。任何 P0/P1/P2 finding 修复都必须产生新的原子提交、重跑 Step 3-7，并让两名 reviewer 基于新的最终 HEAD 全部重审；最后一个双 PASS 之后禁止再提交任何变更。

## 7. 验收矩阵

| 风险 | 必须证据 | 失败处理 |
|---|---|---|
| DTO 映射漂移 | one-hot 双向字段集合测试 | 停止对应 domain lane |
| typed nil/required dependency 漂移 | `storeguard` + constructor tests | 不得改为 noop/default |
| optional tag 丢失 | turn/thread Fx graph + optional scanner | 恢复精确 tag/constructor |
| Fx provider 重复或缺失 | `fx.ValidateApp` + populate | 停止 root integration |
| adapter 横向污染 | canonical registry fixture RED | 收窄 allow policy，不加宽例外 |
| Archtest stale path | 全量 `rg` + Archtest | 更新 scanner/evidence，不跳过 |
| AI/编译边界未改善 | root 指标 + domain package list | 不接受仅改目录名的结果 |
| unrelated dirty 混入 | staged name/status/diff | unstage unrelated，不回退用户文件 |

## 8. 回滚与提交边界

- 每个 Task 2-9 提交都必须让 root App 与 Archtest 保持 GREEN；domain move、root wiring、旧文件删除和对应路径型守卫必须位于同一原子提交。
- 子代理准备分支可以只交付 patch，但 integration owner 不直接合入不可构建 commit；最终集成历史不存在“新旧实现双 owner”或“旧文件已删、守卫下个提交再修”的窗口。
- canonical package rule 在路径型守卫完成原子迁移后新增；如果它无法表达真实依赖，回滚包布局重新设计，不添加宽泛永久 exception。
- pre-commit 会在每次提交刷新并 stage 生成地图；提交后必须检查 `git show --stat --oneline HEAD` 与生成 diff。`04-app-contract.md` 是手工当前文档，其余列明生成物以 generator owner 为准。
- 不使用 `git add .`、`--no-verify`、`git reset --hard` 或 `git checkout --`。

## 9. 执行交接

推荐由一个 integration owner 使用 `执行计划` 在隔离 worktree 中按 T1-T10 串行落地；子代理用于领域 LSP 调研、domain-only patch 准备和每阶段只读复核，不直接写 shared seams 或产出要原样 cherry-pick 的提交。这样保留多代理吞吐，同时把 root aggregator、Archtest 路径与生成物的写所有权固定为单点。T2、T5、T7、T9 后设置人工检查点；每个检查点都必须以当前 HEAD 的测试、LSP、staged diff 与 hook 生成物为准。
