import { expect, it } from "vitest";
import { auditRpcContracts, parseContractMatrixForTest } from "./rpc-contract-audit.mjs";
import {
  createShadowRepo,
  MATRIX_PATH,
  UNUSED_POLICY,
  shadowMatrix,
  createPolicyShadow,
  ignoredResultPolicy,
  consumerValidatedPolicy,
  resultHandledPolicy,
  resultHandledConsumer,
  resultHandler,
  resultHandledRegression,
  ignoredResultRegression,
  publishedCallbackConsumer,
  publishedCallbackRegression,
  consumerValidatedRegression,
} from "./rpc-audit-test-support.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it("rejects result-handled metadata with a mismatched handler locator", async () => {
    const repoRoot = await createPolicyShadow({
      policy: resultHandledPolicy({ handlerSymbol: "otherHandler" }),
      consumer: resultHandledConsumer(),
      handler: resultHandler(),
      regression: resultHandledRegression(),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        kind: "result-handled",
        field: "handler",
      }),
    );
  }, 30000);

  it("rejects ignored-result policy when the consumer reads the result", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() {
          const result = await readConfig()
          return result.value
        }
      `,
      regression: ignoredResultRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        kind: "ignored-result",
        field: "consumer",
        reason: "consumer reads the RPC result",
      }),
    );
  }, 30000);

  it("accepts ignored-result policy with an unobserved awaited call", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() { await readConfig() }
      `,
      regression: ignoredResultRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it("accepts an exact published-callback action and direct regression proof", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ outcomeTarget: ["notices", "showTaskNotice"] }),
      consumer: publishedCallbackConsumer(),
      regression: publishedCallbackRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    [
      "publisher before facade",
      `notices.showTaskNotice('saved'); await facade.readConfig({ cwd: '/repo' })`,
    ],
    [
      "publisher in catch",
      `try { await facade.readConfig({ cwd: '/repo' }) } catch { notices.showTaskNotice('saved') }`,
    ],
    [
      "publisher in finally",
      `try { await facade.readConfig({ cwd: '/repo' }) } finally { notices.showTaskNotice('saved') }`,
    ],
    [
      "publisher in nested sibling",
      `await facade.readConfig({ cwd: '/repo' }); const later = () => notices.showTaskNotice('saved'); return later`,
    ],
    [
      "computed publisher",
      `await facade.readConfig({ cwd: '/repo' }); notices['showTaskNotice']('saved')`,
    ],
    [
      "optional publisher",
      `await facade.readConfig({ cwd: '/repo' }); notices?.showTaskNotice('saved')`,
    ],
    [
      "dynamic publisher object",
      `await facade.readConfig({ cwd: '/repo' }); const sink = notices; sink.showTaskNotice('saved')`,
    ],
    [
      "ambiguous publisher",
      `await facade.readConfig({ cwd: '/repo' }); notices.showTaskNotice('saved'); notices.showTaskNotice('again')`,
    ],
    [
      "block-shadowed publisher root",
      `
      await facade.readConfig({ cwd: '/repo' })
      { const notices = { showTaskNotice() {} }; notices.showTaskNotice('saved') }
    `,
    ],
    [
      "block-shadowed facade root",
      `
      { const facade = { readConfig: async () => undefined }; await facade.readConfig({ cwd: '/repo' }) }
      notices.showTaskNotice('saved')
    `,
    ],
  ])(
    "rejects published-callback production proof with %s",
    async (_label, body) => {
      const repoRoot = await createPolicyShadow({
        policy: ignoredResultPolicy({ outcomeTarget: ["notices", "showTaskNotice"] }),
        consumer: publishedCallbackConsumer(body),
        regression: publishedCallbackRegression(),
      });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          field: "consumer",
          reason: "consumer lacks the exact post-RPC published callback outcome",
        }),
      );
    },
    30000,
  );

  it.each([
    ["wrong import", { importStatement: "import { loadConfig as wrong } from './consumer.js'" }],
    ["shadowed consumer", { beforeCall: "const loadConfig = async () => undefined" }],
    ["rejected RPC mock", { mockMethod: "mockRejectedValue" }],
    ["non-malformed response", { response: "{ ok: true }" }],
    [
      "malformed sentinel on an unrelated same-named spy",
      {
        response: "{ ok: true }",
        beforeCall:
          "const unrelated = { readConfig: vi.fn().mockResolvedValue({ malformed: 'other-sentinel' }) }",
      },
    ],
    [
      "ambiguous spread facade source",
      {
        contextSetup: `
        const first = { facade: { readConfig: vi.fn().mockResolvedValue({ malformed: 'stale-sentinel' }) }, notices: { showTaskNotice: vi.fn() } }
        const second = { facade: { readConfig: vi.fn().mockResolvedValue({ ok: true }) }, notices: { showTaskNotice: vi.fn() } }
        const ctx = { ...first, ...second }
      `,
        assertions: `
        expect(first.facade.readConfig).toHaveBeenCalledWith({ cwd: '/repo' })
        expect(result).toBeUndefined()
        expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')
      `,
      },
    ],
    [
      "publisher assertion before action",
      {
        beforeCall:
          "expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')",
        assertions: `
        expect(ctx.facade.readConfig).toHaveBeenCalledWith({ cwd: '/repo' })
        expect(result).toBeUndefined()
      `,
      },
    ],
    [
      "unrelated publisher spy",
      {
        assertions: `
      expect(ctx.facade.readConfig).toHaveBeenCalledWith({ cwd: '/repo' })
      expect(result).toBeUndefined()
      expect(ctx.other.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')
    `,
      },
    ],
    [
      "no exact facade args assertion",
      {
        assertions: `
      expect(ctx.facade.readConfig).toHaveBeenCalled()
      expect(result).toBeUndefined()
      expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')
    `,
      },
    ],
    [
      "no undefined result assertion",
      {
        assertions: `
      expect(ctx.facade.readConfig).toHaveBeenCalledWith({ cwd: '/repo' })
      expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')
    `,
      },
    ],
  ])(
    "rejects published-callback regression proof with %s",
    async (_label, overrides) => {
      const repoRoot = await createPolicyShadow({
        policy: ignoredResultPolicy({ outcomeTarget: ["notices", "showTaskNotice"] }),
        consumer: publishedCallbackConsumer(),
        regression: publishedCallbackRegression(overrides),
      });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          field: "regressionTest",
        }),
      );
    },
    30000,
  );

  it("rejects consumer-validated policy without executable shape proof", async () => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function assertConfigShape(value) { return value.value }
        export async function loadConfig() {
          const result = await readConfig()
          assertConfigShape(result)
          return result.value
        }
      `,
      regression: consumerValidatedRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        kind: "consumer-validated",
        field: "shape",
        reason: "shape symbol lacks executable narrowing",
      }),
    );
  }, 30000);

  it("accepts consumer-validated policy with dominating executable shape proof", async () => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function assertConfigShape(value) {
          if (!value || typeof value !== 'object' || typeof value.value !== 'string') {
            throw new TypeError('invalid config')
          }
        }
        export async function loadConfig() {
          const result = await readConfig()
          assertConfigShape(result)
          return result.value
        }
      `,
      regression: consumerValidatedRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    [
      "callback-local result",
      `queueMicrotask(async () => { const result = await readConfig(); consume(result.value) }); const result = {}; assertConfigShape(result)`,
    ],
    [
      "nested function result",
      `async function nested() { const result = await readConfig(); consume(result.value) }; const result = {}; assertConfigShape(result)`,
    ],
    [
      "callback-local validator",
      `const result = await readConfig(); queueMicrotask(() => assertConfigShape(result)); consume(result.value)`,
    ],
  ])(
    "rejects consumer validation crossing lexical scope: %s",
    async (_label, body) => {
      const repoRoot = await createPolicyShadow({
        policy: consumerValidatedPolicy(),
        consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function assertConfigShape(value) {
          if (!value || typeof value !== 'object' || typeof value.value !== 'string') throw new TypeError('invalid config')
        }
        function consume(value) { return value }
        export async function loadConfig() { ${body} }
      `,
        regression: consumerValidatedRegression(),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          kind: "consumer-validated",
          field: "shape",
        }),
      );
    },
    30000,
  );

  it.each([ignoredResultPolicy(), consumerValidatedPolicy(), UNUSED_POLICY])(
    "does not allow a response policy to replace a runtime validator",
    async (responsePolicy) => {
      const repoRoot = await createShadowRepo({
        [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${responsePolicy} }`)
          .replaceAll("CONFIG_READ", "UI_STATE_GET")
          .replace("'readConfig'", "'getThreadState'"),
      });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.missingFrontendResponseValidators).toContainEqual({
        key: "UI_STATE_GET",
        method: "ui/state/get",
        responseValidator: "",
        runtimeResponseValidator: "uiStateResponse",
      });
    },
    30000,
  );

  it("rejects a response validator and response policy union", () => {
    expect(() =>
      parseContractMatrixForTest(
        shadowMatrix(`{
      responseValidator: 'uiStateResponse',
      responsePolicy: ${UNUSED_POLICY},
    }`),
      ),
    ).toThrow(
      "RPC_CONTRACT_REGISTRY.CONFIG_READ must declare exactly one of responseValidator or responsePolicy",
    );
  });

  it("handles export specifier variants before reading local bindings", async () => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`),
      "frontend-app/src/shared/api/backendApi.js": `
        export * as backendMethods from './backend/backendRpcMethods.js'
        export { default as createBackendApi } from './backend/backendApiFactoryThread.js'
        export { RPC_METHODS } from './backend/backendRpcMethods.js'
      `,
    });

    await expect(auditRpcContracts({ repoRoot })).resolves.toEqual(
      expect.objectContaining({
        invalidResponsePolicyEvidence: [],
      }),
    );
  }, 30000);
});
