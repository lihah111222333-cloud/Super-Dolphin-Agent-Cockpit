# Task 3 Update Trust 与产物级恢复 E2E 证据

日期：2026-07-17

基线：`647ddbfe934581bcefa77ac4c86f66840a1e2e82`

分支：`codex/reasonix-p0-task3`

## 实现结论

- production update 配置只从 `os.Executable()` 的 exact `.app/Contents/MacOS` 布局推导 `Contents/Resources/update-trust.json`；`ProjectRoot` 不参与信任，update env/CLI override fail-closed。`SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` 仅做 executable-derived Resources 一致性证明，exact 值允许，伪造值拒绝。
- package trust 绑定 source、manifest public key、exact signer、platform、updater/Guard SHA。old/new generation 绑定 transaction；Committed 前解析 old trust，rollback 丢弃 pending，commit 后解析 candidate trust。
- Guard 在 owner、配置、transaction、可执行路径/SHA、进程身份全部校验后发布 armed receipt，下一步直接监督；parent 验证 receipt 并关闭 stdout pipe 后才允许 destructive rename。短暂 transaction lock contention 只重试 exact transaction，其他错误仍 fail-closed。
- updater 存活判断绑定 PID、start token、旧 updater digest，并仅接受 exact target/backup 两个事务路径。PID reuse 的 identity mismatch 视为 exact updater 已死亡，绝不向复用 PID 发信号，并执行 rollback。
- capsule 使用 scanner 治理的临时路径和 journal/capsule 双向 crash-window 收敛；每个文件及 capsule/parent directory fsync，复制后重验 trust generation/helper hashes。Committed/RolledBack 清理 capsule，终态 trust 不依赖 capsule。
- 六目标 capability matrix 仅 `darwin-arm64` 开放 check/install/publish；Linux、Windows、macOS amd64 均无半开更新路由。Windows 普通 `.exe` 产品分发仍保留，但没有 Windows update manifest/publisher。
- release key continuity 严格读取并验证 previous APP/DMG 的 package-owned `manifest_public_key`、production/enabled/exact signer/platform/helper hashes；没有 `.env` fallback。

## RED 到 GREEN

| 场景 | RED / 根因 | GREEN 证据 |
|---|---|---|
| capsule-before-journal / journal-before-capsule | orphan transaction dir 会让 scanner 永久 fail-fast | discovery/transaction crash-window tests 均通过；未知目录仍拒绝，受治理临时 capsule 可收敛 |
| readiness after-effect crash | parent rename 后、activation 前崩溃可让 Guard 因 stdin EOF 退出 | armed receipt 后立即监督；真实 after-effect crash E2E 恢复 old target |
| Guard 锁竞争 | 重复 E2E 捕获 `update transaction is busy` 导致 Guard 退出并停在 `backup_retained` | `Run` 对 exact transaction 的 `ErrTransactionBusy` 有界轮询；两类 Guard E2E 各连续 10 次通过 |
| PID reuse | start/path mismatch 曾作为 error 令 Guard 退出 | mismatch 返回 `(false,nil)`；PID reuse test 证明不 signal 复用 PID且 rollback |
| bundle rename | 仅 rename 后立即 kill 无法证明 Guard 未把 backup path 误判为死亡 | rename 后跨 6 个 100ms Guard poll 保持 `BackupRetained`，再 kill 后回滚 |
| publisher continuity | previous package 仍读 `Contents/Resources/.env` | previous APP/DMG fixture 写真实 `update-trust.json` 和 helper hashes；无 env fallback |

## 验证命令

| 命令 | Exit | 结果 |
|---|---:|---|
| `go test ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./cmd/super-dolphin-release-manifest ./internal/module/appupdate ./internal/platform/appupdaterecovery ./internal/platform/pidregistry ./internal/app -count=1` | 0 | 7 个 focused Go package 通过 |
| `go test ./internal/platform/appupdaterecovery -run 'TestRecoveryGuardProcessRestores(BackupRetainedAfterUpdaterCrash\|AfterRetainEffectCrash)' -count=10` | 0 | 两类真实 Guard crash E2E 共 20 次通过，53.152s |
| `go test ./internal/platform/appupdaterecovery -run 'TestIndependentArtifact(CrashRollbackReopensOldRelease\|HealthyACKObservationCommitsTrust)' -count=3` | 0 | 独立现场构建 old/new artifacts 的 rollback/reopen 与 healthy ACK/commit 各 3 次通过 |
| `go test -race ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./internal/module/appupdate ./internal/platform/appupdaterecovery ./internal/platform/pidregistry ./internal/app -count=1` | 0 | 相关 6 包 race 通过；Darwin linker 有非致命 `LC_DYSYMTAB` warning |
| `go test ./scripts -count=1` | 0 | 最终复跑通过，123.343s |
| `bash -n scripts/package_linux.sh scripts/package_macos.sh scripts/package_macos_github_release.sh scripts/publish_github_release.sh scripts/verify_packaged_app_macos.sh` | 0 | Bash 语法通过 |
| `make codemap-refresh project-map-refresh capcontract-refresh` | 0 | staged truth：codemap 385 files/1540 refs；project-map 4540 files/drift=OK；capcontract 41 packages |
| `make codemap-check project-map-check capcontract-check` | 0 | 三项 generated check 通过 |
| `git diff --check` | 0 | 通过；仅 Git 报告 Windows 文件未来 LF→CRLF 转换 warning |
| `bash .githooks/pre-commit`（最终人工复跑） | 0 | code guard；18 个后端/工具 package 与 scripts；33/33 changed Go LSP；三项 generator check；cached whitespace 全通过 |

## LSP 证据

- 强制链已执行：`grep`/`structure` 定位，`inspect` 理解，`xref` references/call hierarchy 影响分析，`file(read_file)` 精读，`patch_edit` 修改/format，`file(diagnostics)` 复核。
- 33 个 changed Go source/test files 批量 `file(diagnostics)`：0 diagnostics。
- 5 个 changed Bash files 以 `language_id=shellscript` 批量 `file(diagnostics)`：0 diagnostics。
- 4 个 changed PowerShell files：`file(diagnostics)` 返回 `language_unsupported`，当前 LSP toolchain 无 PowerShell adapter，不能宣称 LSP PASS；以 `go test ./scripts` 的 PowerShell package/guard fixtures 覆盖其 fail-closed 语义。
- `.env.packaging.example` 没有注册语言 adapter，不计作 source diagnostics PASS；publisher/packaging guard tests 检查其字段迁移。

## 门禁与残余风险

- pre-commit 首次运行 exit 1，真实捕获 `run()` CC=12、缺中文函数说明、`service_test.go` 超 800 行和裸 goroutine；拆分模式函数/测试文件并改为 `errgroup.Go` 后，最终人工复跑 exit 0。最终 commit 仍会再次执行 pre-commit 与中文 commit-msg。
- 前端未受影响，因此未运行 frontend lint/test/build。
- 已知残余限制：update capability 按设计只支持 `darwin-arm64`；真实 artifact/rename/process E2E 也只能在当前 Darwin arm64 内核执行。Linux、Windows、macOS amd64 由 fail-closed matrix 和 package/script guards 验证，不宣称现场 install E2E。
- 当前无已知未关闭 P0；PowerShell LSP adapter 缺失是工具证据缺口，不是静默 PASS。
