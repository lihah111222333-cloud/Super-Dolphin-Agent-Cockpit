# 全域代码风险多 Agent 修复方案

> 规划基线：`main@459001c167a1c37f57cb244003ee5f00ad9e81e5`，且 `HEAD == origin/main`。
>
> 规划期间出现的 MCP-LSP xref/position 修改和 5 个派生物已由外部并行任务以中文提交 `459001c16` 收口并推送；当前除本计划文档外工作树干净。该提交修复了 xref 列提示并刷新了当前派生物，但没有修复 project-map 跨日非确定性或 capcontract 输入路由漏检。
>
> 本方案覆盖 2026-07-15 全域代码审查确认的 6 个 P1、11 个 P2、wire/mapper/diagnostics P3 债务，并为每项指定根因修复落点、上层防御、RED/GREEN 证据和独占 owner。

## 1. 完成定义

每个问题只有同时满足以下条件才算关闭：

1. 在问题 owner 层消除根因，不在调用方添加默认值、吞错、兼容 fallback 或仅日志告警。
2. 先提交能稳定暴露缺陷的 RED 测试，再提交最小 GREEN 实现；修复提交必须同时包含回归证据。
3. 需要纵深防御的问题必须接入现有上层边界，不创建第二套真值或平行校验链。
4. 涉及 DTO、JSON、schema、mapper、RPC、store 或前端契约的修改必须完成动态字段差集、实现覆盖和 fail-first 字段守卫。
5. 共享符号保留 LSP 定位、理解、影响面、精读和 diagnostics 五类证据；所有 severity 均需处理。
6. 每条 lane 在独立 worktree 和 `codex/` 分支中工作，不触碰其他 lane 写集和主 checkout 现有 dirty 文件。
7. 只有最终集成树通过生成物、测试、race、build、hook 和 Git 状态复核，才可声明可提交或可合并。

## 2. 已收口并行 bundle 与新基线

提交 `459001c16` 已收口：

```text
MCP-LSP xref/position:
  cmd/mcp-lsp/lsp_binary_residual_test.go
  cmd/mcp-lsp/tools/position.go
  cmd/mcp-lsp/tools/tool_position_test.go
  cmd/mcp-lsp/tools/tool_xref.go

配套派生物：
  docs/doc/codemap/capability-contract/capability_manifest.json
  docs/doc/codemap/project-map/AI_PROJECT_MAP.md
  docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json
  docs/doc/codemap/project-map/index/docs-agent.tsv
  docs/doc/codemap/project-map/index/platform-provider.tsv
```

执行规则：

- 所有修复 worktree 从 `459001c167a1c37f57cb244003ee5f00ad9e81e5` 建立，不再从旧审查 SHA 分叉。
- 不把“派生物已经刷新且当前 check 通过”当作 project-map 日期根因或 capcontract 路由根因已经修复。
- 先在干净 worktree 落地治理种子 lane G01，再允许其他 lane 产生正式修复提交。
- 禁止把仅日期变化的 project-map 作为独立修复提交；必须与确定性生成器修复同一闭环。

## 3. 多 Agent 拓扑

| Agent | 独占责任域 | 主要问题 | 共享 seam |
|---|---|---|---|
| G00 | 主控、worktree、rebase、裁决、最终门禁 | 全局 | 只协调，不写业务代码 |
| G01 | 治理种子 | project-map 确定性、capcontract 输入闭包、ignored test guard | hooks、`scripts/ai_maintenance`、生成物；必须最先串行 |
| G02 | MCP-LSP runtime | discovery fail-open、context-aware/bounded walk | 不修改已收口的 xref/position 行为，除非出现真实编译冲突 |
| G03 | MCP common protocol | bootstrap strict JSON、JSON-RPC ID | `internal/mcpserver/common`；两项串行 |
| G04 | orchestration config/cron | node config、agent cwd、cron unknown state、同文件 wire tag | `nodeexec/config.go` 只归本 lane |
| G05 | taskdag/store | final-output config、auditlog/buslog guard、dead mapper | 不改 SQL/schema/sqlc 生成代码 |
| G06 | provider runtime | Codex resume、Claude history | 不改 thread/module 调用方 |
| G07 | lifecycle/dashboard | thread cleanup、memory drain、dashboard page | thread lifecycle 文件独占 |
| G08 | wire DTO/frontend | 全部 `omitempty` 语义、uistate/前端字段闭环 | DTO/前端契约独占，不与 diagnostics lane 并发 |
| G09 | logger security | 日志目录/文件 owner-only | `pkg/logger` 独占 |
| G10 | diagnostics debt | 分代码 owner 清零 Error/Warning/Information/Hint | 只能在语义修复合并后启动 |
| G99 | 唯一生成物与集成 owner | rebase、生成物、全仓门禁、提交/推送证据 | 唯一可刷新派生物的 lane |

