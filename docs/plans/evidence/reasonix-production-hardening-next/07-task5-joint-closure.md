# Task 5 Release / MCP 联合收口证据

日期：2026-07-17

> **最终总审更正（2026-07-17）**：本文件的 Release closure 结论已被最终总审发现的
> 5 个缺口推翻，不能再单独作为 Release gate PASS 证据。原实现没有为 rollback 文件恢复
> 与旧版本 restart 建立 durable intent/token/ACK 协议；正式发布公钥连续性可由本地包
> override 满足；exact termination 仍存在 verify 后按 PID signal 的复用窗口；终态事务按
> wall clock 选择；package trust 未完整拒绝 symlink/alias tree。修复与替代证据统一登记在
> `08-final-review-release-fixes.md`，以下历史命令只说明当时测试通过，不证明这些缺口不存在。

> **MCP / integration 最终总审更正（2026-07-17）**：本文件的 MCP closure、Recovery
> 字段守卫和 staged hook 无泄漏结论又被 7 个已接受 finding 推翻。旧实现未把 schema
> helper 纳入三平台 package identity；生产 helper 路径仍受 `ProjectRoot` 影响；authority
> 未与配置 revision 共事务；MCP binary 初始化无数量/并发上限；Windows Job 在进程启动后
> 才绑定；Recovery 仅锁定 projection；hook cleanup 失败和临时目录泄漏未被测试锁定。
> 最终实现与替代证据统一登记在 `09-final-review-mcp-integration-fixes.md`。下列相关 PASS
> 主张均为历史结果，不再证明最终性质。

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
| crash/timeout/interruption 自动 exact rollback/restart | SUPERSEDED | 旧结论遗漏 restart durable receipt 与 launch-before-ACK 窗口；08 以 intent/token/process/ACK 收敛协议替代。 |
| healthy 后才 commit trust/delete backup | PASS | exact ACK 验证后先持久化 `commit_pending`，再执行 exact backup 删除，最后以 `committed` 暴露 committed trust；healthy 前始终 pending。旧 Task 1 证据已按该 crash-safe 顺序做最小事实修正。 |
| Recovery graph 无 normal 高风险依赖 | PASS | `SelectRecoveryServices` 仅组装 recovery store/handler/runtime/lifecycle；命令级测试证明 recovery 选择先于 normal preflight，normal factory 调用数为 0。 |
| production trust 无 env/CLI downgrade | SUPERSEDED | 旧结论遗漏 canonical alias tree 与正式 GitHub 上一 release 资产 provenance；08 补齐并锁定。 |
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
| D10 Security | Superseded | 旧 PID tuple 在 signal 前仍有 TOCTOU，且 package path 未全量 canonical；08 改为认证协作终止和 alias-tree 拒绝。 |
| D11 Observability | Applied | journal state、chain/producer/field guard 错误和 recovery failure 保留可定位上下文。 |
| D12 Testing | Applied | focused、race、Guard process、independent artifact、scripts/archtest 与 staged hook 均纳入门禁。 |
| D13 Release/Install | Applied | manifest/package/publish guards、backup/rollback、artifact reopen、六目标 capability 与 frontend embed 均验证。 |
| D14 Performance | Applied | probation poll/timeout 有界；supervisor/Guard race PASS；无未回收测试进程。 |
| D15 UX/Product | Superseded | 旧 guard 只锁定 projection/terminal，遗漏顶层 state 与 actions；09 改为三层真实 producer 与前端 exact-field fail-fast。 |
| D16 Git/Workflow | Applied | exact integration HEAD、clean 起点、staged snapshot hook、generated refresh/check、最终 clean/leak 检查。 |
| D17 字段守卫 | Superseded | 旧 Recovery guard 未覆盖 `recoverySurfaceState` 与 `recoveryActionAvailability`；09 补齐 state/actions/projection 三层 producer、consumer 与 mutation RED。 |
| D18 DRY | Applied | transaction、trust、recovery owner 各自唯一；命令层不复制 backup/rollback 控制流。 |
| D19 SSOT | Superseded | 旧终态选择仍依赖 wall clock；08 增加每 target 单调 generation，并以 durable journal 为选择 SSOT。 |

Release lane 残余风险：真实 rename/process/artifact E2E 只能在当前 Darwin arm64
内核执行；其他五目标证明的是 fail-closed capability 与 cross-build，不宣称原生安装
E2E。该限制是计划边界，不伪装为已完成的跨平台现场验证。

## 4. MCP lane DoD

