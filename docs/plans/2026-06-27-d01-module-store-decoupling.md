# D01：module 层 store 直接依赖解耦方案

**状态**：已收口（Phase 0-5 已合入 main；目标违规面复算为 0）
**优先级**：P2（技术债，不阻塞当前发布）
**背景**：代码审查 D01 发现 `internal/module/` 非 assembly 文件仍直接 import `internal/store/*`。当前仓库已有 module 直接 DB import 守卫，但没有真正覆盖 module -> store import，本方案先补门控，再分阶段迁移。

> **复核记录**：2026-06-27 使用 3 个只读子 agent 复核代码与方案。Agent A（archtest / guard / 计数口径）、Agent B（store / contract / DTO 事实）、Agent C（module 批次 / 运行路径 / 验证风险）均给出 FAIL 结论；本文档已吸收三方 finding 后修订。再次 3-agent 复核发现的 guard 净增长漏洞、prompt 符号漏项、模块事实偏差也已补入。
>
> **收口记录**：2026-06-27 主工作区已删除目标违规面的全部非 assembly store import，`moduleStoreImportAllowlist` 为空，`moduleStoreLegacyImportBudget` 已移除。最终验证以本文件后续收口命令记录为准。

---

## 1. 问题范围

### 统计口径

本文后续所有 budget 统一采用 **目标违规面**：排除 `_test.go` 与 `module.go` 的 `internal/module/**/*.go`。`module.go` 是 assembly 边界，允许在其中注入 store 实现；测试文件不纳入生产依赖方向 budget。

当前复现命令：

```bash
rg -n --no-heading '"github.com/anthropic-ai/super-agent-v3/internal/store/[^"]+"' internal/module \
  -g '*.go' -g '!**/*_test.go' -g '!**/module.go' \
  | awk -F: '{files[$1]=1; lines++} END {for (f in files) n++; print "files", n; print "import_lines", lines}'
```

当前 main 复算：

- 目标违规面：0 个文件，0 条 `internal/store/*` import 行。
- 全部非测试 module Go 文件（包含 `module.go`）：13 个文件，28 条 import 行。这个口径只作参考，不用于 Phase budget；这些 import 位于 assembly 边界。
- 原始计划基线：目标违规面 62 个文件、99 条 import 行；全部非测试 module Go 文件 69 个文件、117 条 import 行。

### 原始违规分布（目标违规面）

| 模块 | 文件数 | import 行数 | 主要风险 |
|---|---:|---:|---|
| dashboard | 9 | 33 | UI wire DTO 直接暴露 `commandcardstore.CommandCard`、`promptstore.PromptTemplate`、`sharedfilestore.SharedFile` 等 |
| thread | 19 | 29 | start/resume/fork、prompt snapshot、binding registration、router resolve 直接使用 thread/binding/prompt/sharedfile store DTO |
| prompt | 9 | 11 | 已有 `prompt.Store`，但非 assembly 文件仍直接依赖 prompt/sharedfile/uipreference store 类型 |
| cron | 7 | 7 | service/scheduler/progress 路径使用 `cronstore.Job`、`Run`、`CASRunStatusParams` |
| uistate | 3 | 4 | config RPC / Settings 投影依赖 uipreference/sharedfile store DTO |
| threadprompt | 4 | 4 | runtime catalog / provider 路径只依赖 prompt store DTO |
| insight | 3 | 3 | insightstore 读写模型 |
| datasource_v2 | 2 | 2 | datasourcev2 store DTO |
| skill | 2 | 2 | auditlog store 依赖，Phase 5 必须清理或形成精确豁免 |
| feedback | 1 | 1 | feedback store DTO |
| memory | 1 | 1 | sharedfile cleanup 使用 sharedfile store reader/deleter |
| personalization | 1 | 1 | uipreference store DTO |
| turn | 1 | 1 | turndedupe store DTO |

### 启动时 guard 事实

- `internal/archtest/dependency_direction_test.go::assertModuleDBIsolationRules` 只禁止 `database/sql` 与 pgx 族直接 import。
- `moduleDBImportAllowlist` 只服务直接 DB import 规则，不是 module -> store import 豁免表。
- 当前 `make guard` 只证明 code-size / baseline 守卫，不单独证明 `TestDependencyDirection` 已执行。CI 的 broad Go test 会覆盖，但 Phase 0 必须显式跑 dependency direction 测试。

### 收口后 guard 事实

