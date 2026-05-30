DONE_WITH_CONCERNS — 六修把可在当前 macOS 环境执行的 release smoke 重新跑完并固化成脚本，但 P1 真实 release smoke 仍未闭环；当前不能合并，不能标记 release-qualified。

# t1-runtime-validation 六修报告（2026-05-28）

## 结论

- 已重新盘点并执行当前可跑的 macOS 本地 package / packaged app / DMG 结构 smoke。
- 已新增 `docs/scripts/macos_release_smoke.sh`，把本地 smoke、启动窗口 smoke、notarized DMG 检查、生产 relay turn 检查、clean/offline VM 前置检查固化为可复跑命令。
- 已落盘阻塞日志；未把未执行的 true clean VM、notarized DMG、生产 relay、完整 GUI Codex turn 写成通过。
- P1 仍是 release blocker：不能合并，不能 release-qualified。

## 已执行项

| 项 | 命令 / 动作 | 结果 | 日志 |
| --- | --- | --- | --- |
| RED：缺少 release smoke harness | `go test ./scripts -run TestMacOSReleaseSmokeScriptFailFastContracts -count=1 -v` | exit 1，缺 `../docs/scripts/macos_release_smoke.sh` | 终端输出 |
| GREEN：release smoke harness contract | `bash -n docs/scripts/macos_release_smoke.sh && go test ./scripts -run TestMacOSReleaseSmokeScriptFailFastContracts -count=1 -v` | exit 0 | 终端输出 |
| macOS package rebuild | `SUPER_DOLPHIN_SKIP_FRONTEND_BUILD=1 SUPER_DOLPHIN_POSTGRES_DIST=.build-cache/package-smoke/postgres-darwin-arm64 SUPER_DOLPHIN_CODEX_BIN=/Applications/Codex.app/Contents/Resources/codex SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=http://127.0.0.1:9/v1 SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=<placeholder> ./scripts/package_macos.sh` | exit 0 | `docs/reviews/smoke-logs/2026-05-28/macos-package-build-sixth.log` |
| 本地 app/DMG/relay env/Codex bundle smoke | `docs/scripts/macos_release_smoke.sh local` | exit 0；验证 app bundle、mounted DMG、`.env` relay keys、runtime manifest、bundled `codex app-server --help` 且隐藏 `/opt/homebrew/bin` / `/usr/local/bin` | `docs/reviews/smoke-logs/2026-05-28/macos-release-local-smoke-sixth.log` |
| packaged app 启动窗口 smoke | `STARTUP_WINDOW_SECONDS=20 docs/scripts/macos_release_smoke.sh startup` | exit 0；临时 `HOME`、sanitized `PATH`、blackhole `SUPER_DOLPHIN_CODEX_RELEASE_API_URL`，观察 20s 后终止 | `docs/reviews/smoke-logs/2026-05-28/macos-packaged-app-startup-sixth.log` |
| release blocker 前置条件扫描 | `docs/scripts/macos_release_smoke.sh blockers` | exit 1（预期阻塞）；当前不是 clean/offline VM、未安装到 `/Applications`、无生产 relay env/marker、无 GUI turn marker | `docs/reviews/smoke-logs/2026-05-28/macos-release-blockers-sixth.log` |
| notarized DMG 检查 | `docs/scripts/macos_release_smoke.sh notarized-dmg` | exit 65；当前 DMG 没有 stapled ticket，`NOTARY_PROFILE` 未设置 | `docs/reviews/smoke-logs/2026-05-28/macos-notarized-dmg-smoke-sixth.log` |
| production relay turn 检查 | `docs/scripts/macos_release_smoke.sh relay-turn` | exit 1；缺 `SUPER_DOLPHIN_CODEX_RELAY_BASE_URL` / production relay 前置条件 | `docs/reviews/smoke-logs/2026-05-28/macos-production-relay-turn-sixth.log` |

## 未执行项与阻塞原因

以下仍未真实执行，必须继续作为 P1 release blocker：

1. true macOS 断网 clean VM 安装/启动：当前环境 `kern.hv_vmm_present` 不是 1，存在 default route，未在 `/Applications/Super Dolphin.app` 中安装，且 clean VM marker 未设置；见 `macos-release-blockers-sixth.log`。
2. 从 notarized DMG 安装/启动：当前 DMG 为本地 ad-hoc package，`xcrun stapler validate` 报 “does not have a ticket stapled to it”，`NOTARY_PROFILE` 未设置；见 `macos-notarized-dmg-smoke-sixth.log`。
3. 生产 relay 下完整 Codex turn / relay 验收：未提供生产 relay base URL/API key，未设置 `SUPER_DOLPHIN_PRODUCTION_RELAY_SMOKE=1`；见 `macos-production-relay-turn-sixth.log` 与 `macos-release-blockers-sixth.log`。
4. clean VM 断网完整可用性验收：未在 clean/offline VM 中执行 GUI 创建会话、发送 Codex 消息、收到响应、重启后复验；见 `macos-release-blockers-sixth.log`。
5. 完整 GUI Codex turn：本轮只执行了 packaged app 启动窗口与 bundled Codex help；没有执行 GUI 内发送/接收 Codex response，未设置 `SUPER_DOLPHIN_GUI_CODEX_TURN_SMOKE=1`。

## 修改文件

- `docs/scripts/macos_release_smoke.sh`
- `scripts/package_macos_guard_test.go`
- `docs/packaging/embedded-postgres.md`
- `docs/packaging/macos-clean-vm-checklist.md`
- `docs/packaging/release-notes-2026-05-28.md`
- `docs/reviews/smoke-logs/2026-05-28/macos-package-build-sixth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-release-local-smoke-sixth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-packaged-app-startup-sixth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-release-blockers-sixth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-notarized-dmg-smoke-sixth.log`
- `docs/reviews/smoke-logs/2026-05-28/macos-production-relay-turn-sixth.log`
- `docs/reviews/t1-runtime-validation-sixth-fix-report-2026-05-28.md`

## 验证命令

```bash
bash -n docs/scripts/macos_release_smoke.sh && go test ./scripts -run TestMacOSReleaseSmokeScriptFailFastContracts -count=1 -v
```

结果：exit 0。

```bash
./scripts/test_with_guard.sh ./scripts -count=1
```

结果：exit 0；guard 通过，baseline 棘轮通过；macOS linker 有 deployment-target warning。

```bash
make guard
```

结果：exit 0；guard 通过，baseline 棘轮通过；macOS linker 有 deployment-target warning。

```bash
git diff --check
```

结果：exit 0。

## 最终判断

DONE_WITH_CONCERNS。可复跑的本机 smoke 已执行并落盘，但 P1 的真实 release smoke 仍存在不可执行项；当前不能合并，不能 release-qualified。未提交、未 push、未合并。
