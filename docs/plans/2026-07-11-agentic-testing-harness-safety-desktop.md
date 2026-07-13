# Agentic Testing Harness Safety and Desktop Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Foundation 之上建立可证明的 hard-isolated 写探索，并交付真实 Electron 与严格 Wails mock target adapters。

**Architecture:** Evidence 在采集边界脱敏并受预算控制；Docker provider 生成与 session nonce 绑定的 attestation；Core 只有验证 attestation 后才授权写动作；Electron 和 Wails mock adapters 复用同一长连接 CLI/SDK，不形成第二套 runner。

**Tech Stack:** Foundation stack、Docker CLI/Engine、Playwright Electron、Electron fixture、TypeBox contracts、Vitest adversarial/integration tests、GitHub Actions matrix。

**Verification Surface:** redaction/artifact store、Docker provider、Core write policy、Electron adapter/fixture、Wails mock adapter/fixture、adversarial tests、Linux hard-isolation CI、macOS/Windows adapter contracts。

---

## Preconditions

Foundation 本地基线固定为独立仓库 commit `d79431a4529bb0c2129c9d91070b5f74d0dd25e7`。该基线已在 clean `npm ci` 与 Chromium provisioning 后通过 `npm run verify`：923 项 unit 通过、1 项 Windows-only 用例在 macOS 跳过、Skill 3/3、Web E2E 5/5、package smoke 30/30，且七包 pack closure 通过。

独立仓库当前没有 Git remote，所以 hosted Linux/macOS/Windows 与 Node `22.13.0`/`24`/`26` lanes 尚无可引用 run。配置 remote 后必须先让 commit `d79431a4529bb0c2129c9d91070b5f74d0dd25e7` 的完整 CI 矩阵通过；在此之前不得声称跨平台 Foundation 已验收，也不得开放 Safety/Desktop 实现。

满足 hosted CI 前置条件后，在 `/Users/l4place/Documents/agentic-testing-harness` 从上述 commit 创建新分支 `codex/safety-desktop`，不得从其他未验证工作树状态分支。

### Task 1: Harden evidence redaction and artifact budgets

**Files:**
- Create: `packages/contracts/src/evidence.ts`
- Modify: `packages/contracts/src/index.ts`
- Modify: `packages/core/src/redactor.ts`
- Create: `packages/core/src/artifact-store.ts`
- Create: `packages/runtime-playwright/src/capture-screenshot.ts`
- Modify: `packages/runtime-playwright/src/browser-runtime.ts`
- Test: `packages/core/src/redactor.test.ts`
- Test: `packages/core/src/artifact-store.test.ts`
- Test: `packages/runtime-playwright/src/capture-screenshot.test.ts`

- [ ] **Step 1: Write failing secret and budget tests**

```ts
it('never emits configured secrets in nested evidence', () => {
  const redactor = new Redactor({ sensitiveFields: ['apiKey', 'authorization'] });
  const output = JSON.stringify(redactor.redact({
    apiKey: 'audit-secret-marker-123',
    nested: { authorization: 'Bearer audit-secret-marker-123' },
    url: 'https://fixture.invalid/?token=audit-secret-marker-123',
  }));
  expect(output).not.toContain('audit-secret-marker-123');
});

it('rejects an artifact larger than the remaining budget', async () => {
  const store = await ArtifactStore.create({ maxSessionBytes: 8, maxArtifactBytes: 8 });
  await expect(store.write('dom.json', Buffer.alloc(9))).rejects.toThrow(/evidence budget exceeded/);
});

it('withholds a screenshot when a sensitive element cannot be masked', async () => {
  const result = await captureScreenshot(page, { sensitiveLocators: [ambiguousPasswordLocator] });
  expect(result).toEqual({ exported: false, reason: 'sensitive-mask-unresolved' });
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/core/src/redactor.test.ts packages/core/src/artifact-store.test.ts packages/runtime-playwright/src/capture-screenshot.test.ts`

Expected: FAIL because hardened evidence components do not exist.

- [ ] **Step 3: Implement collection-boundary redaction**

Use field classification plus conservative token/header/query patterns. Sensitive values become `{ redacted: true, length, sessionToken }`; `sessionToken` uses a per-session random key destroyed during cleanup. Redact DOM, ARIA, console, URL query, network headers, bridge payloads, action receipts, candidate YAML, and Markdown inputs before any file write. Screenshot capture resolves every sensitive locator uniquely and applies Playwright masks; unresolved masks suppress export rather than falling back to an unmasked image.

