# A14: Verification Smoke Matrix

**Goal:** 汇总并运行本 DAG 的自动化验证、脚本检查、前端构建、packaged release smoke。A14 是 verification-only 节点，不要求新增红测；完成依据是可复查的验证证据。

**Files:**
- Read: all changed files from `git diff --name-only origin/main...HEAD`
- Modify: `docs/cc/p0/14-verification-smoke-matrix.md` execution notes

**Steps:**
- [ ] Run Go affected package guard for all internal packages touched by A03-A13.
- [ ] Run command package guard for `cmd/agent-terminal`, `cmd/mcp-orch`, `cmd/mcp-lsp`, and `cmd/mcp-ida`.
- [ ] Run scripts guard plus shell/PowerShell syntax checks for changed entrypoint, package, bundle, and verifier scripts.
- [ ] Run frontend completion gate: size guard, full vitest, and build. Targeted vitest from individual frontend nodes is only an inner-loop signal.
- [ ] For broad Go changes crossing `cmd/` and `internal/`, run `make test` and `make build-plain`.
- [ ] Run macOS package build, verify the built `.app`, verify the DMG/local package structure, and run fresh app-data packaged startup smoke. Any skipped macOS package/startup smoke is a hard failure for merge/release readiness.
- [ ] Run Linux package script plus `scripts/verify_packaged_app_linux.sh`, or record an explicit Linux out-of-scope gate with owner/date/reason/re-enable condition. Absence of both Linux verifier evidence and an out-of-scope gate is a hard failure.
- [ ] Record every command, exit code, and artifact/log path in the execution notes.

## Dev local-config pollution smoke

Purpose: prove dev/run-debug launches do not write packaged defaults into a developer's Codex or Claude CLI homes.

Setup and acceptance:
- [ ] Create an isolated temp `HOME` and temp `SUPER_DOLPHIN_HOME`; force A02 runtime mode/capabilities to `dev`, and clear packaged-only env leftovers such as `SUPER_DOLPHIN_PACKAGED_CODEX_IDENTITY` and `CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME` unless A02 provides a stricter dev sentinel.
- [ ] Seed `$HOME/.codex/config.toml` and `$HOME/.claude/settings.json` with sentinel comments/keys before launch.
- [ ] Codex empty prefs smoke: start a Codex thread with no project/global provider details. Assert launch/runtime config does not contain `super-dolphin-relay`, app-managed `$SUPER_DOLPHIN_HOME/providers/codex`, or frontend-injected `codexInstanceKey`; provider home resolves to local CLI behavior (`$HOME/.codex` or omitted until provider defaulting), not packaged app-managed home.
- [ ] Codex global/project prefs smoke: with explicit local CLI prefs in global scope and then project override, assert fallback/override follows A07 and never rewrites `$HOME/.codex/config.toml` with packaged relay defaults.
- [ ] Claude empty prefs smoke: start a Claude thread with no project/global provider details. Assert `claudeHome` is omitted and no app-managed `$SUPER_DOLPHIN_HOME/providers/claude` path is introduced.
- [ ] Claude global/project prefs smoke: with explicit local Claude prefs in global scope and then project override, assert fallback/override follows A07 and `$HOME/.claude/settings.json` sentinel remains intact.
- [ ] After all smoke launches, inspect `$HOME/.codex`, `$HOME/.claude`, `$HOME/.agents`, and `$SUPER_DOLPHIN_HOME/providers/*`; fail if packaged relay identity, packaged model/effort defaults, or app-managed provider-home paths were written during dev mode.

**Validation:**
```bash
./scripts/test_with_guard.sh ./internal/app ./internal/platform/config ./internal/platform/db ./internal/platform/embeddedpg ./internal/platform/runtimeenv ./internal/provider/codexapp ./internal/provider/shared ./internal/module/thread ./internal/module/uistate -count=1
./scripts/test_with_guard.sh ./cmd/agent-terminal ./cmd/mcp-orch ./cmd/mcp-lsp ./cmd/mcp-ida -count=1
./scripts/test_with_guard.sh ./scripts -count=1
bash -n run-debug.sh scripts/package_*.sh scripts/prepare_lsp_bundle_*.sh scripts/build_relocatable_postgres_macos.sh scripts/verify_packaged_app_macos.sh docs/scripts/macos_release_smoke.sh
pwsh -NoProfile -Command '$errors=$null; [System.Management.Automation.PSParser]::Tokenize((Get-Content -Raw run-debug.ps1), [ref]$errors) > $null; if ($errors) { $errors; exit 1 }'
if [ -f scripts/verify_packaged_app_linux.sh ]; then bash -n scripts/verify_packaged_app_linux.sh; else echo 'Linux verifier missing: record explicit out-of-scope gate or mark Not ready'; exit 1; fi
cd cmd/agent-terminal/frontend && node scripts/size-guard.cjs && npx vitest run && npm run build
make test
make build-plain
```

Release smoke evidence is a hard gate for merge/release readiness. The macOS path is mandatory because it protects the already-validated clean VM packaged startup path:

```bash
scripts/package_macos.sh

mac_app_name="${APP_NAME:-Super Dolphin}"
mac_app="dist/package/macos/${mac_app_name}.app"
mac_dmg="dist/package/macos/${mac_app_name}.dmg"
scripts/verify_packaged_app_macos.sh "$mac_app"
APP_PATH="$mac_app" DMG_PATH="$mac_dmg" docs/scripts/macos_release_smoke.sh local
APP_PATH="$mac_app" STARTUP_WINDOW_SECONDS="${STARTUP_WINDOW_SECONDS:-20}" docs/scripts/macos_release_smoke.sh startup

scripts/package_linux.sh
linux_app_name="${APP_NAME:-super-dolphin}"
linux_stage="dist/package/linux/${linux_app_name}-${VERSION:-0.1.0}-linux-$(go env GOARCH)"
scripts/verify_packaged_app_linux.sh "$linux_stage"
```

If Linux is out of this release scope, replace only the Linux package/verifier commands with an explicit A14 execution note containing `Linux release scope: out-of-scope`, owner, date, reason, and re-enable condition. A15 must then report Linux release readiness as Deferred/Not ready rather than silently passing it.
