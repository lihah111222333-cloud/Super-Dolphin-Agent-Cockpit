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
- Create: `packages/contracts/src/json-value.ts`
- Create: `packages/contracts/src/errors.ts`
- Create: `packages/contracts/src/actions.ts`
- Create: `packages/contracts/src/protocol.ts`
- Create: `packages/contracts/src/session.ts`
- Create: `packages/contracts/src/isolation.ts`
- Create: `packages/contracts/src/adapters.ts`
- Create: `packages/contracts/src/candidate.ts`
- Create: `packages/contracts/src/index.ts`
- Create: `scripts/assert-build-output.mjs`
- Modify: `package.json`
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
      data: { action: { type: 'click', target: { kind: 'role', role: 'button', name: 'Save', strict: true } } },
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

`ActionSchema` contains only `navigate`, `click`, `fill`, `select`, `check`, `press`, `upload`, `window`, `waitFor`, and `finish`. Actions that address UI elements use the `target` field; locator schemas use `kind` as their discriminator and support `role`, `testId`, `label`, and explicit `css`. Every locator requires `strict: true`. Do not introduce the alternate wire shape `locator: { type: ... }`. `TargetAdapter` exposes the approved lifecycle including `classifyAction`.

The response envelope requires `schemaVersion`, `requestId`, `sessionId`, `revision`, `ok`, `data`, and `evidenceRefs` on success; failure replaces `data` with `{ code, message, details }`. Define one recursive `JsonValueSchema`/`JsonValue` contract and use it for all serialized open-value fields, including success `data`, error `details`, candidate oracle expectations, isolation attestation claims, and provision metadata. It must reject `undefined`, functions, `bigint`, non-finite numbers, cyclic values, sparse or extended arrays, symbol/accessor/non-enumerable properties, class instances, `Map`, `Set`, `RegExp`, `Date`, custom `toJSON`, live or revoked proxies, and nested non-JSON values. The runtime validator is iterative, not call-stack recursive, and enforces a maximum depth of 256, maximum node count of 100,000, maximum individual string/key length of 1,048,576 UTF-16 code units, and maximum aggregate string/key length of 8,388,608 code units before TypeBox structural traversal. Array length is checked against the remaining node budget before allocating descriptors. Plain objects may use only `Object.prototype` or a null prototype and enumerable string data properties. Every schema-valid envelope must preserve its data through a JSON stringify/parse round trip; TypeBox's permissive record check alone is insufficient. Any custom TypeBox runtime registry kind must include the wire-schema version, reuse an already registered compatible validator for idempotent same-version module copies, and fail fast instead of replacing a foreign validator that owns the same kind.

`isolation.ts` defines `IsolationProvider`, `IsolationAttestation`, and both `LightIsolationReceipt` and `HardIsolationReceipt`. It includes a `VMIsolationProvider` interface but no VM implementation. Pin `@sinclair/typebox@^0.34.41` in the contracts package.

Exclude `src/**/*.test.ts` from `packages/contracts/tsconfig.json` so package build output never publishes test JavaScript or declarations. Keep tests under the root tooling/typecheck project instead.

Root and package builds must recover production output even when `.tmp/*.tsbuildinfo` remains but `dist` has been deleted. Use forced or explicit clean build semantics. `scripts/assert-build-output.mjs` provides a cross-platform persistent guard: it requires `packages/contracts/dist/index.js` and `index.d.ts` and rejects emitted `*.test.js` or `*.test.d.ts`; root `build` and therefore `verify` must run it.

After `packages/contracts/tsconfig.json` exists, replace the bootstrap root config with a solution config that still typechecks Vitest configuration and tests:

```json
{
  "extends": "./tsconfig.base.json",
  "compilerOptions": {
    "noEmit": true,
    "tsBuildInfoFile": "./.tmp/tsconfig.tsbuildinfo"
  },
  "include": [
    "vitest.config.ts",
    "packages/**/*.test.ts",
    "tests/**/*.test.ts"
  ],
  "references": [
    { "path": "./packages/contracts" }
  ]
}
```

- [ ] **Step 4: Run contract tests and typecheck**

