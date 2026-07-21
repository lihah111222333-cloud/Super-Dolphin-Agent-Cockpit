import { expect, it } from "vitest";
import { readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  auditRpcContracts,
  collectHardcodedPayloadGuardFindingsFromSources,
  parseContractMatrixForTest,
  parseRpcMethodsForTest,
} from "./rpc-contract-audit.mjs";
import {
  REPO_ROOT,
  createShadowRepo,
  MATRIX_PATH,
  UNUSED_POLICY,
  createRuntimeDriftFixture,
  shadowMatrix,
} from "./rpc-audit-test-support.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it.each([
    ["missing", "export { createBackendApi } from './backend/backendApiFactoryThread.js'"],
    ["wrong source", "export { RPC_METHODS } from './backend/notRpcMethods.js'"],
    [
      "local definition",
      "export const RPC_METHODS = Object.freeze({ SHADOW_ONLY: 'shadow/only' })",
    ],
  ])("rejects a %s RPC_METHODS facade re-export", async (_label, facadeSource) => {
    const repoRoot = await createShadowRepo({
      "frontend-app/src/shared/api/backendApi.js": facadeSource,
    });

    await expect(auditRpcContracts({ repoRoot })).rejects.toThrow(
      "backendApi.js must named re-export RPC_METHODS from ./backend/backendRpcMethods.js exactly once",
    );
  });

  it("ignores payload guard names outside top-level Set declarations", () => {
    const findings = collectHardcodedPayloadGuardFindingsFromSources({
      frontendSource: `
        // const RPC_ALLOWED_PAYLOAD_KEYS = new Set(['comment'])
        const example = "const THREAD_START_ALLOWED_KEYS = new Set(['string'])"
        function nested() {
          const TURN_START_ALLOWED_KEYS = new Set(['nested'])
        }
        const NOT_ALLOWED_KEYS = Object.freeze(['wrong initializer'])
      `,
    });

    expect(findings).toEqual([]);
  });

  it("reports only top-level payload guard Set declarations", () => {
    const findings = collectHardcodedPayloadGuardFindingsFromSources({
      frontendSource: `
        const RPC_ALLOWED_PAYLOAD_KEYS = new Set(['rpc'])
        const THREAD_START_ALLOWED_KEYS = new Set(['thread'])
      `,
    });

    expect(findings).toEqual([
      "frontend-app/src/shared/api/backendApi.js:RPC_ALLOWED_PAYLOAD_KEYS",
      "frontend-app/src/shared/api/backendApi.js:THREAD_START_ALLOWED_KEYS",
    ]);
  });

  it("scans the runtime payload builder source for hardcoded payload guards", async () => {
    const builderPath = "frontend-app/src/shared/api/backend/backendApiFactoryThread.js";
    const builderSource = await readFile(join(REPO_ROOT, builderPath), "utf8");
    const repoRoot = await createShadowRepo({
      [builderPath]: `const THREAD_START_ALLOWED_KEYS = new Set(['cwd'])\n${builderSource}`,
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.hardcodedPayloadGuardFindings).toEqual([
      `${builderPath}:THREAD_START_ALLOWED_KEYS`,
    ]);
  });

  it("does not duplicate missing registry keys as missing response policies", async () => {
    const repoRoot = await createRuntimeDriftFixture();
    try {
      await writeFile(
        join(repoRoot, "frontend-app/src/shared/api/backend/backendRpcMethods.js"),
        [
          "export const RPC_METHODS = Object.freeze({",
          "  THREAD_START: 'thread/start',",
          "  RESPONSELESS_RPC: 'responseless/rpc',",
          "})",
        ].join("\n"),
        "utf8",
      );
      await writeFile(
        join(repoRoot, "frontend-app/src/shared/api/backendApi.contractMatrix.js"),
        [
          "function contract() {}",
          "export const RPC_CONTRACT_REGISTRY = Object.freeze({",
          "  THREAD_START: contract('THREAD_START', 'startThread', 'P1', 'thread', [], [], false, { responseValidator: 'threadStartResponse' }),",
          "})",
        ].join("\n"),
        "utf8",
      );

      const report = await auditRpcContracts({ repoRoot });

      expect(report.responseContractStrategies).toContainEqual({
        key: "RESPONSELESS_RPC",
        method: "responseless/rpc",
        matrixPolicy: "",
        frontendValidator: false,
      });
      expect(report.missingRegistryKeys).toContain("RESPONSELESS_RPC");
      expect(report.missingResponsePolicies).toEqual([]);
    } finally {
      await rm(repoRoot, { recursive: true, force: true });
    }
  });

  it("parses RPC methods and contract registry entries from AST fixtures", () => {
    const rpcMethodsSource = `
      export const RPC_METHODS = Object.freeze({
        THREAD_START: 'thread/start',
        TURN_START: 'turn/start',
      })
    `;
    const contractMatrixSource = `
      const TESTS = Object.freeze({ API: 'api.test.js' })
      function contract() {}
      export const RPC_CONTRACT_REGISTRY = Object.freeze({
        THREAD_START: contract(
          'THREAD_START', RPC_METHODS.THREAD_START, 'startThread', 'P0', 'thread',
          [TESTS.API], ['runtime lifecycle start'], false, { responseValidator: 'threadStartResponse' },
        ),
        TURN_START: contract(
          'TURN_START', RPC_METHODS.TURN_START, 'startTurn', 'P0', 'turn',
          [TESTS.API], ['runtime lifecycle start'], false, { responsePolicy: ${UNUSED_POLICY} },
        ),
      })
    `;

    expect(parseRpcMethodsForTest(rpcMethodsSource)).toEqual([
      { key: "THREAD_START", method: "thread/start" },
      { key: "TURN_START", method: "turn/start" },
    ]);
    expect(parseContractMatrixForTest(contractMatrixSource)).toEqual([
      {
        key: "THREAD_START",
        declaredKey: "THREAD_START",
        method: "",
        methodReferenceKey: "THREAD_START",
        facade: "startThread",
        level: "P0",
        responseValidator: "threadStartResponse",
        responsePolicy: null,
      },
      {
        key: "TURN_START",
        declaredKey: "TURN_START",
        method: "",
        methodReferenceKey: "TURN_START",
        facade: "startTurn",
        level: "P0",
        responseValidator: "",
        responsePolicy: {
          kind: "unused",
          productionScanRoots: ["frontend-app/src"],
          excludedGlobs: [],
        },
      },
    ]);
  });

  it("uses the production RPC method source and reports missing and mismatched matrix entries", async () => {
    const matrixPath = "frontend-app/src/shared/api/backendApi.contractMatrix.js";
    const productionMethodsPath = "frontend-app/src/shared/api/backend/backendRpcMethods.js";
    const matrixSource = await readFile(join(REPO_ROOT, matrixPath), "utf8");
    const repoRoot = await createShadowRepo({
      "frontend-app/src/shared/api/backendApi.js": `
        export { RPC_METHODS } from './backend/backendRpcMethods.js'
        function threadStartPayload() { return {} }
        function turnStartPayload() { return {} }
      `,
      [productionMethodsPath]: (
        await readFile(join(REPO_ROOT, productionMethodsPath), "utf8")
      ).replace("CONFIG_READ: 'config/read'", "CONFIG_READ: 'config/read-mismatch'"),
      [matrixPath]: matrixSource
        .replace(/\n\s*CONFIG_LSP_PROMPT_HINT_READ: contract\([^\n]+/, "")
        .replace("RPC_METHODS.CONFIG_READ", "'config/read'"),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.missingRegistryKeys).toContain("CONFIG_LSP_PROMPT_HINT_READ");
    expect(report.mismatchedRegistryMethods).toContainEqual({
      key: "CONFIG_READ",
      registryMethod: "config/read",
      rpcMethod: "config/read-mismatch",
    });
  }, 30000);

  it.each([
    ["missing", "", "must declare exactly one of responseValidator or responsePolicy"],
    ["blank validator", "{ responseValidator: '   ' }", "responseValidator must be non-blank"],
    [
      "blank passthrough reason",
      "{ responsePassthroughReason: '   ' }",
      "responsePassthroughReason is forbidden",
    ],
    [
      "one-character passthrough reason",
      "{ responsePassthroughReason: 'x' }",
      "responsePassthroughReason is forbidden",
    ],
    [
      "blanket passthrough reason",
      "{ responsePassthroughReason: 'passthrough for all' }",
      "responsePassthroughReason is forbidden",
    ],
  ])("rejects P0/P1 %s response policy", (_label, options, reason) => {
    expect(() => parseContractMatrixForTest(shadowMatrix(options))).toThrow(
      `RPC_CONTRACT_REGISTRY.CONFIG_READ ${reason}`,
    );
  });

  it("reports a declared response validator that is absent from the runtime validator mapping", async () => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix("{ responseValidator: 'doesNotExist' }", {
        key: "CONFIG_LSP_PROMPT_HINT_READ",
        facade: "readLspPromptHint",
      }),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.missingFrontendResponseValidators).toContainEqual({
      key: "CONFIG_LSP_PROMPT_HINT_READ",
      method: "config/lspPromptHint/read",
      responseValidator: "doesNotExist",
      runtimeResponseValidator: "lspPromptHintResponse",
    });
  }, 30000);

  it("reports an existing runtime validator replaced by a structured response policy", async () => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`, {
        key: "UI_STATE_GET",
        facade: "getThreadState",
      }),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.missingFrontendResponseValidators).toContainEqual({
      key: "UI_STATE_GET",
      method: "ui/state/get",
      responseValidator: "",
      runtimeResponseValidator: "uiStateResponse",
    });
  }, 30000);

  it("reports a runtime validator key that is absent from the contract registry", async () => {
    const validatorPath = "frontend-app/src/shared/api/response-validators/registry.js";
    const validatorSource = await readFile(join(REPO_ROOT, validatorPath), "utf8");
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`),
      [validatorPath]: validatorSource.replace(
        "return Object.freeze({",
        "return Object.freeze({\n    [methods.TOTALLY_UNKNOWN]: validateUIStateResponse,",
      ),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.missingFrontendResponseValidators).toContainEqual({
      key: "TOTALLY_UNKNOWN",
      method: "",
      responseValidator: "",
      runtimeResponseValidator: "uiStateResponse",
    });
  }, 30000);

  it("reports a passthrough facade that cannot be traced to a real backend API export", async () => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`, {
        facade: "totallyFake",
      }),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidFacadeLocators).toContainEqual({
      key: "CONFIG_READ",
      facade: "totallyFake",
      locator: "frontend-app/src/shared/api/backendApi.js",
    });
  }, 30000);

  it("reports a real backend facade that belongs to a different RPC key", async () => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`, {
        facade: "readBuiltinTools",
      }),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidFacadeLocators).toContainEqual({
      key: "CONFIG_READ",
      facade: "readBuiltinTools",
      locator: "frontend-app/src/shared/api/backendApi.js",
    });
  }, 30000);
});
