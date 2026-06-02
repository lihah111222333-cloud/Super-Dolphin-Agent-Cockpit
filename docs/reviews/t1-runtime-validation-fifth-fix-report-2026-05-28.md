# t1-runtime-validation 五修报告（2026-05-28）

## 结论

不应标记为 release-qualified，当前仍不应合并到 `package-embedded-pg`。五修已修复 P2-1 / P2-2 的可复核性与 fail-fast 问题；P1 release smoke 仍按 blocker 处理，因为本轮没有执行 true macOS 断网 clean VM、notarized DMG、生产 relay 或完整 Codex turn 验收。

## P1：release smoke 未闭环

处理选择：B，正式保持 release blocker，不把未执行 smoke 写成通过。

- 已更新 `docs/packaging/release-notes-2026-05-28.md`，补充五修说明：P2 已修，但不关闭 release-smoke blocker。
- 未执行 / 未声称通过：
  - true macOS 断网 clean VM 安装与启动；
  - 从 notarized DMG 安装并启动；
  - 生产 relay 下完整 Codex turn / relay 验收；
  - clean VM 下断网后的完整可用性验收。

## P2-1：Embedded PostgreSQL packaged smoke 证据不可复核

已固化为可提交测试：

- 新增 `internal/platform/embeddedpg/packaged_smoke_test.go`。
- committed smoke 使用 `SUPER_DOLPHIN_PACKAGED_POSTGRES_ROOT` 指向 packaged `postgres/<goos-goarch>` runtime，实际启动 packaged PostgreSQL、执行 `SELECT 1`、重复 `Start`、检查 `0700` 目录权限并 `Stop`。
- 重新生成 `docs/reviews/smoke-logs/2026-05-28/embedded-postgres-packaged-smoke.log`，日志现在指向 committed test source，而不是临时 `TestPackagedPostgresSmokeTmp`。

RED：

```bash
go test ./internal/platform/embeddedpg -run TestPackagedPostgresSmokeCommandIsDocumented -count=1 -v
```

结果：exit 1，旧 smoke log 仍引用临时测试名，未引用 committed `TestPackagedPostgresSmoke`。

GREEN / smoke：

```bash
SUPER_DOLPHIN_PACKAGED_POSTGRES_ROOT="$PWD/dist/package/macos/Super Dolphin.app/Contents/Resources/postgres/$(go env GOOS)-$(go env GOARCH)" go test ./internal/platform/embeddedpg -run '^TestPackagedPostgresSmoke$' -count=1 -v
```

结果：exit 0，日志落盘到 `docs/reviews/smoke-logs/2026-05-28/embedded-postgres-packaged-smoke.log`。

## P2-2：Packaged relay env 缺失时静默跳过 Codex bootstrap

已实现 packaged runtime fail-fast：

- `runDesktopPreflight` 先执行 `.env` priming，并把解析出的 `projectRoot` 传给 Codex bootstrap 检查。
- 当 `projectRoot/runtime-manifest.json` 存在、且 `SUPER_DOLPHIN_CODEX_RELAY_BASE_URL` 与 `SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN` 都缺失时，启动前置检查直接返回错误，错误信息指向 packaged resources `.env` 和两个必需变量。
- 非 packaged/dev runtime 仍允许 relay 未配置时跳过 desktop preflight bootstrap；部分配置仍保持原有 fail-fast。
- 补充隔离 preflight 测试的 relay env，避免外部 env 或 `-shuffle` 顺序导致 `.env` 加载测试污染后续测试。
- `docs/packaging/embedded-postgres.md` 已补充 packaged runtime 缺少 relay config 会阻断启动。

RED：

```bash
go test ./internal/app -run TestRunDesktopPreflightFailsFastInPackagedRuntimeWhenRelayUnset -count=1 -v
```

结果：exit 1，旧行为返回 nil，证明 packaged relay 缺失会静默跳过。

GREEN：

```bash
go test ./internal/app -run TestRunDesktopPreflightFailsFastInPackagedRuntimeWhenRelayUnset -count=1 -v
```

结果：exit 0。

代码审查后补充 RED：

```bash
SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.test/v1 SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=test-key go test ./internal/app -run '^TestRunDesktopPreflightDoesNotEnsureCodexCLIAvailable$' -count=1 -v
```

结果：exit 1，外部 relay env 会让本应验证“unset skip”的测试走 bootstrap 分支。

补充 GREEN：

```bash
SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=https://relay.example.test/v1 SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=test-key go test ./internal/app -run '^TestRunDesktopPreflightDoesNotEnsureCodexCLIAvailable$' -count=1 -v
go test ./internal/app -run 'TestRunDesktopPreflight' -shuffle=on -count=1 -v
```

结果：均 exit 0。

## 修改文件

- `internal/app/app.go`
- `internal/app/desktop_preflight_test.go`
- `internal/platform/embeddedpg/packaged_smoke_test.go`
- `docs/packaging/embedded-postgres.md`
- `docs/packaging/release-notes-2026-05-28.md`
- `docs/reviews/smoke-logs/2026-05-28/embedded-postgres-packaged-smoke.log`
- `docs/reviews/t1-runtime-validation-fifth-fix-report-2026-05-28.md`

## 验证结果

范围限定代码审查：第一次审查发现 1 个 Important（preflight 测试 env 隔离不足），已按上文补充 RED/GREEN 修复；复查未发现新的 Critical/Important。

```bash
go test ./internal/app -count=1
```

结果：exit 0。macOS linker emitted deployment-target warnings, but tests passed.

```bash
go test ./internal/app -run 'TestRunDesktopPreflight' -shuffle=on -count=1 -v
```

结果：exit 0。

```bash
go test ./internal/platform/embeddedpg -count=1
```

结果：exit 0。

```bash
go test ./scripts -count=1
```

结果：exit 0。

```bash
./scripts/test_with_guard.sh ./internal/app ./internal/platform/embeddedpg ./scripts -count=1
```

结果：exit 0，代码守卫通过，baseline 棘轮通过。

```bash
make guard
```

结果：exit 0，代码守卫通过，baseline 棘轮通过。

## 仍然未执行的 release smoke

以下仍是 P1 blocker，不能在当前证据下改写为通过：

- true macOS 断网 clean VM 安装/启动；
- notarized DMG 验收；
- 生产 relay 下完整 Codex turn / relay 验收；
- clean VM 断网后的完整可用性验收。

未提交、未 push。