```bash
npm test -- packages/contracts/src/protocol.test.ts
npm run typecheck
node -e "require('node:fs').rmSync('packages/contracts/dist', { recursive: true, force: true })"
npm run build
node scripts/assert-build-output.mjs
```

Expected: table-driven tests cover every action and locator variant, nested extra fields, missing `strict`, negative revisions, empty identifiers, strict plain-JSON rejection, cycles, and serialization round trips. Tests PASS, TypeScript and build exit 0, a cached build restores deleted production entries, and package build output contains no test files.

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
- Modify: `tsconfig.json`
- Modify: `scripts/build.mjs`
- Modify: `scripts/assert-build-output.mjs`
- Test: `packages/core/src/session-machine.test.ts`
- Test: `packages/core/src/policy.test.ts`
- Test: `packages/core/src/budget.test.ts`

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
  expect(authorizeAction({
    mode: 'read', risk: 'unclassified', actionType: 'click',
    hardAttestationVerified: false, budget: { exhausted: false },
  }))
    .toEqual({ allowed: false, code: 'POLICY_BLOCKED' });
});

it('reports the exact exhausted budget', () => {
  const budget = new BudgetTracker({
    maxActions: 1, maxDurationMs: 1000,
    maxNetworkEvents: 2, maxEvidenceBytes: 8,
  });
  budget.consumeAction();
  expect(() => budget.consumeAction()).toThrow(
    expect.objectContaining({ code: 'ACTION_BUDGET_EXHAUSTED' }),
  );
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/core/src/session-machine.test.ts packages/core/src/policy.test.ts packages/core/src/budget.test.ts`

Expected: FAIL because Core symbols do not exist.

- [ ] **Step 3: Implement minimum state transitions**

Implement phases `provisioning`, `ready`, `observed`, `authorized`, `executed`, `finished`, and `failed`. The machine owns and validates the monotonic revision, rejects concurrent, stale, future, skipped, or repeated revisions, exposes `executed` before the next observation returns it to `observed`, and makes `finished`/`failed` terminal. Illegal transitions fail fast with stable machine error codes; `beginAction` checks an in-flight action before revision staleness so the concurrent-action error is deterministic.

`BudgetTracker` validates that all four configured limits are finite non-negative integers, rejects invalid consumption deltas, and enforces `maxActions`, `maxDurationMs`, `maxNetworkEvents`, and `maxEvidenceBytes`. Reaching a limit is allowed for the operation that reaches it but makes the next authorization exhausted; an operation that would exceed it throws a typed error with exactly one of `ACTION_BUDGET_EXHAUSTED`, `DURATION_BUDGET_EXHAUSTED`, `NETWORK_EVENT_BUDGET_EXHAUSTED`, or `EVIDENCE_BYTE_BUDGET_EXHAUSTED`. Inject a clock in tests rather than sleeping.

Policy rules are exact:

```ts
if (mode === 'write' && !hardAttestationVerified) return blocked('ISOLATION_ERROR');
if (mode === 'read' && risk !== 'read') return blocked('POLICY_BLOCKED');
if (budget.exhausted) return blocked('POLICY_BLOCKED');
return { allowed: true };
```

All policy inputs are required; missing attestation or budget state is a contract error, not an implicit default. `risk` at this layer is only `read`, `write`, or `unclassified`; an adapter-level `blocked` classification is rejected before this function. No label, role name, Agent reason, or CSS selector may lower risk.

Add `packages/core` as a TypeScript project reference after `packages/contracts`, make the Core project reference Contracts, and exclude `src/**/*.test.ts` from Core publication while the root tooling project still typechecks the tests. Extend the cross-platform build driver and output guard so cached builds restore both packages' `dist/index.js` and `dist/index.d.ts` and neither package emits test files.

- [ ] **Step 4: Verify GREEN**

```bash
npm test -- packages/core/src/session-machine.test.ts packages/core/src/policy.test.ts packages/core/src/budget.test.ts
npm run typecheck
npm run build
node scripts/assert-build-output.mjs
```

Expected: all three files PASS; typecheck and build exit 0; Core production entries exist and no test file is emitted.

- [ ] **Step 5: Commit Core state and policy**

```bash
git add packages/core package.json package-lock.json tsconfig.json scripts/build.mjs scripts/assert-build-output.mjs
git diff --cached --check
git commit -m "feat: 实现 harness 会话状态与策略门禁"
```

### Task 4: Add a bounded evidence ledger and candidate model

**Files:**
- Modify: `packages/contracts/src/candidate.ts`
- Modify: `packages/core/package.json`
- Create: `packages/core/src/bounded-buffer.ts`
- Create: `packages/core/src/evidence-ledger.ts`
- Create: `packages/core/src/redactor.ts`
- Create: `packages/core/src/run-writer.ts`
- Create: `packages/core/src/candidate.ts`
- Modify: `packages/core/src/index.ts`
- Modify: `package-lock.json`
- Test: `packages/core/src/bounded-buffer.test.ts`
- Test: `packages/core/src/evidence-ledger.test.ts`
- Test: `packages/core/src/redactor.test.ts`
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
  const writer = await RunWriter.create(tempRunRoot, {
    sessionId: 'session-1', sensitiveFields: ['apiKey'],
  });
  await writer.writeResult({ apiKey: 'foundation-secret-marker', status: 'explored' });
  expect(await readFile(writer.resultPath, 'utf8')).not.toContain('foundation-secret-marker');
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/core/src/bounded-buffer.test.ts packages/core/src/evidence-ledger.test.ts packages/core/src/redactor.test.ts packages/core/src/candidate.test.ts packages/core/src/run-writer.test.ts`

Expected: FAIL because ledger and candidate builder do not exist.

- [ ] **Step 3: Implement ledger and candidate normalization**

`BoundedBuffer` validates finite non-negative safe-integer limits, measures canonical UTF-8 bytes, evicts oldest entries until both limits hold, never retains a single oversized entry, counts every eviction/rejection, and returns immutable snapshots. Every exported JSON clone/freeze/canonicalization helper validates before traversal, reads data descriptors without invoking accessors, and preserves an own `__proto__` key as data without changing the clone prototype.

Evidence input must satisfy the shared strict `JsonValueSchema` before traversal; Core must declare any package it imports directly rather than relying on a transitive dependency. Use canonical UTF-8 JSON with recursively sorted object keys for receipt hashing. Each immutable receipt stores a monotonic sequence, `previousHash`, redacted event, and 64-lowercase-hex `hash`; the first receipt uses 64 zeroes. Dropped receipts still advance the chain head. When retained history is evicted, preserve an anchor hash/sequence so `verifyChain()` validates the remaining suffix instead of treating it as a new chain. Instance verification authenticates only the current instance snapshot: the supplied head, anchor, dropped count, retained event/byte bounds, and complete canonical snapshot must match trusted instance state after self-consistency checks. Any offline self-consistency API is named explicitly and requires caller-supplied limits; it does not claim provenance. It performs descriptor-only snapshot shape, retained-event-count, declared-byte, and minimum canonical receipt-byte preflight before recursive JSON or hash traversal, stopping once the caller budget is exceeded so an under-reported or already out-of-budget snapshot fails in bounded work. Buffer byte accounting includes the complete stored receipt.

Extend the Contracts `CandidateAction` type with a positive safe-integer `expectedRevisionDelta` and per-action `evidenceRefs`. Candidate actions retain semantic locators, expected revision deltas, explicit JSON-safe oracles, and nonempty evidence refs without converting strict locators to CSS. Public candidate types are deeply readonly, and candidate input is validated, cloned, and deeply frozen so caller mutation cannot alter the result. Valid JSON keys such as `__proto__` remain ordinary own data properties. Empty oracle arrays produce `explored` with no promotable candidate, never `candidate`; Foundation does not implement replay-count promotion.

The baseline redactor handles declared sensitive fields, authorization/cookie headers, password/token/API-key/secret names, absolute, relative, protocol-relative, and malformed apparent URL user-info, plus sensitive URL query and fragment parameters before any writer call. It must validate options and values through data descriptors without executing accessors and replace a sensitive value with deterministic session-local metadata containing only a redaction marker, value type, safe length metadata, and an opaque reference. The opaque reference is a version/domain-separated HMAC tag under a random per-instance 256-bit key: structurally equal JSON secrets reuse it within one session, while different sessions cannot correlate it. The key is never persisted, and no raw/unkeyed hash, secret-derived prefix, original secret, or unbounded secret-reference map is retained or written. Built-in matching is case/separator insensitive; declared names are exact after the same normalization.

`RunWriter.create` requires a portable 1–128 character ASCII session ID (`[A-Za-z0-9._-]` with safe first/last characters and no Windows reserved basename) and a new non-symlink run directory, then creates private `manifest.json`, `events.jsonl`, `result.json`, `report.md`, optional redacted `candidate.yaml`, and `artifacts/` paths. It resolves the parent once to an absolute path, pins the run-directory and file device/inode identities, freezes public paths, establishes exact private modes with `fchmod` even under a restrictive process umask, and only cleans or unlinks a path whose identity still matches the operation. Fixed JSON/JSONL/Markdown/candidate writers validate JSON, redact before serialization, terminate text records with a newline, keep the private staging handle open through identity-checked hard-link publication, and publish atomically without overwrite; a failed write never leaves a partial final file or silently deletes a replacement path. `events.jsonl` is pinned to the private regular file created for the run. Deletion, replacement, permission/link changes, or a symlink substitution fail closed without recreation or following the replacement, and the first append I/O failure puts the entire writer into a stable fail-stop state. Result, report, and candidate writers first await all previously queued events, revalidate the pinned run directory and event file before/during/after publication, and inherit that same terminal failure. JSON syntax is valid YAML 1.2 for the candidate file, so Foundation does not add a YAML serializer solely for this output. No artifact or screenshot write API is exposed until the masking and artifact-budget work in Safety/Desktop.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- packages/core/src/bounded-buffer.test.ts packages/core/src/evidence-ledger.test.ts packages/core/src/redactor.test.ts packages/core/src/candidate.test.ts packages/core/src/run-writer.test.ts
npm run typecheck
npm run build
npm pack --dry-run --json --workspace @agentic-testing-harness/core
git add packages/contracts/src/candidate.ts packages/core package-lock.json
git diff --cached --check
git commit -m "feat: 增加有界证据账本与候选模型"
```

### Task 5: Implement light isolation and process cleanup

**Files:**
- Modify: `tsconfig.json`
- Modify: `scripts/build.mjs`
- Modify: `scripts/assert-build-output.mjs`
- Modify: `package-lock.json`
- Create: `packages/isolation/package.json`
- Create: `packages/isolation/tsconfig.json`
- Create: `packages/isolation/src/environment.ts`
- Create: `packages/isolation/src/light-isolation.ts`
- Create: `packages/isolation/src/managed-process.ts`
- Create: `packages/isolation/src/path-guard.ts`
- Create: `packages/isolation/src/index.ts`
- Test: `packages/isolation/src/light-isolation.test.ts`
- Test: `packages/isolation/src/managed-process.test.ts`
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

Create a private root, HOME, project, browser profile, run, upload, temp, and XDG directory layout with `mkdtemp`. Capture environment options synchronously through data descriptors before the first filesystem await. Build the child environment from an explicit allowlist containing only `PATH`, `SystemRoot`, `TMPDIR`, `TEMP`, and adapter-declared non-secret variables, reject credential/cloud/executable-injection names, then overwrite HOME/profile/XDG/temp variables. Environment shape violations use the frozen `CONTRACT_ERROR`; provisioning and cleanup failures use `ISOLATION_ERROR`.

Pin the newly created root identity immediately. Provisioning failure and normal cleanup may recursively remove only the same pinned directory; a disappeared, replaced, symlinked, or identity-mismatched root is retained and fails closed. The public isolation root is its canonical real path. `assertRealPathInside` requires that canonical root, combines `realpath`, `lstat`, full component no-follow checks, and repeated device/inode validation, and rejects prefix collisions plus inward, outward, broken, leaf, intermediate, root, and root-ancestor symlinks or junctions.

`ManagedProcess` must be constructed while its detached POSIX child identity is live. It validates options without invoking accessors or Proxy traps, terminates the complete process group, waits on a monotonic bounded graceful deadline, escalates to forceful termination, and applies an independent bounded force/taskkill deadline before directory cleanup. A late construction that cannot safely prove ownership of a still-live orphan group fails closed. Concurrent and repeated `stop()` calls return the same Promise, and failure never becomes success.

Add `packages/isolation` to root TypeScript references, the cross-platform build driver, and the production-output guard. Publication excludes tests and requires both `dist/index.js` and `dist/index.d.ts`.

- [ ] **Step 4: Verify cleanup on normal and interrupted paths**

```bash
npm test -- packages/isolation
npm run typecheck
npm run lint
npm run build
node scripts/assert-build-output.mjs packages/isolation
npm pack --dry-run --json --workspace @agentic-testing-harness/isolation
```

Expected: PASS on the current platform; child process and temp path assertions report no leftovers; build and pack contain production entries and no test output.

- [ ] **Step 5: Commit light isolation**

```bash
git add packages/isolation package-lock.json tsconfig.json scripts/build.mjs scripts/assert-build-output.mjs
git diff --cached --check
git commit -m "feat: 实现轻隔离与进程清理"
```

### Task 6: Implement the Playwright browser runtime

**Files:**
- Modify: `tsconfig.json`
- Modify: `scripts/build.mjs`
- Modify: `scripts/assert-build-output.mjs`
- Modify: `package-lock.json`
- Create: `packages/runtime-playwright/package.json`
- Create: `packages/runtime-playwright/tsconfig.json`
- Create: `packages/runtime-playwright/src/browser-runtime.ts`
- Create: `packages/runtime-playwright/src/runtime-state.ts`
- Create: `packages/runtime-playwright/src/test-only.ts`
- Create: `packages/runtime-playwright/src/observe.ts`
- Create: `packages/runtime-playwright/src/execute-action.ts`
- Create: `packages/runtime-playwright/src/resolve-locator.ts`
- Create: `packages/runtime-playwright/src/network-policy.ts`
- Create: `packages/runtime-playwright/src/egress-proxy.ts`
- Create: `packages/runtime-playwright/src/index.ts`
- Test: `packages/runtime-playwright/src/browser-runtime.test.ts`
- Test: `packages/runtime-playwright/src/execute-action.test.ts`
- Test: `packages/runtime-playwright/src/network-policy.test.ts`
- Test: `packages/runtime-playwright/src/egress-proxy.test.ts`
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
  await expect(pageForTestOnly(runtime).goto('http://127.0.0.1:4200')).rejects.toThrow(/blocked/i);
  await runtime.close();
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/runtime-playwright/src/observe.test.ts packages/runtime-playwright/src/browser-runtime.test.ts`

Expected: FAIL because runtime functions do not exist.

- [ ] **Step 3: Implement runtime without exposing Page publicly**

Declare `playwright` as a direct dependency of this package. `BrowserRuntime` publicly exposes only `observe`, `execute`, `captureArtifact`, and `close`; Playwright `Page`, `BrowserContext`, browser handles, low-level policy functions, and `pageForTestOnly` are absent from the public package export. The test-only module is excluded from production build and pack output.

Observation uses bounded DOM scanning, computed style, layout, clipping, filter, mask, opacity, transparent-text, and ancestor visibility checks and never infers visibility from DOM presence. It returns no input values, inner HTML, password values, or hidden text. Locator input is strict JSON without Proxy, accessor, callable-object, unknown-field, or ambiguous-match acceptance. Resolver selection pins one visually visible `ElementHandle`; DOM replacement after validation fails instead of retargeting a live locator.

Enforce exact canonical origin policy twice. The browser-wide loopback egress proxy is configured as Chromium's manual proxy with implicit loopback bypass removed, performs a startup capability probe, validates HTTP absolute URLs, distinguishes plaintext WebSocket and TLS CONNECT prefaces before opening the upstream socket, and retains the guard until browser containment is proven. Playwright context routes, WebSocket routes, service-worker blocking, and per-page CDP Fetch interception provide the inner request and redirect guard. Worker, AudioWorklet, Speculation Rules, `fetchLater`, popup, WebSocket, direct request, subresource, and redirect tests must prove blocked targets receive zero requests while allowed targets remain usable.

Only explicitly managed pages count toward the window budget. Page-created popups are closed, popup cleanup failures interrupt the active operation, and no guard failure may be overwritten by a concurrent deadline. Whole actions and trusted callbacks have bounded deadlines; timeout or proxy/page-guard failure closes context and browser before removing the network guards. Cleanup steps have independent deadlines, coalesce, aggregate failures, preserve infrastructure error classification, and never convert failure to success.

In Foundation, action receipts and DOM/ARIA/trace artifact summaries contain only structural counts and action type; they do not return URL, title, accessible names, locator values, or action secrets. Later SDK composition routes them through `RunWriter`. A screenshot request returns `ACTION_ERROR` with reason `screenshot-masking-not-installed`.

- [ ] **Step 4: Verify GREEN with a provisioned browser**

```bash
npx playwright install chromium
npm test -- packages/runtime-playwright
npm run typecheck
npm run lint
npm run build
node scripts/assert-build-output.mjs packages/runtime-playwright
npm pack --dry-run --json --workspace @agentic-testing-harness/runtime-playwright
```

Expected: all runtime and real-Chromium egress tests PASS; build and pack contain production entries and no test or test-only output.

- [ ] **Step 5: Commit runtime**

```bash
git add packages/runtime-playwright package-lock.json tsconfig.json scripts/build.mjs scripts/assert-build-output.mjs
git diff --cached --check
git commit -m "feat: 实现受控 Playwright 浏览器运行时"
```

### Task 7: Implement the Web adapter and target identity

**Files:**
- Modify: `tsconfig.json`
- Modify: `scripts/build.mjs`
- Modify: `scripts/assert-build-output.mjs`
- Modify: `package-lock.json`
- Create: `packages/adapter-web/package.json`
- Create: `packages/adapter-web/tsconfig.json`
- Create: `packages/adapter-web/src/allocate-port.ts`
- Create: `packages/adapter-web/src/identity.ts`
- Create: `packages/adapter-web/src/web-adapter.ts`
- Create: `packages/adapter-web/src/index.ts`
- Test: `packages/adapter-web/src/web-adapter.test.ts`

- [ ] **Step 1: Write a failing wrong-target test**

```ts
it('rejects a managed target that reports the wrong nonce', async () => {
  const adapter = new WebAdapter(isolation, webTargetOptions('wrong-nonce'));
  const provisioned = await adapter.provision(readProvisionContext(isolation));
  await expect(adapter.launch({
    sessionId: 'session-1',
    targetId: provisioned.targetId,
  })).rejects.toMatchObject({ code: 'TARGET_ERROR' });
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/adapter-web/src/web-adapter.test.ts`

Expected: FAIL because the adapter does not exist.

- [ ] **Step 3: Implement managed launch and identity verification**

Reserve a candidate loopback port before spawning the target command and pass only the light-isolation allowlist plus `ATH_TARGET_PORT` and a 256-bit `ATH_TARGET_NONCE`. Because the listener must be released before a normal child can bind, explicitly treat the interval as a TOCTOU window: nonce, source-root, and build identity are the fail-closed acceptance gate, not a claim of continuous socket ownership. The adapter constructs its own numeric-loopback endpoint and never accepts or reuses a caller-supplied server URL.

Spawn without a shell and create/register `ManagedProcess` before the first post-spawn await. The target state machine rejects concurrent or repeated launch, cross-session or cross-target contexts, write mode under light isolation, and all running operations after shutdown or unexpected child exit. Launch or verification failure aborts outstanding health requests, stops the full process tree, and proves the exact port can be rebound. Shutdown coalesces, delegates to `ManagedProcess`, performs the same bind proof, and preserves `ISOLATION_ERROR` separately from `TARGET_ERROR` and `INFRASTRUCTURE_ERROR`.

Health verification accepts only `http://127.0.0.1:<port>` and an absolute same-origin path, does not follow redirects, requires status 200, and compares one exact `x-agentic-testing-harness-nonce` plus configured source-root/build headers. Missing, repeated, comma-coalesced, unsafe, or mismatched identity fails immediately. Connection readiness retries use one monotonic deadline; target exit or cancellation closes the request socket and polling timer, and a verified header response is accepted without consuming an unbounded body.

Configuration, nested identity values, command arguments, adapter contexts, and attestation data are copied through bounded data descriptors and reject Proxy, accessor, callable, sparse, extra-field, or non-JSON shapes. The package public export exposes only `WebAdapter`, `WebAdapterError`, and their contract types; port allocation, HTTP verification, child handles, and test helpers remain internal subpaths.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- packages/adapter-web
npm run typecheck
npm run lint
npm run build
node scripts/assert-build-output.mjs packages/adapter-web
npm pack --dry-run --json --workspace @agentic-testing-harness/adapter-web
```

Expected: adapter identity, lifecycle, failure cleanup, cancellation, and port-rebind tests PASS; the full workspace remains green; build and pack contain production entries only and the package export blocks internal subpaths.

```bash
git add packages/adapter-web package-lock.json tsconfig.json scripts/build.mjs scripts/assert-build-output.mjs
git diff --cached --check
git commit -m "feat: 增加 Web target 身份与生命周期适配"
```

### Task 8: Compose a live session in the SDK

**Files:**
- Modify: `package-lock.json`
- Modify: `tsconfig.json`
- Modify: `scripts/build.mjs`
- Modify: `scripts/assert-build-output.mjs`
- Modify: `packages/runtime-playwright/src/browser-runtime.ts`
- Modify: `packages/runtime-playwright/src/egress-proxy.ts`
- Modify: `packages/runtime-playwright/src/execute-action.ts`
- Modify: `packages/runtime-playwright/src/resolve-locator.ts`
- Modify: `packages/runtime-playwright/src/runtime-state.ts`
- Modify: `packages/runtime-playwright/src/index.ts`
- Test: `packages/runtime-playwright/src/browser-runtime.test.ts`
- Test: `packages/runtime-playwright/src/egress-proxy.test.ts`
- Test: `packages/runtime-playwright/src/execute-action.test.ts`
- Create: `packages/sdk/package.json`
- Create: `packages/sdk/tsconfig.json`
- Create: `packages/sdk/src/config.ts`
- Create: `packages/sdk/src/load-config.ts`
- Create: `packages/sdk/src/harness-session.ts`
- Create: `packages/sdk/src/index.ts`
- Test: `packages/sdk/src/harness-session.test.ts`

- [ ] **Step 1: Write a failing in-process session test**

```ts
it('keeps runtime handles alive across observe and act without exposing them', async () => {
  const session = await startSession({
    config: defineConfig(webFixtureConfig()),
    target: 'fixture',
    mode: 'read',
  });
  const first = await session.observe();
  const receipt = await session.act({
    revision: first.revision,
    action: { type: 'navigate', url: `${fixtureURL}/details` },
  });
  expect(receipt.nextRevision).toBe(first.revision + 1);
  expect((await session.observe()).url).toContain('/details');
  await session.finish();
});

it('settles background network events and preserves the exact hard limit', async () => {
  const session = await startSession(networkFixtureConfig({ maxNetworkEvents: 2 }));
  await session.observe();
  await triggerOneBackgroundRequest();
  expect(session.status().budget).toMatchObject({ networkEvents: 2, exhausted: true });
  await expect(session.finish()).resolves.toMatchObject({ status: 'explored' });
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/sdk/src/harness-session.test.ts`

Expected: FAIL because SDK composition does not exist.

- [ ] **Step 3: Implement the orchestrator and typed config loader**

Load `agentic-testing-harness.config.ts` with Jiti using an enumerable, non-callable default data export. `defineConfig` recursively copies and freezes strict bounded data: reject Proxy, accessor, callable, sparse-array, class-instance, unknown-field, unsafe header/health path, invalid budget, and secret-bearing command shapes instead of coercing or falling back. Resolve `runsRoot` relative to the config file and validate the resolved object again before any target launch.

Capture and validate `startSession` input before creating a run directory. For a valid request, create `RunWriter`, `Redactor`, and `EvidenceLedger` before isolation or target side effects so every later startup failure can publish a durable result. Then create light isolation, provision/launch the Web adapter, verify exact target identity, launch the browser runtime, settle the initial observation, mark Core ready, and return one live `HarnessSession`. The session owns isolation, adapter, runtime, state machine, budget, ledger, and writer for its full lifetime, while its public surface exposes only frozen `status`, `observe`, `act`, and `finish` data—never a browser, page, adapter, process, or isolation handle.

Feed every proxy-observed request count into Core through a synchronous cumulative network callback installed from the same post-probe baseline as the hard proxy limit. Charge initial, subresource, action, and background requests exactly once. Reaching `maxNetworkEvents` is allowed and reported as exhausted; the next request is blocked before upstream connection. The callback is synchronous-only, its failure latches the proxy guard, and a session-local pending failure is adopted by `status`, cached or fresh `observe`, `act`, and `finish`. Reject bounded HTTPS origins until per-request TLS tunnel accounting is enforceable rather than silently weakening the limit.

Keep one operation in flight per session. `act` captures strict JSON, validates the current observation revision, obtains adapter risk classification, settles pending network state, authorizes against mode and budget, enters the Core action state, consumes the action budget, checks the before invariant, executes through `BrowserRuntime`, settles network state, checks the after invariant, completes the state transition, collects adapter evidence, appends the action event, obtains and appends one fresh observation, and only then publishes the new cached revision. Classification, runtime, invariant, evidence, or fresh-observation failure remains terminal once execution has begun; a stale revision or an ordinary read-mode write denial does not bypass Core or execute the action.

Persist real execution evidence rather than restating the request. Locator actions record total and visually visible match counts, strict-visible uniqueness, and the satisfied visible/hidden state. Runtime receipts record before/after page URL, managed-page count, and browser connectivity; Web sessions explicitly record bridge non-applicability, plus invariant results, before-observation summary, cumulative network count, isolation provider/mode, and artifact refs. Redact and preflight exact persisted bytes before append. `events.jsonl` remains append-only with `sequence`, `previousHash`, and SHA-256 receipt hash, and every terminal `result.json` anchors the final ledger head and dropped count.

On finish and failed startup, attempt cleanup strictly runtime → adapter → isolation and append `cleanup.attempted` before each available cleanup without stopping later steps. Browser containment and public `close()` share coalesced Promises. A module-private `WeakSet` brands only genuine guard errors raised after successful containment; the exported predicate permits the SDK to preserve the original policy/product classification without treating that guard as a cleanup failure. Forged errors and any real containment, adapter, isolation, or evidence-write failure remain infrastructure failures. Startup and finish publish matching `policy_blocked`, `product_failed`, or `infrastructure_failed` results only after cleanup attempts; cleanup can escalate a result but never convert failure to success.

Add the SDK to TypeScript references, workspace build output, and production-output checks. `@agentic-testing-harness/sdk` exports only the package root backed by `dist/index.js` and `dist/index.d.ts`; `src/index.ts` re-exports the strict config, loader, session interfaces, starter, and stable error class. Runtime public additions are limited to the hard-limit/count-callback launch options, JSON-safe execution and observation summaries, locator evidence types, and the contained-guard predicate; the `BrowserRuntime` method set remains `observe`, `execute`, `captureArtifact`, and `close`. Tests, test-only helpers, and internal subpaths must not appear in build or pack output.

- [ ] **Step 4: Verify GREEN, public exports, and pack output**

```bash
npm test -- packages/sdk packages/runtime-playwright
npm run lint
npm run typecheck
npm test
npm run build
node scripts/assert-build-output.mjs packages/runtime-playwright packages/sdk
npm pack --dry-run --json --workspace @agentic-testing-harness/runtime-playwright
npm pack --dry-run --json --workspace @agentic-testing-harness/sdk
```

Expected: targeted SDK/runtime coverage and the full workspace PASS; lint, typecheck, and build are green; both packages contain production root entries only, with no tests, test-only helpers, internal subpaths, or live handles.

- [ ] **Step 5: Commit SDK composition**

```bash
git add packages/sdk packages/runtime-playwright package-lock.json tsconfig.json scripts/build.mjs scripts/assert-build-output.mjs
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
