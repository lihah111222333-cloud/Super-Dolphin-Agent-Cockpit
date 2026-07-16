# Task 5 Release / MCP 联合收口证据

日期：2026-07-17

## 1. Review object 与结论边界

- exact integration base / 审查 HEAD：`af51558b4a625764afa1dbd5f92191ef6ce01ddb`。
- 分支：`codex/integration-reasonix-p0`。
- 该 merge commit 的父提交为 `647ddbfe934581bcefa77ac4c86f66840a1e2e82` 与
  `5962787e38701aa86ac4d4559d4de84841385dae`，包含 Task 1/2/3/4A/4B。
- Review object：Release lane 与 Codex MCP lane 的 shared seams、生成物、hook gate
  plan、构建/嵌入/发布脚本，以及 Task 3 对 Task 2、Task 4 对 thread/turn 配置链的
  integration 冲突检查。
- 联合收口审查结果：`0 P0 / 0 P1`，本轮 DoD/门禁内 `P2=0`。两条 lane 均
  `PASS`，但 lane PASS 不等于 repo PASS。
- 仓库 P0 完成状态：`PENDING_THREE_FRESH_FINAL_REVIEWS`。计划第 5、7.3 节要求
  integration 接受 Release、MCP/security、repo-wide integration 三条全新总审，
  三者都达到 `0 P0 / 0 P1` 后才能宣告 P0 完成；本证据不替代该门禁。

## 2. Release lane DoD

| P0 要求 | 结论 | integration 证据 |
| --- | --- | --- |
| probation 期间 backup 存在 | PASS | transaction journal 在 `backup_retained`/probation 保留 exact backup；focused test 与独立 Guard E2E 通过。 |
| crash/timeout/interruption 自动 exact rollback/restart | PASS | Guard armed receipt 后独立监督；candidate crash、probation timeout、父进程中断均落到 exact old target restore/restart；PID/start-token/digest/path 共同绑定进程身份。 |
| healthy 后才 commit trust/delete backup | PASS | exact ACK 验证后先持久化 `commit_pending`，再执行 exact backup 删除，最后以 `committed` 暴露 committed trust；healthy 前始终 pending。旧 Task 1 证据已按该 crash-safe 顺序做最小事实修正。 |
| Recovery graph 无 normal 高风险依赖 | PASS | `SelectRecoveryServices` 仅组装 recovery store/handler/runtime/lifecycle；命令级测试证明 recovery 选择先于 normal preflight，normal factory 调用数为 0。 |
| production trust 无 env/CLI downgrade | PASS | package trust 从 `os.Executable()` exact app layout 推导；source/key/signer/helper digest 由 package-owned trust 固定，production env/CLI override fail-closed。 |
| 六目标 capability 诚实 | PASS | `darwin-arm64` 才开放 check/install/publish；darwin-amd64、linux-amd64/arm64、windows-amd64/arm64 全部显式关闭，不存在半开路由。 |

Task 3 的 package trust、Guard readiness/receipt、artifact E2E 与 Task 2 recovery
bootstrap 在 integration 上没有冲突：Guard 只在 owner/config/transaction/process
identity 全部校验后 armed；recovery 启动不加载 normal graph；transaction terminal
state 与 package trust generation 保持单 owner。

## 3. Release lane D01-D19 coverage

