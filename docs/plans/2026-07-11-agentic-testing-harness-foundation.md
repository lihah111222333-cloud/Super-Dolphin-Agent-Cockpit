# Agentic Testing Harness Foundation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在独立仓库交付一个可安装的只读 Web harness，Codex Agent 可通过长连接 `ath session stream` JSONL 协议完成观察、受控动作、证据记录和 candidate replay。

**Architecture:** Contracts 提供唯一协议真值；Core 维护 session、revision、policy、budget 和 ledger；SDK 在单进程内装配 light isolation、Web adapter 和 Playwright runtime；CLI 保持 runtime handle 并把 SDK 能力映射为 JSONL；Skill 只驱动 CLI。Foundation 不实现写 session、Docker、Electron 或 Wails mock。

**Tech Stack:** Node.js 20.19.0+、22.13.0+ 或 24+、npm workspaces、TypeScript 5.9、Vitest 4、Playwright 1.61、TypeBox、YAML、Jiti、ESLint。

**Verification Surface:** `packages/contracts`、`packages/core`、`packages/isolation`、`packages/runtime-playwright`、`packages/adapter-web`、`packages/sdk`、`packages/cli`、Web fixture、Codex Skill、fresh-install package smoke。

---

## Execution context

执行目录固定为 `/Users/l4place/Documents/agentic-testing-harness`。开始前创建独立 Git 仓库和 `codex/foundation` 分支；不得在 Super-Dolphin worktree 内创建这些 package。

### Task 1: Initialize the independent workspace

**Files:**
- Create: `/Users/l4place/Documents/agentic-testing-harness/package.json`
- Create: `/Users/l4place/Documents/agentic-testing-harness/package-lock.json`
- Create: `/Users/l4place/Documents/agentic-testing-harness/tsconfig.json`
- Create: `/Users/l4place/Documents/agentic-testing-harness/tsconfig.base.json`
- Create: `/Users/l4place/Documents/agentic-testing-harness/vitest.config.ts`
- Create: `/Users/l4place/Documents/agentic-testing-harness/eslint.config.js`
- Create: `/Users/l4place/Documents/agentic-testing-harness/.gitignore`
- Create: `/Users/l4place/Documents/agentic-testing-harness/README.md`

- [ ] **Step 1: Create and enter the repository**

```bash
mkdir -p /Users/l4place/Documents/agentic-testing-harness
cd /Users/l4place/Documents/agentic-testing-harness
test ! -e .git
git init -b main
git switch -c codex/foundation
```

Expected: `git branch --show-current` prints `codex/foundation`.

- [ ] **Step 2: Write the root workspace manifest**

```json
{
  "name": "agentic-testing-harness-workspace",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "workspaces": ["packages/*", "examples/*"],
  "engines": { "node": "^20.19.0 || ^22.13.0 || >=24" },
  "scripts": {
    "build": "tsc -b",
    "typecheck": "tsc -b --pretty false",
    "lint": "eslint .",
    "test": "vitest run",
    "test:unit": "vitest run packages",
    "test:e2e:web": "vitest run tests/e2e/web-session.test.ts",
    "verify": "npm run lint && npm run typecheck && npm test -- --passWithNoTests && npm run build"
  },
  "devDependencies": {
    "@eslint/js": "^10.0.1",
    "@types/node": "^22.0.0",
    "eslint": "^10.0.1",
    "globals": "^17.6.0",
    "typescript": "^5.9.3",
    "typescript-eslint": "^8.57.0",
    "vitest": "^4.1.8"
  }
}
```

Create `tsconfig.base.json` with `target: ES2022`, `module` and `moduleResolution: NodeNext`, `strict: true`, `declaration: true`, `composite: true`, `noUncheckedIndexedAccess: true`, and `exactOptionalPropertyTypes: true`. Configure Vitest to include `packages/**/*.test.ts` and `tests/**/*.test.ts`. Configure ESLint for Node ESM TypeScript and ignore `dist`, `runs`, and fixture build output. README must state Node.js 20.19.0+、22.13.0+ 或 24+，与 ESLint 10 的真实 engine 范围一致，不得笼统声明支持 Node 21、Node 23 或较早的 Node 20/22。

