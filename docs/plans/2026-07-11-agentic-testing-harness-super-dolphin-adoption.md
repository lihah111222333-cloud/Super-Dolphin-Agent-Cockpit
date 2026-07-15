# Agentic Testing Harness Super-Dolphin Adoption Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用独立 `agentic-testing-harness` 作为唯一通用 runner，把 Super-Dolphin 14 个 goals 与现有 business/desktop-wide coverage 迁移成产品 adapter 和 promoted scenarios，并在 parity 后删除旧 trial。

**Architecture:** Super-Dolphin 只拥有配置、产品 adapter、RPC contracts、observations、oracles 和 promoted scenarios；通用 CLI/SDK/runtime/isolation/mock 来自固定 Git 依赖。新旧套件先双跑并归一化结果，删除旧代码是最后一个独立可回滚提交。

**Tech Stack:** Super-Dolphin React/Vite frontend、Vitest、`agentic-testing-harness` Git package、`ath` CLI、Wails mock adapter、Docker hard isolation、现有 repo guards/LSP/codemap。

**Verification Surface:** `frontend-app/tests/agentic-harness`、Vite target identity、package scripts/lockfile、14 promoted scenarios、business/desktop-wide migrated oracles、parity reports、frontend lint/test/build、CI/Stop hooks。

---

## Preconditions

- 独立仓库 `/Users/l4place/Documents/agentic-testing-harness` 已通过 Foundation 和 Safety/Desktop 全部门槛。
- Git tag `v0.1.0-dogfood.1` 可从 `https://github.com/lihah111222333-cloud/agentic-testing-harness.git` 安装；如果远端或 tag 不存在，停止本计划，不改用未固定本地路径。
- 源 worktree `/Users/l4place/Documents/Super-Dolphin/.worktrees/agentic-e2e-harness` 必须先处理完现有未提交文件并保持 clean。随后从 `codex/agentic-e2e-harness` 创建分支 `codex/agentic-testing-harness-adoption` 和独立 worktree `/Users/l4place/Documents/Super-Dolphin/.worktrees/agentic-testing-harness-adoption`；本计划只在新 worktree 执行。

```bash
cd /Users/l4place/Documents/Super-Dolphin
git worktree add /Users/l4place/Documents/Super-Dolphin/.worktrees/agentic-testing-harness-adoption -b codex/agentic-testing-harness-adoption codex/agentic-e2e-harness
cd /Users/l4place/Documents/Super-Dolphin/.worktrees/agentic-testing-harness-adoption
```

### Task 1: Pin the dogfood package and expose v2 commands

**Files:**
- Modify: `frontend-app/package.json`
- Modify: `frontend-app/package-lock.json`
- Create: `frontend-app/scripts/agentic-harness-v2.mjs`
- Create: `frontend-app/scripts/agentic-harness-v2.test.mjs`

- [ ] **Step 1: Write a failing package-resolution test**

```js
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { describe, expect, it } from 'vitest';

const execFileAsync = promisify(execFile);

describe('agentic-testing-harness dependency', () => {
  it('runs the pinned ath doctor from frontend-app', async () => {
    const { stdout } = await execFileAsync('node', ['scripts/agentic-harness-v2.mjs', 'doctor']);
    expect(JSON.parse(stdout)).toMatchObject({ ok: true, data: { capabilities: { chromium: true } } });
  });
});
```

- [ ] **Step 2: Verify RED**

Run: `cd frontend-app && npm test -- --run scripts/agentic-harness-v2.test.mjs`

Expected: FAIL because package and wrapper do not exist.

- [ ] **Step 3: Install the exact Git tag and implement the wrapper**

```bash
cd frontend-app
npm install --save-dev github:lihah111222333-cloud/agentic-testing-harness#v0.1.0-dogfood.1
```

`agentic-harness-v2.mjs` resolves `node_modules/.bin/ath`, rejects a missing executable, uses `execFile`/`spawn` argument arrays, sets cwd to `frontend-app`, and forwards stdout/stderr without shell interpolation. Add scripts:

```json
{
  "agentic:e2e:v2:doctor": "node scripts/agentic-harness-v2.mjs doctor",
  "agentic:e2e:v2:matrix": "node scripts/agentic-harness-matrix-v2.mjs"
}
```

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- --run scripts/agentic-harness-v2.test.mjs
npm run agentic:e2e:v2:doctor
git add package.json package-lock.json scripts/agentic-harness-v2.mjs scripts/agentic-harness-v2.test.mjs
git diff --cached --check
git commit -m "build: 接入固定版本 agentic testing harness"
```

### Task 2: Add target identity and product configuration

**Files:**
- Modify: `frontend-app/vite.config.js`
- Create: `frontend-app/tests/agentic-harness/agentic-testing-harness.config.ts`
- Create: `frontend-app/tests/agentic-harness/adapter.ts`
- Create: `frontend-app/tests/agentic-harness/identity.ts`
- Create: `frontend-app/tests/agentic-harness/adapter.test.ts`

- [ ] **Step 1: Write a failing identity test**

```ts
it('rejects a Vite server whose nonce does not match the session', async () => {
  const target = await launchSuperDolphinTarget({ expectedNonce: 'session-a', serverNonce: 'session-b' });
  await expect(target.verifyIdentity()).rejects.toMatchObject({ code: 'TARGET_ERROR' });
  await target.shutdown();
});
```

- [ ] **Step 2: Verify RED**

Run: `cd frontend-app && npm test -- --run tests/agentic-harness/adapter.test.ts`

Expected: FAIL because product adapter and nonce response do not exist.

- [ ] **Step 3: Implement explicit Vite identity**

Add a Vite development-only plugin activated only when `ATH_TARGET_NONCE` is non-empty. It sets exact headers `x-agentic-testing-harness-nonce`, `x-agentic-testing-harness-root`, and `x-agentic-testing-harness-commit`; missing values fail target startup. Product config launches `npm run dev -- --host 127.0.0.1 --port $ATH_TARGET_PORT --strictPort`, never reuses an existing server, and verifies all three values before opening the app.

Define targets `super-dolphin-read` with light isolation and `super-dolphin-wails-mock-write` with Docker hard isolation plus the generic Wails mock adapter.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- --run tests/agentic-harness/adapter.test.ts
npm run lint
git add vite.config.js tests/agentic-harness/agentic-testing-harness.config.ts tests/agentic-harness/adapter.ts tests/agentic-harness/identity.ts tests/agentic-harness/adapter.test.ts
git diff --cached --check
git commit -m "test: 增加 Super-Dolphin target 身份适配"
```

### Task 3: Port strict product RPC contracts and fixtures

**Files:**
- Create: `frontend-app/tests/agentic-harness/rpc-contracts.ts`
- Create: `frontend-app/tests/agentic-harness/rpc-fixtures.ts`
- Create: `frontend-app/tests/agentic-harness/rpc-contracts.test.ts`

- [ ] **Step 1: Write failing RPC contract tests**

```ts
it('rejects unknown RPCs and non-whitelisted preference keys', () => {
  expect(() => resolveRPC('thread/delete-all', {})).toThrow(/unknown RPC/);
  expect(() => resolveRPC('ui/preferences/set', { key: 'provider.real.apiKey', value: 'secret' }))
    .toThrow(/preference key is not allowed/);
});

it('requires the exact six provider preference writes', () => {
  expect(providerSaveOracle.requiredCalls).toEqual([
    'settings.provider.codex.personality',
    'settings.provider.codex.sandbox',
    'settings.provider.codex.model',
    'settings.provider.codex.effort',
    'settings.provider.codex.codexHome',
    'settings.provider.codex.codexInstanceKey',
  ]);
});
```

- [ ] **Step 2: Verify RED**

Run: `cd frontend-app && npm test -- --run tests/agentic-harness/rpc-contracts.test.ts`

Expected: FAIL because product contracts do not exist.

- [ ] **Step 3: Port product behavior without copying the runner**