| 维度 | Coverage | 证据与残余风险 |
| --- | --- | --- |
| D01 架构边界 | Applied | appupdate 拥有 package trust，appupdaterecovery 拥有 transaction/supervisor，recovery graph 只依赖 allowlist 服务；archtest PASS。 |
| D02 Fail-fast | Applied | 缺 trust、错误布局、错误 ACK、错误 digest/process identity、未知 journal 状态均拒绝；无 env/CLI fallback。 |
| D03 MCP 协议 | N/A | Release lane 不修改 MCP 协议；MCP lane 单独登记。 |
| D04 LSP 工具 | N/A | 产品 LSP 行为未改；本次审查工具证据见第 7 节。 |
| D05 Provider/runtime | Applied | recovery runtime 与 normal runtime 分离；normal factory 在 recovery path 零调用。 |
| D06 Orchestration | Applied | probation/rollback/commit 状态机的幂等、超时、中断与 restart 均有测试；未引入通用 DAG。 |
| D07 Store/sqlc | N/A | 无 SQL/sqlc 变更；journal/capsule 为 platform recovery owner。 |
| D08 Skill/Memory/Prompt/Thread | N/A | Release lane 未改变这些链路。 |
| D09 Frontend | Applied | recovery projection terminal 字段 guard、全量 frontend lint/test/build 与 embed verify PASS。 |
| D10 Security | Applied | exact executable/layout/path/digest/signer/key/receipt/process tuple；伪造 override 与 PID reuse fail-closed。 |
| D11 Observability | Applied | journal state、chain/producer/field guard 错误和 recovery failure 保留可定位上下文。 |
| D12 Testing | Applied | focused、race、Guard process、independent artifact、scripts/archtest 与 staged hook 均纳入门禁。 |
| D13 Release/Install | Applied | manifest/package/publish guards、backup/rollback、artifact reopen、六目标 capability 与 frontend embed 均验证。 |
| D14 Performance | Applied | probation poll/timeout 有界；supervisor/Guard race PASS；无未回收测试进程。 |
| D15 UX/Product | Applied | recovery UI 投影和终端 consumer 由动态字段 guard 锁定；不宣称人工视觉 QA。 |
| D16 Git/Workflow | Applied | exact integration HEAD、clean 起点、staged snapshot hook、generated refresh/check、最终 clean/leak 检查。 |
| D17 字段守卫 | Applied | journal producer 由 reflection 递归枚举；projection producer/mapper/terminal 由 AST/reflection 动态验证并含 mutation RED。 |
| D18 DRY | Applied | transaction、trust、recovery owner 各自唯一；命令层不复制 backup/rollback 控制流。 |
| D19 SSOT | Applied | journal 是 transaction/trust state owner；package trust 是 production policy owner；生成物只由 generator 单向刷新。 |

Release lane 残余风险：真实 rename/process/artifact E2E 只能在当前 Darwin arm64
内核执行；其他五目标证明的是 fail-closed capability 与 cross-build，不宣称原生安装
E2E。该限制是计划边界，不伪装为已完成的跨平台现场验证。

## 4. MCP lane DoD

| P0 要求 | 结论 | integration 证据 |
| --- | --- | --- |
| trusted external mixed good/bad 只隔离坏项 | PASS | raw identity 后逐 tool canonicalize/compile；schema 类错误只写坏项 quarantine，good tool 保持 catalog/proxy/call surface。 |
| managed fail-fast | PASS | managed schema 任一失败撤销旧 surface 并使整代失败；managed provenance 只能由 `BuildManifest` 直接构造。 |
| compiled digest 在 catalog/provider/proxy/call 一致 | PASS | canonical schema SHA-256 是唯一 digest；surface entry、dynamic schema、validator 与 call fence 使用同一 canonical bytes/digest。 |
| stale authority / 丢 `TrustedServerID` 零 surface 零 client call | PASS | issue/CAS/call 均检查 generation、membership/config digest 与 current config；compile 前后、publish 前、validate 前、client call 前均有 fence。 |
| compiler 预算/取消无泄漏 | PASS | one-shot helper 有全局并发 2、250ms capacity wait、2s deadline、1s reap；kill/wait 后释放 slot，reap 失败永久占用 slot并 fail-closed。 |
| 严格限 Codex toolbridge | PASS | landing path 为 v3 Codex toolbridge；未扩展 Claude readiness、通用 provider admission、常驻 worker/cache。 |

Task 4 authority/current-CAS 与 thread/turn 配置链在 integration 上没有冲突：
`MCPServerConfig.TrustedServerID -> mcpServerConfigBinary -> dto.MCPBinary` 在 start、
resume、turn manifest 与 provider shared copy 中保留；external authority 同时要求 exact
server name、trusted ID 与 HTTP/stdio config parity。旧 generation 在 publish/call 前失效。

## 5. MCP lane D01-D19 coverage