Create the root solution entry point required by the fixed `tsc -b` scripts:

```json
{
  "extends": "./tsconfig.base.json",
  "include": ["vitest.config.ts"],
  "compilerOptions": {
    "noEmit": true,
    "tsBuildInfoFile": "./.tmp/tsconfig.tsbuildinfo"
  }
}
```

This bootstrap config gives `tsc -b` one real TypeScript input without adding fake product source. Task 2 replaces it with a solution config after the first package project exists; later package tasks append project references without changing the build command.

- [ ] **Step 3: Install and prove the empty workspace is valid**

```bash
npm install
npm run lint
npm run typecheck
npm test -- --passWithNoTests
```

Expected: all commands exit 0 and `package-lock.json` is created.

- [ ] **Step 4: Commit the workspace boundary**

```bash
git add package.json package-lock.json tsconfig.json tsconfig.base.json vitest.config.ts eslint.config.js .gitignore README.md
git diff --cached --check
git commit -m "chore: 初始化 agentic testing harness 工作区"
```

### Task 2: Define versioned contracts

**Files:**
- Create: `packages/contracts/package.json`
- Create: `packages/contracts/tsconfig.json`
- Create: `packages/contracts/src/errors.ts`
- Create: `packages/contracts/src/actions.ts`
- Create: `packages/contracts/src/protocol.ts`
- Create: `packages/contracts/src/session.ts`
- Create: `packages/contracts/src/isolation.ts`
- Create: `packages/contracts/src/adapters.ts`
- Create: `packages/contracts/src/candidate.ts`
- Create: `packages/contracts/src/index.ts`
- Modify: `tsconfig.json`
- Test: `packages/contracts/src/protocol.test.ts`

- [ ] **Step 1: Write failing protocol tests**

```ts
import { Value } from '@sinclair/typebox/value';
import { describe, expect, it } from 'vitest';
import { SessionRequestSchema } from './protocol.js';

describe('SessionRequestSchema', () => {
  it('accepts observe and revision-bound act requests', () => {
    expect(Value.Check(SessionRequestSchema, {
      schemaVersion: '1', requestId: 'r1', type: 'observe',
    })).toBe(true);
    expect(Value.Check(SessionRequestSchema, {
      schemaVersion: '1', requestId: 'r2', type: 'act', revision: 3,
      data: { action: { type: 'click', target: { kind: 'role', role: 'button', name: 'Save' } } },
    })).toBe(true);
  });

  it('rejects unknown request types and missing revisions', () => {
    expect(Value.Check(SessionRequestSchema, {
      schemaVersion: '1', requestId: 'r3', type: 'executeJavascript',
    })).toBe(false);
    expect(Value.Check(SessionRequestSchema, {
      schemaVersion: '1', requestId: 'r4', type: 'act', data: { action: { type: 'click' } },
    })).toBe(false);
  });
});
```

- [ ] **Step 2: Run the test to verify RED**

Run: `npm test -- packages/contracts/src/protocol.test.ts`

Expected: FAIL because `SessionRequestSchema` and the package do not exist.

- [ ] **Step 3: Implement the contract package**

Use TypeBox as the single source for runtime schema and TypeScript types. Define these exact discriminants:

```ts
export type ErrorCode =
  | 'CONFIG_ERROR' | 'CONTRACT_ERROR' | 'POLICY_BLOCKED'
  | 'ISOLATION_ERROR' | 'TARGET_ERROR' | 'ACTION_ERROR'
  | 'ORACLE_FAILED' | 'INFRASTRUCTURE_ERROR';

export type SessionMode = 'read' | 'write';
export type SessionRequestType = 'observe' | 'act' | 'status' | 'finish';
export type SessionResultStatus =
  | 'explored' | 'candidate' | 'replay_passed'
  | 'product_failed' | 'policy_blocked' | 'infrastructure_failed';
```