Artifact store writes through a single API, rejects path traversal and symlink destinations, and tracks `maxArtifactBytes`, `maxSessionBytes`, retained count, and dropped count.

- [ ] **Step 4: Verify GREEN and scan generated fixtures**

```bash
npm test -- packages/core/src/redactor.test.ts packages/core/src/artifact-store.test.ts packages/runtime-playwright/src/capture-screenshot.test.ts
npm run typecheck
if rg -n "audit-secret-marker-123" .tmp/test-runs; then exit 1; fi
```

Expected: tests PASS; final `rg` returns no matches.

- [ ] **Step 5: Commit evidence hardening**

```bash
git add packages/contracts packages/core packages/runtime-playwright
git diff --cached --check
git commit -m "feat: 强化 harness 证据脱敏与预算"
```

### Task 2: Establish the adversarial regression suite

**Files:**
- Create: `tests/adversarial/helpers/fixture-server.ts`
- Create: `tests/adversarial/helpers/run-session.ts`
- Create: `tests/adversarial/hidden-dom.test.ts`
- Create: `tests/adversarial/network-egress.test.ts`
- Create: `tests/adversarial/secret-evidence.test.ts`
- Create: `tests/adversarial/symlink-escape.test.ts`
- Create: `tests/adversarial/stale-revision.test.ts`
- Create: `tests/adversarial/wrong-target.test.ts`
- Create: `tests/adversarial/stdout-protocol.test.ts`
- Modify: `package.json`

- [ ] **Step 1: Add the adversarial command and regression probes**

Add script `"test:adversarial": "vitest run tests/adversarial"`. Each test asserts a specific failure code rather than merely a non-zero exit.

```ts
it('does not describe display-none content as visible', async () => {
  const observation = await observeHiddenFixture();
  expect(observation.elements).toEqual([]);
});
```

- [ ] **Step 2: Run probes against the current Foundation**

Run: `npm run test:adversarial`

Expected: all probes PASS against the completed Foundation plus Task 1. A failure means a Foundation invariant regressed and must be fixed before committing the suite.

- [ ] **Step 3: Wire probes through public boundaries**

Tests that model an Agent use `ath session stream`; unit probes use exported public SDK contracts. No test imports a private Playwright page or mutates internal Core state. External network tests run two loopback servers and require the disallowed server hit count to remain zero.

- [ ] **Step 4: Run the stable subset and commit tests**

```bash
npm run test:adversarial
npm run typecheck
git add tests/adversarial package.json package-lock.json
git diff --cached --check
git commit -m "test: 建立 harness 安全对抗基线"
```

Expected: all current adversarial probes pass and no critical case is disabled.

### Task 3: Implement Docker hard isolation and attestation

**Files:**
- Create: `packages/isolation/src/command-runner.ts`
- Create: `packages/isolation/src/docker-provider.ts`
- Create: `packages/isolation/src/attestation.ts`
- Create: `packages/isolation/src/docker-policy.ts`
- Modify: `packages/isolation/src/index.ts`
- Test: `packages/isolation/src/docker-provider.test.ts`
- Test: `tests/integration/docker-isolation.test.ts`
- Create: `examples/docker-target/Dockerfile`
- Create: `examples/docker-target/entrypoint.sh`
- Modify: `package.json`

- [ ] **Step 1: Write failing attestation tests**

```ts
it('binds attestation to image, policies, and session nonce', async () => {
  const receipt = await provider.provision(writeConfig({ nonce: 'session-1' }));
  expect(verifyAttestation(receipt.attestation, {
    nonce: 'session-1', imageDigest: receipt.imageDigest,
    mountPolicyHash: hashMountPolicy(noHostWrites),
    networkPolicyHash: hashNetworkPolicy(targetOnlyNetwork),
  })).toEqual({ verified: true });
});

it('rejects docker socket and writable host mounts', () => {
  expect(() => validateDockerPolicy({ mounts: ['/var/run/docker.sock:/sock'] })).toThrow(/docker socket/);
  expect(() => validateDockerPolicy({ mounts: ['/Users:/host:rw'] })).toThrow(/writable host mount/);
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/isolation/src/docker-provider.test.ts`

Expected: FAIL because provider and attestation do not exist.

- [ ] **Step 3: Implement the Docker provider**