| 维度 | Coverage | 证据与残余风险 |
| --- | --- | --- |
| D01 架构边界 | Applied | mcp_server 拥有 authority/quarantine current-CAS，toolbridge 拥有 admission/surface/call，schema package 拥有 helper client/compiler；archtest PASS。 |
| D02 Fail-fast | Applied | raw ambiguity、缺身份/TrustedServerID、managed schema failure、stale authority、helper protocol/budget failure均拒绝。 |
| D03 MCP 协议 | Applied | HTTP/stdio 只解 envelope/tools array，逐 item 保留 RawMessage；strict identity 与 helper protocol 均有 fixture。 |
| D04 LSP 工具 | N/A | 产品 LSP 行为未改；本次审查工具证据见第 7 节。 |
| D05 Provider/runtime | Applied | provider shared 只消费 config-owner 生产的 trusted ID；范围严格为 Codex v3 toolbridge。 |
| D06 Orchestration | N/A | 未引入 DAG/cron/wakeup；authority generation 是 mcp_server owner 状态。 |
| D07 Store/sqlc | N/A | 无 schema/sqlc 变更；quarantine 按冻结决策为 process-local，restart 零 surface 后重新编译。 |
| D08 Skill/Memory/Prompt/Thread | Applied | thread start/resume 与 turn assembly 的 TrustedServerID producer chain 动态守卫通过。 |
| D09 Frontend | N/A | MCP lane 没有前端 surface 变更；repo 级 frontend gates 仍独立通过。 |
| D10 Security | Applied | managed constructor provenance、external exact config/ID、absolute helper path、generation/digest fence 与 no PATH fallback。 |
| D11 Observability | Applied | 稳定 schema error code 与 server/tool/generation 上下文保留；不输出 secret config。 |
| D12 Testing | Applied | mixed/managed/stale/repair/parity/budget/cancel/concurrency/race/六目标 build 与 staged hook 全部 PASS。 |
| D13 Release/Install | Applied | helper artifact 以 `<ProjectRoot>/bin` exact absolute path 使用；六目标受影响包 cross-build PASS。 |
| D14 Performance | Applied | 输入输出/deadline/capacity/concurrency/reap 均有上限；取消与 stale-success 测试、race PASS。 |
| D15 UX/Product | N/A | 未改变 UI；坏 external tool 不进入可见 surface，managed 失败撤销整代。 |
| D16 Git/Workflow | Applied | integration exact HEAD、generated checks、normal staged hook 与最终 leak/clean 检查。 |
| D17 字段守卫 | Applied | TrustedServerID、authority/quarantine/canonical schema producer 均由 reflection/AST 动态枚举，mutation 报 chain/producer/field。 |
| D18 DRY | Applied | Task 4A canonicalizer/helper 是唯一 compiler；无 in-process fallback、resident worker 或 cache。 |
| D19 SSOT | Applied | mcp_server owner 产生 generation/current authority；canonical SHA-256 是 schema identity；thread/turn 仅传递 owner 配置。 |

MCP lane 残余风险：quarantine history 按冻结决策不跨进程持久化；restart 时 owner
与 surface 同时清空，必须重新 fetch/identity/compile/CAS 后才能发布。Linux/Windows
只证明 cross-build；原生 kill/reap 行为仍属于后续平台发布验证，不宣称现场 E2E。

## 6. 命令、门禁与结果

| 命令/门禁 | Exit | 结果 |
| --- | ---: | --- |
| Release focused `test_with_guard`：updater/Guard/release-manifest/appupdate/recovery/pidregistry/runtimeenv/app/scripts/archtest | 0 | PASS；scripts 121.241s。 |
| MCP focused `test_with_guard`：contract/mcp_server/thread/turn/provider shared/toolbridge/schema/helper/archtest | 0 | PASS。 |
| integration changed Go directories + provider/shared 统一 `test_with_guard` | 0 | 20 个 changed Go 目录全部作为显式 package 参数通过；helper 与 dto/mcp 无测试文件但编译通过，scripts 130.776s。 |
| Release `test_with_guard --with-race`（6 个并发/恢复包） | 0 | normal 与 race 两轮 PASS。 |
| MCP `test_with_guard --with-race`（mcp_server/toolbridge/schema/thread/turn/provider shared） | 0 | normal 与 race 两轮 PASS。 |
| Guard crash E2E 两场景 `-count=10` | 0 | 共 20 次通过，50.518s。 |
| independent artifact rollback/healthy E2E `-count=3` | 0 | 两场景各 3 次通过。 |
| schema helper `TestSchemaCompiler*` | 0 | budget、capacity、cancel、stale fence、protocol/isolation 全部通过。 |
| `CGO_ENABLED=0` 六目标 affected package build | 0 | darwin/linux/windows x amd64/arm64 全部通过。 |
| `frontend-app: npm run lint` | 0 | PASS。 |
| `frontend-app: npm test` | 0 | 161 files / 2438 tests PASS；RPC registry 140/140，无 hardcoded payload guard。 |
| `frontend-app: npm run build` | 0 | Vite 5594 modules 构建并同步 web-dist。 |
| `make frontend-embed-verify` | 0 | manifest PASS；smoke hash `a89d4c965d9d8b69b6ab7c3ad1e29cb0f0bf130a47be4ee3c32e5f4154010334`。 |
| `make codemap-refresh project-map-refresh capcontract-refresh` | 0 | staged snapshot：codemap 385 files/1540 refs；project-map 4541 files/drift=OK；capcontract 41 packages。 |
| `make codemap-check project-map-check capcontract-check` | 0 | 三项 generated check 全部 PASS。 |
| `git diff --check` | 0 | pre-commit 前 PASS；staged hook 的 `git diff --cached --check` 也 PASS。 |
| `.githooks/pre-commit`（正常 staged snapshot） | 0 | 自动刷新/暂存 project-map，AI maintenance command gates 与 whitespace gate PASS；未使用 `--no-verify`。hook 提示未注入 LSP evidence file，本次 LSP controller 证据由第 7 节的全量人工链独立提供。 |
| 测试进程/临时 worktree/stash 泄漏检查 | 0 | 无 `go test`/Vitest/hook 临时进程，stash 为空，pre-commit 临时 worktree 已清理；6 个 Task 0-4 既有 `.worktrees` 保留，非 Task 5 泄漏且未擅自删除。 |

