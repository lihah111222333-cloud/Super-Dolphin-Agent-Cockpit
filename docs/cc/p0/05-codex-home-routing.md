# A05: Codex Home Routing

**Goal:** dev 下空 `codexHome` 使用本机 Codex CLI 默认 home/auth；只有 A02 判定为 packaged runtime，或用户显式选择允许的 app-managed home 时，才启用 app-managed Codex home。不得让 packaged relay/default identity 污染开发者本机 `~/.codex`。

**Depends on:** A01, A02. This node must consume A02/backend runtime mode or capabilities as the single source of truth; do not infer packaged/dev mode from paths, env leftovers, or `.app` shape locally.

**Files:**
- Modify: `internal/module/thread/lifecycle_helpers.go`
- Modify: `internal/provider/codexapp/**`
- Modify: `internal/provider/shared/**`
- Test: `internal/module/thread/codex_identity_start_test.go`
- Test: `internal/provider/codexapp/*home*_test.go`
- Test: `internal/provider/shared/*home*_test.go`

**Required red tests:**
- [ ] `internal/module/thread/codex_identity_start_test.go`: dev runtime + Codex provider + empty config does not inject `codexHome`, `codexInstanceKey`, or `codexModelProvider`, even when packaged-looking env/path leftovers exist.
- [ ] `internal/module/thread/codex_identity_start_test.go`: packaged runtime/capability completes missing Codex identity with app-managed home, `default` instance key, and `super-dolphin-relay` only when A02 says packaged is valid.
- [ ] `internal/module/thread/codex_identity_start_test.go`: explicit user Codex identity is preserved and never overwritten by packaged defaults.
- [ ] `internal/provider/codexapp/*home*_test.go`: dev empty raw home does not set `useAppManagedHome=true` and resolves to the user's Codex CLI home (`$HOME/.codex`) for provider launch.
- [ ] `internal/provider/codexapp/*home*_test.go`: packaged-ready capabilities may select app-managed Codex home.
- [ ] `internal/provider/codexapp/*home*_test.go`: explicit app-managed selection still works in the allowed mode.
- [ ] `internal/provider/shared/*home*_test.go`: invalid explicit home paths fail fast; empty home defaults to user CLI home, not app-managed home.

**Steps:**
- [ ] Thread-layer identity injection is mode-aware and delegated to A02/runtime capabilities; raw `SUPER_DOLPHIN_PACKAGED_CODEX_IDENTITY` or legacy env cannot by itself enable packaged defaults in dev.
- [ ] Provider-layer home selection is mode-aware and shares the same runtime decision as the thread layer.
- [ ] Preserve fail-fast for malformed or non-absolute explicit `codexHome` values.
- [ ] Ensure mirror targets for dev empty home use user provider roots and do not redirect personal mirrors into app-managed packaged roots.

**Validation:**
```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/provider/codexapp ./internal/provider/shared -count=1
```