所有 agent 从同一门禁绿色基线建立 worktree。G01 完成前，G02-G10 不得产生正式修复提交。

## 4. 问题级修复矩阵

### 4.1 治理与交付真值

| ID | 根因与最优主修落点 | 是否需要上层防御 | 上层防御落点与方法 | RED / GREEN | Owner |
|---|---|---|---|---|---|
| F01 | `scripts/generate_ai_project_map.mjs`：只解析一次稳定 `generationDate`，已有 manifest 时严格复用合法 `generated_at`；manifest 缺失时首次 refresh 才取当前日期。`renderManifest/renderMap` 不得自行读时钟。 | 需要 | `scripts/ai_maintenance/gate_cache.go` 删除 UTC day fingerprint；generator 与 cache 测试锁定“跨日、相同输入、相同 bytes/fingerprint”。损坏/缺失日期必须 fail-fast。 | RED：HEAD 仅跨日即 drift；GREEN：跨日 fixture 连续 `--check --strict-drift` 通过。 | G01 |
| F02 | capcontract 根因不是单纯忘记刷新，而是 pre-push/stop-gate 未把 `internal/contract`、`internal/provider`、`cmd/mcp-orch/{orchestration,tools}` 识别为输入。主修 `.githooks/pre-commit`、`.githooks/pre-push`、`scripts/codex_stop_gate.sh`、`scripts/ai_maintenance` gate 注册。 | 需要，工程三层 | AST scanner 动态枚举生产集合；pre-commit 在 staged snapshot 刷新并精确 stage manifest；任一 backend Go 变化在 pre-push/stop gate 执行 `capcontract-check`，避免三份 roots 手抄漂移。 | RED：只改 provider symbol 时当前 plan 不含 capcontract；GREEN：pre-commit 刷新、pre-push/stop 均命中。 | G01 |
| F03 | 删除已经失效且带 `//go:build ignore` 的 `scripts/code_size_guard_test.go`；不要简单去掉 tag，因为它引用的生产函数已不存在。 | 需要 | 在 `internal/guards` 新增 tracked `*_test.go` build-constraint guard，使用 Go parser 拒绝默认测试链永不收集的无 owner 测试，并登记 specialized guard。 | RED：现状精确报告 ignored test；GREEN：删除后通过，临时 fixture 可再次触发 RED。 | G01 |

### 4.2 MCP-LSP 与 MCP 协议