- `internal/archtest/dependency_direction_test.go::assertModuleDBIsolationRules` 已包含 `rule17b_module_non_assembly_cannot_import_store`。
- `internal/archtest/dependency_direction_module_store_test.go` 中的 `moduleStoreImportAllowlist` 为空；未保留 skill auditlog 例外。
- `moduleStoreLegacyImportBudget` 已删除；新增或过期的 file + import path 都会直接失败。

### 根因

非 assembly 文件有两类 store 使用：

1. **持有 store 包接口**：例如 `threadstore.Store`、`bindingstore.Store`。即使字段是接口，仍需要 import `internal/store/*`，所以不满足解耦目标。
2. **使用 store DTO / Params**：例如 `threadstore.Thread`、`bindingstore.Binding`、`cronstore.CASRunStatusParams`、`promptstore.PromptTemplateSection`。这是迁移成本的主体，不能靠机械替换 import 完成。

### 已纠正的过期断言

`internal/store/prompt/contract.go` 已经导出 `Reader`、`Store`、`RuntimePromptCatalog`。Phase 1.5 不再新建 prompt Store，而是审计 prompt 能力面和 DTO 迁移策略。

---

## 2. 目标架构

```text
internal/module/<name>/module.go      <- 允许 import internal/store/*，负责 assembly / adapter 注入
internal/module/<name>/*.go           <- 不 import internal/store/*，只依赖 port 和 DTO
internal/contract/store_<domain>.go   <- 跨模块稳定 port，小接口，按来源域拆分
internal/dto/<domain>/...             <- UI / RPC / provider / event 等 wire DTO
internal/store/<domain>/...           <- 持久化 DTO 与 sqlc 细节，只在 adapter 内转换
```

**原则**：

- 非 `module.go` 生产文件不得直接 import `internal/store/*`；临时例外必须进入 `moduleStoreImportAllowlist`，包含 file + import 精确项、原因和清理 Phase。
- 非 assembly 文件也不得通过持有 `promptstore.Store`、`cronstore.Store` 这类 store 包接口绕过目标；接口类型本身也会形成 store import。
- port 按来源域和方法集合拆分，避免 consumer 聚合型大接口。dashboard 不建 `DashboardQueryStore` 这类横跨多个 store 子包的接口。
- DTO 不按“跨 3 个文件”机械提升。优先判断边界属性：
  - UI / RPC / provider / event wire 模型放 `internal/dto/<domain>`。
  - contract 接口专属入参/返回可放 `internal/contract`，或在 `contract` 中 type alias 到 `internal/dto`。
  - 纯持久化模型保留在 `internal/store`，由 `module.go` adapter 转换，避免扩大公共 API。
- 若 adapter 代码过大，默认仍先放在 `module.go`。确需拆出独立 adapter 文件时，必须在 Phase 0 明确加入 `moduleStoreImportAllowlist` 并在 Phase 5 前清理。

---

## 3. 迁移策略

### 轨道 A：快速收敛（小模块）

适用：turn、personalization、feedback、memory、datasource_v2、insight。
当前目标面合计 9 条 import 行。

标准动作：

1. 为该模块实际需要的方法定义窄 port。
2. 在 `module.go` 将 concrete store 适配成 port。
3. 将非 assembly 文件的 store DTO 转为 contract/dto 或本模块非 store 类型。
4. 每改一个 Go 文件先跑单文件守卫，再跑受影响 package 和 dependency direction 测试。

### 轨道 B：系统性迁移（大模块）

适用：cron、uistate、prompt、dashboard、threadprompt、thread。

标准动作：

1. 先用 `rg` 精确列出文件、store alias、DTO/Params 使用点。
2. 区分“只需接口替换”和“DTO / wire payload 必须转换”的文件。
3. 先落 port 与 adapter，再迁移调用方；不得在同一批里同时大幅改变 DTO 语义和业务逻辑。
4. 每批不超过 5 个生产文件。若触及 RPC / event / frontend JSON 字段，必须同步跑前端契约测试。

---

## 4. 历史分阶段执行计划

本节保留原执行拆分和验收口径，供追溯每个 Phase 的设计约束；当前 main 已按顶部收口记录完成，不再表示仍有待开启的迁移 lane。

> **硬性前置**：Phase 0 必须合并到 main 且 CI 通过后，所有迁移 lane 才能开启。

### Phase 0：基线固化与门控建立（1-2 天）

**目标**：新增真实 module -> store import 守卫，固定目标违规面 budget 为 99。

**① 新增独立 legacy allowlist 与上限守卫**