| P0 要求 | 结论 | integration 证据 |
| --- | --- | --- |
| trusted external mixed good/bad 只隔离坏项 | PASS | raw identity 后逐 tool canonicalize/compile；schema 类错误只写坏项 quarantine，good tool 保持 catalog/proxy/call surface。 |
| managed fail-fast | PASS | managed schema 任一失败撤销旧 surface 并使整代失败；managed provenance 只能由 `BuildManifest` 直接构造。 |
| compiled digest 在 catalog/provider/proxy/call 一致 | PASS | canonical schema SHA-256 是唯一 digest；surface entry、dynamic schema、validator 与 call fence 使用同一 canonical bytes/digest。 |
| stale authority / 丢 `TrustedServerID` 零 surface 零 client call | SUPERSEDED | 旧 generation/config 重查不与配置写入共事务，仍有 stale publish/call TOCTOU；09 用配置 revision lease 关闭窗口。 |
| compiler 预算/取消无泄漏 | SUPERSEDED | 旧预算只覆盖 helper process，不覆盖 MCP binary 数量和 factory/ListTools 初始化并发；09 增加 32 hard cap 与 factory 前 4-slot semaphore。 |
| 严格限 Codex toolbridge | PASS | landing path 为 v3 Codex toolbridge；未扩展 Claude readiness、通用 provider admission、常驻 worker/cache。 |

Task 4 authority/current-CAS 与 thread/turn 配置链在 integration 上没有冲突：
`MCPServerConfig.TrustedServerID -> mcpServerConfigBinary -> dto.MCPBinary` 在 start、
resume、turn manifest 与 provider shared copy 中保留；但旧 authority generation 未绑定配置
owner 的单调 revision，不能证明在 publish/call 前失效。09 的 revision lease 取代该主张。

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
| D10 Security | Superseded | 旧 absolute path 仍从可控 `ProjectRoot` 推导且 verify 后按路径执行；09 改为 `os.Executable` canonical package layout、manifest identity 与 verified snapshot execution。 |
| D11 Observability | Applied | 稳定 schema error code 与 server/tool/generation 上下文保留；不输出 secret config。 |
| D12 Testing | Superseded | 旧测试未覆盖配置写入与 publish/call 的可控并发、package helper 混装/篡改、factory 峰值、Windows suspended bind 和 hook cleanup failure；09 补齐。 |
| D13 Release/Install | Superseded | `<ProjectRoot>/bin` 不是生产 package trust root，且 helper 未进入三平台 package/publish/verify；09 以 package-owned helper+manifest 取代。 |
| D14 Performance | Superseded | 旧上限只约束 schema helper；09 另对 MCP binary 配置数量与 factory/ListTools 初始化并发施加硬预算。 |
| D15 UX/Product | N/A | 未改变 UI；坏 external tool 不进入可见 surface，managed 失败撤销整代。 |
| D16 Git/Workflow | Superseded | 旧 staged-hook 测试未控制 `TMPDIR`，也未证明 cleanup failure 会使 hook 失败；09 增加确定性泄漏与失败注入测试。 |
| D17 字段守卫 | Applied | TrustedServerID、authority/quarantine/canonical schema producer 均由 reflection/AST 动态枚举，mutation 报 chain/producer/field。 |
| D18 DRY | Applied | Task 4A canonicalizer/helper 是唯一 compiler；无 in-process fallback、resident worker 或 cache。 |
| D19 SSOT | Applied | mcp_server owner 产生 generation/current authority；canonical SHA-256 是 schema identity；thread/turn 仅传递 owner 配置。 |

MCP lane 残余风险：quarantine history 按冻结决策不跨进程持久化；restart 时 owner
与 surface 同时清空，必须重新 fetch/identity/compile/CAS 后才能发布。09 已加入 Windows
suspended process -> Job -> resume 顺序，但 Linux/Windows 仍只证明 cross-build/guard，
不宣称原生 kill/reap 现场 E2E。

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
| 测试进程/临时 worktree/stash 泄漏检查 | SUPERSEDED | 旧测试未使用受控 `TMPDIR`，现场遗留过 `pre-commit-worktree`、codemap/index 临时目录；09 在测试证明 cleanup 后按 exact path 清理并复核。 |

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
- 本段“没有新增/修改生产结构化字段”只描述旧 Task 5。09 为 authority 增加
  `ConfigRevision`，并按字段守卫要求锁定 Recovery state/actions/projection 三层真实
  producer；不得沿用本文件的旧字段覆盖结论。
