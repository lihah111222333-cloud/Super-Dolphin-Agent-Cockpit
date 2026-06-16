# A06: Frontend Provider Defaults

**Goal:** 前端缺省偏好时不注入 `super-dolphin-relay`、app-managed home、`codexInstanceKey` 或任何 packaged-only identity；Codex/Claude 的 packaged defaults 只由 A02 输出的 backend runtime capabilities contract 明确允许后才能展示或下发。前端不得自行根据路径、env、`.app` 形态或资源目录猜测 packaged/dev。

**Depends on:** A01, A02. A02 owns the backend runtime capabilities schema, field semantics, and API/source-of-truth. A06 is a consumer only; if the capability API or required field is missing, stop and hand off to A02 instead of defining a competing Vue-side contract.

**Files:**
- Modify: `cmd/agent-terminal/frontend/vue-app/provider-config-options.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js`
- Modify: `cmd/agent-terminal/frontend/vue-app/pages/settings/ProviderSettings.ts`
- Modify: `cmd/agent-terminal/frontend/vue-app/services/api.js` or the concrete frontend API wrapper used to read backend runtime capabilities
- Test: `cmd/agent-terminal/frontend/vue-app/provider-config-options.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/thread-store-codex-default-home.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/thread-store-provider-preference.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/thread-store.actions.test.js`
- Test: `cmd/agent-terminal/frontend/vue-app/provider-settings.behavior.test.js`

**Required red tests:**
- [ ] No Codex provider prefs produces a `thread/start` payload without `super-dolphin-relay`, `codexHome`, `codexInstanceKey`, or `codexModelProvider`.
- [ ] No Claude provider prefs produces a `thread/start` payload without `claudeHome` and without packaged-only model/effort defaults.
- [ ] Packaged-ready backend capabilities allow packaged Codex defaults to be displayed or forwarded; dev capabilities do not.
- [ ] Frontend code reads only the backend capability shape and has no path/env guessing branch for packaged defaults.
- [ ] Saving unrelated Provider Settings in dev does not persist Codex packaged defaults or Claude packaged defaults into project/global prefs.
- [ ] Settings page has a visible “use local CLI / clear packaged identity” path; clearing must use the A07 tombstone semantics, not an empty string that accidentally falls back or persists a packaged default.

**Steps:**
- [ ] Remove packaged identity values from unconditional frontend constants; model/effort dropdown display defaults may remain UI-only but must not be persisted or launched unless explicitly touched.
- [ ] Thread launch helper treats missing Codex/Claude prefs as omitted fields and lets backend/provider contracts decide.
- [ ] Provider Settings consumes A02/backend capabilities for packaged-only UI affordances; dev mode shows local CLI behavior and no packaged relay identity.
- [ ] Coordinate with A10 for sandbox payload shape; A06 must not reintroduce sandbox defaults while removing provider defaults.

**Validation:**
```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  vue-app/provider-config-options.test.js \
  vue-app/thread-store-codex-default-home.test.js \
  vue-app/thread-store-provider-preference.test.js \
  vue-app/thread-store.actions.test.js \
  vue-app/provider-settings.behavior.test.js
```