| ID | 根因与最优主修落点 | 是否需要上层防御 | 上层防御落点与方法 | RED / GREEN | Owner |
|---|---|---|---|---|---|
| F04 | `cmd/mcp-lsp/http_runner.go:httpRunner.Run`：discovery 写失败后用 fresh bounded context 停 server，并返回 `errors.Join(discoveryErr, stopErr)`；正常退出也聚合 cleanup/stop 错误。 | 需要，但不新增控制面 fallback | 保留 `runtime.Run -> runner.runOne` 错误传播；增加可注入 discovery writer/cleanup seam，证明失败时不记录/维持 listening。 | 注入 writer sentinel，断言快速退出、不等待父 ctx；覆盖 discovery/cleanup/stop 组合错误。 | G02 |
| F05 | `cmd/mcp-lsp/multilsp/adapter.go`：`findProjectRootWithin`、bootstrap/first-source walker 接收 ctx，共享受控 walk helper，每个 entry 先检查 ctx，再检查 entry/depth budget；同步传递到 `gomod.go` 与 manager lifecycle。 | 需要 | 复用已有请求 ctx，不再增加第二个 timer；middleware timeout 保留为外层 deadline。取消或超限必须返回明确错误，不得退回目录 fallback 或“未找到”。 | 预取消、深度超限、entry 超限、ignored-dir、预算内 marker；关键包 `-race`。 | G02 |
| F06 | `internal/mcpserver/common/bootstrap/env.go`：`parseBootSnapshot` 与 `normalizeConfig` 返回 error，strict decoder 拒绝未知字段、错误类型、trailing JSON；`bootstrap.New` 改 `(*Client,error)`。 | 需要 | `cmd/mcp-lsp/fx.go`、`cmd/mcp-ida/fx.go`、`cmd/mcp-orch/fx_transport.go` 的 provider 传播构造错误，在 register 前阻断；不保留无 error 兼容构造器。 | 截断、unknown、双文档、错误类型 RED；合法/空 raw GREEN；三个 binary 构造测试。 | G03 |
| F07 | `internal/mcpserver/common/server.go` 增加共享 `validateJSONRPCID`，只允许 absent/null/string/number；stdio 与 HTTP dispatch 在执行 method 前共同调用，非法响应 `id:null`。 | 需要 | 两条 transport 共用同一 validator 和参数化 fixture；业务 tool/service 不重复校验。 | object/array/bool × stdio/HTTP，断言 Invalid Request、id null、provider call count=0。 | G03 |

### 4.3 Orchestration、Cron 与 Store

| ID | 根因与最优主修落点 | 是否需要上层防御 | 上层防御落点与方法 | RED / GREEN | Owner |
|---|---|---|---|---|---|
| F08 | `cmd/mcp-orch/orchestration/nodeexec/config.go` 增加包内 strict decoder；`ParseAgentConfig/ParseAutomationConfig/ParseHybridConfig` 全部使用 `DisallowUnknownFields + trailing EOF`。不要给 DTO 分别写 `UnmarshalJSON`。 | 需要 | 写边界 `ValidatePersistableNodeConfig` 必须使用同一 parser；执行层继续解析历史行，禁止兼容 fallback。 | `cwdd`、`timeout_secc`、追加第二 JSON 文档当前误过，修后全部失败。 | G04 |
| F09 | `ValidatePersistableNodeConfig` 对 agent 调用 `ValidateLaunchCWDForNodeConfig`，让 add/create/update 在持久化前拒绝空、缺失、相对 cwd。 | 需要，三层 | 保留 `DispatchNode` 入队校验和 `AgentExecutor.Execute` launcher 前校验，用于历史坏行/手工 DB 修改；三层必须复用同一规则。 | add/update 缺 cwd RED；写入、入队、执行三层正负例 GREEN。 | G04 |
| F10 | `internal/module/cron/scheduler_recovery.go:recoverDanglingRun` 的 default 返回包含 run/job/status 的 error，不自动把未知状态推进 failed。 | 需要，已有边界为主 | 保留 migration status CHECK、SQL unresolved 状态过滤；页恢复继续处理其他合法项，但最终返回聚合错误并记录坏 run。增加状态生产集合与 recovery switch 消费集合的 missing/stale 守卫。 | 混合合法/未知状态页：合法项处理，最终 error 包含坏 ID/status。 | G04 |
| F11 | `cmd/mcp-orch/store/taskdag/store_complete_downstream.go:configuredRunFinalSharedfilePath` 改 `(string,error)`；语法/类型错误向 `finalOutputMetadataFromNode -> maybeFinalizeRunTx` 传播并回滚事务。 | 需要 | F08/F09 阻断新坏配置；store 层仍校验历史坏行。局部投影解析不能 `DisallowUnknownFields`，因为完整 config 合法含其他字段。 | malformed、`outputs:"bad"` 必须回滚；含合法额外 config 字段不得误拒。 | G05 |
| F12 | `internal/store/auditlog/store_field_guard_test.go` 动态枚举 `sqlc.AuditEvent -> AuditEvent` producer/consumer，显式 registry 并 one-hot 调用真实 `mapAuditEvent`。 | 需要，但只需测试防线 | 运行时不能区分合法零值与漏映射零值；正确上层防线是同包动态 missing/stale + one-hot guard，并进入受影响包/pre-push。 | 临时删 mapper 分支和 registry 条目分别 RED；恢复 GREEN。 | G05 |
| F13 | `internal/store/buslog/store_field_guard_test.go` 分别守卫 list row 与 detail row 两条方向，不能共用一个假覆盖 registry。 | 需要，测试防线 | 动态 producer/consumer 差集；特别验证 `HasTraceback/HasExtra int64->bool`、时间和 JSON 转换。 | 分别删除 list/detail 映射，只有对应方向 RED。 | G05 |
| F14 | 删除 `cmd/mcp-orch/store/workspace/store.go:fromSQLCRun`；保留真实使用的 row-specific mapper。 | 不需要 | LSP xref + diagnostics 是充分 owner 证据；不为未来可能用途保留第二个 mapper 真值。 | 包测试与 diagnostics 0。 | G05 |