在 `internal/archtest/dependency_direction_test.go` 中新增 `moduleStoreImportAllowlist`。不要复用 `moduleDBImportAllowlist`。

Phase 0 必须把当前 99 条 `file + import path` 逐条登记为 legacy allowlist；后续新增 import 不得靠总数 budget 抵消。登记清单用本文第 1 节的复现命令生成，再人工检查后写入源码。

```go
const moduleStoreLegacyImportBudget = 99

var moduleStoreImportAllowlist = map[string]map[string]string{
	"internal/module/example/service.go": {
		"github.com/anthropic-ai/super-agent-v3/internal/store/example": "legacy import: Phase N 清理",
	},
}
```

在 `assertModuleDBIsolationRules` 中增加独立子测试：

```go
	t.Run("rule17b_module_non_assembly_cannot_import_store", func(t *testing.T) {
		files := parseImportFiles(t, root, "internal/module")
		legacy, unknown, staleAllowlist := collectModuleStoreImportViolations(files, moduleStoreImportAllowlist)
		if len(unknown) > 0 {
			t.Fatalf("module->store imports introduced outside legacy allowlist:\n%s",
				strings.Join(unknown, "\n"))
		}
		if len(staleAllowlist) > 0 {
			t.Fatalf("module->store legacy allowlist contains stale entries:\n%s",
				strings.Join(staleAllowlist, "\n"))
		}
		if got := len(legacy); got > moduleStoreLegacyImportBudget {
			t.Fatalf("module->store legacy import budget exceeded: got %d, budget %d\n%s",
				got, moduleStoreLegacyImportBudget, strings.Join(legacy, "\n"))
		}
	})
```

collector 规则：

- 跳过 `_test.go`。
- 跳过 `module.go`。
- 只统计 `github.com/anthropic-ai/super-agent-v3/internal/store/` 前缀。
- allowlist 必须精确到 `file + import path`，不能只按文件豁免。
- collector 必须返回三类结果：`legacy` 表示仍命中 legacy allowlist 的既有 import；`unknown` 表示未登记的新 import；`staleAllowlist` 表示 allowlist 中已不存在于当前 import 集合的 `file + import path`。
- `unknown` 或 `staleAllowlist` 任意非空立即失败；`legacy` 数量小于或等于 budget 才通过。
- 为 collector 加单元测试：
  - 删除 1 条 legacy import 但保留 allowlist 项时必须失败，防止旧豁免残留后被重新引入。
  - 删除 1 条 legacy import 同时新增 1 条 unknown import 时必须失败，防止只看净数量。

该 guard 的语义是“禁止新增，允许减少，禁止 stale allowlist”。后续每个 Phase 合并时同步下调 `moduleStoreLegacyImportBudget`，并删除已清理的 legacy allowlist 项；最终报告写明旧值、新值和复现命令输出。

**② 新增 `internal/contract/README.md`**

这是新约定，不是现有文件。内容至少写清：

- `store_<domain>.go`：跨模块稳定 store port，domain 取来源域或 store 子包名。
- 接口命名优先 `<Domain>Reader`、`<Domain>Writer`、`<Domain>Store`；读写分离时拆开。
- 禁止跨多个 store 职责的 consumer 聚合接口。
- DTO 放置规则：wire DTO 优先 `internal/dto`，port 专属模型可放 `internal/contract`，store 持久化 DTO 默认不外泄。
- `store_<domain>.go` 仅适用于本次新增的 module -> store 解耦 port；不得要求重排 `HookReviewStore`、`ThreadMetadataStore` 等既有领域契约文件。

**③ 验收命令**

```bash
./scripts/test_with_guard.sh ./internal/archtest -run TestDependencyDirection -count=1
make guard
git status --short
git diff -- internal/archtest/baseline*.json internal/archtest/freeze_registry.go
git diff --check
```

说明：当前 `make guard` 不单独证明 dependency direction 子测试执行；Phase 0 必须显式运行 `./internal/archtest -run TestDependencyDirection`。
若 `make guard` 带出 `internal/archtest/baseline*.json` 或 `internal/archtest/freeze_registry.go` diff，必须在报告中逐项说明；不得把非本 Phase 所有的 baseline/freeze 漂移混进提交。

**完成标准**：legacy budget = 99；unknown import = 0；dependency direction 测试和 guard 均通过；`internal/contract/README.md` 就绪。

---

### Phase 1：小模块快速清零（4-6 天）