Use argument arrays with `spawn`, never shell concatenation. `docker create` sets read-only rootfs where supported, tmpfs for writable runtime paths, no Docker socket, no host HOME, dropped capabilities, resource limits, and an internal target-only network. Obtain container ID, immutable image digest, mounts, and networks from `docker inspect`; calculate policy hashes from canonical JSON; bind the random nonce. Artifact export uses `docker cp` after the container stops and then passes through `ArtifactStore`.

- [ ] **Step 4: Run unit and opt-in Docker integration tests**

```bash
npm test -- packages/isolation/src/docker-provider.test.ts
ATH_RUN_DOCKER_TESTS=1 npm test -- tests/integration/docker-isolation.test.ts
```

Expected: unit and integration tests PASS; integration proves host sentinel files unchanged and container/network removed.

- [ ] **Step 5: Commit Docker isolation**

```bash
git add packages/isolation examples/docker-target tests/integration package.json package-lock.json
git diff --cached --check
git commit -m "feat: 增加 Docker 硬隔离与证明"
```

### Task 4: Gate write sessions on verified hard isolation

**Files:**
- Modify: `packages/core/src/policy.ts`
- Modify: `packages/sdk/src/harness-session.ts`
- Modify: `packages/cli/src/commands/doctor.ts`
- Test: `packages/core/src/policy.test.ts`
- Test: `packages/sdk/src/write-session.test.ts`
- Test: `tests/e2e/web-write-session.test.ts`

- [ ] **Step 1: Write failing write-gate tests**

```ts
it('rejects write mode before target launch when attestation is absent', async () => {
  await expect(startSession(webFixtureConfig('write', { isolation: 'light' })))
    .rejects.toMatchObject({ code: 'ISOLATION_ERROR' });
  expect(targetLaunchCount()).toBe(0);
});

it('allows a write action only with matching verified attestation', () => {
  expect(authorizeAction(writeContext({ nonce: 's1', attestationNonce: 's1', verified: true })))
    .toEqual({ allowed: true });
  expect(authorizeAction(writeContext({ nonce: 's1', attestationNonce: 's2', verified: true })))
    .toMatchObject({ allowed: false, code: 'ISOLATION_ERROR' });
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/core/src/policy.test.ts packages/sdk/src/write-session.test.ts`

Expected: mismatched attestation case FAILS before implementation.

- [ ] **Step 3: Implement fail-fast write startup**

SDK selects Docker provider for `mode=write`, validates attestation before adapter launch, and revalidates it before every action. A container disappearance, mount/network drift, or nonce mismatch transitions the session to `infrastructure_failed` and prevents further actions. `ath doctor --json` reports Docker availability and hard-isolation readiness separately.

- [ ] **Step 4: Verify hard-isolated Web write**

```bash
npm test -- packages/core/src/policy.test.ts packages/sdk/src/write-session.test.ts
ATH_RUN_DOCKER_TESTS=1 npm test -- tests/e2e/web-write-session.test.ts
```

Expected: all PASS; Save mutates only disposable target state and exported evidence contains no secret marker.

- [ ] **Step 5: Commit write gating**

```bash
git add packages/core packages/sdk packages/cli tests/e2e
git diff --cached --check
git commit -m "feat: 以硬隔离证明授权写会话"
```

### Task 5: Implement the real Electron adapter and fixture

**Files:**
- Create: `packages/adapter-electron/package.json`
- Create: `packages/adapter-electron/tsconfig.json`
- Create: `packages/adapter-electron/src/electron-adapter.ts`
- Create: `packages/adapter-electron/src/window-identity.ts`
- Create: `packages/adapter-electron/src/index.ts`
- Test: `packages/adapter-electron/src/electron-adapter.test.ts`
- Create: `examples/electron-fixture/package.json`
- Create: `examples/electron-fixture/src/main.ts`
- Create: `examples/electron-fixture/src/preload.ts`
- Create: `examples/electron-fixture/src/index.html`
- Create: `examples/electron-fixture/agentic-testing-harness.config.ts`
- Create: `tests/e2e/electron-session.test.ts`
- Modify: `package.json`

- [ ] **Step 1: Write failing lifecycle and identity tests**

