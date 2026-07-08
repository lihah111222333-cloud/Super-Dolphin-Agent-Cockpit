# Settings Provider Save E2E Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a controlled dangerous-action E2E path that clicks the real Provider settings save button while strict mock Wails records only sandbox-scoped, sanitized preference writes.

**Architecture:** Extend the existing Agentic E2E strict Wails mock instead of adding a second backend stub. Add one goal-runner path for `settings-provider-save-mocked`, then extend the opt-in desktop-wide Playwright probe to exercise the same user-visible settings save path. Keep Model Provider Registry save as the next phase after this path is stable.

**Tech Stack:** React/Vite frontend, Playwright, Vitest, existing `agentic-e2e` runner, existing strict Wails mock, LSP diagnostics.

**Verification Surface:** `frontend-app/scripts/agentic-e2e.test.mjs`, `frontend-app/tests/e2e/desktop-wide.spec.js`, `frontend-app/scripts/agentic-e2e-wails-mock.mjs`, `frontend-app/scripts/agentic-e2e-goals.mjs`, `frontend-app/scripts/agentic-e2e-planner.mjs`, `frontend-app/scripts/agentic-e2e.mjs`, `frontend-app/src/pages/settings/components/ProviderSettingsPanels.jsx`.

---

## File Structure

- Modify: `frontend-app/scripts/agentic-e2e-wails-mock.mjs`
  - Add a strict `ui/preferences/set` handler.
  - Record sanitized Provider preference writes in `settingsWrites`.
  - Fail on non-whitelisted keys, missing sandbox cwd, path escapes, or unexpected payload shape.

- Modify: `frontend-app/scripts/agentic-e2e-goals.mjs`
  - Add `settings-provider-save-mocked`.
  - Define stable field targets and harmless sandbox-safe values.

- Modify: `frontend-app/scripts/agentic-e2e-planner.mjs`
  - Add planner logic for Provider settings save.
  - Normalize Provider settings facts.

- Modify: `frontend-app/scripts/agentic-e2e.mjs`
  - Collect Provider settings form facts.
  - Add `select` support to `performAction`.
  - Add readiness for Provider settings save notice.

- Modify: `frontend-app/src/pages/settings/components/ProviderSettingsPanels.jsx`
  - Add stable `data-testid` anchors for the Provider runtime card, save button, Model, Effort, Personality, Codex Home, Instance Key, Writable Roots, and Network Access checkbox.

- Modify: `frontend-app/tests/e2e/desktop-wide.spec.js`
  - Add a Provider settings save user-level probe.
  - Assert sanitized mock write evidence.

- Modify: `frontend-app/scripts/agentic-e2e.test.mjs`
  - Add RED/GREEN coverage for mock contract, goal definition, planner behavior, fact collection, action execution, and report redaction.

## Task 1: Lock the Provider Preference Write Mock Contract

**Files:**
- Modify: `frontend-app/scripts/agentic-e2e.test.mjs`
- Modify: `frontend-app/scripts/agentic-e2e-wails-mock.mjs`

- [ ] **Step 1: Write failing mock contract tests**

In `frontend-app/scripts/agentic-e2e.test.mjs`, inside `describe('agentic e2e strict Wails mock', () => { ... })`, add these tests after the existing project/file picker test:

```js
  it('records sandbox-scoped provider preference writes without leaking raw paths', async () => {
    const browser = await chromium.launch({ headless: true });
    try {
      const page = await browser.newPage();
      const sandbox = sandboxFixture('/tmp/agentic-e2e-preferences');
      await installAgenticE2EMockWails(page, { sandbox });
      await page.goto('data:text/html,<main>mock</main>');

      await expect(callMockWailsRPC(page, 'ui/preferences/set', {
        cwd: sandbox.projectDir,
        key: 'settings.provider.codex.codexHome',
        value: `${sandbox.homeDir}/.codex`,
      })).resolves.toEqual(expect.objectContaining({ result: { ok: true } }));
      await expect(callMockWailsRPC(page, 'ui/preferences/set', {
        cwd: sandbox.projectDir,
        key: 'settings.provider.codex.sandbox',
        value: { type: 'workspaceWrite', writableRoots: [sandbox.projectDir], networkAccess: false },
      })).resolves.toEqual(expect.objectContaining({ result: { ok: true } }));

      const state = await readAgenticE2EMockWailsState(page);
      expect(state.settingsWrites).toEqual([
        expect.objectContaining({
          method: 'ui/preferences/set',
          key: 'settings.provider.codex.codexHome',
          cwd: 'sandbox',
          valueType: 'path',
          path: 'sandbox',
        }),
        expect.objectContaining({
          method: 'ui/preferences/set',
          key: 'settings.provider.codex.sandbox',
          cwd: 'sandbox',
          valueType: 'object',
          sandboxPolicy: 'workspaceWrite',
          writableRoots: ['sandbox'],
          networkAccess: false,
        }),
      ]);
      expect(JSON.stringify(state.settingsWrites)).not.toContain(sandbox.rootDir);
      expect(() => assertAgenticE2EMockWailsClean(state)).not.toThrow();
    }
    finally {
      await browser.close();
    }
  });

  it('fails provider preference writes for non-whitelisted keys and out-of-sandbox paths', async () => {
    const browser = await chromium.launch({ headless: true });
    try {
      const page = await browser.newPage();
      const sandbox = sandboxFixture('/tmp/agentic-e2e-preference-guard');
      await installAgenticE2EMockWails(page, { sandbox });
      await page.goto('data:text/html,<main>mock</main>');

      const unsupported = await callMockWailsRPC(page, 'ui/preferences/set', {
        cwd: sandbox.projectDir,
        key: 'settings.provider.codex.secretKey',
        value: 'sk-live-secret',
      });
      expect(unsupported.error.message).toMatch(/unsupported settings preference key/);

      const escaped = await callMockWailsRPC(page, 'ui/preferences/set', {
        cwd: sandbox.projectDir,
        key: 'settings.provider.codex.codexHome',
        value: '/home/l4place/.codex',
      });
      expect(escaped.error.message).toMatch(/outside sandbox/);

      const unexpected = await callMockWailsRPC(page, 'ui/preferences/set', {
        cwd: sandbox.projectDir,
        key: 'settings.provider.codex.model',
        value: 'gpt-5',
        secret: 'sk-live-secret',
      });
      expect(unexpected.error.message).toMatch(/unsupported preference payload field/);

      const state = await readAgenticE2EMockWailsState(page);
      expect(state.failures.map((failure) => failure.method)).toEqual(['ui/preferences/set', 'ui/preferences/set', 'ui/preferences/set']);
      expect(() => assertAgenticE2EMockWailsClean(state)).toThrow(/ui\/preferences\/set/);
    }
    finally {
      await browser.close();
    }
  });
```

- [ ] **Step 2: Run focused tests and confirm RED**

Run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs -t "provider preference writes"
```

Expected: FAIL. The first failure should show `unhandled agentic e2e mock RPC: ui/preferences/set`.

- [ ] **Step 3: Add strict preference write handling**

In `frontend-app/scripts/agentic-e2e-wails-mock.mjs`, add this constant near the top of the `page.addInitScript` callback, after `const state = { ... };` or before `responseForRPC`:

```js
    const allowedProviderPreferenceKeys = new Set([
      'settings.provider.codex.personality',
      'settings.provider.codex.sandbox',
      'settings.provider.codex.model',
      'settings.provider.codex.effort',
      'settings.provider.codex.codexHome',
      'settings.provider.codex.codexInstanceKey',
    ]);
    const allowedPreferencePayloadFields = new Set(['cwd', 'key', 'value']);
```

In `responseForRPC`, immediately after the existing `ui/preferences/getAll` branch, add:

```js
      if (method === 'ui/preferences/set') return savePreference(params, method);
```

Then add these helper functions before `saveVideoApiKey`:

```js
    function savePreference(params = {}, method) {
      assertPreferencePayloadShape(params, method);
      const cwd = String(params.cwd || '');
      const key = String(params.key || '');
      if (!cwd) throw new Error(`${method} cwd is required`);
      assertSandboxPath(method, cwd);
      if (!allowedProviderPreferenceKeys.has(key)) throw new Error(`${method} unsupported settings preference key: ${key}`);
      if (!Object.prototype.hasOwnProperty.call(params, 'value')) throw new Error(`${method} value is required`);
      const summary = sanitizedPreferenceWrite(method, key, params.value);
      state.settingsWrites.push({
        method,
        key,
        cwd: 'sandbox',
        ...summary,
      });
      return { ok: true };
    }

    function assertPreferencePayloadShape(params, method) {
      for (const field of Object.keys(params || {})) {
        if (!allowedPreferencePayloadFields.has(field)) {
          throw new Error(`${method} unsupported preference payload field: ${field}`);
        }
      }
    }

    function sanitizedPreferenceWrite(method, key, value) {
      if (key === 'settings.provider.codex.codexHome') {
        assertSandboxPath(method, value);
        return { valueType: 'path', path: 'sandbox' };
      }
      if (key === 'settings.provider.codex.sandbox') return sanitizedSandboxPreference(method, value);
      if (key === 'settings.provider.codex.codexInstanceKey') return { valueType: 'string', value: sanitizedScalar(value) };
      if (key === 'settings.provider.codex.personality') return { valueType: 'string', value: sanitizedScalar(value) };
      if (key === 'settings.provider.codex.model') return { valueType: 'string', value: sanitizedScalar(value) };
      if (key === 'settings.provider.codex.effort') return { valueType: 'string', value: sanitizedScalar(value) };
      throw new Error(`${method} unsupported settings preference key: ${key}`);
    }

    function sanitizedSandboxPreference(method, value) {
      if (!value || typeof value !== 'object' || Array.isArray(value)) {
        throw new Error(`${method} sandbox preference must be an object`);
      }
      const type = String(value.type || '');
      if (!['workspaceWrite', 'readOnly', 'dangerFullAccess'].includes(type)) {
        throw new Error(`${method} unsupported sandbox policy: ${type}`);
      }
      const writableRoots = Array.isArray(value.writableRoots) ? value.writableRoots : [];
      const readableRoots = Array.isArray(value.readableRoots) ? value.readableRoots : [];
      for (const root of [...writableRoots, ...readableRoots]) assertSandboxPath(method, root);
      return {
        valueType: 'object',
        sandboxPolicy: type,
        writableRoots: writableRoots.map(() => 'sandbox'),
        readableRoots: readableRoots.map(() => 'sandbox'),
        networkAccess: Boolean(value.networkAccess),
        readOnlyMode: String(value.readOnlyMode || ''),
      };
    }

    function sanitizedScalar(value) {
      const text = String(value || '').trim();
      if (/sk-[a-z0-9_-]{8,}/iu.test(text)) throw new Error('secret-like preference value must not be recorded');
      return text;
    }