**目标**：turn(1)、personalization(1)、feedback(1)、memory(1)、datasource_v2(2)、insight(3)，合计 9 条 import 行。
**完成后 budget**：`moduleStoreLegacyImportBudget <= 90`。

每模块标准命令：

```bash
rg -n '"github.com/anthropic-ai/super-agent-v3/internal/store/[^"]+"' internal/module/<name> \
  -g '*.go' -g '!**/*_test.go' -g '!**/module.go'
```

每改完一个 Go 文件立即运行：

```bash
./scripts/test_with_guard.sh <changed-file.go>
```

批次验证：

```bash
./scripts/test_with_guard.sh ./internal/module/turn/... -count=1
./scripts/test_with_guard.sh ./internal/module/personalization/... -count=1
./scripts/test_with_guard.sh ./internal/module/feedback/... -count=1
./scripts/test_with_guard.sh ./internal/module/memory/... -count=1
./scripts/test_with_guard.sh ./internal/module/datasource_v2/... -count=1
./scripts/test_with_guard.sh ./internal/module/insight/... -count=1
./scripts/test_with_guard.sh ./internal/archtest -run TestDependencyDirection -count=1
```

注意：小模块的 store 来源不同，不要预设共用 binding port。当前切分为：turn 使用 turndedupe；memory 使用 sharedfile reader/deleter；insight 使用 insightstore；feedback 使用 feedback store；personalization 使用 uipreference；datasource_v2 使用 datasourcev2。

---

### Phase 1.5：prompt store 能力面与 DTO 决策（2-3 天，可与 Phase 1 并行）

**目标**：不改 budget，先消除过期设计假设，为 Phase 3 的 prompt / threadprompt 做准入。

当前事实：

- `internal/store/prompt/contract.go` 已有 `Reader`、`Store`、`RuntimePromptCatalog`。
- 已知导出模型和 filter 包括 `PromptTemplate`、`PromptTemplateSection`、`PromptTemplateVersion`、`PromptIntentDraft`、`ListFilter`、`RuntimeListFilter`、`PromptIntentDraftListFilter`。
- 非 assembly 文件还直接调用 `TemplateTags`、`IsRuntimeAssetTemplate` 等 helper，不能只按 DTO 和方法集合迁移。

必须产出：

1. 列出 prompt 非 assembly 文件实际使用的全部 `promptstore` 导出符号，包括接口、方法、DTO、filter / params、helper 函数。
2. 决定上述符号的归属：`internal/dto`、`internal/contract`、本模块 helper，或继续留在 store adapter 内转换。
3. 为 `ListFilter`、`PromptIntentDraftListFilter`、`TemplateTags`、`IsRuntimeAssetTemplate` 写明迁移归属，避免执行到 Phase 3 时留下 store import。
4. 为 `RuntimePromptCatalog` 写明归属；当前 `threadprompt/runtime_catalog.go` 仍 type alias 到 `promptstore.RuntimePromptCatalog`，thread 运行路径也持有该接口。
5. 写出 Phase 3 要新增的 port 名称和方法签名，避免执行中临时改接口。

验证命令：

```bash
./scripts/test_with_guard.sh ./internal/store/prompt/... ./internal/module/prompt/... -count=1
./scripts/test_with_guard.sh ./internal/archtest -run TestDependencyDirection -count=1
```

---

### Phase 2：cron、uistate（3-4 天）

**目标**：cron(7)、uistate(4)，合计 11 条 import 行。
**完成后 budget**：`moduleStoreLegacyImportBudget <= 79`。

**cron**

- 不能让非 assembly 文件继续持有 `cronstore.Store`。
- 需为 service / scheduler / progress 发布定义窄 port。
- `Job`、`Run`、`CASRunStatusParams`、状态常量必须进入 DTO/contract 决策，不得继续从 `cronstore` 泄露。

验证：

```bash
./scripts/test_with_guard.sh ./internal/module/cron/... -count=1
./scripts/test_with_guard.sh ./internal/archtest -run TestDependencyDirection -count=1
```

**uistate**

- 为 uipreference / sharedfile 读写定义窄 port。
- 若 config RPC、Settings payload、LSP prompt hint 字段发生 JSON 变化，必须同步验证前端契约。

验证：

```bash
./scripts/test_with_guard.sh ./internal/module/uistate/... -count=1
./scripts/test_with_guard.sh ./internal/archtest -run TestDependencyDirection -count=1
```

若触及前端 JSON payload：

