# MCP / integration 最终总审修复证据

日期：2026-07-17

## 1. 审查对象与结论

- exact baseline：`f56d9ca870f735dd5e75c8c181c4db8a79a4f446`。
- 分支：`codex/reasonix-p0-final-mcp-integration-fixes`。
- 范围：仅处理最终总审已接受的 7 个 MCP / integration finding；不扩展 Release 设计。
- 当前结论：7 项 finding 的实现与 focused/race/artifact/frontend/scripts/archtest 门禁均
  GREEN。Makefile/PowerShell LSP adapter 不可用，明确记录为 unsupported blocker，不伪称 PASS。

## 2. Finding 清零矩阵

| # | 最终实现 | fail-fast / 测试证据 | 状态 |
| ---: | --- | --- | --- |
| 1 | `build-peer-binaries` 构建 helper 与 manifest；`build-agent-terminal`、`build-agent-terminal-plain`、`run`、`run-plain` 显式依赖该目标，macOS/Linux/Windows package 和 verify 均携带 package-owned helper+manifest。helper 与 desktop app 由同一 `APP_COMMIT` linker identity 构建，三平台 package cache key 也绑定该 commit。manifest 锁定 presence、regular/non-symlink、executable policy、SHA-256、protocol、app commit、Go version、GOOS/GOARCH。 | `TestDesktopBuildAndRunTargetsBuildCurrentPeerArtifacts` 锁定四个依赖、无反向循环及 helper/app/manifest 的同一 commit identity；真实 `make build-agent-terminal-plain` 后 helper 自校验通过。`helper_manifest_test.go` 另覆盖缺失、symlink、非 executable、篡改及 identity 混装。 | GREEN |
| 2 | production helper 仅由 `os.Executable()` 对应 canonical package layout 推导；desktop/test dev profile 才允许项目 `bin`，且同样要求 manifest。client 在构造时校验并保存 bytes，每次执行写入私有 0700 snapshot，拒绝 symlink 与校验后路径替换。 | `PROJECT_ROOT`/env/PATH 不影响 production；替换原 helper 后仍只执行 pinned image；snapshot cleanup 失败 fail-closed。 | GREEN |
| 3 | mcp_server 配置 owner 增加单调 `configRevision` 与共享 `configMu`；authority token 绑定 revision。Add/Delete/SQLite/Playwright 配置写入、publish CAS 与 CallTool lease 进入同一事务边界。 | `TestAuthorityCallLeaseBlocksDeleteUntilCallbackReturns` 用 channel barrier 保持真实 With callback，并由 `configMu.TryLock` 确认 lease 持锁；并发 Delete 在释放前不能提交，释放后完成且旧 token stale。`TestAuthorityPublishCASLeaseBlocksDeleteUntilCallbackReturns` 对 publish CAS 使用同一确定性证明；normal 50 次、race 20 次通过。 | GREEN |
| 4 | MCP binary 配置入口 hard cap=32；factory/ListTools 前使用 4-slot semaphore，超限在任何 factory 前失败。 | over-limit factory call=0；峰值 active factory 为 2..4；失败批次只关闭所有实际创建成功的 client，race 启动屏障测试连续 20 次通过。 | GREEN |
| 5 | Windows helper 使用 `CREATE_SUSPENDED`，先创建 kill-on-close Job、assign，再 `NtResumeProcess`；assign/resume 失败关闭 Job，由调用方 kill/reap。 | source-order guard、失败清理测试与 Windows amd64/arm64 cross-build 通过；未执行也不宣称 Windows 原生 E2E。 | GREEN |
| 6 | Go guard 反射真实 `recoverySurfaceState`、`recoveryActionAvailability`、`app.RecoveryProjection` JSON tags；前端分别对 state/actions/projection 做 exact-field fail-fast。 | 三层 producer mutation 均 RED，错误含 chain/producer/stage/field；前端缺失/未知字段测试、161 files / 2440 tests、build/embed 全部通过。 | GREEN |
| 7 | staged hook 所有临时对象进入显式 `${TMPDIR:-/tmp}`；cleanup 检查每次 remove 与 worktree registration，失败会覆盖成功退出码。 | 受控 TMPDIR 退出为空、fixture 仅主 worktree；注入 cleanup failure 时 hook 必须失败并输出明确错误。测试验证后仅按 exact path 删除本任务发现的 5 个遗留临时目录。 | GREEN |

## 3. Artifact identity

本机 `make build-peer-binaries` 生成并自校验：

