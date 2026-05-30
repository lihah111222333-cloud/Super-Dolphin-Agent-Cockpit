# A04: Relay Preflight Gating

**Goal:** packaged relay/bootstrap checks must not block non-packaged dev desktop startup, while real packaged startup and explicit app-managed Codex launch still fail fast.

**Files:**
- Modify: `internal/app/app.go`
- Modify: app-managed Codex launch/preflight path if separate from desktop startup
- Test: `internal/app/desktop_preflight_test.go`
- Test: provider/thread launch tests for app-managed Codex if desktop preflight no longer owns that check

**Boundary:**
- Desktop startup preflight validates packaged-only relay/bootstrap env only when `RuntimeMode=packaged`.
- Non-packaged dev desktop startup must ignore residual `SUPER_DOLPHIN_CODEX_RELAY_*` values, including partial relay env and privileged API key env.
- Explicit app-managed Codex provider launch is a separate boundary: if a user or packaged capability truly selects app-managed Codex, relay/bootstrap validation happens at that launch path and fails fast there.
- Dev/local Codex launch must not be converted into app-managed launch merely because relay env exists.

**Steps:**
- [ ] Write red test: non-packaged partial relay env does not fail desktop startup preflight.
- [ ] Write red test: non-packaged privileged relay API key env does not fail dev desktop startup preflight.
- [ ] Write red test: non-packaged dev desktop startup with explicit app-managed preference still starts; the later app-managed provider launch owns relay validation.
- [ ] Write red test: dev/local Codex launch ignores residual relay env and does not require packaged bootstrap.
- [ ] Write red test: explicit app-managed Codex launch with partial relay/bootstrap config fails fast at provider launch with an app-managed relay error.
- [ ] Write red test: explicit app-managed Codex launch with privileged relay API key fails fast at provider launch and does not suggest using that key for packaged bootstrap.
- [ ] Write red test: packaged sentinel with partial relay env fails fast during packaged desktop startup preflight.
- [ ] Write red test: packaged sentinel with privileged API key env fails fast during packaged desktop startup preflight.
- [ ] Gate desktop `ensurePackagedCodexBootstrap` by `RuntimeMode=packaged` only.
- [ ] Move or retain app-managed Codex relay checks at the actual app-managed launch boundary, not the generic dev desktop startup boundary.

**Validation:**
```bash
./scripts/test_with_guard.sh ./internal/app -run 'Test.*Preflight|Test.*Relay' -count=1
```