```bash
cd frontend-app
npm test -- src/shared/api/backendApi.test.js src/shared/api/wailsBridge.test.js src/pages/settings/SettingsPage.test.jsx
```

若本批实际修改 `frontend-app` 文件，定向测试只是补充证据，还必须运行：

```bash
npm run lint
npm test
npm run build
```

---

### Phase 3：prompt、dashboard、threadprompt（2-3 周）

**目标**：prompt(11)、dashboard(33)、threadprompt(4)，合计 48 条 import 行。
**完成后 budget**：`moduleStoreLegacyImportBudget <= 31`。

**prompt**

- Phase 1.5 已确认 `prompt.Store` 存在，本阶段不是新建 Store。
- 非 assembly 文件不得继续持有 `promptstore.Store` 或 `promptstore.Reader`。
- `PromptTemplate`、`PromptTemplateSection`、`PromptTemplateVersion`、`PromptIntentDraft`、`ListFilter`、`RuntimeListFilter`、`PromptIntentDraftListFilter`、`RuntimePromptCatalog`、`TemplateTags`、`IsRuntimeAssetTemplate` 依据 Phase 1.5 决策迁移或 adapter 转换。

验证：

```bash
./scripts/test_with_guard.sh ./internal/module/prompt/... -count=1
./scripts/test_with_guard.sh ./internal/store/prompt/... -count=1
```

若 prompt UI / API payload 变化：

```bash
cd frontend-app
npm test -- src/features/prompts/PromptPageView.test.jsx src/shared/api/backendApi.test.js
```

若本批实际修改 `frontend-app` 文件，定向测试只是补充证据，还必须运行：

```bash
npm run lint
npm test
npm run build
```

**dashboard**

dashboard 不建立 consumer 聚合型 `store_dashboard.go`。按来源域拆 port，并在 `dashboard/module.go` 注入 concrete store 后适配：

- `AgentStatusReader`
- `AILogReader`
- `AuditLogReader`
- `BusLogReader`
- `SystemLogReader`
- `CommandCardReader`
- `DBQueryExecutor`
- `PromptTemplateReader`
- `SharedFileReader` / `SharedFileWriter`

必须新增 wire DTO 转换清单，按 endpoint + source file 登记当前直接暴露的 store DTO。前端 JSON 字段名不得因 store DTO 脱钩而静默变化。

转换清单至少覆盖：

- `dashboard/agentStatus`：`agent_status.go`、`contract.go`、`rpc.go`；`agentstatusstore.AgentStatus`
- `dashboard/logs/audit/bus/system`：`logs.go`、`factory.go`、`service.go`、`rpc.go`；`auditlogstore.ListFilter` / `AuditEvent`、`buslogstore.ListFilter` / `BusExceptionLog`、`systemlogstore.ListFilter` / `SystemLog`
- `dashboard/aiLogs/recent/stats`：`ai_logs.go`、`rpc.go`；`ailogstore.AILog`、`ailogstore.StatusCount`
- `dashboard/commandCards`：`rpc.go`；`commandcardstore.CommandCard`
- `dashboard/prompts`：`ui_page.go`、`rpc.go`；`promptstore.PromptTemplate`
- `dashboard/sharedFiles`：`ui_page.go`、`rpc.go`；`sharedfilestore.SharedFile`、`FinalOutputRef`、`SharedFileRetention`
- `dashboard/workflowMaterialWrite`：`workflow_material.go`、`rpc.go`；`sharedfilestore.UpsertParams`、`sharedfilestore.SharedFile`、`WorkflowMaterialWriteRequest` / `WorkflowMaterialWriteResponse`
- mapper / service 层：`contract.go`、`service.go`、`factory.go`、`logs.go`、`ai_logs.go`、`agent_status.go`、`workflow_material.go`

验证：

```bash
./scripts/test_with_guard.sh ./internal/module/dashboard/... -count=1
```

若 dashboard API / frontend payload 变化：

```bash
cd frontend-app
npm test -- src/shared/api/backendApi.test.js src/shared/api/backendApi.surface.test.js src/shared/api/backendApi.contractMatrix.test.js src/pages/backendApiConsumer.surface.test.js
```

若本批实际修改 `frontend-app` 文件，定向测试只是补充证据，还必须运行：

```bash
npm run lint
npm test
npm run build
```

**threadprompt**