Translate RPC schemas and response fixtures from `frontend-app/scripts/agentic-e2e-wails-mock.mjs`. Keep all Super-Dolphin method names in this product file. Every mutating RPC declares allowed parameter schema, evidence redaction fields, side-effect summary, and exact scenario allowlist. Unknown methods, unknown fields, unexpected mutation, missing required call, and mock absence fail.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- --run tests/agentic-harness/rpc-contracts.test.ts
npm run typecheck:contracts
git add tests/agentic-harness/rpc-contracts.ts tests/agentic-harness/rpc-fixtures.ts tests/agentic-harness/rpc-contracts.test.ts
git diff --cached --check
git commit -m "test: 迁移 Super-Dolphin 严格 RPC 契约"
```

### Task 4: Define product observations, action risks, and oracles

**Files:**
- Create: `frontend-app/tests/agentic-harness/observations.ts`
- Create: `frontend-app/tests/agentic-harness/actions.ts`
- Create: `frontend-app/tests/agentic-harness/oracles.ts`
- Create: `frontend-app/tests/agentic-harness/observations.test.ts`
- Create: `frontend-app/tests/agentic-harness/oracles.test.ts`
- Modify: `frontend-app/tests/agentic-harness/adapter.ts`

- [ ] **Step 1: Write failing visibility and oracle tests**

```ts
it('does not expose hidden product anchors as visible facts', () => {
  const facts = productObservationFromElements([
    { testId: 'settings-page', visible: false, name: 'Settings' },
  ]);
  expect(facts.settingsPageVisible).toBe(false);
});

it('classifies all form and submit actions as write', () => {
  expect(classifyProductAction({ type: 'fill', target: testId('settings-provider-model') })).toBe('write');
  expect(classifyProductAction({ type: 'click', target: testId('settings-provider-save-button') })).toBe('write');
});

it('fails provider save when any exact preference call is missing', () => {
  expect(evaluateProviderSaveOracle(fiveOfSixCalls)).toMatchObject({ passed: false });
});
```

- [ ] **Step 2: Verify RED**

Run: `cd frontend-app && npm test -- --run tests/agentic-harness/observations.test.ts tests/agentic-harness/oracles.test.ts`

Expected: FAIL because product observation and oracle functions do not exist.

- [ ] **Step 3: Port facts as product extensions**

Translate relevant selectors and facts from `frontend-app/scripts/agentic-e2e.mjs`, but derive visibility only from generic runtime elements already marked visible. Product observations may record non-sensitive enum/text values; API keys and token fields expose only `empty`, `present`, and `length`. Define exact action identities for navigation anchors, log query, composer, project menu, upload, settings forms, and save buttons. Only the seven pure navigation/query actions below are read; every other action is write.

Oracles evaluate URL, visible stable anchors, exact RPC multiset, attachment count, sandbox file snapshot, preference writes, and absence of page/console/network/mock errors.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- --run tests/agentic-harness/observations.test.ts tests/agentic-harness/oracles.test.ts
npm run lint
git add tests/agentic-harness/observations.ts tests/agentic-harness/actions.ts tests/agentic-harness/oracles.ts tests/agentic-harness/observations.test.ts tests/agentic-harness/oracles.test.ts tests/agentic-harness/adapter.ts
git diff --cached --check
git commit -m "test: 定义 Super-Dolphin 观察动作与裁决"
```

### Task 5: Promote the seven read-only navigation scenarios

**Files:**
- Create: `frontend-app/tests/agentic-harness/promoted/observability-latest-logs.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/plugins-skills-open.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/automation-open.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/prompts-open.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/shared-files-open.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/memory-open.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/settings-open.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/read-scenarios.test.ts`

- [ ] **Step 1: Write the failing scenario inventory test**

```ts
it('loads exactly seven read scenarios with only read-classified actions', async () => {
  const scenarios = await loadPromotedScenarios('tests/agentic-harness/promoted');
  const read = scenarios.filter((scenario) => scenario.mode === 'read');
  expect(read.map((scenario) => scenario.id).sort()).toEqual([
    'automation-open', 'memory-open', 'observability-latest-logs',
    'plugins-skills-open', 'prompts-open', 'settings-open', 'shared-files-open',
  ]);
  expect(read.flatMap((scenario) => scenario.actions).every(isReadClassified)).toBe(true);
});
```

- [ ] **Step 2: Verify RED**

Run: `cd frontend-app && npm test -- --run tests/agentic-harness/promoted/read-scenarios.test.ts`

Expected: FAIL because promoted YAML files do not exist.

- [ ] **Step 3: Encode semantic actions and explicit oracles**

