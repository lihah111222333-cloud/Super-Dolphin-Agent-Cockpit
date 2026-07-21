import { describe, expect, it, onTestFinished } from "vitest";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import {
  auditRpcContracts,
  astReferencesFacadeForTest,
  collectFrontendPayloadKeysFromSource,
  collectHardcodedPayloadGuardFindingsFromSources,
  collectPayloadRegistryDrift,
  collectSidebarRequiredFieldFindingsFromSources,
  parseContractMatrixForTest,
  parseRpcMethodsForTest,
} from "./rpc-contract-audit.mjs";

export const REPO_ROOT = resolve(import.meta.dirname, "../..");
export const SHADOW_FILES = [
  "internal/contract/rpc_handler.go",
  "internal/module/thread/rpc_types.go",
  "internal/module/turn/rpc_types.go",
  "internal/module/uistate/state.go",
  "frontend-app/src/shared/api/backendApi.js",
  "frontend-app/src/shared/api/backend/backendRpcMethods.js",
  "frontend-app/src/shared/api/backendApi.contractMatrix.js",
  "frontend-app/src/shared/api/response-validators/registry.js",
  "frontend-app/src/shared/api/response-validators/runtime/sidebar-state.js",
  "frontend-app/src/shared/api/backend/backendApiFactoryCore.js",
  "frontend-app/src/shared/api/backend/backendApiFactoryOps.js",
  "frontend-app/src/shared/api/backend/backendApiFactoryThread.js",
  "frontend-app/src/shared/api/wails/wailsBridgeConstants.js",
  "frontend-app/src/shared/api/wails/wailsBridgeRpc.js",
  "frontend-app/src/shared/api/wails/wailsBridgeTraceTransport.js",
  "frontend-app/src/pages/files/services/filesPageService.js",
  "frontend-app/src/pages/memory/services/memoryPageService.js",
  "frontend-app/src/pages/observability/services/observabilityPageService.js",
  "frontend-app/src/pages/prompts/services/promptPageService.js",
  "frontend-app/scripts/rpc-contract-audit.mjs",
  "frontend-app/scripts/rpc-audit/audit-config.mjs",
  "frontend-app/scripts/rpc-audit/audit-evidence.mjs",
  "frontend-app/scripts/rpc-audit/audit-runner.mjs",
  "frontend-app/scripts/rpc-audit/ast-parsing.mjs",
  "frontend-app/scripts/rpc-audit/backend-go-contracts.mjs",
  "frontend-app/scripts/rpc-audit/backend-go-support.mjs",
  "frontend-app/scripts/rpc-audit/backend-go.mjs",
  "frontend-app/scripts/rpc-audit/facade-call-provenance.mjs",
  "frontend-app/scripts/rpc-audit/facade-binding-provenance.mjs",
  "frontend-app/scripts/rpc-audit/published-callback-regression-evidence.mjs",
  "frontend-app/scripts/rpc-audit/response-policy-resolution.mjs",
  "frontend-app/scripts/rpc-audit/source-index-shape.mjs",
  "frontend-app/scripts/rpc-audit/state-owner-evidence.mjs",
  "frontend-app/scripts/rpc-audit/turn-interrupt-runtime-checks.mjs",
  "frontend-app/scripts/rpc-audit/turn-interrupt-runtime-evidence.mjs",
  "frontend-app/scripts/rpc-audit/turn-interrupt-injection-evidence.mjs",
  "frontend-app/scripts/rpc-audit/frontend-payload-discovery.mjs",
  "frontend-app/scripts/rpc-audit/registry.mjs",
  "frontend-app/scripts/rpc-audit/report.mjs",
  "frontend-app/scripts/rpc-audit/ignored-result-policy-evidence.mjs",
  "frontend-app/scripts/rpc-audit/state-dismissal-evidence.mjs",
  "frontend-app/scripts/rpc-audit/ui-outcome-evidence.mjs",
  "frontend-app/scripts/rpc-audit/turn-interrupt-regression-evidence.mjs",
  "frontend-app/scripts/rpc-audit/response-policy-regression-evidence.mjs",
  "frontend-app/scripts/rpc-audit/response-policy-locators.mjs",
  "frontend-app/scripts/rpc-audit/source-index.mjs",
];
export const SHADOW_GO_FILES = [
  "internal/contract/rpc_handler.go",
  "internal/module/uistate/state.go",
  "internal/module/thread/rpc_types.go",
  "internal/module/turn/rpc_types.go",
];

export async function createShadowRepo(overrides) {
  const repoRoot = await mkdtemp(join(tmpdir(), "rpc-contract-audit-"));
  onTestFinished(() => rm(repoRoot, { recursive: true, force: true }));
  await mkdir(join(repoRoot, "cmd"), { recursive: true });
  for (const filePath of SHADOW_GO_FILES) {
    const target = join(repoRoot, filePath);
    await mkdir(dirname(target), { recursive: true });
    await writeFile(target, await readFile(join(REPO_ROOT, filePath), "utf8"));
  }
  for (const filePath of new Set([...SHADOW_FILES, ...Object.keys(overrides)])) {
    const target = join(repoRoot, filePath);
    await mkdir(dirname(target), { recursive: true });
    let source = overrides[filePath] ?? (await readFile(join(REPO_ROOT, filePath), "utf8"));
    if (filePath === "frontend-app/src/shared/api/backendApi.contractMatrix.js") {
      source = shadowStructuredMatrix(source);
    }
    await writeFile(target, source);
  }
  return repoRoot;
}