### 4.4 Provider、Thread、Memory 与 Dashboard

| ID | 根因与最优主修落点 | 是否需要上层防御 | 上层防御落点与方法 | RED / GREEN | Owner |
|---|---|---|---|---|---|
| F15 | `internal/provider/codexapp`：严格解码 resume/fork result，缺/空 `thread.id` 必须 error；删除 `decodeThreadID` fallback；recovery 收到 decode error 时禁止 replay。 | 需要 | 请求前 `requireProviderResumeThreadID` 保留；响应后只有严格解码成功才能 `setThreadID` 和 replay。旧 ID 不能充当远端确认值。 | invalid JSON、缺/空 ID，即使传旧 ID 也失败；recovery 不更新、不 replay。 | G06 |
| F16 | `internal/provider/claudecli/session_history.go`：resolved UUID fallback 读取 error 直接返回；只有成功且非空才替换 messages。 | 不需要重复业务判断 | 上层 memory/thread 已传播 `ReadHistory` error；不得把空列表解释为 I/O/权限错误。 | resolved sentinel 通过 `errors.Is`；首 source 非空时不访问 fallback；合法空仍成功。 | G06 |
| F17 | `internal/module/thread/stop.go:cleanupThreadTurns` 返回聚合 error，并继续尝试全部 unique target；复用/泛化 typed lifecycle partial-cleanup error。 | 需要 | Stop/Archive/Delete 已有 durable 状态变化时仍发布真实 stopped/archived/deleted event，避免 UI 陈旧；RPC 随后返回 typed partial error，禁止报告全成功。不得在 RPC 层重复 cleanup。 | 三入口注错；所有 target 仍尝试；event 一次且 RPC error 保留 cause。 | G07 |
| F18 | `internal/module/memory/extract_runtime.go`：用容量 1 的 result channel 返回 `Wait()` 结果，recover 转明确 error；不再用 `close(done)` 表达成功。 | 需要 | 复用 `internal/app/runner.go` pre-drain 错误链，让 shutdown result 收到错误；不让 safego 仅记录 panic 后继续成功。 | wait panic、正常 wait、ctx timeout、app sentinel drainer；memory/app `-race`。 | G07 |
| F19 | `internal/module/dashboard/ui_page.go`：loader switch 返回 `([]loader,error)`，default 对空/未知 page 返回 unsupported-page error；不维护第二份 allowed-page 数组。 | 需要，薄上层 | `ui/dashboard/get` StrictHandler 只传播 service error；前端 page 枚举仅作 UX，不是正确性边界。可加动态测试证明所有前端发出的合法 page 都有 loader。 | 空、空白、unknown RED；所有合法 page 和 RPC envelope GREEN。 | G07 |

