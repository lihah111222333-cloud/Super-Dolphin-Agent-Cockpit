# A07: Provider Prefs Scope

**Goal:** active provider、provider 细节、Settings UI、toolbar/provider mode 使用同一 fallback 规则：explicit launch override > project non-empty > global non-empty > project tombstone clear > omit/backend default。

**Clear/tombstone semantics:** empty string, `null`, and missing values mean “absent” and must continue fallback to the next scope. An explicit clear is a project-scoped tombstone value, represented as JSON `{ "cleared": true }` for the same preference key. A tombstone stops fallback for that key and causes launch to omit the field so backend/provider defaults apply. Do not use empty string as a clear marker.

**Files:**
- Modify: `cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/pages/settings/ProviderSettings.ts`
- Modify: `cmd/agent-terminal/frontend/vue-app/composables/useProviderMode.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/stores/preferences.js` if cache/materialization must understand tombstones
- Modify: `internal/module/uistate/**` if backend prefs resolution participates
- Test: `cmd/agent-terminal/frontend/vue-app/thread-store-provider-preference.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/thread-store.actions.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/provider-settings.behavior.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/use-provider-mode.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/use-provider-mode.cross-page.test.js`
- Test: `internal/module/uistate/*_test.go` if backend prefs resolution participates

**Required red tests:**
- [ ] Explicit launch override wins over project/global prefs for active provider, model, effort, Codex identity, Claude home, and sandbox-relevant provider details.
- [ ] Global-only Codex prefs are used for provider details when project prefs are absent.
- [ ] Global-only Claude prefs are used for provider details when project prefs are absent, without materializing packaged Claude defaults into project scope.
- [ ] Project partial prefs do not erase global Codex home/provider details unless the project stores the tombstone for that exact key.
- [ ] Project partial prefs do not erase global Claude home/model/effort details unless the project stores the tombstone for that exact key.
- [ ] Project full override wins over global for Codex and Claude.
- [ ] Project tombstone clear stops fallback and omits that field from launch payload; it must not write an empty string that later falls back accidentally.
- [ ] Provider Settings displays effective values from the same fallback chain and writes tombstones when the user chooses clear/local CLI behavior.
- [ ] Toolbar/provider mode (`useProviderMode`) does not materialize a default `codex` project preference just because the project scope is empty; it should display the effective fallback/default without persisting it.

**Steps:**
- [ ] Implement one shared normalization for “missing”, “non-empty”, and “tombstone” preference values.
- [ ] Apply the same resolver in launch payload construction, Settings UI load/save, and toolbar/provider mode materialization.
- [ ] Keep invalid provider values fail-fast; do not silently fallback from an invalid non-empty project value to global.
- [ ] Preserve A06 behavior: UI-only defaults are display-only until explicitly saved or launched.

**Validation:**

Targeted vitest is only the red/green inner loop for this node:

```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  vue-app/thread-store-provider-preference.test.js \
  vue-app/thread-store.actions.test.js \
  vue-app/provider-settings.behavior.test.js \
  vue-app/use-provider-mode.test.js \
  vue-app/use-provider-mode.cross-page.test.js
```

Node completion requires the full frontend gate:

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

If `internal/module/uistate/**` is changed, also run the affected Go package gate:

```bash
./scripts/test_with_guard.sh ./internal/module/uistate -count=1
```