```ts
it('launches a fresh Electron process with disposable user data', async () => {
  const adapter = await startElectronFixture({ sessionNonce: 'electron-s1' });
  expect(adapter.launchReceipt.userDataDir).toContain('agentic-testing-harness');
  expect(await adapter.verifyIdentity()).toMatchObject({ nonce: 'electron-s1' });
  await adapter.shutdown();
  await expect(access(adapter.launchReceipt.userDataDir)).rejects.toThrow();
});

it('refuses write-mode Electron without hard isolation', async () => {
  await expect(startElectronFixture({ mode: 'write', isolation: 'light' }))
    .rejects.toMatchObject({ code: 'ISOLATION_ERROR' });
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/adapter-electron/src/electron-adapter.test.ts tests/e2e/electron-session.test.ts`

Expected: FAIL because adapter and fixture do not exist.

- [ ] **Step 3: Implement the Electron lifecycle**

Declare `playwright` as a direct dependency and `electron` as a fixture dev dependency. Launch with Playwright `_electron.launch`, pass a disposable `--user-data-dir`, inject `ATH_TARGET_NONCE`, and verify the first window through an exact nonce exposed by fixture preload. Do not attach to an existing process. Expose the renderer only through the same restricted runtime interface as Web. Shutdown closes windows, waits for the application process, then validates process and path cleanup.

- [ ] **Step 4: Run unit and E2E tests**

```bash
npm test -- packages/adapter-electron/src/electron-adapter.test.ts
npm test -- tests/e2e/electron-session.test.ts
```

Expected: tests PASS; read navigation works in a real Electron process and write mode requires Docker.

- [ ] **Step 5: Commit Electron support**

```bash
git add packages/adapter-electron examples/electron-fixture tests/e2e/electron-session.test.ts package.json package-lock.json
git diff --cached --check
git commit -m "feat: 增加真实 Electron target 适配"
```

### Task 6: Implement the strict generic Wails mock adapter

**Files:**
- Create: `packages/contracts/src/wails-mock.ts`
- Modify: `packages/contracts/src/index.ts`
- Create: `packages/adapter-wails-mock/package.json`
- Create: `packages/adapter-wails-mock/tsconfig.json`
- Create: `packages/adapter-wails-mock/src/install-bridge.ts`
- Create: `packages/adapter-wails-mock/src/mock-state.ts`
- Create: `packages/adapter-wails-mock/src/wails-mock-adapter.ts`
- Create: `packages/adapter-wails-mock/src/index.ts`
- Test: `packages/adapter-wails-mock/src/wails-mock-adapter.test.ts`
- Test: `tests/adversarial/mock-missing.test.ts`
- Create: `examples/wails-mock-fixture/src/server.ts`
- Create: `examples/wails-mock-fixture/src/app.html`
- Create: `examples/wails-mock-fixture/agentic-testing-harness.config.ts`
- Create: `tests/e2e/wails-mock-session.test.ts`
- Modify: `package.json`

- [ ] **Step 1: Write failing strictness tests**

```ts
it('fails when mock state is absent instead of accepting null', async () => {
  const session = await startWailsFixture({ installMock: false });
  await expect(session.finish()).rejects.toMatchObject({ code: 'TARGET_ERROR' });
});

it('fails unknown RPC and exact-call oracle mismatches', async () => {
  const session = await startWailsFixture({ allowedRPCs: ['ui/preferences/set'] });
  await session.invokeRPC('thread/delete', {});
  await expect(session.finish()).rejects.toMatchObject({ code: 'ORACLE_FAILED' });
});

it('does not pass successful non-target network traffic', async () => {
  const session = await startWailsFixture();
  await expect(session.openExternalSocket(externalURL)).rejects.toThrow(/network policy blocked/);
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/adapter-wails-mock/src/wails-mock-adapter.test.ts tests/e2e/wails-mock-session.test.ts`

Expected: FAIL because the strict adapter does not exist.

- [ ] **Step 3: Implement one generic bridge truth source**

The consumer supplies exact method schemas, response fixtures, allowed side-effect descriptors, and required call oracles. Install the bridge with `context.addInitScript` before application code. `readMockState` rejects absent or schema-invalid state. Unknown methods and payloads are recorded as failures and receive an error response. Adapter invariants fail on unhandled RPCs, unexpected calls, missing expected calls, sandbox violations, or mock absence. Do not embed Super-Dolphin method names.

- [ ] **Step 4: Complete all mock adversarial tests**

```bash
npm test -- packages/adapter-wails-mock
npm test -- tests/e2e/wails-mock-session.test.ts
npm run test:adversarial
```

Expected: all PASS, including `mock-missing.test.ts`; there are no skipped critical tests.

- [ ] **Step 5: Commit Wails mock support**

