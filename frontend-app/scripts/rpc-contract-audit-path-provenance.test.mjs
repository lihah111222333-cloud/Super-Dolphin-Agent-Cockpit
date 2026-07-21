import { expect, it, onTestFinished } from "vitest";
import { mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { auditRpcContracts } from "./rpc-contract-audit.mjs";
import {
  createShadowRepo,
  MATRIX_PATH,
  CONSUMER_PATH,
  REGRESSION_PATH,
  UNUSED_POLICY,
  shadowMatrix,
  createPolicyShadow,
  ignoredResultPolicy,
  consumerValidatedPolicy,
  ignoredResultRegression,
  consumerValidatedRegression,
} from "./rpc-audit-test-support.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it("rejects a response-policy locator file that is a symlink escaping the repository", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      regression: ignoredResultRegression(),
    });
    const externalRoot = await mkdtemp(join(tmpdir(), "rpc-contract-audit-external-"));
    onTestFinished(() => rm(externalRoot, { recursive: true, force: true }));
    const externalConsumer = join(externalRoot, "consumer.js");
    await writeFile(externalConsumer, `export async function loadConfig() {}`);
    await rm(join(repoRoot, CONSUMER_PATH));
    await symlink(externalConsumer, join(repoRoot, CONSUMER_PATH));

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        field: "consumer",
        reason: "path must not resolve through a symbolic link",
      }),
    );
  }, 30000);

  it("fails when the production scan tree contains a symlink escaping the repository", async () => {
    const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY });
    const externalRoot = await mkdtemp(join(tmpdir(), "rpc-contract-audit-scan-"));
    onTestFinished(() => rm(externalRoot, { recursive: true, force: true }));
    await writeFile(join(externalRoot, "escape.js"), `readConfig()`);
    await symlink(externalRoot, join(repoRoot, "frontend-app/src/escaped-scan"));

    await expect(auditRpcContracts({ repoRoot })).rejects.toThrow(
      "production scan tree must not contain symbolic links",
    );
  }, 30000);

  it.each([
    [
      "wrong-object member",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      const unrelated = { readConfig() {} }
      export async function loadConfig() { await unrelated.readConfig() }
    `,
    ],
    [
      "shadowed identifier",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      export async function loadConfig() {
        const readConfig = async () => undefined
        await readConfig()
      }
    `,
    ],
    [
      "shadowed namespace receiver",
      `
      import * as backendApi from '../../shared/api/backendApi.js'
      export async function loadConfig() {
        const backendApi = { readConfig: async () => undefined }
        await backendApi.readConfig()
      }
    `,
    ],
  ])(
    "rejects %s as facade provenance",
    async (_label, consumer) => {
      const repoRoot = await createPolicyShadow({
        policy: ignoredResultPolicy(),
        consumer,
        regression: ignoredResultRegression(),
      });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          field: "consumer",
          reason: "symbol does not call the facade for this RPC key",
        }),
      );
    },
    30000,
  );

  it.each([
    [
      "named import shadowed by a parameter",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      export async function loadConfig(readConfig) { await readConfig() }
    `,
    ],
    [
      "namespace import shadowed by a parameter",
      `
      import * as backendApi from '../../shared/api/backendApi.js'
      export async function loadConfig(backendApi) { await backendApi.readConfig() }
    `,
    ],
    [
      "named import shadowed in a nested scope",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      export async function loadConfig() {
        async function nested(readConfig) { await readConfig() }
        await nested(async () => undefined)
      }
    `,
    ],
    [
      "namespace import shadowed by a catch binding",
      `
      import * as backendApi from '../../shared/api/backendApi.js'
      export async function loadConfig() {
        try { throw { readConfig: async () => undefined } }
        catch (backendApi) { await backendApi.readConfig() }
      }
    `,
    ],
  ])(
    "resolves %s at the candidate call site",
    async (_label, consumer) => {
      const repoRoot = await createPolicyShadow({
        policy: ignoredResultPolicy(),
        consumer,
        regression: ignoredResultRegression(),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          field: "consumer",
          reason: "symbol does not call the facade for this RPC key",
        }),
      );
    },
    30000,
  );

  it("still finds an unshadowed facade call beside a nested shadow", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() {
          { const readConfig = async () => undefined; await readConfig() }
          await readConfig()
        }
      `,
      regression: ignoredResultRegression(),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    [
      "named wrapper export",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      function loadConfigFromService(payload) { return readConfig(payload) }
      export { loadConfigFromService }
    `,
      `
      import { loadConfigFromService } from './configService.js'
      async function loadConfig() { await loadConfigFromService({}) }
    `,
    ],
    [
      "destructured object wrapper export",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      export const configService = Object.freeze({
        load: (payload) => readConfig(payload),
      })
    `,
      `
      import { configService } from './configService.js'
      const { load } = configService
      async function loadConfig() { await load({}) }
    `,
    ],
  ])(
    "traces an exact %s without weakening consumer result-use proof",
    async (_label, service, consumer) => {
      const servicePath = "frontend-app/src/pages/audit-fixture/configService.js";
      const repoRoot = await createShadowRepo({
        [MATRIX_PATH]: shadowMatrix(
          `{ responsePolicy: ${ignoredResultPolicy({ consumerVisibility: "module-private" })} }`,
        ),
        [servicePath]: service,
        [CONSUMER_PATH]: consumer,
        [REGRESSION_PATH]: ignoredResultRegression(),
      });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.invalidResponsePolicyEvidence).toEqual([]);
    },
    30000,
  );

  it("allows a wrapper binding only when the exact facade result flows directly to return", async () => {
    const servicePath = "frontend-app/src/pages/audit-fixture/configService.js";
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(
        `{ responsePolicy: ${ignoredResultPolicy({ consumerVisibility: "module-private" })} }`,
      ),
      [servicePath]: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfigFromService(payload) {
          const result = await readConfig(payload)
          return result
        }
      `,
      [CONSUMER_PATH]: `
        import { loadConfigFromService } from './configService.js'
        async function loadConfig() { await loadConfigFromService({}) }
      `,
      [REGRESSION_PATH]: ignoredResultRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    [
      "observed branch",
      `const observed = await readConfig(payload); if (observed?.ok) consume(observed); return observed`,
    ],
    ["destructure", `const { ok } = await readConfig(payload); return ok`],
    [
      "pass to another function",
      `const result = await readConfig(payload); consume(result); return result`,
    ],
    [
      "conditional return branch",
      `const result = await readConfig(payload); if (payload.flag) return result; return result`,
    ],
    [
      "extra inspection",
      `const result = await readConfig(payload); inspect(result); return result`,
    ],
  ])(
    "rejects an imported wrapper with internal result %s",
    async (_label, body) => {
      const servicePath = "frontend-app/src/pages/audit-fixture/configService.js";
      const repoRoot = await createShadowRepo({
        [MATRIX_PATH]: shadowMatrix(
          `{ responsePolicy: ${ignoredResultPolicy({ consumerVisibility: "module-private" })} }`,
        ),
        [servicePath]: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfigFromService(payload) { ${body} }
      `,
        [CONSUMER_PATH]: `
        import { loadConfigFromService } from './configService.js'
        async function loadConfig() { await loadConfigFromService({}) }
      `,
        [REGRESSION_PATH]: ignoredResultRegression(),
      });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          field: "consumer",
          reason: "symbol does not call the facade for this RPC key",
        }),
      );
    },
    30000,
  );

  it("rejects an exact service wrapper whose imported facade belongs to a different RPC key", async () => {
    const servicePath = "frontend-app/src/pages/audit-fixture/configService.js";
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(
        `{ responsePolicy: ${ignoredResultPolicy({ consumerVisibility: "module-private" })} }`,
      ),
      [servicePath]: `
        import { startThread } from '../../shared/api/backendApi.js'
        export function loadConfigFromService(payload) { return startThread(payload) }
      `,
      [CONSUMER_PATH]: `
        import { loadConfigFromService } from './configService.js'
        async function loadConfig() { await loadConfigFromService({}) }
      `,
      [REGRESSION_PATH]: ignoredResultRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        field: "consumer",
        reason: "symbol does not call the facade for this RPC key",
      }),
    );
  }, 30000);

  it("rejects ignored-result consumers when any matching call result is observed", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() {
          await readConfig()
          const observed = await readConfig()
          return observed
        }
      `,
      regression: ignoredResultRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        field: "consumer",
        reason: "consumer reads the RPC result",
      }),
    );
  }, 30000);

  it.each([
    [
      "unrelated parse",
      `
      const OtherShape = { parse() {} }
      export function assertConfigShape(value) {
        OtherShape.parse('unrelated')
        return value
      }
    `,
    ],
    [
      "arbitrary parse implementation",
      `
      const ConfigShape = { parse(value) { return value } }
      export function assertConfigShape(value) {
        ConfigShape.parse(value)
      }
    `,
    ],
    [
      "inverted safeParse branch",
      `
      const ConfigShape = { safeParse() { return { success: true } } }
      export function assertConfigShape(value) {
        const result = ConfigShape.safeParse(value)
        if (result.success) throw new TypeError('valid config rejected')
      }
    `,
    ],
    [
      "unrelated safeParse",
      `
      const OtherShape = { safeParse() { return { success: false } } }
      export function assertConfigShape(value) {
        const result = OtherShape.safeParse('unrelated')
        if (!result.success) throw new TypeError('invalid config')
        return value
      }
    `,
    ],
    [
      "ignored safeParse result",
      `
      const ConfigShape = { safeParse() { return { success: false } } }
      export function assertConfigShape(value) {
        ConfigShape.safeParse(value)
      }
    `,
    ],
  ])(
    "rejects consumer shape proof with %s",
    async (_label, shapeSource) => {
      const repoRoot = await createPolicyShadow({
        policy: consumerValidatedPolicy(),
        consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        ${shapeSource}
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
          field: "shape",
          reason: "shape symbol lacks executable narrowing",
        }),
      );
    },
    30000,
  );
});