### 4.5 Wire 契约、前端消费与安全

| ID | 根因与最优主修落点 | 是否需要上层防御 | 上层防御落点与方法 | RED / GREEN | Owner |
|---|---|---|---|---|---|
| F20 | 对值 struct/time 的 `omitempty` 逐项做语义裁决：确属“零值可省略”的字段改 `omitzero`，必传字段移除 `omitempty`，只有需要区分 absent 与 explicit zero 才改指针。禁止机械全仓替换。 | 需要，wire 测试 | owner 文件包括 `nodeexec/config.go`、`internal/contract/{agent_state,dream,runtime_reporter}.go`、`internal/dto/{cron,event,provider/session}.go`、`cmd/mcp-orch/workspace/event.go`、`internal/module/uistate/model_providers.go`。每包增加零值省略/非零保留测试。 | 零值当前错误出现 `{}`/零时间；修后省略且非零 key/value 不变。 | G04 只处理 config；G08 处理其余 |
| F21 | uistate `Budget/TokenPool` 若保持 optional，Go 改 `omitzero`；前端 `ModelProvidersCardModel` 已能把缺失对象规范为 `{}`，必须用测试锁定，而不是依赖后端永远发 `{}`。 | 需要，跨层字段守卫 | `backendResponseValidators.js` 保持 optional-if-present 严格 key/type 校验；新增“字段缺失时 model 正常归一化、字段存在时严格校验”的前端测试。生产 Go 字段集合与 validator key set 做 missing/stale 守卫。 | 删除 budget/tokenPool 返回字段，UI 仍可编辑；未知 nested key 继续 RED；frontend lint/test/build。 | G08 |
| F22 | `pkg/logger` 增加私有 `ensurePrivateLogDir(0700)` 和 `openPrivateAppendFile(0600)`；初次、agent file、watchdog 重建统一使用，并显式收紧既存宽权限。 | 需要 | 包级行为测试覆盖新建、既存 0755/0644 收紧、agent 日志、watchdog 重建、chmod/open 失败；不再在三个入口复制 mode。 | Unix mode 当前 RED；helper 后 `go test` 与 `-race` GREEN。 | G09 |

### 4.6 Diagnostics 债务

| ID | 根因与最优主修落点 | 是否需要上层防御 | 上层防御落点与方法 | RED / GREEN | Owner |
|---|---|---|---|---|---|
| F23 | 语义修复合并后按代码 owner 清理剩余 Error/Warning/Information/Hint；机械 modernize 与语义修复不得混在同一提交。 | 需要 | 建立“changed files diagnostics 必须 0 + 定期全仓 diagnostics 必须 0”两层门禁；collector 遇到 timeout、no package metadata、build-context 污染必须非零退出，禁止写空 baseline。 | 每片保存原始 diagnostics；修后同文件 0 项。`[aix,ppc64]` 污染先修工具/重载，不得用 `go test` 冒充 diagnostics。 | G10 |

## 5. 上层防御选择原则

| 风险类型 | 根因层 | 推荐上层防御 | 禁止做法 |
|---|---|---|---|
| 非法输入/协议 | parser/DTO owner | transport parity test、严格 handler、写边界复用同一 validator | 每层各写一份枚举或默认值 |
| 持久化坏数据 | 写入 validator | 读取/执行层继续 fail-fast，保护历史行和人工修改 | 读取失败解释为字段缺失 |
| 生命周期部分提交 | domain coordinator | typed partial error + 真实 durable event + retry/可观测性 | 只日志、假成功、回滚已经完成的真实状态 |
| 并发/取消 | 资源 owner | request ctx、bounded budget、外层 deadline/race fixture | 第二套 timer、后台继续扫完 |
| mapper 字段遗漏 | mapper owner | 动态字段差集 + one-hot/roundtrip 测试 | 运行时根据零值猜漏字段 |
| 生成物漂移 | generator/input closure | staged refresh + push/stop check + 单一生成 owner | 手改 JSON、多个 agent 并发刷新 |
| 文件权限 | 文件创建 owner | 统一 secure helper + 重建/既存文件测试 | 只改首次创建 mode |