Each file sets `schemaVersion: "1"`, a unique ID, `target: super-dolphin-read`, `mode: read`, semantic locator actions, and URL/visible-element oracles. `observability-latest-logs` may invoke only the adapter-declared read-only log query action. CSS is allowed only for an anchor that has no role/label/testId and must remain strict.

- [ ] **Step 4: Replay all read scenarios and commit**

```bash
npm test -- --run tests/agentic-harness/promoted/read-scenarios.test.ts
node scripts/agentic-harness-matrix-v2.mjs --mode read
git add tests/agentic-harness/promoted
git diff --cached --check
git commit -m "test: 迁移七个只读产品场景"
```

Expected: seven scenarios return `replay_passed`; target root/commit/nonce match this worktree.

### Task 6: Promote the seven hard-isolated write scenarios

**Files:**
- Create: `frontend-app/tests/agentic-harness/promoted/frontend-navigation-probe.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/chat-composer.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/chat-send-mocked.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/project-add-sandbox.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/file-attach-sandbox.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/settings-video-key-save-mocked.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/settings-provider-save-mocked.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/write-scenarios.test.ts`

- [ ] **Step 1: Write the failing write inventory and oracle tests**

```ts
it('requires hard isolation for every write scenario', async () => {
  const scenarios = await loadWriteScenarios();
  expect(scenarios).toHaveLength(7);
  expect(scenarios.every((scenario) => scenario.mode === 'write' && scenario.isolation === 'docker')).toBe(true);
});

it('provider save requires six exact calls rather than one method match', async () => {
  const scenario = await loadScenario('settings-provider-save-mocked');
  expect(scenario.oracles.exactRPCCalls['ui/preferences/set']).toHaveLength(6);
});
```

- [ ] **Step 2: Verify RED**

Run: `cd frontend-app && npm test -- --run tests/agentic-harness/promoted/write-scenarios.test.ts`

Expected: FAIL because write scenarios do not exist.

- [ ] **Step 3: Encode write scenarios without raw secrets**

All seven scenarios use `target: super-dolphin-wails-mock-write`, `mode: write`, and `isolation: docker`. Composer and settings values are injected as sensitive runtime inputs, not stored in argv, YAML, reports, or candidates. Chat send requires the exact mocked send RPC and rendered user turn. Project/file scenarios require exact sandbox snapshots. Video key save requires the expected RPC plus success notice. Provider save requires the six exact preference keys, values summarized by schema, stable final UI, and zero unexpected mutations.

- [ ] **Step 4: Replay all write scenarios and scan evidence**

```bash
npm test -- --run tests/agentic-harness/promoted/write-scenarios.test.ts
ATH_RUN_DOCKER_TESTS=1 node scripts/agentic-harness-matrix-v2.mjs --mode write
if rg -n "agentic-e2e-video-key|audit-secret-marker" ../.tmp/agentic-testing-harness; then exit 1; fi
```

Expected: seven scenarios return `replay_passed`; evidence scan returns no matches.

- [ ] **Step 5: Commit write scenarios**

```bash
git add tests/agentic-harness/promoted
git diff --cached --check
git commit -m "test: 迁移七个硬隔离写场景"
```

### Task 7: Migrate business-flow and desktop-wide coverage

**Files:**
- Create: `frontend-app/tests/agentic-harness/promoted/business-read-surfaces.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/business-chat-send.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/desktop-workbench-regions.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/desktop-business-pages.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/desktop-controlled-probes.yaml`
- Create: `frontend-app/tests/agentic-harness/promoted/desktop-business.test.ts`
- Modify: `frontend-app/tests/agentic-harness/oracles.ts`

- [ ] **Step 1: Write a failing migrated-coverage test**

```ts
it('preserves the two business flows and six desktop viewport cases', async () => {
  const matrix = await loadBusinessDesktopMatrix();
  expect(matrix.business.map((item) => item.id)).toEqual([
    'business-read-surfaces', 'business-chat-send',
  ]);
  expect(matrix.desktop.flatMap((item) => item.viewports)).toHaveLength(6);
  expect(matrix.desktop.every((item) => item.viewports.map((v) => v.width).join(',') === '1440,1600')).toBe(true);
});
```

- [ ] **Step 2: Verify RED**

Run: `cd frontend-app && npm test -- --run tests/agentic-harness/promoted/desktop-business.test.ts`

