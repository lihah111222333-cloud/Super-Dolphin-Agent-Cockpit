import { expect, it } from "vitest";
import { auditRpcContracts } from "./rpc-contract-audit.mjs";
import {
  createShadowRepo,
  MATRIX_PATH,
  REGRESSION_PATH,
  shadowMatrix,
  createPolicyShadow,
  ignoredResultPolicy,
  consumerValidatedPolicy,
  ignoredResultRegression,
  pageIgnoredResultRegression,
  directWailsIgnoredResultRegression,
  DIRECT_WAILS_IGNORED_RESULT_CONSUMER,
} from "./rpc-audit-test-support.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it("rejects an ambiguous setter with multiple useState bindings", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
      consumer: `
        import { useState } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        function Fixture() {
          const [dialogOpen, setDialogOpen] = useState(true)
          async function loadConfig() {
            await readConfig()
            setDialogOpen(false)
          }
          return dialogOpen ? <div role="dialog" aria-label="Ambiguous dialog" /> : null
        }
        function OtherFixture() {
          const [otherOpen, setDialogOpen] = useState(true)
          return otherOpen ? <div role="dialog" aria-label="Other dialog" /> : null
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion:
          "expect(screen.queryByRole('dialog', { name: 'Ambiguous dialog' })).not.toBeInTheDocument()",
      }),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        field: "regressionTest",
      }),
    );
  }, 30000);

  it.each([
    ["wrong RPC facade", { mockFacade: "startThread" }],
    ["no malformed sentinel", { response: "{ value: 'valid-looking' }" }],
    ["only facade call-count assertion", { assertion: "" }],
    [
      "no post-trigger assertion",
      { assertion: "", trigger: "fireEvent.click(screen.getByRole('button', { name: 'save' }))" },
    ],
  ])(
    "rejects page-level ignored-result regression proof with %s",
    async (_label, overrides) => {
      const repoRoot = await createPolicyShadow({
        policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
        consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        async function loadConfig() { await readConfig(); showNotice('saved') }
      `,
        regression: pageIgnoredResultRegression(overrides),
      });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          field: "regressionTest",
          reason: "test callback lacks executable assertions tied to the consumer and RPC key",
        }),
      );
    },
    30000,
  );

  it.each([
    [
      "no exact facade invocation assertion",
      {
        invocationAssertion: "",
        assertion: "expect(await screen.findByText('existing unrelated text')).toBeInTheDocument()",
      },
    ],
    [
      "exact facade invocation with unrelated screen assertion",
      {
        assertion: "expect(await screen.findByText('existing unrelated text')).toBeInTheDocument()",
      },
    ],
  ])(
    "rejects page-level false-green evidence with %s",
    async (_label, overrides) => {
      const repoRoot = await createPolicyShadow({
        policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
        consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        async function loadConfig() { await readConfig(); showNotice('saved') }
      `,
        regression: pageIgnoredResultRegression(overrides),
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

  it.each([
    [
      "query/refetch key after the RPC",
      `
      async function loadConfig() {
        await readConfig()
        await queryClient.refetchQueries({ queryKey: ['unrelated-query-key'] })
      }
    `,
      "unrelated-query-key",
    ],
    [
      "log text after the RPC",
      `
      async function loadConfig() {
        await readConfig()
        log('post-call-log')
      }
    `,
      "post-call-log",
    ],
    [
      "unused static literal after the RPC",
      `
      async function loadConfig() {
        await readConfig()
        const unused = 'unused-static-literal'
      }
    `,
      "unused-static-literal",
    ],
    [
      "caller sibling text",
      `
      async function loadConfig() { await readConfig() }
      function invokeLoad() { log(loadConfig(), 'caller-sibling-text') }
    `,
      "caller-sibling-text",
    ],
  ])(
    "rejects page-level text evidence sourced only from %s",
    async (_label, consumerBody, assertedText) => {
      const repoRoot = await createPolicyShadow({
        policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
        consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        ${consumerBody}
      `,
        regression: pageIgnoredResultRegression({
          assertion: `expect(await screen.findByText('${assertedText}')).toBeInTheDocument()`,
        }),
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

  it("rejects a negative alert assertion backed only by a generic catch error setter", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        async function loadConfig() {
          try {
            await readConfig()
            showNotice('saved')
          } catch (error) {
            setLoadError({ error })
          }
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion: "expect(screen.queryByRole('alert')).not.toBeInTheDocument()",
      }),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        field: "regressionTest",
      }),
    );
  }, 30000);

  it("accepts exact direct-wails rejection propagation evidence for the configured RPC method", async () => {
    const consumerPath = "frontend-app/src/shared/api/wails/wailsBridgeRpc.js";
    const policy = ignoredResultPolicy({
      consumerPath,
      consumerSymbol: "sendFrontendLogBatch",
      regressionSymbol: "propagates frontend log batch RPC failures",
    });
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${policy} }`, {
        key: "UI_LOG",
        facade: "sendFrontendLogBatch",
      }),
      [consumerPath]: DIRECT_WAILS_IGNORED_RESULT_CONSUMER,
      [REGRESSION_PATH]: directWailsIgnoredResultRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    [
      "wrong RPC method",
      {
        methodAssertion:
          "expect(byID).toHaveBeenCalledWith(expect.any(Number), 'thread/start', expect.any(Object))",
      },
    ],
    ["resolved transport", { mockMethod: "mockResolvedValue" }],
    ["no rejection assertion", { rejectionAssertion: "" }],
    ["no exact method assertion", { methodAssertion: "" }],
  ])(
    "rejects unsound direct-wails regression proof with %s",
    async (_label, overrides) => {
      const consumerPath = "frontend-app/src/shared/api/wails/wailsBridgeRpc.js";
      const policy = ignoredResultPolicy({
        consumerPath,
        consumerSymbol: "sendFrontendLogBatch",
        regressionSymbol: "propagates frontend log batch RPC failures",
      });
      const repoRoot = await createShadowRepo({
        [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${policy} }`, {
          key: "UI_LOG",
          facade: "sendFrontendLogBatch",
        }),
        [consumerPath]: DIRECT_WAILS_IGNORED_RESULT_CONSUMER,
        [REGRESSION_PATH]: directWailsIgnoredResultRegression(overrides),
      });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "UI_LOG",
          field: "regressionTest",
        }),
      );
    },
    30000,
  );

  it("rejects consumer-validated regression evidence without malformed-shape rejection", async () => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function assertConfigShape(value) {
          if (!value) throw new TypeError('invalid config')
        }
        export async function loadConfig() {
          const result = await readConfig()
          assertConfigShape(result)
          return result
        }
      `,
      regression: `
        import { loadConfig } from './consumer.js'
        it('rejects malformed config', async () => {
          expect(await loadConfig()).toBeDefined()
        })
      `,
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        field: "regressionTest",
      }),
    );
  }, 30000);

  it.each([
    ["parameter", `async (loadConfig) => { expect(await loadConfig()).toBeUndefined() }`],
    [
      "nested parameter",
      `async () => { async function nested(loadConfig) { expect(await loadConfig()).toBeUndefined() }; await nested(async () => undefined) }`,
    ],
    [
      "catch binding",
      `async () => { try { throw async () => undefined } catch (loadConfig) { expect(await loadConfig()).toBeUndefined() } }`,
    ],
    [
      "block binding",
      `async () => { { const loadConfig = async () => undefined; expect(await loadConfig()).toBeUndefined() } }`,
    ],
  ])(
    "rejects ignored-result regression using a shadowed consumer alias: %s",
    async (_label, callback) => {
      const repoRoot = await createPolicyShadow({
        policy: ignoredResultPolicy(),
        consumer: `import { readConfig } from '../../shared/api/backendApi.js'; export async function loadConfig() { await readConfig() }`,
        regression: `import { loadConfig } from './consumer.js'; it('keeps config result irrelevant', ${callback})`,
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

  it("accepts regression evidence using the unshadowed imported consumer alias", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `import { readConfig } from '../../shared/api/backendApi.js'; export async function loadConfig() { await readConfig() }`,
      regression: ignoredResultRegression(),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    [
      "parameter",
      `async (loadConfig) => { await expect(loadConfig()).rejects.toThrow('invalid config') }`,
    ],
    [
      "nested parameter",
      [
        "async () => { async function nested(loadConfig) { await expect(loadConfig()).rejects.toThr",
        "ow('invalid config') }; await nested(async () => { throw new TypeError('invalid config') }",
        ") }",
      ].join(""),
    ],
    [
      "catch binding",
      `async () => { try { throw async () => { throw new TypeError('invalid config') } } catch (loadConfig) { await expect(loadConfig()).rejects.toThrow('invalid config') } }`,
    ],
  ])(
    "rejects consumer-validated regression using a shadowed consumer alias: %s",
    async (_label, callback) => {
      const repoRoot = await createPolicyShadow({
        policy: consumerValidatedPolicy(),
        consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function assertConfigShape(value) { if (!value) throw new TypeError('invalid config') }
        export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result }
      `,
        regression: `
        import { vi } from 'vitest'
        import { loadConfig } from './consumer.js'
        vi.mock('../../shared/api/backendApi.js', () => ({ readConfig: vi.fn().mockResolvedValue(null) }))
        it('rejects malformed config', ${callback})
      `,
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
});
