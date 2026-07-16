# MCP / integration 最终总审修复证据

日期：2026-07-17

## 1. 审查对象与结论

- exact baseline：`f56d9ca870f735dd5e75c8c181c4db8a79a4f446`。
- 分支：`codex/reasonix-p0-final-mcp-integration-fixes`。
- 范围：仅处理最终总审已接受的 7 个 MCP / integration finding；不扩展 Release 设计。
- 当前结论：7 项 finding 的实现与 focused/race/artifact/frontend/scripts/archtest 门禁均
  GREEN。PowerShell LSP adapter 不可用，明确记录为 unsupported blocker，不伪称 PASS。

## 2. Finding 清零矩阵

| # | 最终实现 | fail-fast / 测试证据 | 状态 |
| ---: | --- | --- | --- |
| 1 | `build-peer-binaries` 构建 helper 与 manifest；macOS/Linux/Windows package 和 verify 均携带 package-owned helper+manifest。manifest 锁定 presence、regular/non-symlink、executable policy、SHA-256、protocol、app commit、Go version、GOOS/GOARCH。 | `helper_manifest_test.go` 覆盖缺失、symlink、非 executable、篡改、commit/protocol/Go/OS/ARCH 混装；完整 scripts guards 与 helper 自校验通过。 | GREEN |
| 2 | production helper 仅由 `os.Executable()` 对应 canonical package layout 推导；desktop/test dev profile 才允许项目 `bin`，且同样要求 manifest。client 在构造时校验并保存 bytes，每次执行写入私有 0700 snapshot，拒绝 symlink 与校验后路径替换。 | `PROJECT_ROOT`/env/PATH 不影响 production；替换原 helper 后仍只执行 pinned image；snapshot cleanup 失败 fail-closed。 | GREEN |
| 3 | mcp_server 配置 owner 增加单调 `configRevision` 与共享 `configMu`；authority token 绑定 revision。Add/Delete/SQLite/Playwright 配置写入、publish CAS 与 CallTool lease 进入同一事务边界。 | 可控并发测试证明读取后删除/重配时 publish=0；最终 check 后删除时 client call=0。六包 race 通过。 | GREEN |
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
| SHA-256 | `6c5e159fba14cfb990ad1a8604ae21cb30ec4085d2f4339f738cc7e37c5d09fb` |
| executable policy | `owner_execute` |
| app commit | `f56d9ca870f735dd5e75c8c181c4db8a79a4f446` |
| Go / target | `go1.25.7` / `darwin-arm64` |

package verifier 在归档/发布前执行 packaged helper 的 `--verify-package`，因此缺 helper、
缺 manifest、混装或篡改均在 package/publish 前失败；没有 env/PATH silent fallback。

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

失败处置事实：一次把 focused 与重型 scripts 并跑时，stderr overflow 超过 2s deadline；
串行连续 5 次和最终 focused 均稳定通过，因此最终门禁固定串行执行。首次 race 又暴露测试
未保证全部预期 client 已创建；加入 factory 启动屏障后目标 race 连续 20 次与六包 race
全部通过。没有放宽 deadline，也没有接受多种错误码。

## 5. LSP 与平台边界

- 已完成 `grep/structure -> inspect -> xref -> file(read_file) -> patch_edit ->
  file(diagnostics)` 全链。
- 最终 diagnostics：25 个 changed Go、2 个 JS、5 个 shell/hook source 均为 0，所有
  severity 清零。
- `scripts/package_windows.ps1` 与 `scripts/verify_packaged_app_windows.ps1` 的 diagnostics
  返回 `language_unsupported`；这是工具链能力 blocker。两文件由 scripts guards 和
  Windows cross-build 对应产物链验证，但不把这些替代项写成 PowerShell LSP PASS。
- Windows 只完成 cross-build/source-order/失败清理 guard；没有本机原生 Job Object E2E。

## 6. 泄漏与 Git 边界

- staged hook 目标测试后受控 `TMPDIR` 为空，fixture `git worktree list --porcelain` 仅主
  worktree；cleanup failure 注入按预期使测试失败。
- 现场发现的 2 个 codemap、2 个 index、1 个 pre-commit-worktree 历史临时目录只在上述
  测试 GREEN 后按 exact path 删除；未删除 7 个历史任务 worktree。
- stash 为空。最终 full pre-commit、提交 SHA、clean worktree 与进程/worktree 复核以提交
  阶段和最终回报为准。