Expected: FAIL because migrated scenarios do not exist.

- [ ] **Step 3: Translate behavior and geometry oracles**

Port the two tests from `tests/e2e/business-flows.spec.js` and three tests × two viewports from `tests/e2e/desktop-wide.spec.js`. Preserve viewport sizes 1440×900 and 1600×1000. Geometry oracles require critical regions within viewport bounds and click targets reachable at their center point. Business chat send retains exact bridge call and rendered-turn oracles. All cases use the shared product RPC contracts; no scenario embeds a second mock.

- [ ] **Step 4: Replay migrated cases and commit**

```bash
npm test -- --run tests/agentic-harness/promoted/desktop-business.test.ts
ATH_RUN_DOCKER_TESTS=1 node scripts/agentic-harness-matrix-v2.mjs --suite business-desktop
git add tests/agentic-harness/promoted tests/agentic-harness/oracles.ts
git diff --cached --check
git commit -m "test: 迁移业务流与桌面宽屏覆盖"
```

Expected: two business cases and six desktop viewport cases return `replay_passed`.

### Task 8: Build old/new parity and matrix reporting

**Files:**
- Create: `frontend-app/scripts/agentic-harness-matrix-v2.mjs`
- Create: `frontend-app/scripts/agentic-harness-parity.mjs`
- Create: `frontend-app/scripts/agentic-harness-parity.test.mjs`
- Create: `frontend-app/tests/agentic-harness/parity-map.ts`
- Modify: `frontend-app/package.json`

- [ ] **Step 1: Write a failing parity test**

```js
it('requires one-to-one parity for all fourteen legacy goals', async () => {
  const report = compareHarnessResults(legacyFourteenResults, v2FourteenResults);
  expect(report.summary).toEqual({ expected: 14, matched: 14, missing: 0, divergent: 0 });
});

it('fails when either harness tested a different source identity', () => {
  expect(() => compareHarnessResults(
    [{ goal: 'settings-open', commit: 'a' }],
    [{ goal: 'settings-open', commit: 'b' }],
  )).toThrow(/target identity mismatch/);
});
```

- [ ] **Step 2: Verify RED**

Run: `cd frontend-app && npm test -- --run scripts/agentic-harness-parity.test.mjs`

Expected: FAIL because parity tools do not exist.

- [ ] **Step 3: Implement deterministic matrix and normalized comparison**

Matrix v2 reads promoted files, starts a fresh target/isolation session for each scenario, writes one bounded result directory, and exits non-zero on any non-`replay_passed` result. Parity maps legacy goal IDs to v2 scenario IDs, compares success state, target root/commit, final route, required RPC multiset, sandbox snapshot, and oracle result. It writes `.tmp/agentic-harness-parity/report.json` and `report.md`; it does not treat missing legacy evidence as a match.

- [ ] **Step 4: Run 14-goal parity three times**

```bash
npm test -- --run scripts/agentic-harness-parity.test.mjs
node scripts/agentic-harness-parity.mjs --runs 3
```

Expected: each run reports `{ expected: 14, matched: 14, missing: 0, divergent: 0 }`; v2 promotion receipts use three distinct isolation IDs.

- [ ] **Step 5: Commit parity tooling**

```bash
git add scripts/agentic-harness-matrix-v2.mjs scripts/agentic-harness-parity.mjs scripts/agentic-harness-parity.test.mjs tests/agentic-harness/parity-map.ts package.json package-lock.json
git diff --cached --check
git commit -m "test: 建立新旧 harness 场景对照"
```

### Task 9: Promote the v2 gate in CI and Codex hooks

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `.codex/hooks.json`
- Modify: `frontend-app/package.json`
- Create: `frontend-app/scripts/assert-agentic-target-root.mjs`
- Create: `frontend-app/scripts/assert-agentic-target-root.test.mjs`

- [ ] **Step 1: Write a failing worktree-root guard test**

```js
it('fails when hook cwd and expected target root differ', () => {
  expect(() => assertTargetRoot({
    hookRoot: '/repo/parent', targetRoot: '/repo/.worktrees/agentic',
  })).toThrow(/worktree root mismatch/);
});
```

- [ ] **Step 2: Verify RED**