`frontend-app/node_modules` 的 vite、package.json 与 package-lock 时间戳均新鲜，故本次
无需重复 `npm ci`；Makefile 和 pre-commit staged worktree 也只复用 exact matching 且
新鲜的依赖快照。

## 7. LSP 证据与缺口

两条 lane 都执行了 `grep/structure -> inspect -> xref -> file(read_file) ->
patch_edit(format) -> file(diagnostics)`：

- Release：定位 `BackupRetained`、package trust、recovery graph 与 supervisor/Guard；
  definition/hover 和 references/call hierarchy 复核 `Run`、commit/rollback、配置 owner；
  精读 supervisor、Guard、updater、journal、trust/capability 与 recovery bootstrap。
- MCP：定位 `MCPToolAuthority`、current-CAS、raw decode、canonical schema、helper client
  与 `TrustedServerID`；definition/references/call hierarchy 复核 authority issue/CAS、
  Execute/call fences 与 thread/turn/provider consumers；精读 transport、surface store、
  compiler client、owner 与动态 guard。
- `patch_edit(format)`：
  `internal/platform/appupdaterecovery/supervisor.go` 与
  `internal/platform/toolbridge/handler_codex_surface_store.go` 都返回 `NO_CHANGE`；随后
  diagnostics=0。MCP 第一次 format 使用了错误的 module 路径并返回 `file_not_found`，
  更正为实际 platform owner 路径后成功完成 no-op format 与 diagnostics。
- diagnostics：integration 相对冻结基线 `3d6fccfc58b904e2c9a6f358285cdee6d6ea7753`
  的 109 个 Go、8 个 JS/JSX/MJS、1 个 CSS、6 个 shell/hook source 全部 0（所有
  severity）。
- blocker：`structure(workspace_symbol, query=SuperviseProbation)` 在 repo root 超过
  project walk 10000 文件限制；缩窄 `work_dir` 到 package 后底层仍绑定 repo root，
  同样超限。改用 exact file 的 `structure(document_symbol)` 后成功取得结构证据。
- unsupported blocker：4 个 changed PowerShell 文件
  `scripts/package_windows.ps1`、`scripts/package_windows_github_release.ps1`、
  `scripts/package_windows_local.ps1`、`scripts/verify_packaged_app_windows.ps1` 的
  `file(diagnostics)` 返回 `language_unsupported`。因此 PowerShell LSP 不宣称 PASS；
  release scripts guard tests 覆盖其 fail-closed 文本/顺序契约。

## 8. 事实修正与 follow-up 边界

- 最小事实修正：`03-task1-release-transaction-core.md` 原文称 healthy commit “先提交
  trust 再删除 backup”，与实际 crash-safe journal 顺序不符。本次改为：exact healthy
  ACK 后持久化 `commit_pending`，执行 backup 删除，最后进入 `committed` 并暴露
  committed trust。代码与 DoD 未改变。
- 明确 follow-up，不伪装完成：Task 0 延后的 Claude readiness、provider-wide
  executable hardening、通用 RPC/admission、SBOM provenance/component graph、跨平台
  原生 install/kill/reap E2E，以及计划要求的三条 fresh final review。
- 本轮没有新增/修改生产结构化字段；复核的 Release/MCP 字段链继续使用真实 producer
  的 reflection/AST 动态 guard，没有建立全仓手写字段清单。