export const MATRIX_PATH = "frontend-app/src/shared/api/backendApi.contractMatrix.js";
export const CONSUMER_PATH = "frontend-app/src/pages/audit-fixture/consumer.js";
export const HANDLER_PATH = "frontend-app/src/pages/audit-fixture/resultHandler.js";
export const REGRESSION_PATH = "frontend-app/src/pages/audit-fixture/consumer.test.js";
export const UNUSED_POLICY = `{
  kind: 'unused',
  productionScanRoots: ['frontend-app/src'],
  excludedGlobs: [],
}`;
export function shadowStructuredMatrix(source) {
  return source.replace(
    /\{\s*responsePassthroughReason:\s*'[^'\n]*'\s*\}/g,
    `{ responsePolicy: ${UNUSED_POLICY} }`,
  );
}

export async function createRuntimeDriftFixture() {
  return createShadowRepo({});
}

export function shadowMatrix(
  options = "",
  { key = "CONFIG_READ", facade = "readConfig", level = "P1" } = {},
) {
  return `
    function contract() {}
    export const RPC_CONTRACT_REGISTRY = Object.freeze({
      ${key}: contract(
        '${key}',
        RPC_METHODS.${key},
        '${facade}',
        '${level}',
        'config',
        [],
        [],
        false,
        ${options ? `${options},` : ""}
      ),
    })
  `;
}

export async function createPolicyShadow({ policy, consumer = "", handler = "", regression = "" }) {
  return createShadowRepo({
    [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${policy} }`),
    [CONSUMER_PATH]: consumer,
    [HANDLER_PATH]: handler,
    [REGRESSION_PATH]: regression,
  });
}

export function ignoredResultPolicy({
  consumerPath = CONSUMER_PATH,
  consumerSymbol = "loadConfig",
  consumerVisibility = "",
  outcomeTarget = null,
  regressionPath = REGRESSION_PATH,
  regressionSymbol = "keeps config result irrelevant",
} = {}) {
  return `{
    kind: 'ignored-result',
    consumer: { path: '${consumerPath}', symbol: '${consumerSymbol}'${consumerVisibility ? `, visibility: '${consumerVisibility}'` : ""} },
    ${outcomeTarget ? `outcome: { kind: 'published-callback', target: [${outcomeTarget.map((part) => `'${part}'`).join(", ")}] },` : ""}
    regressionTest: { path: '${regressionPath}', symbol: '${regressionSymbol}' },
  }`;
}

export function consumerValidatedPolicy({
  consumerPath = CONSUMER_PATH,
  consumerSymbol = "loadConfig",
  shapePath = CONSUMER_PATH,
  shapeSymbol = "assertConfigShape",
  regressionPath = REGRESSION_PATH,
  regressionSymbol = "rejects malformed config",
} = {}) {
  return `{
    kind: 'consumer-validated',
    consumer: { path: '${consumerPath}', symbol: '${consumerSymbol}' },
    shape: { path: '${shapePath}', symbol: '${shapeSymbol}' },
    regressionTest: { path: '${regressionPath}', symbol: '${regressionSymbol}' },
  }`;
}

export function resultHandledPolicy({
  consumerPath = CONSUMER_PATH,
  consumerSymbol = "interruptTurn",
  handlerPath = HANDLER_PATH,
  handlerSymbol = "handleInterruptResult",
  regressionPath = REGRESSION_PATH,
  regressionSymbol = "warns when interrupt is rejected",
} = {}) {
  return `{
    kind: 'result-handled',
    consumer: { path: '${consumerPath}', symbol: '${consumerSymbol}' },
    handler: { path: '${handlerPath}', symbol: '${handlerSymbol}' },
    regressionTest: { path: '${regressionPath}', symbol: '${regressionSymbol}' },
  }`;
}

export function resultHandledConsumer({
  facade = "readConfig",
  argument = "result",
  ignored = false,
} = {}) {
  return `
    import { ${facade} } from '../../shared/api/backendApi.js'
    import { handleInterruptResult } from './resultHandler.js'
    export async function interruptTurn() {
      const result = await ${facade}()
      ${ignored ? "" : `return handleInterruptResult(${argument})`}
    }
  `;
}

export function resultHandler({
  body = `
  if (!result.ok) {
    console.warn(result.error)
    return false
  }
  return true
`,
} = {}) {
  return `
    export function handleInterruptResult(result) {
      ${body}
    }
  `;
}

export function resultHandledRegression({
  mockFacade = "readConfig",
  assertion = "expect(warn).toHaveBeenCalledWith('interrupt denied')",
  response = "{ ok: false, error: 'interrupt denied' }",
  consumerInvocation = "await interruptTurn()",
  beforeAssertion = "",
  extraImports = "",
} = {}) {
  return `
    import { vi } from 'vitest'
    import { interruptTurn } from './consumer.js'
    ${extraImports}
    vi.mock('../../shared/api/backendApi.js', () => ({
      ${mockFacade}: vi.fn().mockResolvedValue(${response}),
    }))
    it('warns when interrupt is rejected', async () => {
      const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
      ${consumerInvocation}
      ${beforeAssertion}
      ${assertion}
    })
  `;
}

export {
  describe,
  expect,
  it,
  onTestFinished,
  spawnSync,
  mkdtemp,
  mkdir,
  readFile,
  realpath,
  rm,
  symlink,
  writeFile,
  tmpdir,
  dirname,
  join,
  resolve,
  auditRpcContracts,
  astReferencesFacadeForTest,
  collectFrontendPayloadKeysFromSource,
  collectHardcodedPayloadGuardFindingsFromSources,
  collectPayloadRegistryDrift,
  collectSidebarRequiredFieldFindingsFromSources,
  parseContractMatrixForTest,
  parseRpcMethodsForTest,
};