```

- [ ] **Step 4: Run focused tests and confirm GREEN**

Run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs -t "provider preference writes"
```

Expected: PASS for both new mock contract tests.

- [ ] **Step 5: Commit the mock contract**

Run:

```bash
git status --short
git add frontend-app/scripts/agentic-e2e.test.mjs frontend-app/scripts/agentic-e2e-wails-mock.mjs
git diff --cached --check
git commit -m "test: 约束设置偏好 mock 写入"
```

## Task 2: Add Stable Provider Settings UI Anchors

**Files:**
- Modify: `frontend-app/src/pages/settings/components/ProviderSettingsPanels.jsx`
- Modify: `frontend-app/scripts/agentic-e2e.test.mjs`

- [ ] **Step 1: Write failing source contract test**

In `frontend-app/scripts/agentic-e2e.test.mjs`, inside `describe('agentic e2e config', () => { ... })`, add:

```js
  it('exposes stable provider settings anchors for dangerous-action e2e', async () => {
    const source = await readFile(path.join(process.cwd(), 'src/pages/settings/components/ProviderSettingsPanels.jsx'), 'utf8');
    expect(source).toContain('data-testid="settings-provider-runtime-card"');
    expect(source).toContain('data-testid="settings-provider-save-button"');
    expect(source).toContain('data-testid="settings-provider-model"');
    expect(source).toContain('data-testid="settings-provider-effort"');
    expect(source).toContain('data-testid="settings-provider-personality"');
    expect(source).toContain('data-testid="settings-provider-codex-home"');
    expect(source).toContain('data-testid="settings-provider-instance-key"');
    expect(source).toContain('data-testid="settings-provider-network-access"');
    expect(source).toContain('data-testid="settings-provider-writable-roots"');
  });
```

- [ ] **Step 2: Run focused test and confirm RED**

