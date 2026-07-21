import { expect, it } from "vitest";
import { auditRpcContracts } from "./rpc-contract-audit.mjs";
import {
  createShadowRepo,
  MATRIX_PATH,
  CONSUMER_PATH,
  REGRESSION_PATH,
  shadowMatrix,
  createPolicyShadow,
  ignoredResultPolicy,
  consumerValidatedPolicy,
  pageIgnoredResultRegression,
  consumerValidatedRegression,
} from "./rpc-audit-test-support.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it.each([
    ["inverted truthiness", `if (value) throw new TypeError('valid config rejected')`],
    [
      "inverted object type",
      `if (typeof value === 'object') throw new TypeError('valid config rejected')`,
    ],
    [
      "inverted field type",
      `if (typeof value.value === 'string') throw new TypeError('valid config rejected')`,
    ],
  ])(
    "rejects an %s guard",
    async (_label, guard) => {
      const repoRoot = await createPolicyShadow({
        policy: consumerValidatedPolicy(),
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      export function assertConfigShape(value) { ${guard} }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `,
        regression: consumerValidatedRegression(),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          field: "shape",
          reason: "shape symbol lacks executable narrowing",
        }),
      );
    },
    30000,
  );

  it.each([
    ["falsy value", `if (!value) throw new TypeError('invalid config')`],
    ["non-object value", `if (typeof value !== 'object') throw new TypeError('invalid config')`],
    [
      "non-string field",
      `if (typeof value.value !== 'string') throw new TypeError('invalid config')`,
    ],
  ])(
    "accepts a supported %s guard",
    async (_label, guard) => {
      const repoRoot = await createPolicyShadow({
        policy: consumerValidatedPolicy(),
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      export function assertConfigShape(value) { ${guard} }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `,
        regression: consumerValidatedRegression(),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toEqual([]);
    },
    30000,
  );

  it("accepts parse only from a locally proven throwing schema implementation", async () => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      const ConfigSchema = { parse(value) {
        if (!value || typeof value !== 'object' || typeof value.value !== 'string') throw new TypeError('invalid config')
        return value
      } }
      export function assertConfigShape(value) { ConfigSchema.parse(value) }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `,
      regression: consumerValidatedRegression(),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    ["negated success", `if (!parsed.success) throw new TypeError('invalid config')`],
    ["false success", `if (parsed.success === false) throw new TypeError('invalid config')`],
  ])(
    "accepts proven safeParse with an explicit %s failure branch",
    async (_label, failureGuard) => {
      const repoRoot = await createPolicyShadow({
        policy: consumerValidatedPolicy(),
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      const ConfigSchema = { safeParse(value) {
        if (!value || typeof value !== 'object' || typeof value.value !== 'string') return { success: false }
        return { success: true, data: value }
      } }
      export function assertConfigShape(value) { const parsed = ConfigSchema.safeParse(value); ${failureGuard} }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `,
        regression: consumerValidatedRegression(),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toEqual([]);
    },
    30000,
  );

  it("rejects a locally shadowed otherwise-proven schema binding", async () => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      const ConfigSchema = { parse(value) { if (!value) throw new TypeError('invalid config'); return value } }
      export function assertConfigShape(value, ConfigSchema = { parse(input) { return input } }) {
        ConfigSchema.parse(value)
      }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `,
      regression: consumerValidatedRegression(),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        field: "shape",
        reason: "shape symbol lacks executable narrowing",
      }),
    );
  }, 30000);

  it("rejects a same-name consumer binding that is not the resolved shape symbol", async () => {
    const shapePath = "frontend-app/src/pages/audit-fixture/configShape.js";
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${consumerValidatedPolicy({ shapePath })} }`),
      [shapePath]: `
        export function assertConfigShape(value) {
          if (!value) throw new TypeError('invalid config')
        }
      `,
      [CONSUMER_PATH]: `
        import { readConfig } from '../../shared/api/backendApi.js'
        function assertConfigShape(_value) {}
        export async function loadConfig() {
          const result = await readConfig()
          assertConfigShape(result)
          return result.value
        }
      `,
      [REGRESSION_PATH]: consumerValidatedRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        field: "shape",
        reason: "shape proof does not dominate consumer use",
      }),
    );
  }, 30000);

  it.each([
    [
      "ordinary declaration",
      `export function keepsConfigResultIrrelevant() { expect(true).toBe(true) }`,
    ],
    ["empty test callback", `it('keeps config result irrelevant', () => {})`],
    [
      "unrelated test callback",
      `
      import { loadConfig } from './consumer.js'
      it('keeps config result irrelevant', async () => {
        await loadConfig()
        expect(Math.max(1, 2)).toBe(2)
      })
    `,
    ],
  ])(
    "rejects regression locator evidence from an %s",
    async (_label, regression) => {
      const policy =
        _label === "ordinary declaration"
          ? ignoredResultPolicy({ regressionSymbol: "keepsConfigResultIrrelevant" })
          : ignoredResultPolicy();
      const repoRoot = await createPolicyShadow({
        policy,
        consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() { await readConfig() }
      `,
        regression,
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

  it("rejects ignored-result regression evidence that merely observes the consumer result", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() { await readConfig() }
      `,
      regression: `
        import { loadConfig } from './consumer.js'
        it('keeps config result irrelevant', async () => {
          const result = await loadConfig()
          expect(result).toBeDefined()
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

  it("rejects page-level success text without an explicit published-callback outcome contract", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
      consumer: `
        import { useState } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        function Fixture() {
          const [notice, setNotice] = useState('')
          async function loadConfig() { await readConfig(); setNotice('saved') }
          return notice ? <p>{notice}</p> : null
        }
      `,
      regression: pageIgnoredResultRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        field: "regressionTest",
      }),
    );
  }, 30000);

  it("accepts a negative dialog assertion backed by a concrete post-call state dismissal", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
      consumer: `
        import { useState } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        function Fixture() {
          const [deletingDoc, setDeletingDoc] = useState({ id: 1 })
          async function loadConfig() {
            await readConfig()
            setDeletingDoc(null)
          }
          return deletingDoc ? <div role="dialog" aria-label="Delete data source" /> : null
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion:
          "expect(screen.queryByRole('dialog', { name: 'Delete data source' })).not.toBeInTheDocument()",
      }),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it("rejects a negative dialog assertion when the state dismissal precedes the RPC", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
      consumer: `
        import { useState } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        function Fixture() {
          const [deletingDoc, setDeletingDoc] = useState({ id: 1 })
          async function loadConfig() {
            setDeletingDoc(null)
            await readConfig()
          }
          return deletingDoc ? <div role="dialog" aria-label="Delete data source" /> : null
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion:
          "expect(screen.queryByRole('dialog', { name: 'Delete data source' })).not.toBeInTheDocument()",
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

  it("rejects an unrelated negative dialog assertion after an unrelated post-call setter", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        async function loadConfig() {
          await readConfig()
          setTotallyUnrelated(false)
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion:
          "expect(screen.queryByRole('dialog', { name: 'Unrelated dialog' })).not.toBeInTheDocument()",
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

  it("rejects a dialog controlled by a different state than the post-call setter", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
      consumer: `
        import { useState } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        function Fixture() {
          const [completed, setCompleted] = useState(true)
          const [dialogOpen] = useState(true)
          async function loadConfig() {
            await readConfig()
            setCompleted(false)
          }
          return dialogOpen ? <div role="dialog" aria-label="Different state dialog" /> : null
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion:
          "expect(screen.queryByRole('dialog', { name: 'Different state dialog' })).not.toBeInTheDocument()",
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
});
