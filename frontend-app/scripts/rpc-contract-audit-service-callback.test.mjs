import { expect, it } from "vitest";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import {
  auditRpcContracts,
  collectFrontendPayloadKeysFromSource,
  collectPayloadRegistryDrift,
  parseContractMatrixForTest,
} from "./rpc-contract-audit.mjs";
import {
  REPO_ROOT,
  createShadowRepo,
  MATRIX_PATH,
  CONSUMER_PATH,
  UNUSED_POLICY,
  shadowMatrix,
  createPolicyShadow,
  ignoredResultPolicy,
  ignoredResultRegression,
} from "./rpc-audit-test-support.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it("reports a service facade whose downstream destructure outlives its factory member", async () => {
    const servicePath = "frontend-app/src/pages/prompts/services/promptPageService.js";
    const serviceSource = await readFile(join(REPO_ROOT, servicePath), "utf8");
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`, {
        key: "PROMPT_ASSETS_LIST",
        facade: "promptPageService.listPromptAssets",
      }),
      [servicePath]: serviceSource.replace(
        /    listPromptAssets\(payload\) \{\n[\s\S]*?\n    \},\n/,
        "",
      ),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidFacadeLocators).toContainEqual({
      key: "PROMPT_ASSETS_LIST",
      facade: "promptPageService.listPromptAssets",
      locator: servicePath,
    });
  }, 30000);

  it("reports payload registry drift when frontend builders miss Go fields", () => {
    const drift = collectPayloadRegistryDrift(
      new Map([["thread/start", ["cwd", "provider", "new_go_field"]]]),
      new Map([["thread/start", ["cwd", "provider"]]]),
    );

    expect(drift).toEqual([
      {
        method: "thread/start",
        missingFrontendKeys: ["new_go_field"],
        extraFrontendKeys: [],
      },
    ]);
  });

  it("extracts consumed payload keys from facade builders instead of static key lists", () => {
    const source = `
      const api = {
        start: (params) => callBackend(RPC_METHODS.THREAD_START, threadStartPayload(params)),
        turn: (params) => callBackend(RPC_METHODS.TURN_START, turnStartPayload(params)),
        interrupt: (params) => callBackend(RPC_METHODS.TURN_INTERRUPT, turnInterruptPayload(params)),
      }
      function threadStartPayload(params) {
        const unused = { ...params }
        // takePayloadField(unused, 'ghost_comment')
        const probe = "takePayloadField(unused, 'ghost_string')"
        const providerRaw = takePayloadField(unused, 'provider')
        const request = cleanObject({
          cwd: takePayloadField(unused, 'cwd'),
        })
        if (probe) {
          return request
        }
        return request
      }
      function turnStartPayload(params) {
        const unused = { ...params }
        const input = takePayloadField(unused, 'input')
        return takePayloadFields(unused, [
          'threadId',
          'thread_id',
        ])
      }
      function turnInterruptPayload(params) {
        const unused = { ...params }
        return takePayloadFields(unused, [
          'expectedTurnId',
          'requestId',
          'threadId',
        ])
      }
    `;

    expect(collectFrontendPayloadKeysFromSource(source).get("THREAD_START")).toEqual([
      "cwd",
      "provider",
    ]);
    expect(collectFrontendPayloadKeysFromSource(source).get("TURN_START")).toEqual([
      "input",
      "thread_id",
      "threadId",
    ]);
    expect(collectFrontendPayloadKeysFromSource(source).get("TURN_INTERRUPT")).toEqual([
      "expectedTurnId",
      "requestId",
      "threadId",
    ]);
  });

  it("rejects generated response passthrough prose", () => {
    expect(() =>
      parseContractMatrixForTest(
        shadowMatrix(`{
      responsePassthroughReason: 'CONFIG_READ response is consumed unchanged by readConfig',
    }`),
      ),
    ).toThrow("RPC_CONTRACT_REGISTRY.CONFIG_READ responsePassthroughReason is forbidden");
  });

  it.each([
    ["missing", ""],
    [
      "extra metadata",
      `{ responsePolicy: {
      kind: 'ignored-result',
      consumer: { path: 'frontend-app/src/pages/audit-fixture/consumer.js', symbol: 'loadConfig' },
      regressionTest: { path: 'frontend-app/src/pages/audit-fixture/consumer.test.js', symbol: 'keeps config result irrelevant' },
      note: 'prose',
    } }`,
    ],
    [
      "duplicate metadata",
      `{ responsePolicy: {
      kind: 'ignored-result',
      consumer: { path: 'frontend-app/src/pages/audit-fixture/consumer.js', symbol: 'loadConfig' },
      regressionTest: { path: 'frontend-app/src/pages/audit-fixture/consumer.test.js', symbol: 'keeps config result irrelevant' },
      kind: 'ignored-result',
    } }`,
    ],
    [
      "computed metadata",
      `{ responsePolicy: {
      kind: 'ignored-result',
      consumer: { path: 'frontend-app/src/pages/audit-fixture/consumer.js', symbol: 'loadConfig' },
      regressionTest: { path: 'frontend-app/src/pages/audit-fixture/consumer.test.js', symbol: 'keeps config result irrelevant' },
      ['note']: 'prose',
    } }`,
    ],
    ["spread metadata", `{ responsePolicy: { ...policy } }`],
  ])("rejects %s structured response policy metadata", (_label, policy) => {
    expect(() => parseContractMatrixForTest(shadowMatrix(policy))).toThrow(
      "RPC_CONTRACT_REGISTRY.CONFIG_READ",
    );
  });

  it("parses a strict published-callback outcome target", () => {
    const [entry] = parseContractMatrixForTest(
      shadowMatrix(`{
      responsePolicy: ${ignoredResultPolicy({ outcomeTarget: ["notices", "showTaskNotice"] })}
    }`),
    );

    expect(entry.responsePolicy.outcome).toEqual({
      kind: "published-callback",
      target: ["notices", "showTaskNotice"],
    });
  });

  it.each([
    ["empty target", "{ kind: 'published-callback', target: [] }"],
    ["blank target part", "{ kind: 'published-callback', target: ['notices', ' '] }"],
    ["bad kind", "{ kind: 'rendered-text', target: ['notices', 'showTaskNotice'] }"],
    [
      "extra field",
      "{ kind: 'published-callback', target: ['notices', 'showTaskNotice'], note: 'x' }",
    ],
    ["computed field", "{ kind: 'published-callback', ['target']: ['notices'] }"],
    ["spread field", "{ ...outcome }"],
  ])("rejects published-callback outcome with %s", (_label, outcome) => {
    const policy = ignoredResultPolicy().replace(
      "regressionTest:",
      `outcome: ${outcome}, regressionTest:`,
    );
    expect(() => parseContractMatrixForTest(shadowMatrix(`{ responsePolicy: ${policy} }`))).toThrow(
      "RPC_CONTRACT_REGISTRY.CONFIG_READ responsePolicy.outcome",
    );
  });

  it.each([
    [
      "blank path",
      ignoredResultPolicy({ consumerPath: "   " }),
      "consumer",
      "path must be non-blank",
    ],
    [
      "escaping path",
      ignoredResultPolicy({ consumerPath: "../consumer.js" }),
      "consumer",
      "path must be normalized and repository-confined",
    ],
    [
      "nonexistent path",
      ignoredResultPolicy({ consumerPath: "frontend-app/src/pages/missing.js" }),
      "consumer",
      "file does not exist",
    ],
    [
      "nonexistent symbol",
      ignoredResultPolicy({ consumerSymbol: "missingConsumer" }),
      "consumer",
      "symbol was not found",
    ],
    [
      "wrong test file kind",
      ignoredResultPolicy({ regressionPath: CONSUMER_PATH }),
      "regressionTest",
      "path must identify a JavaScript test file",
    ],
  ])(
    "rejects invalid response policy locator evidence: %s",
    async (_label, policy, field, reason) => {
      const repoRoot = await createPolicyShadow({
        policy,
        consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() { await readConfig() }
      `,
        regression: ignoredResultRegression(),
      });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.invalidResponsePolicyEvidence).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            key: "CONFIG_READ",
            kind: "ignored-result",
            field,
            reason,
          }),
        ]),
      );
    },
    30000,
  );

  it.each([
    [
      "nested-only declaration",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      function wrapper() { async function loadConfig() { await readConfig() } }
    `,
    ],
    [
      "unexported declaration",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      async function loadConfig() { await readConfig() }
    `,
    ],
    [
      "duplicate module-level declarations",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      export var loadConfig = async () => { await readConfig() }
      var loadConfig = async () => { await readConfig() }
    `,
    ],
    [
      "object-member coincidence",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      export const unrelated = { async loadConfig() { await readConfig() } }
    `,
    ],
  ])(
    "rejects a locator backed by a %s",
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
          reason: "symbol was not found",
        }),
      );
    },
    30000,
  );

  it("resolves one explicit module-private consumer without weakening exported locator defaults", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        async function loadConfig() { await readConfig() }
      `,
      regression: ignoredResultRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it("resolves one explicit nested module-private consumer and proves only its own body", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function wrapper() {
          async function loadConfig() { await readConfig() }
          function observedSibling() { return readConfig() }
          return { loadConfig, observedSibling }
        }
      `,
      regression: ignoredResultRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it("resolves an exact hook-bound module-private callback and proves only its callback body", async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: "module-private" }),
      consumer: `
        import { useCallback } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        export function wrapper() {
          const loadConfig = useCallback(async () => { await readConfig() }, [])
          const observedSibling = useCallback(async () => readConfig(), [])
          return { loadConfig, observedSibling }
        }
      `,
      regression: ignoredResultRegression(),
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    [
      "missing private symbol",
      "missingConsumer",
      `async function loadConfig() { await readConfig() }`,
      "symbol was not found",
    ],
    [
      "duplicate private binding",
      "loadConfig",
      `
      async function loadConfig() { await readConfig() }
      async function outer() { async function loadConfig() { await readConfig() } }
    `,
      "symbol was not found",
    ],
    [
      "multiple ignored private calls",
      "loadConfig",
      `async function loadConfig() { await readConfig(); await readConfig() }`,
      "consumer calls the facade more than once",
    ],
    [
      "assigned private result",
      "loadConfig",
      `async function loadConfig() { const result = await readConfig(); return result }`,
      "consumer reads the RPC result",
    ],
    [
      "returned private result",
      "loadConfig",
      `async function loadConfig() { return readConfig() }`,
      "consumer reads the RPC result",
    ],
    [
      "inspected private result",
      "loadConfig",
      `async function loadConfig() { const result = await readConfig(); if (result.ok) consume() }`,
      "consumer reads the RPC result",
    ],
    [
      "destructured private result",
      "loadConfig",
      `async function loadConfig() { const { ok } = await readConfig(); return ok }`,
      "consumer reads the RPC result",
    ],
    [
      "passed private result",
      "loadConfig",
      `async function loadConfig() { consume(await readConfig()) }`,
      "consumer reads the RPC result",
    ],
    [
      "multiple private calls with one observed",
      "loadConfig",
      `async function loadConfig() { await readConfig(); return readConfig() }`,
      "consumer reads the RPC result",
    ],
  ])(
    "rejects an explicit module-private locator with %s",
    async (_label, consumerSymbol, body, reason) => {
      const repoRoot = await createPolicyShadow({
        policy: ignoredResultPolicy({ consumerSymbol, consumerVisibility: "module-private" }),
        consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        ${body}
      `,
        regression: ignoredResultRegression(),
      });

      const report = await auditRpcContracts({ repoRoot });

      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "CONFIG_READ",
          field: "consumer",
          reason,
        }),
      );
    },
    30000,
  );
});