| 字段 | 值 |
| --- | --- |
| protocol | `reasonix.mcp-schema-helper/v1` |
| helper | `mcp-schema-compiler-helper` |
| SHA-256 | `0e4588865dbe96d7cae7de1328cffd5be24f75d7f14423a26ce8abcc43195d91` |
| executable policy | `owner_execute` |
| app commit | `ee924464c76910615b5d9e04bf2f434a9d19dc1e`（follow-up 构建时 HEAD） |
| Go / target | `go1.25.7` / `darwin-arm64` |

package verifier 在归档/发布前执行 packaged helper 的 `--verify-package`，因此缺 helper、
缺 manifest、混装或篡改均在 package/publish 前失败；没有 env/PATH silent fallback。
follow-up 真实执行 `make build-agent-terminal-plain` 后，helper `--verify-package` 与 agent
binary commit string 检查均通过；linked worktree 不再依赖 Go 错误指向主 worktree 的自动
`vcs.revision`。

## 4. 门禁结果

| 门禁 | 结果 |
| --- | --- |
| focused：mcp_server/toolbridge/schema/thread/turn/provider shared | PASS；schema 7.695s，其余通过或 cache hit。 |
| race：同上六包 | PASS；toolbridge 4.218s，schema 及其余包通过。 |
| helper budget/cancel/process/package identity | PASS；malicious stderr overflow 单独连续 5 次稳定为 `MCP_SCHEMA_OUTPUT_TOO_LARGE`。 |
| scripts package/publish/hook guards | PASS；`go test ./scripts` 151.737s。 |
| archtest | PASS；32.026s。 |
| frontend lint/test/build/embed | PASS；161 files / 2440 tests，5594 modules；embed smoke `a89d4c965d9d8b69b6ab7c3ad1e29cb0f0bf130a47be4ee3c32e5f4154010334`。 |
| affected cross-build | PASS；darwin/linux/windows x arm64/amd64，目标为 schema helper、toolbridge、mcp_server。 |
| `git diff --check`、shell syntax | PASS。 |
| authority lease follow-up | PASS；CallTool 与 publish CAS barrier normal 50 次、race 20 次；mcp_server/schema 全包 normal/race 通过。 |
| desktop helper closure follow-up | PASS；真实 `make build-agent-terminal-plain` + helper 自校验；`go test ./scripts` 150.816s；六目标 helper/schema/toolbridge/mcp_server strict cross-build 通过。 |

失败处置事实：一次把 focused 与重型 scripts 并跑时，stderr overflow 超过 2s deadline；
串行连续 5 次和最终 focused 均稳定通过，因此最终门禁固定串行执行。首次 race 又暴露测试
未保证全部预期 client 已创建；加入 factory 启动屏障后目标 race 连续 20 次与六包 race
全部通过。没有放宽 deadline，也没有接受多种错误码。

follow-up 首轮 full scripts 唯一失败是 Windows package cache guard 仍匹配未包含
`APP_COMMIT` 的旧 `$goInputs` 文本；实现已把 commit 加入三平台 cache identity，guard 同步
为强制要求新字段后 focused 与第二轮 full scripts 通过。一次额外 desktop cross-build 探针
因未启用 `set -e` 且 Wails 在 macOS/Linux `CGO_ENABLED=0` 下不可构建而作废；最终证据只采信
启用 `set -e` 的既定 affected 边界，不把该探针写成 PASS。

## 5. LSP 与平台边界

- 已完成 `grep/structure -> inspect -> xref -> file(read_file) -> patch_edit ->
  file(diagnostics)` 全链。
- 最终 diagnostics：原 25 个 changed Go、2 个 JS、5 个 shell/hook source，以及 follow-up
  新增/修改的 8 个 Go 与 2 个 shell 文件均为 0，所有可用 adapter severity 清零。
- `Makefile`、`scripts/package_windows.ps1` 与 `scripts/verify_packaged_app_windows.ps1` 的 diagnostics
  返回 `language_unsupported`；这是工具链能力 blocker。三文件由 scripts guards 和
  Windows cross-build 对应产物链验证，但不把这些替代项写成 PowerShell LSP PASS。
- Windows 只完成 cross-build/source-order/失败清理 guard；没有本机原生 Job Object E2E。

## 6. 泄漏与 Git 边界

- staged hook 目标测试后受控 `TMPDIR` 为空，fixture `git worktree list --porcelain` 仅主
  worktree；cleanup failure 注入按预期使测试失败。
- 现场发现的 2 个 codemap、2 个 index、1 个 pre-commit-worktree 历史临时目录只在上述
  测试 GREEN 后按 exact path 删除；未删除 7 个历史任务 worktree。
- stash 为空。最终 full pre-commit、提交 SHA、clean worktree 与进程/worktree 复核以提交
  阶段和最终回报为准。