`ActionSchema` contains only `navigate`, `click`, `fill`, `select`, `check`, `press`, `upload`, `window`, `waitFor`, and `finish`. Locator schemas support `role`, `testId`, `label`, and explicit `css`; every locator sets `strict: true`. `TargetAdapter` exposes the approved lifecycle including `classifyAction`.

The response envelope requires `schemaVersion`, `requestId`, `sessionId`, `revision`, `ok`, `data`, and `evidenceRefs` on success; failure replaces `data` with `{ code, message, details }`.

`isolation.ts` defines `IsolationProvider`, `IsolationAttestation`, and both `LightIsolationReceipt` and `HardIsolationReceipt`. It includes a `VMIsolationProvider` interface but no VM implementation. Pin `@sinclair/typebox@^0.34.41` in the contracts package.

After `packages/contracts/tsconfig.json` exists, replace the bootstrap root config with:

```json
{
  "files": [],
  "references": [
    { "path": "./packages/contracts" }
  ]
}
```

- [ ] **Step 4: Run contract tests and typecheck**

```bash
npm test -- packages/contracts/src/protocol.test.ts
npm run typecheck
```

Expected: tests PASS and TypeScript exits 0.

- [ ] **Step 5: Commit contracts**

```bash
git add packages/contracts package.json package-lock.json tsconfig.json
git diff --cached --check
git commit -m "feat: 定义 harness 版本化能力契约"
```

### Task 3: Implement the Core session state machine and policy

**Files:**
- Create: `packages/core/package.json`
- Create: `packages/core/tsconfig.json`
- Create: `packages/core/src/session-machine.ts`
- Create: `packages/core/src/policy.ts`
- Create: `packages/core/src/budget.ts`
- Create: `packages/core/src/index.ts`
- Test: `packages/core/src/session-machine.test.ts`
- Test: `packages/core/src/policy.test.ts`

- [ ] **Step 1: Write failing state and policy tests**

```ts
it('allows only one revision-bound action at a time', () => {
  const machine = SessionMachine.createReadSession('s1');
  machine.markReady();
  machine.recordObservation({ revision: 1 });
  expect(machine.beginAction(1).phase).toBe('authorized');
  expect(() => machine.beginAction(1)).toThrow(/action already in flight/);
  machine.completeAction({ nextRevision: 2 });
  expect(() => machine.beginAction(1)).toThrow(/stale observation revision/);
});

it('blocks unclassified clicks in read mode', () => {
  expect(authorizeAction({ mode: 'read', risk: 'unclassified', actionType: 'click' }))
    .toEqual({ allowed: false, code: 'POLICY_BLOCKED' });
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/core/src/session-machine.test.ts packages/core/src/policy.test.ts`

Expected: FAIL because Core symbols do not exist.

- [ ] **Step 3: Implement minimum state transitions**

Implement phases `provisioning`, `ready`, `observed`, `authorized`, `executed`, `finished`, and `failed`. The machine owns the monotonic revision and rejects concurrent or stale actions. `BudgetTracker` enforces `maxActions`, `maxDurationMs`, `maxNetworkEvents`, and `maxEvidenceBytes` with explicit error codes.

Policy rules are exact:

```ts
if (mode === 'write' && !hardAttestationVerified) return blocked('ISOLATION_ERROR');
if (mode === 'read' && risk !== 'read') return blocked('POLICY_BLOCKED');
if (budget.exhausted) return blocked('POLICY_BLOCKED');
return { allowed: true };
```

No label, role name, Agent reason, or CSS selector may lower risk.

- [ ] **Step 4: Verify GREEN**

```bash
npm test -- packages/core/src/session-machine.test.ts packages/core/src/policy.test.ts
npm run typecheck
```

Expected: both files PASS and typecheck exits 0.

- [ ] **Step 5: Commit Core state and policy**

```bash
git add packages/core package.json package-lock.json
git diff --cached --check
git commit -m "feat: 实现 harness 会话状态与策略门禁"
```

### Task 4: Add a bounded evidence ledger and candidate model