## 6. 执行波次与依赖

```text
Wave 0  G00 复核 459001c16 本地/远端 SHA、工作树与当前门禁
   |
Wave 1  G01 治理种子（F01 -> F02 -> F03，三个中文提交）
   |
Wave 2  并行语义修复
   |      G02 MCP-LSP
   |      G03 MCP common
   |      G04 orchestration/cron
   |      G06 provider
   |      G07 lifecycle/dashboard
   |      G09 logger
   |
Wave 3  数据与跨层防御
   |      G05 taskdag/store/mapper guards
   |      G08 wire DTO/frontend
   |
Wave 4  G10 diagnostics debt（只能基于已合并语义代码）
   |
Wave 5  G99 串行 rebase、review、唯一生成物刷新、全仓门禁
```

共享冲突规则：

- G04 独占 `nodeexec/config.go`；严格解码、cwd、同文件 `omitzero` 同一提交链完成。
- G03 的 bootstrap 构造签名变化先合并，再由三个 binary 调用方同步；JSON-RPC ID 在同包下一提交完成。
- G05 的 auditlog/buslog 守卫只写同包测试，不修改 `internal/archtest`。
- G08 不修改 G04 已处理的 config wire tag；只处理 contract/dto/uistate/frontend。
- G10 不得重写语义 lane 的文件，除非 owner 完成后明确移交。
- 除 G99 外，任何 agent 都不得刷新 codemap、project-map、capability manifest、sqlc 或 embed 产物。

## 7. 每个 Agent 的交付协议

```text
STATE: DONE | BLOCKED
BASE_SHA:
BRANCH:
COMMITS:
WRITE_SET:
RED_COMMAND_AND_FAILURE:
GREEN_COMMAND_AND_RESULT:
LSP_LOCATE_INSPECT_XREF_READ_DIAGNOSTICS:
FIELD_GUARD_PRODUCER_CONSUMER_DIFF:
UPPER_DEFENSE_PROOF:
RESULT_GATES:
REMAINING_RISKS:
WORKTREE_STATUS:
```

越界时必须停止并输出：

```text
NEEDS_APPROVAL
lane:
requested_paths:
reason:
risk_if_denied:
```

## 8. 分片验证

各 lane 至少运行以下匹配命令；不能用一条全仓 PASS 替代 focused RED/GREEN：

```bash
# G01
go test ./scripts/ai_maintenance ./scripts/capcontract ./internal/devtools/capcontract ./internal/guards ./internal/archtest -count=1
make project-map-check capcontract-check guard

# G02-G03
./scripts/test_with_guard.sh ./cmd/mcp-lsp/... ./internal/mcpserver/common/... -count=1
./scripts/test_with_guard.sh -race ./cmd/mcp-lsp/multilsp ./cmd/mcp-lsp/middleware -count=1

# G04-G05
./scripts/test_with_guard.sh ./cmd/mcp-orch/... ./internal/module/cron ./internal/store/auditlog ./internal/store/buslog -count=1
make sqlc-verify

# G06-G07
./scripts/test_with_guard.sh ./internal/provider/... ./internal/module/thread ./internal/module/memory/... ./internal/module/dashboard ./internal/app -count=1
./scripts/test_with_guard.sh -race ./internal/provider/codexapp ./internal/provider/claudecli ./internal/module/thread ./internal/module/memory/... -count=1

# G08
./scripts/test_with_guard.sh ./internal/contract ./internal/dto/... ./internal/module/uistate -count=1
cd frontend-app && npm run lint && npm test && npm run build

# G09
./scripts/test_with_guard.sh ./pkg/logger -count=1
./scripts/test_with_guard.sh -race ./pkg/logger -count=1
```

## 9. 最终集成门禁

