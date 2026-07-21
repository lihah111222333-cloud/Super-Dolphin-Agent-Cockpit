import { expect, it } from "vitest";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { auditRpcContracts } from "./rpc-contract-audit.mjs";
import {
  REPO_ROOT,
  createShadowRepo,
  MATRIX_PATH,
  shadowMatrix,
  createPolicyShadow,
  resultHandledPolicy,
  resultHandledConsumer,
  resultHandler,
  resultHandledRegression,
  REAL_INJECTION_PATH,
  REAL_CONSUMER_PATH,
  REAL_REGRESSION_PATH,
  realResultHandledPolicy,
  createRealResultHandledShadow,
  createMutatedSingleHelperResultHandledShadow,
  createMutatedRealResultHandledShadow,
} from "./rpc-audit-test-support.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it.each([
    [
      "an unconditional return between the no-thread guard and try",
      (source) => source.replace("          try {", "          return null\n          try {"),
    ],
    [
      "an unconditional throw between the no-thread guard and try",
      (source) =>
        source.replace(
          "          try {",
          "          throw new Error('unreachable try')\n          try {",
        ),
    ],
    [
      "an if (true) return between the no-thread guard and try",
      (source) =>
        source.replace("          try {", "          if (true) return null\n          try {"),
    ],
    [
      "an extra statement after the result try",
      (source) =>
        source.replace(
          `          } catch (error) {
            return { ok: false, threadId, result: null }
          }
        }
        const activeThreadRPC`,
          `          } catch (error) {
            return { ok: false, threadId, result: null }
          }
          notifyAction('late result', 'info')
        }
        const activeThreadRPC`,
        ),
    ],
  ])(
    "rejects a noncanonical single-helper body with %s",
    async (_label, mutateRuntime) => {
      const repoRoot = await createMutatedSingleHelperResultHandledShadow(mutateRuntime);
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "TURN_INTERRUPT",
          field: "consumer",
        }),
      );
    },
    30000,
  );

  it.each([
    [
      "direct warning seeded before awaited runtime call",
      {
        regressionBeforeAwait: "runtime.notifyAction('turn already completed', 'warning')",
      },
    ],
    [
      "indirect warning seeded before awaited runtime call",
      {
        regressionDefinitions:
          "function seedWarning(runtime) { runtime.addWarning('warn', 'thread.interrupt.failed', { error: 'turn already completed' }) }",
        regressionBeforeAwait: "seedWarning(runtime)",
      },
    ],
  ])(
    "rejects TURN_INTERRUPT regression bypass: %s",
    async (_label, overrides) => {
      const repoRoot = await createRealResultHandledShadow(overrides);
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({ key: "TURN_INTERRUPT", field: "regressionTest" }),
      );
    },
    30000,
  );

  it("rejects a decoy runtime flow when the exact runActiveThreadRPC helper passes response instead of result", async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      runtime: (source) =>
        source.replace(
          "if (notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })) return { ok: false, threadId, result };",
          "if (notifyThreadActionFailure({ action, addWarning, notifyAction, response: result, threadId })) return { ok: false, threadId, result };",
        ).concat(`
          async function decoyRuntimeFlow(action, rpc, addWarning, notifyAction, threadId) {
            const result = await rpc({})
            return notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })
          }
        `),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "TURN_INTERRUPT",
        field: "consumer",
      }),
    );
  }, 30000);

  it("rejects a nested handler-call decoy inside the exact runActiveThreadRPC helper", async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      runtime: (source) =>
        source.replace(
          "if (notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })) return { ok: false, threadId, result };",
          [
            "const decoy = () => notifyThreadActionFailure({ action, addWarning, notifyAction, result, ",
            "threadId });\n      if (notifyThreadActionFailure({ action, addWarning, notifyAction, respo",
            "nse: result, threadId })) return { ok: false, threadId, result };",
          ].join(""),
        ),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "TURN_INTERRUPT",
        field: "consumer",
      }),
    );
  }, 30000);

  it("rejects a fabricated interrupt failure helper return after dead derived-message evidence", async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      runtime: (source) =>
        source
          .replace(
            "    notifyAction('中断当前执行失败，请重试。', 'warning', { threadId });",
            "    if (false) notifyAction('中断当前执行失败，请重试。', 'warning', { threadId });",
          )
          .replace(
            "    return true;\n  }\n  if (action === 'thread.force_complete'",
            "    return 'fabricated interrupt failure';\n  }\n  if (action === 'thread.force_complete'",
          ),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "TURN_INTERRUPT",
        field: "handler",
      }),
    );
  }, 30000);

  it("rejects a TURN_INTERRUPT handler predicate weakened by an always-true disjunction", async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      runtime: (source) =>
        source.replace(
          "action === 'thread.interrupt' && result?.ok === false",
          "((action === 'thread.interrupt' && result?.ok === false) || true)",
        ),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "TURN_INTERRUPT",
        field: "handler",
      }),
    );
  }, 30000);

  it.each([
    ["a top-level throw before the return", "throw new Error('unreachable injection');"],
    ["an infinite loop before the return", "while (true) {}"],
  ])(
    "rejects an unreachable real TURN_INTERRUPT injection after %s",
    async (_label, blocker) => {
      const repoRoot = await createMutatedRealResultHandledShadow({
        injection: (source) =>
          source.replace(
            "function createActiveThreadActions(runtime, deps) {\n  return {",
            `function createActiveThreadActions(runtime, deps) {\n  ${blocker}\n  return {`,
          ),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "TURN_INTERRUPT",
          field: "consumer",
        }),
      );
    },
    30000,
  );

  it.each([
    [
      "a duplicate later property",
      '    interruptActiveThread: () => runtime.activeThreadRPC("thread.force_complete", forceCompleteTurn),',
    ],
    [
      "a later spread property",
      '    ...{ interruptActiveThread: () => runtime.activeThreadRPC("thread.force_complete", forceCompleteTurn) },',
    ],
  ])(
    "rejects a real TURN_INTERRUPT injection overridden by %s",
    async (_label, override) => {
      const repoRoot = await createMutatedRealResultHandledShadow({
        injection: (source) =>
          source.replace(
            '    interruptActiveThread: () =>\n      runtime.activeThreadRPC("thread.interrupt", interruptTurn),',
            '    interruptActiveThread: () =>\n      runtime.activeThreadRPC("thread.interrupt", interruptTurn),\n' +
              override,
          ),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "TURN_INTERRUPT",
          field: "consumer",
        }),
      );
    },
    30000,
  );

  it("rejects warning seeding after the first post-matcher rpc assertion", async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      regression: (source) =>
        source
          .replace(
            "import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js';",
            "import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js';\nfunction seedWarning(runtime) { runtime.notifyAction('seeded', 'warning'); }",
          )
          .replaceAll(
            "      source: 'ui_stop',\n    });\n    expect(runtime.notifyAction)",
            "      source: 'ui_stop',\n    });\n    seedWarning(runtime);\n    expect(runtime.notifyAction)",
          ),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "TURN_INTERRUPT",
        field: "regressionTest",
      }),
    );
  }, 30000);

  it("rejects a warning-producing variable declaration before the awaited runtime assertion", async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      regression: (source) =>
        source.replace(
          [
            "it('reports interrupt ok:false as warning without showing success', async () => {",
            "\n    const runtime = createRuntime();",
          ].join(""),
          [
            "it('reports interrupt ok:false as warning without showing success', async () => {",
            "\n    const runtime = createRuntime();",
            "\n    const seeded = runtime.notifyAction('seeded', 'warning');",
          ].join(""),
        ),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "TURN_INTERRUPT",
        field: "regressionTest",
      }),
    );
  }, 30000);

  it("rejects a post-await helper call that only nests expect in an argument", async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      regression: (source) =>
        source
          .replace(
            "import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js';",
            [
              "import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js';\nfunction seedW",
              "arning(runtime, proof) { runtime.notifyAction('seeded', 'warning'); return proof; }",
            ].join(""),
          )
          .replace(
            "await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false);",
            "await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false);\n    seedWarning(runtime, expect(true).toBe(true));",
          ),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "TURN_INTERRUPT",
        field: "regressionTest",
      }),
    );
  }, 30000);

  it.each([
    ["wrong injected facade", { facade: "startThread" }],
    ["wrong action literal", { action: "thread.force_complete" }],
    [
      "dead injection branch",
      {
        injectionBody:
          "if (false) return { interruptActiveThread: () => runtime.activeThreadRPC('thread.interrupt', interruptTurn) }; return {}",
      },
    ],
    [
      "conditional injection branch",
      {
        injectionBody:
          "if (runtime.enabled) return { interruptActiveThread: () => runtime.activeThreadRPC('thread.interrupt', interruptTurn) }; return {}",
      },
    ],
    [
      "loop-only injection",
      {
        injectionBody:
          "while (runtime.enabled) return { interruptActiveThread: () => runtime.activeThreadRPC('thread.interrupt', interruptTurn) }; return {}",
      },
    ],
    [
      "nested callback injection",
      {
        injectionBody:
          "return { interruptActiveThread: () => () => runtime.activeThreadRPC('thread.interrupt', interruptTurn) }",
      },
    ],
    ["different result property", { resultProperty: "response" }],
    ["constant-true handler", { handlerCondition: "true" }],
    ["constant-false handler", { handlerCondition: "false" }],
    [
      "success-only handler",
      { handlerCondition: "action === 'thread.interrupt' && result?.ok === true" },
    ],
    ["indirect warning helper", { warningBody: "emitWarning(notifyAction, result.error)" }],
    [
      "unrelated info helper",
      {
        handlerDefinitions: "function unrelatedMessage(result) { return result.info }",
        warningBody:
          "const message = unrelatedMessage(result); notifyAction(message, 'warning'); addWarning('warn', action, { error: message })",
      },
    ],
    [
      "unproved helper between consumer and assertion",
      { regressionBetween: "assertWarning(runtime)" },
    ],
  ])(
    "rejects unsound real TURN_INTERRUPT proof: %s",
    async (_label, overrides) => {
      const repoRoot = await createRealResultHandledShadow(overrides);
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({ key: "TURN_INTERRUPT", kind: "result-handled" }),
      );
    },
    30000,
  );

  it("does not leak the runtime/private-handler exception to another result-handled key", async () => {
    const actual = {
      injection: await readFile(join(REPO_ROOT, REAL_INJECTION_PATH), "utf8"),
      runtime: await readFile(join(REPO_ROOT, REAL_CONSUMER_PATH), "utf8"),
      regression: await readFile(join(REPO_ROOT, REAL_REGRESSION_PATH), "utf8"),
    };
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${realResultHandledPolicy()} }`, {
        key: "CONFIG_READ",
        facade: "readConfig",
      }),
      [REAL_INJECTION_PATH]: actual.injection,
      [REAL_CONSUMER_PATH]: actual.runtime,
      [REAL_REGRESSION_PATH]: actual.regression,
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "CONFIG_READ",
        field: "consumer",
      }),
    );
  }, 30000);

  it.each([
    ["wrong facade", { consumer: resultHandledConsumer({ facade: "startThread" }) }],
    ["ignored facade result", { consumer: resultHandledConsumer({ ignored: true }) }],
    [
      "unrelated handler argument",
      { consumer: resultHandledConsumer({ argument: "{ ok: false }" }) },
    ],
    [
      "shadowed handler import",
      {
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn(handleInterruptResult) {
        const result = await readConfig()
        return handleInterruptResult(result)
      }
    `,
      },
    ],
    [
      "nested handler forwarding",
      {
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn() {
        const result = await readConfig()
        return () => handleInterruptResult(result)
      }
    `,
      },
    ],
    [
      "block-shadowed result binding",
      {
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn() {
        const result = await readConfig()
        {
          const result = { ok: true }
          return handleInterruptResult(result)
        }
      }
    `,
      },
    ],
    [
      "catch-parameter-shadowed result binding",
      {
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn() {
        try {
          const result = await readConfig()
          throw result
        } catch (result) {
          return handleInterruptResult(result)
        }
      }
    `,
      },
    ],
    [
      "dead consumer path",
      {
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn() {
        if (false) {
          const result = await readConfig()
          return handleInterruptResult(result)
        }
      }
    `,
      },
    ],
    [
      "one-sided handler branch",
      {
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn(flag) {
        const result = await readConfig()
        if (flag) return handleInterruptResult(result)
        return true
      }
    `,
      },
    ],
    [
      "loop-only handler path",
      {
        consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn(flag) {
        const result = await readConfig()
        while (flag) return handleInterruptResult(result)
        return true
      }
    `,
      },
    ],
    ["handler does not inspect outcome", { handler: resultHandler({ body: "return true" }) }],
    [
      "dead handler branch",
      { handler: resultHandler({ body: "if (false && !result.ok) console.warn(result.error)" }) },
    ],
    [
      "nested callback handling",
      {
        handler: resultHandler({
          body: "return () => { if (!result.ok) console.warn(result.error) }",
        }),
      },
    ],
    ["wrong mocked facade", { regression: resultHandledRegression({ mockFacade: "startThread" }) }],
    [
      "generic assertion",
      { regression: resultHandledRegression({ assertion: "expect(true).toBe(true)" }) },
    ],
    [
      "handler test mismatch",
      {
        regression: resultHandledRegression({
          assertion: "expect(warn).toHaveBeenCalledWith('different warning')",
        }),
      },
    ],
    [
      "transport rejection",
      {
        regression: resultHandledRegression({
          response: "Promise.reject(new Error('offline'))",
          assertion: "await expect(interruptTurn()).rejects.toThrow('offline')",
        }),
      },
    ],
    [
      "synthetic warning",
      {
        regression: resultHandledRegression({
          beforeAssertion: "console.warn('interrupt denied')",
        }),
      },
    ],
    [
      "direct warning handler invocation",
      {
        regression: resultHandledRegression({
          extraImports: "import { handleInterruptResult } from './resultHandler.js'",
          beforeAssertion: "handleInterruptResult({ ok: false, error: 'interrupt denied' })",
        }),
      },
    ],
    [
      "dead consumer invocation",
      {
        regression: resultHandledRegression({
          consumerInvocation: "if (false) await interruptTurn()",
          beforeAssertion: "console.warn('interrupt denied')",
        }),
      },
    ],
    [
      "unawaited consumer invocation",
      {
        regression: resultHandledRegression({
          consumerInvocation: "interruptTurn()",
          beforeAssertion: "console.warn('interrupt denied')",
        }),
      },
    ],
  ])(
    "rejects unsound result-handled proof: %s",
    async (_label, overrides) => {
      const repoRoot = await createPolicyShadow({
        policy: resultHandledPolicy(),
        consumer: resultHandledConsumer(),
        handler: resultHandler(),
        regression: resultHandledRegression(),
        ...overrides,
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toEqual([
        expect.objectContaining({ key: "CONFIG_READ", kind: "result-handled" }),
      ]);
    },
    30000,
  );
});