```bash
git add packages/contracts packages/adapter-wails-mock examples/wails-mock-fixture tests/e2e tests/adversarial package.json package-lock.json
git diff --cached --check
git commit -m "feat: 增加严格通用 Wails mock 适配"
```

### Task 7: Enforce oracle evaluation and three-run promotion

**Files:**
- Create: `packages/core/src/oracle-evaluator.ts`
- Create: `packages/core/src/promotion.ts`
- Modify: `packages/core/src/candidate.ts`
- Modify: `packages/cli/src/commands/replay.ts`
- Test: `packages/core/src/oracle-evaluator.test.ts`
- Test: `packages/core/src/promotion.test.ts`
- Test: `tests/e2e/candidate-promotion.test.ts`

- [ ] **Step 1: Write failing promotion tests**

```ts
it('requires three fresh clean replay receipts', () => {
  expect(evaluatePromotion([pass('env-1'), pass('env-2')])).toEqual({ eligible: false, reason: 'need-3-clean-runs' });
  expect(evaluatePromotion([pass('env-1'), pass('env-2'), pass('env-3')])).toEqual({ eligible: true });
});

it('never promotes an Agent-only success claim', () => {
  expect(evaluateOracle({ type: 'agentClaim', value: 'passed' }, evidence)).toEqual({ passed: false });
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- packages/core/src/oracle-evaluator.test.ts packages/core/src/promotion.test.ts`

Expected: FAIL because evaluators do not exist.

- [ ] **Step 3: Implement deterministic oracles and promotion receipts**

Support URL, visible element, exact bridge/RPC multiset, sandbox file snapshot, process state, and adapter invariant oracles. Each replay receipt records a fresh isolation instance ID and target identity. Three distinct clean instances are mandatory. Promotion emits eligibility evidence only; it never edits CI configuration or promoted scenario files.

- [ ] **Step 4: Verify GREEN and commit**

```bash
npm test -- packages/core/src/oracle-evaluator.test.ts packages/core/src/promotion.test.ts
ATH_RUN_DOCKER_TESTS=1 npm test -- tests/e2e/candidate-promotion.test.ts
git add packages/core packages/cli tests/e2e/candidate-promotion.test.ts
git diff --cached --check
git commit -m "feat: 增加候选重放裁决与晋升证明"
```

### Task 8: Complete cross-platform and hard-isolation CI gates

**Files:**
- Modify: `.github/workflows/ci.yml`
- Create: `.github/workflows/nightly.yml`
- Create: `tests/package/direct-dependencies.test.ts`
- Create: `tests/integration/resource-budgets.test.ts`
- Modify: `README.md`
- Modify: `package.json`

- [ ] **Step 1: Write failing packaging and resource tests**

```ts
it('declares every directly imported runtime package', async () => {
  expect(await undeclaredDirectImports()).toEqual([]);
});

it('stops a run at configured action and evidence budgets', async () => {
  const result = await runBudgetFixture({ maxActions: 2, maxEvidenceBytes: 1024 });
  expect(result.status).toBe('policy_blocked');
  expect(result.droppedCount).toBeGreaterThanOrEqual(0);
});
```

- [ ] **Step 2: Verify RED**

Run: `npm test -- tests/package/direct-dependencies.test.ts tests/integration/resource-budgets.test.ts`

Expected: FAIL until scripts and package declarations are complete.

- [ ] **Step 3: Implement CI matrix**

Add root scripts `test:docker`, `test:e2e:electron`, `test:e2e:wails-mock`, and `test:adversarial`. Linux required jobs install Chromium and run Docker hard-isolation tests. macOS and Windows install Chromium and run unit, Web, Electron, Wails mock contract, and fresh-package smoke. Nightly runs long sessions and resource budgets; no required job relies on a developer browser cache.

- [ ] **Step 4: Run the complete Safety/Desktop gate**

```bash
npm run verify
ATH_RUN_DOCKER_TESTS=1 npm run test:docker
npm run test:e2e:electron
npm run test:e2e:wails-mock
npm run test:adversarial
npm pack --dry-run
```

Expected: all commands exit 0; evidence scan finds no secret markers; Docker reports no leftover container or network.

- [ ] **Step 5: Commit Safety/Desktop completion**

```bash
git add .github README.md package.json package-lock.json tests/package tests/integration
git diff --cached --check
git commit -m "ci: 完成桌面与硬隔离验证矩阵"
```