- 当前目标违规面只直接依赖 prompt store；依赖 Phase 3 前段的 prompt port。
- `default_rules_provider.go`、`providers.go`、`runtime_catalog.go`、`runtime_intent.go` 的 promptstore 符号必须在本阶段列清并迁移，不能留到 thread Phase 4。清单至少包括 `RuntimePromptCatalog`、`RuntimeListFilter`、`PromptTemplate`、`PromptTemplateSection`、`PromptTemplateVersion`、`TemplateTags`、`IsRuntimeAssetTemplate`、`ListTemplates`、`GetTemplate`、`ListSectionsByTemplateID`、`ListRecallSections`、`ListDefaultRuleSections`、`InsertVersion` 和 section list helper。
- 放在 Phase 3 最后批次处理，避免 prompt DTO 决策反复返工。

阶段总验证：

```bash
./scripts/test_with_guard.sh ./internal/archtest -run TestDependencyDirection -count=1
```

---

### Phase 4：thread（3-5 周）

**目标**：thread 29 条 import 行。
**完成后 budget**：`moduleStoreLegacyImportBudget <= 2`（仅剩 skill auditlog 依赖，Phase 5 决定清理或精确豁免）。

**入场检查**

不能只看 `factory.go`，必须覆盖 thread 高密度文件和四类 store alias：

```bash
rg -n 'threadstore\.|bindingstore\.|promptstore\.|sharedfilestore\.' internal/module/thread \
  -g '*.go' -g '!**/*_test.go' -g '!**/module.go'
```

同时生成每个文件的 alias 使用密度，用于拆批：

```bash
rg -n 'threadstore\.|bindingstore\.|promptstore\.|sharedfilestore\.' internal/module/thread \
  -g '*.go' -g '!**/*_test.go' -g '!**/module.go' \
  | awk -F: '{count[$1]++} END {for (f in count) print count[f], f}' | sort -nr
```

重点文件至少包括所有命中 alias 的生产文件：

- `binding_registration.go`
- `service.go`
- `router_resolve.go`
- `service_constructor.go`
- `lifecycle_helpers.go`
- `stop.go`
- `factory_config.go`
- `history.go`
- `events.go`
- `spawn.go`
- `factory.go`
- `command.go`
- `prompt_snapshot.go`
- `handoffrender/text.go`
- `scratchpad.go`
- `start_session_helpers.go`
- `start_prompt_context.go`
- `lifecycle.go`
- `contract_adapter.go`

**Wails / RPC / event 影响评估**

风险不只在 `cmd/agent-terminal`。开始迁移前运行：

```bash
rg -n 'threadstore\.|bindingstore\.|promptstore\.|sharedfilestore\.' internal/app internal/platform/eventsurface internal/ui/wails cmd/agent-terminal \
  -g '*.go'
rg -n 'CacheKeepalive|CacheKeepaliveBindingLookup|CacheKeepaliveThreadLookup' internal/app internal/platform/cachekeepalive \
  -g '*.go'
```

如果 `internal/app/modules.go`、`internal/app/toolbridge_adapters.go`、`internal/platform/eventsurface/bind.go`、`internal/platform/cachekeepalive/manager.go`、`internal/ui/wails/binding.go` 或 Wails bridge 间接依赖 store DTO，需要同步新增 DTO 转换和前端契约测试。

**DTO 策略**

- 跨 provider / event / frontend 的 thread wire 模型优先放 `internal/dto/thread` 或既有 `internal/dto/provider`。
- Phase 4 入场时必须确认 `RuntimePromptCatalog` 已在 Phase 3 从 threadprompt 清零；若 thread 仍持有该 promptstore 接口，必须先补 prompt port，不得直接进入 thread DTO 迁移。
- thread service 内部临时模型可放 `internal/module/thread`，但不得 import store 包。
- 纯持久化字段只在 `module.go` adapter 内与 store DTO 互转。
- 若某个 DTO 密集文件暂时不能迁移，必须进入 `moduleStoreImportAllowlist` 的 file+import 精确豁免，并写清 Phase 4 的清理批次。

**验证**

每批：

```bash
./scripts/test_with_guard.sh ./internal/module/thread/... -count=1
./scripts/test_with_guard.sh ./internal/archtest -run TestDependencyDirection -count=1
```

若触及 thread module port、Fx output、app / eventsurface / frontend thread payload：

```bash
./scripts/test_with_guard.sh ./internal/app/... ./internal/platform/eventsurface/... -count=1
./scripts/test_with_guard.sh ./internal/app/... -run TestAppModuleGraphIsClosed -count=1
cd frontend-app
npm test -- src/shared/api/backendApi.test.js src/shared/api/backendApi.contractMatrix.test.js src/shared/api/wailsBridge.test.js src/App.test.jsx src/entities/client/model/useClientStore.test.js
```