Run: `cd frontend-app && npm test -- --run scripts/assert-agentic-target-root.test.mjs`

Expected: FAIL because root guard does not exist.

- [ ] **Step 3: Add explicit provisioning and scoped gates**

CI installs the Git dependency, runs `npx playwright install --with-deps chromium`, then runs the v2 14-scenario matrix in a Linux Docker-capable job and uploads only redacted failure artifacts. Stop/SubagentStop hooks pass an explicit task/worktree root to the guard and fail before tests if it differs from target identity. Keep the legacy suite available but non-authoritative until Task 10.

- [ ] **Step 4: Run the promoted gate locally**

```bash
npm test -- --run scripts/assert-agentic-target-root.test.mjs
npm run lint
npm test
npm run build
npm run agentic:e2e:v2:matrix
```

Expected: all commands exit 0 and the matrix reports 14/14.

- [ ] **Step 5: Commit gate promotion**

```bash
git add ../.github/workflows/ci.yml ../.codex/hooks.json package.json package-lock.json scripts/assert-agentic-target-root.mjs scripts/assert-agentic-target-root.test.mjs
git diff --cached --check
git commit -m "ci: 启用 agentic testing harness 产品门禁"
```

### Task 10: Remove the legacy trial in one reversible commit

**Files:**
- Delete: `frontend-app/scripts/agentic-e2e.mjs`
- Delete: `frontend-app/scripts/agentic-e2e-discovery.mjs`
- Delete: `frontend-app/scripts/agentic-e2e-goals.mjs`
- Delete: `frontend-app/scripts/agentic-e2e-planner.mjs`
- Delete: `frontend-app/scripts/agentic-e2e-reporter.mjs`
- Delete: `frontend-app/scripts/agentic-e2e-sandbox.mjs`
- Delete: `frontend-app/scripts/agentic-e2e-wails-mock.mjs`
- Delete: `frontend-app/scripts/agentic-e2e.test.mjs`
- Delete: `frontend-app/playwright.business-flows.config.js`
- Delete: `frontend-app/playwright.desktop-wide.config.js`
- Delete: `frontend-app/tests/e2e/business-flows.spec.js`
- Delete: `frontend-app/tests/e2e/desktop-wide.spec.js`
- Modify: `frontend-app/package.json`
- Modify: `frontend-app/package-lock.json`
- Modify: `README.md`
- Generated: `docs/doc/codemap/**` via `make codemap-refresh`

- [ ] **Step 1: Prove replacement coverage before deletion**

```bash
cd frontend-app
node scripts/agentic-harness-parity.mjs --runs 3
npm run agentic:e2e:v2:matrix
```

Expected: parity is 14/14 for three runs; two business and six desktop viewport cases pass.

- [ ] **Step 2: Delete only replaced implementation and update scripts/docs**

Remove legacy `agentic:e2e`, business Playwright, and desktop-wide Playwright package scripts. Preserve unrelated `playwright.desktop.config.js` and desktop smoke suites. Update README to point to `frontend-app/tests/agentic-harness` and the independent project, then run `make codemap-refresh`; do not hand-edit generated codemap content.

- [ ] **Step 3: Verify no stale imports or duplicate mocks remain**

```bash
if rg -n "agentic-e2e-(planner|reporter|sandbox|wails-mock)|playwright\.business-flows|playwright\.desktop-wide" frontend-app; then exit 1; fi
rg -n "agentic-testing-harness|agentic:e2e:v2" frontend-app/package.json frontend-app/tests/agentic-harness README.md
```

Expected: first command returns no matches; second finds the v2 integration.

- [ ] **Step 4: Run final repository verification**

```bash
npm run lint
npm test
npm run build
npm run agentic:e2e:v2:matrix
cd ..
make guard
git diff --check
```

Expected: all commands exit 0; LSP diagnostics for changed JS/TS/TSX files report no Error, Warning, Information, or Hint.

- [ ] **Step 5: Commit the reversible deletion**

```bash
git add -u frontend-app README.md docs/doc/codemap
git add frontend-app/package.json frontend-app/package-lock.json
git diff --cached --check
git commit -m "refactor: 移除已替代的 agentic e2e trial"
```

Do not squash this deletion into earlier migration commits. Rollback is `git revert` of this commit only.