**Files:**
- Create: `packages/core/src/bounded-buffer.ts`
- Create: `packages/core/src/evidence-ledger.ts`
- Create: `packages/core/src/redactor.ts`
- Create: `packages/core/src/run-writer.ts`
- Create: `packages/core/src/candidate.ts`
- Modify: `packages/core/src/index.ts`
- Test: `packages/core/src/evidence-ledger.test.ts`
- Test: `packages/core/src/run-writer.test.ts`
- Test: `packages/core/src/candidate.test.ts`

- [ ] **Step 1: Write failing ledger tests**

```ts
it('chains receipts and reports dropped events', () => {
  const ledger = new EvidenceLedger({ maxEvents: 2, maxBytes: 4096 });
  ledger.append({ type: 'observation', revision: 1 });
  ledger.append({ type: 'action', revision: 1 });
  ledger.append({ type: 'observation', revision: 2 });
  expect(ledger.snapshot().droppedCount).toBe(1);
  expect(ledger.verifyChain()).toEqual({ valid: true });
});

it('does not promote an oracle-free trace', () => {
  expect(buildCandidate({ actions: [], oracles: [] }).status).toBe('explored');
});

it('redacts sensitive fields before writing the run layout', async () => {
  const writer = await RunWriter.create(tempRunRoot, { sensitiveFields: ['apiKey'] });
  await writer.writeResult({ apiKey: 'foundation-secret-marker', status: 'explored' });
  expect(await readFile(writer.resultPath, 'utf8')).not.toContain('foundation-secret-marker');
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/core/src/evidence-ledger.test.ts packages/core/src/candidate.test.ts packages/core/src/run-writer.test.ts`

Expected: FAIL because ledger and candidate builder do not exist.

- [ ] **Step 3: Implement ledger and candidate normalization**

Use canonical UTF-8 JSON with sorted object keys for receipt hashing. Each receipt stores `previousHash` and `hash`; the first receipt uses 64 zeroes. Candidate actions retain semantic locators, expected revision deltas, explicit oracles, and evidence refs. Empty oracle arrays produce `explored`, never `candidate`.

The baseline redactor handles declared sensitive fields, authorization/cookie headers, password/token/API-key names, and URL query secrets before any writer call. `RunWriter` creates `manifest.json`, `events.jsonl`, `result.json`, `report.md`, optional redacted `candidate.yaml`, and `artifacts/`. Foundation does not export screenshots; screenshot support remains disabled until the masking implementation in the Safety/Desktop plan.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- packages/core/src/evidence-ledger.test.ts packages/core/src/candidate.test.ts packages/core/src/run-writer.test.ts
npm run typecheck
git add packages/core
git diff --cached --check
git commit -m "feat: 增加有界证据账本与候选模型"
```

### Task 5: Implement light isolation and process cleanup

**Files:**
- Create: `packages/isolation/package.json`
- Create: `packages/isolation/tsconfig.json`
- Create: `packages/isolation/src/environment.ts`
- Create: `packages/isolation/src/light-isolation.ts`
- Create: `packages/isolation/src/managed-process.ts`
- Create: `packages/isolation/src/path-guard.ts`
- Create: `packages/isolation/src/index.ts`
- Test: `packages/isolation/src/light-isolation.test.ts`
- Test: `packages/isolation/src/path-guard.test.ts`

- [ ] **Step 1: Write failing isolation tests**

```ts
it('creates disposable HOME and omits credential variables', async () => {
  const isolation = await createLightIsolation({ sourceEnv: {
    PATH: process.env.PATH ?? '', HOME: '/real/home', AWS_SECRET_ACCESS_KEY: 'secret',
  }});
  expect(isolation.env.HOME).toBe(isolation.homeDir);
  expect(isolation.env.AWS_SECRET_ACCESS_KEY).toBeUndefined();
  await isolation.cleanup();
  await expect(access(isolation.rootDir)).rejects.toThrow();
});

