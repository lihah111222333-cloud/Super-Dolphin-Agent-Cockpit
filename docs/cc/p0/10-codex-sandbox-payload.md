# A10: Codex Sandbox Payload

**Goal:** undefined sandbox 不应被前端默认覆盖为 `workspace-write`；已保存的 writable roots/network access/read-only access 必须按 canonical payload 下发，不得被静默丢弃。Sandbox defaults must not be used as a trigger for packaged relay/home behavior.

**Canonical payload:**
- Missing/`null`/empty sandbox preference returns `null`; caller omits `config.sandbox` entirely.
- Workspace write payload is `{ "mode": "workspace-write", "writable_roots": ["/abs/path"], "network_access": true|false }`.
- Read-only payload is `{ "mode": "read-only" }` for full read-only, or `{ "mode": "read-only", "access": { "type": "restricted", "readable_roots": ["/abs/path"], "include_platform_defaults": true } }` for restricted read-only.
- Danger-full-access payload is `{ "mode": "danger-full-access" }`.
- UI camelCase persisted fields may be accepted as input, but launch payload must normalize to the snake_case canonical shape above. If a UI-exposed field cannot be represented by the provider contract, fail the red test and fix the contract; do not silently drop the field.

**Files:**
- Modify: `cmd/agent-terminal/frontend/vue-app/stores/codex-sandbox-defaults.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/pages/settings/ProviderSettings.ts` if save/load shape must be aligned with canonical payload
- Test: `cmd/agent-terminal/frontend/vue-app/codex-sandbox-defaults.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/thread-store.actions.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/provider-settings.behavior.test.js` if Settings save/load shape changes

**Required red tests:**
- [ ] Undefined sandbox returns `null` and `thread/start` omits `config.sandbox`.
- [ ] Persisted workspace-write sandbox preserves `writableRoots`/`writable_roots` and `networkAccess`/`network_access` in canonical launch payload.
- [ ] Persisted restricted read-only sandbox preserves readable roots and `includePlatformDefaults`/`include_platform_defaults` in canonical launch payload.
- [ ] Danger-full-access and read-only modes normalize to canonical `mode` values, not shorthand objects.
- [ ] No packaged relay/home behavior changes when sandbox is missing, workspace-write, read-only, or danger-full-access.

**Steps:**
- [ ] Replace frontend workspace-write defaulting with `null`/omitted payload for missing prefs.
- [ ] Normalize persisted camelCase UI fields to the canonical snake_case launch payload.
- [ ] Keep absolute-path validation at UI boundaries for roots that the user enters.
- [ ] Add regression coverage proving roots/network fields are not dropped.

**Validation:**
```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  vue-app/codex-sandbox-defaults.test.js \
  vue-app/thread-store.actions.test.js \
  vue-app/provider-settings.behavior.test.js
```