若触及 Wails `CallAPI`、`LaunchAgent`、trace meta 或前端 RPC payload，还必须运行：

```bash
./scripts/test_with_guard.sh ./internal/ui/wails/... -count=1
```

若修改 RPC method、params 或 return shape，`backendApi.contractMatrix` 只能作为前端注册表证据，还必须同步运行受影响 Go handler 测试，并在报告中列出 frontend facade <-> Go handler 映射。

若 toolbridge port / payload 或 thread/binding adapter 变化，还必须运行：

```bash
./scripts/test_with_guard.sh ./internal/platform/toolbridge/... ./internal/platform/difftracker/... ./internal/provider/codexapp/... -count=1
```

若 binding/thread DTO、adapter 或 agent/thread identity 字段变化，还必须运行并解释 `contract.CacheKeepalive*` 映射：

```bash
./scripts/test_with_guard.sh ./internal/platform/cachekeepalive/... ./internal/app/... -count=1
```

若本批实际修改 `frontend-app` 文件，定向测试只是补充证据，还必须运行：

```bash
npm run lint
npm test
npm run build
```

---

### Phase 5：archtest 收紧（1-2 天）

**目标**：删除数字 ratchet，改为精确 allowlist 守卫。

默认目标是 `moduleStoreImportAllowlist` 为空，即非 `_test.go`、非 `module.go` 的 `internal/module/**/*.go` 不允许 import `internal/store/*`。

若确需保留 skill auditlog 例外，必须满足：

- file + import path 精确豁免。
- reason 写明为什么不能移到 `module.go` adapter。
- 由主审确认该例外不破坏“module 非 assembly 不依赖 store”的目标。

收紧后规则：

- `moduleDBImportAllowlist` 只服务 `database/sql` / pgx 直接 DB import。
- `moduleStoreImportAllowlist` 只服务 store import，且默认为空。
- 删除 `moduleStoreLegacyImportBudget` 数字上限；未命中精确 allowlist 的 violation 直接失败。

验证：

```bash
./scripts/test_with_guard.sh ./internal/archtest -run TestDependencyDirection -count=1
make guard
git status --short
git diff -- internal/archtest/baseline*.json internal/archtest/freeze_registry.go
make test
```

**收口验证（2026-06-27）**：

- 目标违规面复算：`files 0`、`import_lines 0`。
- `./scripts/test_with_guard.sh ./internal/archtest -run TestDependencyDirection -count=1`：通过。
- `make guard`：通过。
- `make test`：通过；包含前端 build、全仓 Go `-race` 主批次，以及 `internal/provider/claudecli` / `internal/provider/codexapp` deferred E2E 包。
- 收口时额外修复 `cmd/mcp-orch/orchestration.TestService_LaunchWithLocal` 的测试清理 race：测试不再二次调用 `cmd.Wait()`，改为复用 `stopAndDrainServiceTestAgent` 让 `exitMonitor` 作为唯一 Wait owner 收束。

---

## 5. 历史每阶段共同门禁

Phase 1-4 每个迁移批次当时必须满足：

1. 重跑第 1 节的 import 计数命令，记录阶段前后 import 行数。
2. `unknown import = 0`，否则该阶段不能合并。
3. `staleAllowlist = 0`，否则该阶段不能合并。
4. `moduleStoreImportAllowlist` diff 只能删除本阶段已清理的 `file + import path` 项；不得新增 unrelated legacy 豁免。
5. 对已从 allowlist 删除的 `file + import path` 做 fail-first 验证：临时恢复该 import 时 `TestDependencyDirection` 必须失败，恢复后通过。
6. 最终报告必须列出 budget 旧值、新值、allowlist 删除项摘要和验证命令输出。

---

## 6. 风险与缓解