it('rejects a symlink whose real path leaves the root', async () => {
  await expect(assertRealPathInside(root, linkedOutsidePath)).rejects.toThrow(/outside isolation root/);
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/isolation/src/light-isolation.test.ts packages/isolation/src/path-guard.test.ts`

Expected: FAIL because isolation functions do not exist.

- [ ] **Step 3: Implement the light provider**

Create root, HOME, project, browser profile, run, and upload directories with `mkdtemp`. Build environment from an explicit allowlist containing only `PATH`, `SystemRoot`, `TMPDIR`, `TEMP`, and adapter-declared non-secret variables, then overwrite HOME/profile variables. `assertRealPathInside` combines `realpath`, `lstat`, and no-follow checks. `ManagedProcess.stop()` terminates the process tree and waits for exit before directory cleanup.

- [ ] **Step 4: Verify cleanup on normal and interrupted paths**

```bash
npm test -- packages/isolation
npm run typecheck
```

Expected: PASS on the current platform; child process and temp path assertions report no leftovers.

- [ ] **Step 5: Commit light isolation**

```bash
git add packages/isolation package.json package-lock.json
git diff --cached --check
git commit -m "feat: 实现轻隔离与进程清理"
```

### Task 6: Implement the Playwright browser runtime

**Files:**
- Create: `packages/runtime-playwright/package.json`
- Create: `packages/runtime-playwright/tsconfig.json`
- Create: `packages/runtime-playwright/src/browser-runtime.ts`
- Create: `packages/runtime-playwright/src/observe.ts`
- Create: `packages/runtime-playwright/src/execute-action.ts`
- Create: `packages/runtime-playwright/src/resolve-locator.ts`
- Create: `packages/runtime-playwright/src/network-policy.ts`
- Create: `packages/runtime-playwright/src/index.ts`
- Test: `packages/runtime-playwright/src/browser-runtime.test.ts`
- Test: `packages/runtime-playwright/src/observe.test.ts`

- [ ] **Step 1: Write failing visibility and network tests**

```ts
it('excludes hidden semantic nodes', async () => {
  await page.setContent('<main style="display:none"><button data-testid="save">Save</button></main>');
  const observation = await observePage(page);
  expect(observation.elements.some((item) => item.testId === 'save')).toBe(false);
});

it('blocks origins outside the target allowlist', async () => {
  const runtime = await BrowserRuntime.launch({ allowedOrigins: ['http://127.0.0.1:4100'] });
  await expect(runtime.pageForTestOnly().goto('http://127.0.0.1:4200')).rejects.toThrow(/network policy blocked/);
  await runtime.close();
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/runtime-playwright/src/observe.test.ts packages/runtime-playwright/src/browser-runtime.test.ts`

Expected: FAIL because runtime functions do not exist.

- [ ] **Step 3: Implement runtime without exposing Page publicly**

Declare `playwright` as a direct dependency of this package. `BrowserRuntime` publicly exposes only `observe`, `execute`, `captureArtifact`, and `close`; `pageForTestOnly` is exported from a test-only module excluded from package exports. Observation uses computed visibility and strict semantic identities and never infers visibility from DOM presence. Locator resolution requires exactly one visible match. Route interception blocks all non-allowed origins before a request leaves Chromium.

In Foundation, `captureArtifact` supports only redacted DOM/ARIA and trace summaries routed through `RunWriter`; a screenshot request returns `ACTION_ERROR` with reason `screenshot-masking-not-installed`.

- [ ] **Step 4: Verify GREEN with a provisioned browser**

```bash
npx playwright install chromium
npm test -- packages/runtime-playwright
npm run typecheck
```

Expected: all runtime tests PASS.

- [ ] **Step 5: Commit runtime**

```bash
git add packages/runtime-playwright package.json package-lock.json
git diff --cached --check
git commit -m "feat: 实现受控 Playwright 浏览器运行时"
```

### Task 7: Implement the Web adapter and target identity

**Files:**
- Create: `packages/adapter-web/package.json`
- Create: `packages/adapter-web/tsconfig.json`
- Create: `packages/adapter-web/src/allocate-port.ts`
- Create: `packages/adapter-web/src/identity.ts`
- Create: `packages/adapter-web/src/web-adapter.ts`
- Create: `packages/adapter-web/src/index.ts`
- Test: `packages/adapter-web/src/web-adapter.test.ts`

- [ ] **Step 1: Write a failing wrong-target test**

```ts
it('rejects a healthy server with the wrong nonce', async () => {
  const server = await startIdentityFixture({ nonce: 'other-run' });
  const adapter = new WebAdapter({ expectedNonce: 'session-nonce', baseURL: server.url });
  await expect(adapter.verifyIdentity(context)).rejects.toThrow(/target identity mismatch/);
  await server.close();
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/adapter-web/src/web-adapter.test.ts`

Expected: FAIL because the adapter does not exist.

- [ ] **Step 3: Implement managed launch and identity verification**

Reserve a free port before spawning the target command and pass `ATH_TARGET_PORT` plus `ATH_TARGET_NONCE`. Health verification requires an exact `x-agentic-testing-harness-nonce` header and captures source root/build identity from configured headers. Existing servers are never reused. Shutdown delegates to `ManagedProcess` and then proves the port is free.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- packages/adapter-web
npm run typecheck
git add packages/adapter-web package.json package-lock.json
git diff --cached --check
git commit -m "feat: 增加 Web target 身份与生命周期适配"
```

### Task 8: Compose a live session in the SDK

**Files:**
- Create: `packages/sdk/package.json`
- Create: `packages/sdk/tsconfig.json`
- Create: `packages/sdk/src/config.ts`
- Create: `packages/sdk/src/load-config.ts`
- Create: `packages/sdk/src/harness-session.ts`
- Create: `packages/sdk/src/index.ts`
- Test: `packages/sdk/src/harness-session.test.ts`

- [ ] **Step 1: Write a failing in-process session test**

```ts
it('keeps runtime handles alive across observe and act', async () => {
  const session = await startSession(webFixtureConfig('read'));
  const first = await session.observe();
  const receipt = await session.act({
    revision: first.revision,
    action: { type: 'navigate', url: `${fixtureURL}/details` },
  });
  expect(receipt.nextRevision).toBe(first.revision + 1);
  expect((await session.observe()).url).toContain('/details');
  await session.finish();
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/sdk/src/harness-session.test.ts`

Expected: FAIL because SDK composition does not exist.

- [ ] **Step 3: Implement the orchestrator and typed config loader**

`startSession` provisions isolation, launches and verifies the adapter, launches the runtime, marks Core ready, and returns one live `HarnessSession` object. `act` performs revision validation, adapter risk classification, policy authorization, runtime execution, invariant checks, evidence append, and a fresh observation. Cleanup order is runtime → adapter → isolation; every attempted cleanup is recorded, and failure never converts the result to success. Load `agentic-testing-harness.config.ts` with Jiti and validate the exported config before target launch.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- packages/sdk
npm run typecheck
git add packages/sdk package.json package-lock.json
git diff --cached --check
git commit -m "feat: 装配长生命周期 harness 会话"
```

### Task 9: Implement the long-lived CLI JSONL stream

**Files:**
- Create: `packages/cli/package.json`
- Create: `packages/cli/tsconfig.json`
- Create: `packages/cli/src/main.ts`
- Create: `packages/cli/src/jsonl-stream.ts`
- Create: `packages/cli/src/session-stream.ts`
- Create: `packages/cli/src/shutdown.ts`
- Create: `packages/cli/src/exit-codes.ts`
- Test: `packages/cli/src/session-stream.test.ts`

- [ ] **Step 1: Write failing subprocess tests**

```ts
it('emits ready, one response per request, and no stdout logs', async () => {
  const cli = spawnAth(['session', 'stream', '--target', 'web-fixture', '--mode', 'read', '--jsonl']);
  expect(await cli.readJSON()).toMatchObject({ ok: true, data: { event: 'session.ready' } });
  cli.writeJSON({ schemaVersion: '1', requestId: 'r1', type: 'observe' });
  expect(await cli.readJSON()).toMatchObject({ requestId: 'r1', ok: true });
  expect(cli.nonJSONStdout()).toEqual([]);
  cli.writeJSON({ schemaVersion: '1', requestId: 'r2', type: 'finish' });
  await expect(cli.exitCode()).resolves.toBe(0);
});

it('fails fast and cleans up on malformed JSONL', async () => {
  const cli = spawnAth(streamArgs);
  await cli.readJSON();
  cli.stdin.write('{bad json}\n');
  await expect(cli.exitCode()).resolves.not.toBe(0);
  expect(await assertFixtureStopped()).toBe(true);
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/cli/src/session-stream.test.ts`

Expected: FAIL because `ath` is not implemented.

- [ ] **Step 3: Implement stream dispatch and shutdown**

The CLI package exposes bin `{ "ath": "./dist/main.js" }`. Use `readline` over stdin; after provisioning write one `session.ready` envelope. Validate every line against `SessionRequestSchema`; unknown fields or malformed JSON terminate the session after a failure envelope. Process exactly one request at a time. `observe`, `act`, `status`, and `finish` call the live SDK object. `stdout.write` is centralized in `writeEnvelope`; all other logging uses stderr. EOF, `SIGINT`, `SIGTERM`, and uncaught errors call one idempotent shutdown function.

Exit codes are fixed: success `0`, `CONFIG_ERROR=2`, `CONTRACT_ERROR=3`, `POLICY_BLOCKED=4`, `ISOLATION_ERROR=5`, `TARGET_ERROR=6`, `ACTION_ERROR=7`, `ORACLE_FAILED=8`, and `INFRASTRUCTURE_ERROR=9`.

- [ ] **Step 4: Verify GREEN including signals**

```bash
npm run build
npm test -- packages/cli/src/session-stream.test.ts
npm run typecheck
```

Expected: ready/request/finish, malformed JSONL, EOF, and signal cases PASS with no leaked process.

- [ ] **Step 5: Commit CLI stream**

```bash
git add packages/cli package.json package-lock.json
git diff --cached --check
git commit -m "feat: 提供 ath 长连接 JSONL 会话"
```

### Task 10: Add init, doctor, report, and replay commands

**Files:**
- Create: `packages/cli/src/commands/init.ts`
- Create: `packages/cli/src/commands/doctor.ts`
- Create: `packages/cli/src/commands/report.ts`
- Create: `packages/cli/src/commands/replay.ts`
- Create: `packages/cli/src/templates/config-template.ts`
- Modify: `packages/cli/src/main.ts`
- Test: `packages/cli/src/commands.test.ts`

- [ ] **Step 1: Write failing command tests**

```ts
it('init refuses to overwrite an existing config', async () => {
  await writeFile(configPath, 'export default {}');
  const result = await runAth(['init'], tempProject);
  expect(result.exitCode).not.toBe(0);
  expect(result.stderr).toContain('CONFIG_ERROR');
});

it('doctor reports the Chromium capability', async () => {
  const result = await runAth(['doctor', '--json'], tempProject);
  expect(JSON.parse(result.stdout).data.capabilities.chromium).toBe(true);
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/cli/src/commands.test.ts`

Expected: FAIL because commands do not exist.

- [ ] **Step 3: Implement commands**

`init` writes a typed config containing one `web-local` profile and never overwrites. `doctor` checks Node version, direct Playwright resolution, Chromium executable, writable runs directory, and reports Docker as optional in Foundation. `report` reads only validated result/evidence files. `replay` loads a redacted candidate, starts a fresh live session, executes each action with current revisions, evaluates its oracles, and returns `replay_passed` only when every oracle succeeds.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- packages/cli/src/commands.test.ts
npm run typecheck
git add packages/cli
git diff --cached --check
git commit -m "feat: 增加 harness 初始化诊断与重放命令"
```

### Task 11: Build the Web fixture and end-to-end vertical slice

**Files:**
- Create: `examples/web-fixture/package.json`
- Create: `examples/web-fixture/src/server.ts`
- Create: `examples/web-fixture/src/app.html`
- Create: `examples/web-fixture/agentic-testing-harness.config.ts`
- Create: `tests/e2e/helpers/cli-session.ts`
- Create: `tests/e2e/web-session.test.ts`

- [ ] **Step 1: Write the failing E2E**

```ts
it('explores a visible Web path and blocks a write in read mode', async () => {
  const session = await openCliSession({ target: 'web-fixture', mode: 'read' });
  const first = await session.observe();
  expect(first.data.elements).toContainEqual(expect.objectContaining({ name: 'Details', visible: true }));
  expect(await session.act(first.revision, navigateToDetails)).toMatchObject({ ok: true });
  const writeResult = await session.act(first.revision + 1, clickSave);
  expect(writeResult).toMatchObject({ ok: false, error: { code: 'POLICY_BLOCKED' } });
  await session.finish();
  expect(await session.assertNoProcessOrPortLeak()).toBe(true);
});
```

- [ ] **Step 2: Verify RED**

Run: `npm run test:e2e:web`

Expected: FAIL because fixture and test helper do not exist.

- [ ] **Step 3: Implement the fixture and identity contract**

The Node HTTP fixture serves `/health` and `/`, returns the exact nonce header, renders visible Details navigation plus a Save button, and exposes a hidden duplicate button to prove visibility filtering. Its adapter classifies Details navigation as `read` and Save as `write`. The E2E helper keeps one child process open and communicates only by JSONL.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm run build
npm run test:e2e:web
npm run verify
git add examples/web-fixture tests/e2e package.json package-lock.json
git diff --cached --check
git commit -m "test: 验证 Web Agent 探索纵切"
```

### Task 12: Add the Codex Skill, CI, and fresh-package smoke

**Files:**
- Create: `skills/codex-agentic-testing-harness/SKILL.md`
- Create: `skills/codex-agentic-testing-harness/references/protocol.md`
- Create: `tests/skill/skill-contract.test.ts`
- Create: `tests/package/fresh-install.test.ts`
- Create: `.github/workflows/ci.yml`
- Modify: `README.md`

- [ ] **Step 1: Write failing Skill and package tests**

```ts
it('Skill uses only public ath commands', async () => {
  const skill = await readFile('skills/codex-agentic-testing-harness/SKILL.md', 'utf8');
  expect(skill).toContain('ath session stream');
  expect(skill).not.toMatch(/playwright|from ['"]@agentic-testing-harness\/sdk/);
});

it('packed CLI runs doctor from a clean directory', async () => {
  const installed = await installPackedWorkspaceInTempDir();
  const result = await installed.run(['ath', 'doctor', '--json']);
  expect(result.exitCode).toBe(0);
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- tests/skill/skill-contract.test.ts tests/package/fresh-install.test.ts`

Expected: FAIL because Skill, workflow, and package helper do not exist.

- [ ] **Step 3: Implement Skill and reproducible CI**

Skill instructions run `ath doctor`, select read mode for ordinary exploration, hold the stream process handle, send one JSON request at a time, stop on any contract/policy/isolation error, and report `explored` separately from `candidate` or `replay_passed`. CI installs Node 20.19.0, runs `npm ci`, runs `npx playwright install --with-deps chromium` on Linux, then lint/typecheck/test/build/package smoke. macOS and Windows run unit, Web fixture, and package smoke with `npx playwright install chromium`.

- [ ] **Step 4: Run the complete Foundation gate**

```bash
npm ci
npx playwright install chromium
npm run verify
npm run test:e2e:web
npm pack --dry-run
```

Expected: all commands exit 0; test output has no skipped critical test and the package list contains the `ath` bin plus built public packages.

- [ ] **Step 5: Commit Foundation completion**

```bash
git add skills tests/package tests/skill .github/workflows/ci.yml README.md package.json package-lock.json
git diff --cached --check
git commit -m "ci: 完成 harness foundation 验证闭环"
```
