import { expect, it } from "vitest";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { auditRpcContracts } from "./rpc-contract-audit.mjs";
import {
  CONSUMER_PATH,
  UNUSED_POLICY,
  createPolicyShadow,
  consumerValidatedPolicy,
  consumerValidatedRegression,
} from "./rpc-audit-test-support.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it("tracks an unused facade imported through a barrel re-export", async () => {
    const barrelPath = "frontend-app/src/pages/audit-fixture/backendApiBarrel.js";
    const repoRoot = await createPolicyShadow({
      policy: UNUSED_POLICY,
      consumer: `
        import { readConfig } from './backendApiBarrel.js'
        readConfig()
      `,
    });
    await writeFile(
      join(repoRoot, barrelPath),
      `
      export { readConfig } from '../../shared/api/backendApi.js'
    `,
    );
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        kind: "unused",
        path: CONSUMER_PATH,
        reason: "production facade reference exists",
      }),
    );
  }, 30000);

  it.each([
    [
      "single alias",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export { readConfig as loadConfig } from '../../shared/api/backendApi.js'`,
      },
      `import { loadConfig } from './barrel-a.js'; loadConfig()`,
    ],
    [
      "alias then star",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export { readConfig as loadConfig } from '../../shared/api/backendApi.js'`,
        "frontend-app/src/pages/audit-fixture/barrel-b.js": `export * from './barrel-a.js'`,
      },
      `import { loadConfig } from './barrel-b.js'; loadConfig()`,
    ],
    [
      "star then alias",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export * from '../../shared/api/backendApi.js'`,
        "frontend-app/src/pages/audit-fixture/barrel-b.js": `export { readConfig as loadConfig } from './barrel-a.js'`,
      },
      `import { loadConfig } from './barrel-b.js'; loadConfig()`,
    ],
  ])(
    "tracks an unused facade through %s re-exports",
    async (_label, barrels, consumer) => {
      const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer });
      for (const [filePath, source] of Object.entries(barrels))
        await writeFile(join(repoRoot, filePath), source);
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          kind: "unused",
          path: CONSUMER_PATH,
        }),
      );
    },
    30000,
  );

  it.each([
    [
      "default import through renamed default",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export { readConfig as default } from '../../shared/api/backendApi.js'`,
      },
      `import loadConfig from './barrel-a.js'; loadConfig()`,
    ],
    [
      "namespace member through renamed export",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export { readConfig as loadConfig } from '../../shared/api/backendApi.js'`,
      },
      `import * as api from './barrel-a.js'; api.loadConfig()`,
    ],
    [
      "default alias after star hop",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export * from '../../shared/api/backendApi.js'`,
        "frontend-app/src/pages/audit-fixture/barrel-b.js": `export { readConfig as default } from './barrel-a.js'`,
      },
      `import loadConfig from './barrel-b.js'; loadConfig()`,
    ],
    [
      "namespace alias after alias and star hops",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export { readConfig as intermediate } from '../../shared/api/backendApi.js'`,
        "frontend-app/src/pages/audit-fixture/barrel-b.js": `export * from './barrel-a.js'`,
        "frontend-app/src/pages/audit-fixture/barrel-c.js": `export { intermediate as loadConfig } from './barrel-b.js'`,
      },
      `import * as api from './barrel-c.js'; api.loadConfig()`,
    ],
  ])(
    "tracks an unused facade through %s",
    async (_label, barrels, consumer) => {
      const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer });
      for (const [filePath, source] of Object.entries(barrels))
        await writeFile(join(repoRoot, filePath), source);
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          kind: "unused",
          path: CONSUMER_PATH,
        }),
      );
    },
    30000,
  );

  it.each([
    [
      "direct namespace export",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export * as api from '../../shared/api/backendApi.js'`,
      },
      `import { api } from './barrel-a.js'; api.readConfig()`,
    ],
    [
      "multi-hop namespace export",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export * as api from '../../shared/api/backendApi.js'`,
        "frontend-app/src/pages/audit-fixture/barrel-b.js": `export { api as backend } from './barrel-a.js'`,
      },
      `import { backend } from './barrel-b.js'; backend.readConfig()`,
    ],
  ])(
    "tracks an unused facade through a %s",
    async (_label, barrels, consumer) => {
      const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer });
      for (const [filePath, source] of Object.entries(barrels))
        await writeFile(join(repoRoot, filePath), source);
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          kind: "unused",
          path: CONSUMER_PATH,
        }),
      );
    },
    30000,
  );

  it("does not treat an unrelated member of a namespace export as facade usage", async () => {
    const repoRoot = await createPolicyShadow({
      policy: UNUSED_POLICY,
      consumer: `import { api } from './barrel-a.js'; api.createBackendApi()`,
    });
    await writeFile(
      join(repoRoot, "frontend-app/src/pages/audit-fixture/barrel-a.js"),
      `export * as api from '../../shared/api/backendApi.js'`,
    );
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    [
      "direct nested namespace member",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export * as api from '../../shared/api/backendApi.js'`,
      },
      `import * as barrel from './barrel-a.js'; barrel.api.readConfig()`,
    ],
    [
      "multi-hop nested namespace member",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export * as api from '../../shared/api/backendApi.js'`,
        "frontend-app/src/pages/audit-fixture/barrel-b.js": `export { api as backend } from './barrel-a.js'`,
      },
      `import * as barrel from './barrel-b.js'; const api = barrel.backend; const alias = api; alias.readConfig()`,
    ],
    [
      "static computed nested namespace member",
      {
        "frontend-app/src/pages/audit-fixture/barrel-a.js": `export * as api from '../../shared/api/backendApi.js'`,
      },
      `import * as barrel from './barrel-a.js'; barrel['api']['readConfig']()`,
    ],
  ])(
    "tracks an unused facade through a %s",
    async (_label, barrels, consumer) => {
      const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer });
      for (const [filePath, source] of Object.entries(barrels))
        await writeFile(join(repoRoot, filePath), source);
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          kind: "unused",
          path: CONSUMER_PATH,
        }),
      );
    },
    30000,
  );

  it.each([
    ["unrelated nested member", `barrel.api.createBackendApi()`],
    ["dynamic computed member", `barrel[key].readConfig()`],
    ["shadowed barrel binding", `function use(barrel) { barrel.api.readConfig() }`],
    [
      "shadowed nested api binding",
      `const api = barrel.api; function use(api) { api.readConfig() }`,
    ],
  ])(
    "does not treat a %s as nested namespace facade usage",
    async (_label, use) => {
      const repoRoot = await createPolicyShadow({
        policy: UNUSED_POLICY,
        consumer: `import * as barrel from './barrel-a.js'; const key = 'api'; ${use}`,
      });
      await writeFile(
        join(repoRoot, "frontend-app/src/pages/audit-fixture/barrel-a.js"),
        `export * as api from '../../shared/api/backendApi.js'`,
      );
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toEqual([]);
    },
    30000,
  );

  it("does not treat unrelated aliased or star exports as facade usage", async () => {
    const barrelPath = "frontend-app/src/pages/audit-fixture/unrelatedBarrel.js";
    const repoRoot = await createPolicyShadow({
      policy: UNUSED_POLICY,
      consumer: `import { loadOther } from './unrelatedBarrel.js'; loadOther()`,
    });
    await writeFile(
      join(repoRoot, barrelPath),
      `
      export { createBackendApi as loadOther } from '../../shared/api/backendApi.js'
      export * from '../../shared/api/backend/backendRpcMethods.js'
    `,
    );
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    [
      "namespace member",
      `
      import * as backendApi from '../../shared/api/backendApi.js'
      backendApi.readConfig()
    `,
    ],
    [
      "namespace destructuring alias",
      `
      import * as backendApi from '../../shared/api/backendApi.js'
      const { readConfig: load } = backendApi
      load()
    `,
    ],
    [
      "transitive local alias",
      `
      import { readConfig as importedRead } from '../../shared/api/backendApi.js'
      const localRead = importedRead
      const load = localRead
      load()
    `,
    ],
  ])(
    "tracks an unused facade through a %s",
    async (_label, consumer) => {
      const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          kind: "unused",
          path: CONSUMER_PATH,
          reason: "production facade reference exists",
        }),
      );
    },
    30000,
  );
  it.each([
    ["dead branch", `if (false) assertConfigShape(result)`],
    ["one-sided branch", `if (flag) assertConfigShape(result)`],
    ["callback", `items.forEach(() => assertConfigShape(result))`],
    ["loop", `while (flag) { assertConfigShape(result); break }`],
    ["after use", `const value = result.value; assertConfigShape(result)`],
  ])(
    "rejects non-dominating validation in a %s",
    async (_label, validation) => {
      const repoRoot = await createPolicyShadow({
        policy: consumerValidatedPolicy(),
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      export function assertConfigShape(value) { if (!value) throw new TypeError('invalid config') }
      export async function loadConfig(flag = false, items = []) { const result = await readConfig(); ${validation}; return result.value }
    `,
        regression: consumerValidatedRegression(),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          field: "shape",
          reason: "shape proof does not dominate consumer use",
        }),
      );
    },
    30000,
  );
  it.each([
    ["nested guard", `function nested() { if (!value) throw new TypeError('invalid config') }`, ""],
    [
      "callback guard",
      `[value].forEach(() => { if (!value) throw new TypeError('invalid config') })`,
      "",
    ],
    ["dead guard", `if (false) { if (!value) throw new TypeError('invalid config') }`, ""],
    [
      "nested parser",
      `function nested() { ConfigSchema.parse(value) }`,
      `const ConfigSchema = { parse(input) { if (!input) throw new TypeError('invalid config') } }`,
    ],
    [
      "callback parser",
      `[value].forEach(() => ConfigSchema.parse(value))`,
      `const ConfigSchema = { parse(input) { if (!input) throw new TypeError('invalid config') } }`,
    ],
    [
      "dead parser",
      `if (false) ConfigSchema.parse(value)`,
      `const ConfigSchema = { parse(input) { if (!input) throw new TypeError('invalid config') } }`,
    ],
  ])(
    "rejects non-executable %s evidence",
    async (_label, proof, schema) => {
      const repoRoot = await createPolicyShadow({
        policy: consumerValidatedPolicy(),
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'; ${schema}
      export function assertConfigShape(value) { ${proof} }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `,
        regression: consumerValidatedRegression(),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          field: "shape",
          reason: "shape symbol lacks executable narrowing",
        }),
      );
    },
    30000,
  );

  it.each([
    [
      "no malformed input",
      `import { loadConfig } from './consumer.js'; it('rejects malformed config', async () => { await expect(loadConfig()).rejects.toThrow('invalid config') })`,
    ],
    [
      "transport rejection",
      [
        "import { vi } from 'vitest'; import { loadConfig } from './consumer.js'; vi.mock('../../sh",
        "ared/api/backendApi.js', () => ({ readConfig: vi.fn().mockRejectedValue(new Error('transpo",
        "rt failed')) })); it('rejects malformed config', async () => { await expect(loadConfig()).",
        "rejects.toThrow('transport failed') })",
      ].join(""),
    ],
    [
      "unrelated throw",
      [
        "import { vi } from 'vitest'; import { loadConfig } from './consumer.js'; vi.mock('../../sh",
        "ared/api/backendApi.js', () => ({ readConfig: vi.fn().mockResolvedValue({ malformed: true ",
        "}) })); it('rejects malformed config', async () => { throw new Error('invalid config') })",
      ].join(""),
    ],
    [
      "generic rejection",
      [
        "import { vi } from 'vitest'; import { loadConfig } from './consumer.js'; vi.mock('../../sh",
        "ared/api/backendApi.js', () => ({ readConfig: vi.fn().mockResolvedValue({ malformed: true ",
        "}) })); it('rejects malformed config', async () => { await expect(loadConfig()).rejects.to",
        "Throw() })",
      ].join(""),
    ],
  ])(
    "rejects consumer regression with %s",
    async (_label, regression) => {
      const repoRoot = await createPolicyShadow({
        policy: consumerValidatedPolicy(),
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      export function assertConfigShape(value) { if (!value) throw new TypeError('invalid config') }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `,
        regression,
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({ field: "regressionTest" }),
      );
    },
    30000,
  );

  it.each([
    ["parameter", `function shadow(readConfig) { readConfig() }`, false],
    ["nested", `function nested() { const readConfig = () => {}; readConfig() }`, false],
    ["block", `{ const readConfig = () => {}; readConfig() }`, false],
    ["catch", `try {} catch (readConfig) { readConfig() }`, false],
    ["namespace parameter", `function shadow(backendApi) { backendApi.readConfig() }`, true],
  ])(
    "ignores an unused facade referenced only by a shadowed %s binding",
    async (_label, use, namespace) => {
      const consumer = namespace
        ? `import * as backendApi from '../../shared/api/backendApi.js'; ${use}`
        : `import { readConfig } from '../../shared/api/backendApi.js'; ${use}`;
      const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toEqual([]);
    },
    30000,
  );

  it.each([
    [
      "named",
      `import { readConfig } from '../../shared/api/backendApi.js'; function shadow(readConfig) { readConfig() }; readConfig()`,
    ],
    [
      "namespace",
      `import * as backendApi from '../../shared/api/backendApi.js'; function shadow(backendApi) { backendApi.readConfig() }; backendApi.readConfig()`,
    ],
  ])(
    "finds a real %s facade reference beside a shadow",
    async (_label, consumer) => {
      const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({ kind: "unused" }),
      );
    },
    30000,
  );
  it.each([
    [
      "failure guard in callback",
      `const parsed = ConfigSchema.safeParse(value); [parsed].forEach(() => { if (!parsed.success) throw new TypeError('invalid config') })`,
      `if (!value) return { success: false }; return { success: true, data: value }`,
    ],
    [
      "schema invalid return in callback",
      `const parsed = ConfigSchema.safeParse(value); if (!parsed.success) throw new TypeError('invalid config')`,
      `[value].forEach(() => { if (!value) return { success: false } }); return { success: true, data: value }`,
    ],
    [
      "schema success return in dead branch",
      `const parsed = ConfigSchema.safeParse(value); if (!parsed.success) throw new TypeError('invalid config')`,
      `if (!value) return { success: false }; if (false) return { success: true, data: value }`,
    ],
  ])(
    "rejects non-dominating safeParse proof with %s",
    async (_label, shapeBody, schemaBody) => {
      const repoRoot = await createPolicyShadow({
        policy: consumerValidatedPolicy(),
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      const ConfigSchema = { safeParse(value) { ${schemaBody} } }
      export function assertConfigShape(value) { ${shapeBody} }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `,
        regression: consumerValidatedRegression(),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          field: "shape",
          reason: "shape symbol lacks executable narrowing",
        }),
      );
    },
    30000,
  );
});