G99 按 `G01 -> G02 -> G03 -> G04 -> G06 -> G07 -> G09 -> G05 -> G08 -> G10` 顺序 rebase 和吸收。每次吸收后复跑该 lane focused gates，全部完成后只刷新一次生成物。

最终至少执行：

```bash
git diff --check
python3 scripts/validate_super_agent_skills.py
make guard
make codemap-check
make project-map-check
make capcontract-check
make sqlc-verify
make build-plain
./scripts/test_with_guard.sh ./cmd/... ./internal/... ./pkg/... ./scripts/... -count=1
cd frontend-app && npm run lint && npm test && npm run build
```

同时要求：

- 所有修改文件 `file(diagnostics)` 为 0；工具错误必须有 blocker，不能记 PASS。
- `git status --short` 只包含计划内、明确 owner 的文件；生成物 diff 与源变更一一对应。
- 每个中文 fix commit 包含对应回归测试并通过正常 hooks，禁止 `--no-verify`。
- 若执行 push，必须记录 push 前 HEAD、pre-push 最终结果和远端目标 ref SHA；本地绿色不等于已推送。

## 10. 明确不采用的方案

- 不为 bootstrap、node config、provider resume 或 history 增加默认对象/旧 ID fallback。
- 不在前端白名单、RPC handler、service 和 store 四层复制同一枚举。
- 不用延长 timeout、扩大扫描 ignore、降低 diagnostics severity 或刷新空 baseline 制造假绿。
- 不手改 sqlc、codemap、project-map、capability manifest 或 embed 产物。
- 不把 mapper 字段完整性做成运行时零值 panic；正确防线是动态字段守卫。
- 不让多个 agent 在各自 worktree 刷新共享生成物后互相覆盖。

## 11. 2026-07-15 集成执行结果

执行工作树：`.worktrees/risk-wave3-integration-20260715`；集成分支：`codex/risk-wave3-integration-20260715`。本计划最终修复统一收敛到该工作树；主工作区存在此前遗留的同路径脏文件，因此最终以集成分支提交和 staged-tree 门禁为交付真值，不以主工作区状态作隔离证明。

本轮按最新产品范围收敛为 SQLite-only：

- 删除内置 PostgreSQL MCP 启动、安装、RPC、契约与顶层 PostgreSQL migrations；移除 pgx/pgpass/pgservice/puddle 模块依赖。
- 保留 PostgreSQL 环境变量清洗、发布包禁带 PostgreSQL runtime、旧 RPC/命令拒绝和 pgx deny-import，作为防止兼容面回流的上层防御。
- `dbquery` 只接受 SQLite `?` 占位符；`$n` 立即报错。
- `command_card.sql` 采用 SQLite 事务内 update -> conditional insert -> get 的等价实现，保持不可变字段并更新可变字段。
- SQL diagnostics 保留现有 SQL peer 的语义能力，只在当前产品仓库布局内用生产 SQLite 迁移器和 modernc SQLite 替换诊断；覆盖隔离 worktree、未落盘新迁移、特殊迁移正文、sqlc 查询、schema patch、fixture、文档版本和增量变更拒绝。
- SQL xref 两次拒绝已稳定复现为 `sql-language-server 1.7.1` 未声明 references capability 且请求返回 `-32601 Unhandled method`，不是位置选择错误；当前组合层不伪造 references 能力。

阶段性验证已取得 focused race/test、`make guard`、codemap/project-map/capcontract/sqlc checks、`make build-plain`、后端全包验证及前端 lint/test/build 绿色。全量 `make lsp-diagnostics-check` 在修正非法 JSON 故障夹具后于 7 分 11 秒完成，覆盖 3506 个 tracked candidates、检查 3443 个文件、按主机构建约束跳过 63 个文件、diagnostics=0；该结果早于新文件暂存和最新主线回放，只作为性能与故障定位检查点。最终交付仍须在 stage、官方生成物刷新并吸收最新 `main` 后重跑全部门禁，通过正常中文提交 hooks；提交 SHA 在实际提交成功后记录，不预填。