Run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs -t "provider settings anchors"
```

Expected: FAIL because the Provider runtime card and several input anchors do not yet exist.

- [ ] **Step 3: Add the stable anchors**

In `frontend-app/src/pages/settings/components/ProviderSettingsPanels.jsx`, replace the Provider panel and relevant inputs with this shape:

```jsx
function ProviderSettingsPanel({ copy, runtime, viewConfig }) {
  const { changeActiveProvider, form, saveProviderSettings, updateForm } = runtime;
  return (
    <Panel title="PROVIDER">
      <div data-testid="settings-provider-runtime-card">
        <ProviderSettingsForm changeActiveProvider={changeActiveProvider} copy={copy} form={form} updateForm={updateForm} viewConfig={viewConfig} />
        <div className="settings-actions">
          <button className="btn btn-primary" type="button" data-testid="settings-provider-save-button" onClick={() => void saveProviderSettings()}>{copy.provider.saveSettings}</button>
        </div>
      </div>
    </Panel>
  );
}
```

In `ProviderSettingsForm`, update these controls:

```jsx
      <label>Provider Model<select aria-label="Provider Model" data-testid="settings-provider-model" value={form.providerModel} onChange={updateForm('providerModel')}>{modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Provider Effort<select aria-label="Provider Effort" data-testid="settings-provider-effort" value={form.providerEffort} onChange={updateForm('providerEffort')}>{effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Personality<select aria-label="Personality" data-testid="settings-provider-personality" value={form.personality} onChange={updateForm('personality')}>{personalityOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      {form.activeProvider === 'codex' ? <label>Codex Home<input aria-label="Codex Home" data-testid="settings-provider-codex-home" value={form.codexHome} onChange={updateForm('codexHome')} /></label> : null}
      {form.activeProvider === 'codex' ? <label>Instance Key<input aria-label="Instance Key" data-testid="settings-provider-instance-key" value={form.codexInstanceKey} onChange={updateForm('codexInstanceKey')} /></label> : null}
      {form.sandboxPolicy === 'workspaceWrite' ? <label className="checkbox-line"><input type="checkbox" data-testid="settings-provider-network-access" checked={form.networkAccess} onChange={updateForm('networkAccess')} /> Network Access</label> : null}
      {form.sandboxPolicy === 'workspaceWrite' ? <label className="wide">Writable Roots<textarea aria-label="Writable Roots" data-testid="settings-provider-writable-roots" value={form.writableRoots} onChange={updateForm('writableRoots')} /></label> : null}
```

Do not rename the existing `settings-provider-sandbox-card` or `provider-sandbox-save-button`; those belong to the separate Provider Properties card and existing tests depend on them.

- [ ] **Step 4: Run focused test and SettingsPage tests**

Run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs -t "provider settings anchors"
npm test -- src/pages/settings/SettingsPage.test.jsx
```

Expected: both commands PASS.

- [ ] **Step 5: Commit the UI anchors**

Run:

```bash
git status --short
git add frontend-app/scripts/agentic-e2e.test.mjs frontend-app/src/pages/settings/components/ProviderSettingsPanels.jsx
git diff --cached --check
git commit -m "test: 暴露设置保存 E2E 稳定锚点"
```

## Task 3: Add the Agentic Goal and Planner Path

**Files:**
- Modify: `frontend-app/scripts/agentic-e2e-goals.mjs`
- Modify: `frontend-app/scripts/agentic-e2e-planner.mjs`
- Modify: `frontend-app/scripts/agentic-e2e.mjs`
- Modify: `frontend-app/scripts/agentic-e2e.test.mjs`

- [ ] **Step 1: Write failing goal and planner tests**

In `frontend-app/scripts/agentic-e2e.test.mjs`, extend the stable goal candidates assertion to include:

```js
      'settings-provider-save-mocked',
```

Then add this planner test after the existing `saves the settings video key through the real settings form` test:

```js
  it('saves provider settings through the real settings form', () => {
    const providerGoal = {
      id: 'settings-provider-save-mocked',
      modelValue: 'gpt-5',
      effortValue: 'high',
      personalityValue: 'friendly',
      codexHomeValue: '/tmp/agentic-e2e/home/.codex',
      writableRootsValue: '/tmp/agentic-e2e/project',
    };

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsProviderSaveVisible: false,
    }, providerGoal)).toEqual(expect.objectContaining({
      type: 'fail',
      reason: expect.stringContaining('Provider settings save button is not visible'),
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsProviderSaveVisible: true,
      settingsProviderModelValue: 'gpt-5.5',
    }, providerGoal)).toEqual(expect.objectContaining({
      type: 'select',
      target: { type: 'testId', value: 'settings-provider-model' },
      value: 'gpt-5',
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsProviderSaveVisible: true,
      settingsProviderModelValue: 'gpt-5',
      settingsProviderEffortValue: 'xhigh',
    }, providerGoal)).toEqual(expect.objectContaining({
      type: 'select',
      target: { type: 'testId', value: 'settings-provider-effort' },
      value: 'high',
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsProviderSaveVisible: true,
      settingsProviderModelValue: 'gpt-5',
      settingsProviderEffortValue: 'high',
      settingsProviderPersonalityValue: 'pragmatic',
    }, providerGoal)).toEqual(expect.objectContaining({
      type: 'select',
      target: { type: 'testId', value: 'settings-provider-personality' },
      value: 'friendly',
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsProviderSaveVisible: true,
      settingsProviderModelValue: 'gpt-5',
      settingsProviderEffortValue: 'high',
      settingsProviderPersonalityValue: 'friendly',
      settingsProviderCodexHomeValue: '',
    }, providerGoal)).toEqual(expect.objectContaining({
      type: 'fill',
      target: { type: 'testId', value: 'settings-provider-codex-home' },
      value: '/tmp/agentic-e2e/home/.codex',
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsProviderSaveVisible: true,
      settingsProviderModelValue: 'gpt-5',
      settingsProviderEffortValue: 'high',
      settingsProviderPersonalityValue: 'friendly',
      settingsProviderCodexHomeValue: '/tmp/agentic-e2e/home/.codex',
      settingsProviderInstanceKeyValue: '',
    }, providerGoal)).toEqual(expect.objectContaining({
      type: 'fill',
      target: { type: 'testId', value: 'settings-provider-instance-key' },
      value: 'agentic-e2e',
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsProviderSaveVisible: true,
      settingsProviderModelValue: 'gpt-5',
      settingsProviderEffortValue: 'high',
      settingsProviderPersonalityValue: 'friendly',
      settingsProviderCodexHomeValue: '/tmp/agentic-e2e/home/.codex',
      settingsProviderInstanceKeyValue: 'agentic-e2e',
      settingsProviderWritableRootsValue: '',
    }, providerGoal)).toEqual(expect.objectContaining({
      type: 'fill',
      target: { type: 'testId', value: 'settings-provider-writable-roots' },
      value: '/tmp/agentic-e2e/project',
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsProviderSaveVisible: true,
      settingsProviderModelValue: 'gpt-5',
      settingsProviderEffortValue: 'high',
      settingsProviderPersonalityValue: 'friendly',
      settingsProviderCodexHomeValue: '/tmp/agentic-e2e/home/.codex',
      settingsProviderInstanceKeyValue: 'agentic-e2e',
      settingsProviderWritableRootsValue: '/tmp/agentic-e2e/project',
      mockWailsCallMethods: [],
    }, providerGoal)).toEqual(expect.objectContaining({
      type: 'click',
      target: { type: 'testId', value: 'settings-provider-save-button' },
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsProviderSaveVisible: true,
      mockWailsCallMethods: ['ui/preferences/set'],
    }, providerGoal)).toEqual(expect.objectContaining({
      type: 'done',
      reason: expect.stringContaining('settings-provider-save-mocked'),
    }));
  });
```

- [ ] **Step 2: Run focused test and confirm RED**

Run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs -t "provider settings|stable goal"
```

Expected: FAIL because the new goal is unsupported and facts are not normalized.

- [ ] **Step 3: Define the new goal**

In `frontend-app/scripts/agentic-e2e-goals.mjs`, add this entry after `settings-video-key-save-mocked`:

```js
  'settings-provider-save-mocked': Object.freeze({
    id: 'settings-provider-save-mocked',
    kind: 'settings-provider-save-mocked',
    targetRoute: '/settings',
    navigationTarget: appSidebarNav('设置'),
    modelTarget: Object.freeze({ type: 'testId', value: 'settings-provider-model' }),
    effortTarget: Object.freeze({ type: 'testId', value: 'settings-provider-effort' }),
    personalityTarget: Object.freeze({ type: 'testId', value: 'settings-provider-personality' }),
    codexHomeTarget: Object.freeze({ type: 'testId', value: 'settings-provider-codex-home' }),
    instanceKeyTarget: Object.freeze({ type: 'testId', value: 'settings-provider-instance-key' }),
    writableRootsTarget: Object.freeze({ type: 'testId', value: 'settings-provider-writable-roots' }),
    saveTarget: Object.freeze({ type: 'testId', value: 'settings-provider-save-button' }),
    modelValue: 'gpt-5',
    effortValue: 'high',
    personalityValue: 'friendly',
    codexHomeValue: 'AGENTIC_E2E_SANDBOX_HOME/.codex',
    instanceKeyValue: 'agentic-e2e',
    writableRootsValue: 'AGENTIC_E2E_SANDBOX_PROJECT',
    requiredRPCs: Object.freeze(['ui/preferences/set']),
    requiresMockWails: true,
    requiresSandbox: true,
  }),
```

Do not add this goal to `STABLE_GOAL_BY_LABEL`; normal discovery of the Settings nav should still suggest `settings-open`, not a mutating save action.

In the same file, update `normalizeGoal` so explicit value overrides survive the second normalization that happens inside `decideNextAction`:

```js
  const overrideFields = ['modelValue', 'effortValue', 'personalityValue', 'codexHomeValue', 'instanceKeyValue', 'writableRootsValue'];
  const goalFields = { ...definition };
  for (const field of overrideFields) {
    const value = normalizeString(rawGoal[field]);
    if (value) goalFields[field] = value;
  }
  return Object.freeze({
    ...goalFields,
    composerText,
  });
```

Replace the current `return Object.freeze({ ...definition, composerText });` with that block.

- [ ] **Step 4: Normalize Provider settings facts and add planner logic**

In `frontend-app/scripts/agentic-e2e-planner.mjs`, add the branch after the video key branch:

```js
  if (goal.kind === 'settings-provider-save-mocked') return decideSettingsProviderSaveMocked(facts, goal);
```

Extend `normalizeFacts` with:

```js
    settingsProviderSaveVisible: Boolean(facts.settingsProviderSaveVisible),
    settingsProviderModelValue: normalizeString(facts.settingsProviderModelValue),
    settingsProviderEffortValue: normalizeString(facts.settingsProviderEffortValue),
    settingsProviderPersonalityValue: normalizeString(facts.settingsProviderPersonalityValue),
    settingsProviderCodexHomeValue: normalizeString(facts.settingsProviderCodexHomeValue),
    settingsProviderInstanceKeyValue: normalizeString(facts.settingsProviderInstanceKeyValue),
    settingsProviderWritableRootsValue: normalizeString(facts.settingsProviderWritableRootsValue),
```

Add this function after `decideSettingsVideoKeySaveMocked`:

```js
function decideSettingsProviderSaveMocked(facts, goal) {
  if (hasObservedRPCs(facts, goal.requiredRPCs)) {
    return action('done', { reason: `${goal.id} observed mocked provider preference writes` });
  }
  if (!routeMatches(facts.url, goal.targetRoute) || !facts.settingsPageVisible) {
    return action('click', {
      target: goal.navigationTarget,
      expectRoute: goal.targetRoute,
      reason: `open ${goal.id} target route`,
    });
  }
  if (!facts.settingsProviderSaveVisible) {
    return action('fail', { reason: 'Provider settings save button is not visible' });
  }
  if (facts.settingsProviderModelValue !== goal.modelValue) {
    return action('select', {
      target: goal.modelTarget,
      value: goal.modelValue,
      reason: 'select a harmless Provider Model before saving provider settings',
    });
  }
  if (facts.settingsProviderEffortValue !== goal.effortValue) {
    return action('select', {
      target: goal.effortTarget,
      value: goal.effortValue,
      reason: 'select a harmless Provider Effort before saving provider settings',
    });
  }
  if (facts.settingsProviderPersonalityValue !== goal.personalityValue) {
    return action('select', {
      target: goal.personalityTarget,
      value: goal.personalityValue,
      reason: 'select a harmless Personality before saving provider settings',
    });
  }
  if (facts.settingsProviderCodexHomeValue !== goal.codexHomeValue) {
    return action('fill', {
      target: goal.codexHomeTarget,
      value: goal.codexHomeValue,
      reason: 'fill sandbox Codex Home before saving provider settings',
    });
  }
  if (facts.settingsProviderInstanceKeyValue !== goal.instanceKeyValue) {
    return action('fill', {
      target: goal.instanceKeyTarget,
      value: goal.instanceKeyValue,
      reason: 'fill harmless Codex instance key before saving provider settings',
    });
  }
  if (facts.settingsProviderWritableRootsValue !== goal.writableRootsValue) {
    return action('fill', {
      target: goal.writableRootsTarget,
      value: goal.writableRootsValue,
      reason: 'fill sandbox writable root before saving provider settings',
    });
  }
  return action('click', {
    target: goal.saveTarget,
    reason: 'click the real Provider settings save button while strict Wails mock records preference writes',
  });
}
```

- [ ] **Step 5: Resolve sandbox tokens in normalized goals**

In `frontend-app/scripts/agentic-e2e.mjs`, inside `agenticE2EConfig`, replace the current `runID`, `goal`, `outputBaseDir`, and `sandbox` initialization block with this block so the goal can receive sandbox-derived values:

```js
  const runID = normalizeRunID(env.SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID || new Date().toISOString());
  const sandbox = agenticE2ESandboxForRun(repoRoot, runID);
  const goal = goalWithSandboxValues(normalizeGoal({
    id: cli.goal || env.SUPER_DOLPHIN_AGENTIC_E2E_GOAL || DEFAULT_AGENTIC_GOAL.id,
    composerText: cli.composerText || env.SUPER_DOLPHIN_AGENTIC_E2E_COMPOSER_TEXT || DEFAULT_AGENTIC_GOAL.composerText,
  }), sandbox);
  const outputBaseDir = cli.outputDir || env.SUPER_DOLPHIN_AGENTIC_E2E_OUTPUT_DIR || path.join(repoRoot, '.tmp', 'agentic-e2e', runID);
```

The final function must contain only one `const sandbox = agenticE2ESandboxForRun(repoRoot, runID);`.

Add this helper near `agenticE2EConfig`:

```js
function goalWithSandboxValues(goal, sandbox) {
  if (goal.id !== 'settings-provider-save-mocked') return goal;
  return Object.freeze({
    ...goal,
    codexHomeValue: goal.codexHomeValue.replace('AGENTIC_E2E_SANDBOX_HOME', sandbox.homeDir),
    writableRootsValue: goal.writableRootsValue.replace('AGENTIC_E2E_SANDBOX_PROJECT', sandbox.projectDir),
  });
}
```

- [ ] **Step 6: Collect Provider settings facts and readiness**

In `collectPageFacts`, add inside the page `evaluate` return object:

```js
      settingsProviderSaveVisible: visibleByTestId('settings-provider-save-button'),
      settingsProviderModelValue: String(document.querySelector('[data-testid="settings-provider-model"]')?.value || ''),
      settingsProviderEffortValue: String(document.querySelector('[data-testid="settings-provider-effort"]')?.value || ''),
      settingsProviderPersonalityValue: String(document.querySelector('[data-testid="settings-provider-personality"]')?.value || ''),
      settingsProviderCodexHomeValue: String(document.querySelector('[data-testid="settings-provider-codex-home"]')?.value || ''),
      settingsProviderInstanceKeyValue: String(document.querySelector('[data-testid="settings-provider-instance-key"]')?.value || ''),
      settingsProviderWritableRootsValue: String(document.querySelector('[data-testid="settings-provider-writable-roots"]')?.value || ''),
```

Add the same default fields to the `.catch(() => ({ ... }))` object with `false` or `''`.

Add these fields to the final returned facts object:

```js
    settingsProviderSaveVisible: structuralFacts.settingsProviderSaveVisible,
    settingsProviderModelValue: structuralFacts.settingsProviderModelValue,
    settingsProviderEffortValue: structuralFacts.settingsProviderEffortValue,
    settingsProviderPersonalityValue: structuralFacts.settingsProviderPersonalityValue,
    settingsProviderCodexHomeValue: structuralFacts.settingsProviderCodexHomeValue,
    settingsProviderInstanceKeyValue: structuralFacts.settingsProviderInstanceKeyValue,
    settingsProviderWritableRootsValue: structuralFacts.settingsProviderWritableRootsValue,
```

In `performAction`, add this branch after the existing `fill` case:

```js
    case 'select':
      await resolveLocator(page, action.target).selectOption(action.value);
      return;
```

In `readinessForAction`, add:

```js
  if (action.target?.value === 'settings-provider-save-button') return { type: 'stableDOM' };
```

- [ ] **Step 7: Run focused tests and confirm GREEN**

Run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs -t "provider settings|stable goal|goal selection"
```

Expected: PASS.

- [ ] **Step 8: Commit the goal runner path**

Run:

```bash
git status --short
git add frontend-app/scripts/agentic-e2e-goals.mjs frontend-app/scripts/agentic-e2e-planner.mjs frontend-app/scripts/agentic-e2e.mjs frontend-app/scripts/agentic-e2e.test.mjs
git diff --cached --check
git commit -m "test: 新增设置 Provider 保存目标"
```

## Task 4: Add Desktop Wide Provider Save Coverage

**Files:**
- Modify: `frontend-app/tests/e2e/desktop-wide.spec.js`

- [ ] **Step 1: Write the desktop-wide Provider save probe**

In `frontend-app/tests/e2e/desktop-wide.spec.js`, extend the `risk-controlled read and settings probes stay inside mock Wails` test after the video key save assertion. Add:

```js
  const sandbox = testInfo._desktopWide.sandbox;
  await expectLocatorInViewport(page.getByTestId('settings-provider-runtime-card'));
  await expectCenterPointClickable(page.getByTestId('settings-provider-save-button'));
  await page.getByTestId('settings-provider-model').selectOption('gpt-5');
  await page.getByTestId('settings-provider-effort').selectOption('high');
  await page.getByTestId('settings-provider-personality').selectOption('friendly');
  await page.getByTestId('settings-provider-codex-home').fill(`${sandbox.homeDir}/.codex`);
  await page.getByTestId('settings-provider-instance-key').fill('desktop-wide-e2e');
  await page.getByTestId('settings-provider-writable-roots').fill(sandbox.projectDir);
  const networkAccess = page.getByTestId('settings-provider-network-access');
  if (await networkAccess.isChecked()) await networkAccess.uncheck();
  await page.getByTestId('settings-provider-save-button').click();
  await expect(page.locator('.settings-status')).toContainText('Provider');
```

Because this code uses `testInfo`, change the test signature from:

```js
test('risk-controlled read and settings probes stay inside mock Wails', async ({ page }) => {
```

to:

```js
test('risk-controlled read and settings probes stay inside mock Wails', async ({ page }, testInfo) => {
```

- [ ] **Step 2: Strengthen mock write assertions**

Replace the existing `mock.settingsWrites` assertion with:

```js
  expect(mock.settingsWrites).toEqual(expect.arrayContaining([
    expect.objectContaining({ method: 'ui/video/setApiKey', apiKeyLength: 'desktop-wide-video-key'.length }),
    expect.objectContaining({ method: 'ui/preferences/set', key: 'settings.provider.codex.model', value: 'gpt-5' }),
    expect.objectContaining({ method: 'ui/preferences/set', key: 'settings.provider.codex.effort', value: 'high' }),
    expect.objectContaining({ method: 'ui/preferences/set', key: 'settings.provider.codex.personality', value: 'friendly' }),
    expect.objectContaining({ method: 'ui/preferences/set', key: 'settings.provider.codex.codexHome', path: 'sandbox' }),
    expect.objectContaining({ method: 'ui/preferences/set', key: 'settings.provider.codex.codexInstanceKey', value: 'desktop-wide-e2e' }),
    expect.objectContaining({ method: 'ui/preferences/set', key: 'settings.provider.codex.sandbox', sandboxPolicy: 'workspaceWrite', writableRoots: ['sandbox'], networkAccess: false }),
  ]));
  expect(JSON.stringify(mock.settingsWrites)).not.toContain(sandbox.rootDir);
```

- [ ] **Step 3: Run desktop-wide E2E and confirm behavior**

Run:

```bash
cd frontend-app
npm run test:e2e:desktop-wide
```

Expected: `6 passed`. If the environment blocks localhost socket access, rerun in an environment that allows local Playwright webServer readiness checks and record that the first failure was environmental.

- [ ] **Step 4: Commit desktop-wide coverage**

Run:

```bash
git status --short
git add frontend-app/tests/e2e/desktop-wide.spec.js
git diff --cached --check
git commit -m "test: 覆盖设置 Provider 保存链路"
```

## Task 5: Verify the New Agentic Goal End to End

**Files:**
- Modify only if verification exposes a defect in the files changed by Tasks 1-4.

- [ ] **Step 1: Run the goal runner with strict mock Wails**

Start or reuse a Vite dev server on `127.0.0.1:5176`, then run:

```bash
cd frontend-app
SUPER_DOLPHIN_AGENTIC_E2E_BASE_URL=http://127.0.0.1:5176 \
SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID=settings-provider-save \
npm run agentic:e2e -- --mock-wails --goal=settings-provider-save-mocked
```

Expected: PASS. The final `result.json` should show success and `settingsWrites` should include only sanitized `ui/preferences/set` records.

- [ ] **Step 2: Inspect result evidence for redaction**

Run:

```bash
rg -n "/home/|sk-live|sk-test|apiKey|SUPER_DOLPHIN|provider credential" ../.tmp .tmp
```

Expected: no sensitive value from the Provider save path appears in the new goal output. Any matches from unrelated old `.tmp` reports should be ignored only after confirming their paths do not belong to `settings-provider-save`.

- [ ] **Step 3: Run the focused regression set**

Run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs -t "settings provider|provider preference|provider settings|stable goal|goal selection"
npm run test:e2e:desktop-wide
npm run test:e2e:business
git diff --check
```

Expected: all commands PASS.

- [ ] **Step 4: Run LSP diagnostics**

Run diagnostics for:

- `frontend-app/scripts/agentic-e2e-wails-mock.mjs`
- `frontend-app/scripts/agentic-e2e-goals.mjs`
- `frontend-app/scripts/agentic-e2e-planner.mjs`
- `frontend-app/scripts/agentic-e2e.mjs`
- `frontend-app/tests/e2e/desktop-wide.spec.js`
- `frontend-app/src/pages/settings/components/ProviderSettingsPanels.jsx`

Expected: no diagnostics of any severity.

- [ ] **Step 5: Commit verification-only fixes if needed**

If verification exposes a real defect, fix only the affected file and add a focused regression check. Commit with:

```bash
git add frontend-app/scripts/agentic-e2e-wails-mock.mjs frontend-app/scripts/agentic-e2e-goals.mjs frontend-app/scripts/agentic-e2e-planner.mjs frontend-app/scripts/agentic-e2e.mjs frontend-app/tests/e2e/desktop-wide.spec.js frontend-app/src/pages/settings/components/ProviderSettingsPanels.jsx frontend-app/scripts/agentic-e2e.test.mjs
git diff --cached --check
git commit -m "fix: 稳定设置 Provider 保存 E2E"
```

If no fix is needed, do not create a verification-only commit.

## Task 6: C Phase Gate for Model Provider Registry Save

**Files:**
- Modify: `docs/superpowers/specs/2026-07-08-settings-provider-save-e2e-design.md` only if implementation evidence changes the C entry criteria.
- Create a new plan after B is complete: `docs/plans/2026-07-08-model-provider-registry-save-e2e.md`.

- [ ] **Step 1: Check B completion evidence**

Confirm these facts before starting C:

```text
settings-provider-save-mocked passes under strict mock Wails
desktop-wide Provider save probe passes at 1440x900 and 1600x1000
ui/preferences/set reports are sanitized
unknown preference keys fail fast
path escapes fail fast
no real provider call is made
```

- [ ] **Step 2: Do not implement C in this plan**

Stop after B is complete. Create a separate design update or implementation plan for `modelProviders/save` so Provider runtime preference writes and Model Provider Registry writes remain reviewable as separate risk surfaces.