| 风险 | 概率 | 缓解措施 |
|---|---:|---|
| Phase 0 未完成就开迁移 lane，违规数继续漂移 | 高 | Phase 0 是硬性前置，CI 通过后才开；每个 Phase 同步下调 budget |
| 误把 `moduleDBImportAllowlist` 当 store import 豁免 | 高 | 新增 `moduleStoreImportAllowlist`，两张表职责分离 |
| 只看 import 总数导致“删旧增新”漂移 | 高 | Phase 0 区分 unknown、legacy 与 staleAllowlist；unknown/stale 任意非空失败，legacy 才受 budget 控制 |
| cron CAS / progress 事件被接口替换破坏 | 中 | Phase 2 单列 scheduler/service/progress 测试，不只跑编译 |
| dashboard DTO 脱钩导致前端 JSON 字段变化 | 中 | Phase 3 新增 wire DTO 转换清单，跑 backendApi / surface 测试 |
| thread DTO 深度超预期，批次 A 实际为空 | 高 | Phase 4 入场先统计四类 alias 和重点文件，按 DTO 密度重排批次 |
| Wails / eventsurface / toolbridge / cachekeepalive 间接依赖 store DTO | 中 | Phase 4 扩大 grep 到 `internal/app`、`internal/platform/eventsurface`、`internal/ui/wails`、`internal/platform/cachekeepalive`、`cmd/agent-terminal` |
| 多 lane 修改 `internal/contract` 命名冲突 | 中 | Phase 0 新增 README 规范；同一 domain 的 port 由一个 lane 负责 |
| skill auditlog import 被长期豁免 | 中 | Phase 5 默认清零；如保留必须主审批准 file+import 精确豁免 |

---

## 7. 验收标准

| 阶段 | 验收条件 |
|---|---|
| Phase 0 | `moduleStoreLegacyImportBudget = 99`；unknown import = 0；staleAllowlist = 0；dependency direction 测试通过；`internal/contract/README.md` 就绪 |
| Phase 1 | budget <= 90；共同门禁通过；小模块 package 测试全绿 |
| Phase 1.5 | prompt Store 事实已更新；prompt DTO/port 决策文档化；budget 不升 |
| Phase 2 | budget <= 79；共同门禁通过；cron / uistate package 测试全绿；触及 payload 时前端契约测试通过 |
| Phase 3 | budget <= 31；共同门禁通过；prompt/dashboard/threadprompt package 测试全绿；dashboard wire DTO 转换完成 |
| Phase 4 | budget <= 2；共同门禁通过；thread start/resume/fork/stop、eventsurface/command/rpc/module graph、cachekeepalive、Wails/toolbridge/frontend 受影响测试通过 |
| Phase 5 | 数字 ratchet 删除；未命中精确 allowlist 的 store import 数为 0；如有 skill 例外则 file+import 精确批准并经主审确认 |

---

## 8. 原始工时估算

以下为执行前估算，收口后仅保留作计划追溯。

| Phase | 估算 | 主要成本 |
|---|---:|---|
| Phase 0 | 1-2 天 | 新 guard、contract README、CI/guard 口径校准 |
| Phase 1 | 4-6 天 | 小模块 port 和 DTO 转换 |
| Phase 1.5 | 2-3 天 | prompt 现有 Store 能力面梳理和 DTO 决策 |
| Phase 2 | 3-4 天 | cron CAS/progress、uistate RPC/Settings 验证 |
| Phase 3 | 2-3 周 | prompt DTO、dashboard wire DTO、threadprompt 依赖收口 |
| Phase 4 | 3-5 周 | thread start/resume/fork 核心路径、eventsurface/toolbridge 兼容 |
| Phase 5 | 1-2 天 | 精确 allowlist 收紧、全量验证 |
| **总计** | **8-12 周** | |

可并行加速：Phase 1 小模块可拆并行；Phase 1.5 可与 Phase 1 并行；Phase 2 的 cron / uistate 可并行。Phase 3 里的 dashboard 与 prompt 可并行，但 threadprompt 必须等 binding/prompt port 稳定后再做。Phase 4 不建议并行拆太细，避免 thread 核心路径 DTO 策略冲突。

---

## 9. 参考

- archtest 入口：`internal/archtest/dependency_direction_test.go::TestDependencyDirection`
- 当前 DB import allowlist：`internal/archtest/dependency_direction_test.go::moduleDBImportAllowlist`，不得复用为 store import allowlist
- prompt Store 事实源：`internal/store/prompt/contract.go`
- store 子包与 fx 暴露：`docs/doc/codemap/10-store.md`
- module 读写路径：`docs/doc/codemap/07-module-read.md`、`docs/doc/codemap/07-module-write.md`
- DTO 分层：`docs/doc/codemap/05-dto.md`
- 当前前端契约测试：`frontend-app/src/shared/api/backendApi.test.js`、`frontend-app/src/shared/api/wailsBridge.test.js`、`frontend-app/src/entities/client/model/useClientStore.test.js`
