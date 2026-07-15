# Settings Provider Save E2E Design

## Goal

Promote the current Agentic/DesktopUI E2E experiment from read-path and first-screen checks into a controlled dangerous-action test for settings saves.

The first target is Provider runtime preferences save. It should exercise the real settings UI, real form controls, and the real save button while strict Wails mocking prevents writes to the user's real configuration or provider environment.

After this is stable, the same safety model can expand to Model Provider Registry saves.

## Current Evidence

The repository already has a lower-risk settings save path:

- `settings-video-key-save-mocked` plans a fill and save action for `#settings-sf-key`.
- The strict mock handles `ui/video/setApiKey`.
- The mock records only `apiKeyLength`, not the secret value.
- `desktop-wide.spec.js` already clicks the video key save button inside mock Wails.

Provider runtime preferences are different:

- `ProviderSettingsPanel` renders real controls for model, effort, personality, Codex home, instance key, sandbox policy, writable roots, and readable roots.
- `saveProviderRuntimePreferences` writes through `setPreference`.
- The save path emits `settings.provider.codex.*` keys, including sandbox and Codex identity preferences.
- The current strict mock handles `ui/preferences/get`, but it does not yet allow or record `ui/preferences/set`.

That means clicking "Save Provider Settings" at E2E level should fail today unless the mock explicitly accepts and audits the write. This is the right boundary for a controlled dangerous action.

## B: Provider Runtime Preferences Save

Add a dedicated mocked E2E goal and Playwright coverage for Provider settings save.

The test should:

- Navigate to `/settings` through the user-visible settings entry.
- Wait for `settings-page` and the Provider panel to become usable.
- Change a small set of real controls:
  - Provider Model
  - Provider Effort
  - Personality
  - Sandbox Policy
  - Codex Home
  - Instance Key
  - Writable Roots or Readable Roots, depending on the chosen sandbox policy
- Click the real "Save Provider Settings" button.
- Assert a visible success status.
- Assert strict mock cleanliness.
- Assert every recorded preference write is expected, scoped, and sanitized.

The first scenario should use Codex plus a sandbox-safe identity:

- `codexHome`: `.tmp/agentic-e2e/sandbox/<run-id>/home/.codex`
- `codexInstanceKey`: a harmless test value, such as `agentic-e2e`
- roots: inside `.tmp/agentic-e2e/sandbox/<run-id>/project`

## Mock Contract

Extend the strict Wails mock with an explicit `ui/preferences/set` handler.

Allowed writes must be limited to a whitelist:

- `settings.provider.codex.personality`
- `settings.provider.codex.sandbox`
- `settings.provider.codex.model`
- `settings.provider.codex.effort`
- `settings.provider.codex.codexHome`
- `settings.provider.codex.codexInstanceKey`

The handler must fail fast when:

- `cwd` is missing.
- `cwd` is outside the test sandbox.
- the key is not in the whitelist.
- a path-valued field escapes the sandbox.
- an unexpected payload field appears.
- a secret-like value would be written to reports.

Recorded evidence should include:

- method
- key
- cwd classification, such as `sandbox`
- value type
- value summary for non-sensitive scalar values
- path classification for path-like values

Recorded evidence must not include:

- real user home paths
- real project paths
- complete API keys
- full raw payload dumps
- provider credentials

## Agentic Goal Runner Extension

Add a goal after the mock contract is safe:

- id: `settings-provider-save-mocked`
- kind: `settings-provider-save-mocked`
- target route: `/settings`
- navigation target: sidebar Settings
- action sequence:
  - fill/select Provider fields
  - click the Provider save button
  - stop only after mock write evidence and visible success status exist

The planner should not guess arbitrary form controls. It should only act on stable role names, `data-testid`, or explicit goal selectors. If a control is not visible, it should fail with a precise reason.

## Playwright Coverage

Add or extend deterministic Playwright coverage once the mock supports provider preference writes.

The preferred first home is the desktop-wide settings probe because it already validates desktop UI geometry and mock cleanliness. If the test grows too large, split it into a focused settings-save spec with its own Playwright config.

Assertions:

- settings page visible
- Provider settings controls visible and in viewport
- save button center-clickable
- success status visible
- mock writes equal the expected key set
- no unhandled RPCs
- no sandbox violations
- no console/page/request failures
- generated JSON report is sanitized

## C: Model Provider Registry Save

Only start this after Provider runtime preferences save is stable.

Model Provider Registry save should reuse the same safety model but treat `modelProviders/save` as a higher-risk mutation.

Entry criteria:

- `ui/preferences/set` mock and report redaction are already tested.
- The settings page has stable selectors for model provider registry controls.
- The test can modify a harmless in-memory registry without applying it to a live provider.
- `modelProviders/save` mock validates schema and strips secret-like fields from reports.

The first C scenario should save a fake vendor record with:

- fake vendor id
- fake base URL under an example domain
- fake model name
- fake API key marker that is never reported verbatim

It must not:

- click apply/use-provider actions
- trigger model validation against a real endpoint
- write real provider config
- start a chat turn

## Safety Rules

All dangerous settings tests must run with strict mock Wails.

Unknown RPCs fail. Missing mock telemetry fails. Sandbox path escapes fail. Redaction failures fail. The test must not silently downgrade to a read-only assertion when a mutation path is unavailable.

The suite remains opt-in during the experiment phase. It should not be added to CI, hooks, or broad smoke commands until the test is stable and the runtime cost is accepted.

## Verification Plan

After implementation, run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs -t "settings provider"
npm run test:e2e:desktop-wide
npm run test:e2e:business
git diff --check
```

Also run LSP diagnostics for changed frontend and script files.

## Non-Goals

This design does not add CI integration.

This design does not test real provider API calls.

This design does not write to the user's real `.codex` or project configuration.

This design does not make Model Provider Registry save part of the first implementation step. It defines the next step after Provider runtime preferences save is proven.
