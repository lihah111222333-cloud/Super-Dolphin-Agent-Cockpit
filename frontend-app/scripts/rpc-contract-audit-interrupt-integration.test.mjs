import { expect, it } from "vitest";
import { auditRpcContracts, parseContractMatrixForTest } from "./rpc-contract-audit.mjs";
import {
  CONSUMER_PATH,
  UNUSED_POLICY,
  shadowMatrix,
  createPolicyShadow,
  ignoredResultPolicy,
  resultHandledPolicy,
  resultHandledConsumer,
  resultHandler,
  resultHandledRegression,
  createRealResultHandledShadow,
  createMutatedSingleHelperResultHandledShadow,
  createMutatedRealResultHandledShadow,
  ignoredResultRegression,
} from "./rpc-audit-test-support.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it("rejects an invalid response locator visibility marker", () => {
    expect(() =>
      parseContractMatrixForTest(
        shadowMatrix(`{
      responsePolicy: ${ignoredResultPolicy({ consumerVisibility: "private-ish" })}
    }`),
      ),
    ).toThrow("responsePolicy.consumer.visibility");
  });

  it.each([
    [
      "exported function",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      function wrapper() { async function loadConfig() { return readConfig() } }
      export async function loadConfig() { await readConfig() }
    `,
    ],
    [
      "exported const",
      `
      import { readConfig } from '../../shared/api/backendApi.js'
      const wrapper = { loadConfig: () => readConfig() }
      export const loadConfig = async () => { await readConfig() }
    `,
    ],
  ])(
    "resolves the exact %s binding for a locator and regression import",
    async (_label, consumer) => {
      const repoRoot = await createPolicyShadow({
        policy: ignoredResultPolicy(),
        consumer,
        regression: ignoredResultRegression(),
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toEqual([]);
    },
    30000,
  );

  it("rejects unused policy with a production reference", async () => {
    const repoRoot = await createPolicyShadow({
      policy: UNUSED_POLICY,
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() { return readConfig() }
      `,
    });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.invalidResponsePolicyEvidence).toContainEqual({
      key: "CONFIG_READ",
      kind: "unused",
      field: "productionScanRoots",
      path: CONSUMER_PATH,
      symbol: "readConfig",
      reason: "production facade reference exists",
    });
  }, 30000);

  it("accepts result-handled proof for the real consumer, envelope handler, and warning regression", async () => {
    const repoRoot = await createPolicyShadow({
      policy: resultHandledPolicy(),
      consumer: resultHandledConsumer(),
      handler: resultHandler(),
      regression: resultHandledRegression(),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it("accepts the real TURN_INTERRUPT injection, private handler, and runtime regression shape", async () => {
    const repoRoot = await createRealResultHandledShadow();
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it("rejects TURN_INTERRUPT timeout regression proof when the exact timeout fixture becomes success", async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      regression: (source) => {
        const mutated = source.replace(
          "ok: false, accepted: true, requestId:",
          "ok: true, accepted: true, requestId:",
        );
        expect(mutated).not.toBe(source);
        return mutated;
      },
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
    [
      "outer predicate is false",
      (source) =>
        source.replace(
          "if (action === 'thread.interrupt' && result?.ok === false) {",
          "if (false) {",
        ),
    ],
    [
      "timeout warning is deleted",
      (source) =>
        source.replace(
          "      notifyAction(INTERRUPT_UNCONFIRMED_MESSAGE, 'warning', { threadId });\n",
          "",
        ),
    ],
    [
      "timeout warning message is changed",
      (source) =>
        source.replace(
          "const INTERRUPT_UNCONFIRMED_MESSAGE = '停止未确认，任务可能仍在运行';",
          "const INTERRUPT_UNCONFIRMED_MESSAGE = '错误的停止提示';",
        ),
    ],
    [
      "timeout warning code is changed",
      (source) => source.replace("`${action}.unconfirmed`", "`${action}.other`"),
    ],
  ])(
    "rejects TURN_INTERRUPT handler proof when %s",
    async (_label, mutateRuntime) => {
      const repoRoot = await createMutatedRealResultHandledShadow({
        runtime: (source) => {
          const mutated = mutateRuntime(source);
          expect(mutated).not.toBe(source);
          return mutated;
        },
      });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({
          key: "TURN_INTERRUPT",
          field: "handler",
        }),
      );
    },
    30000,
  );

  it("rejects TURN_INTERRUPT when branch validation is removed", async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      runtime: (source) =>
        source.replace(
          "      if (action === 'thread.interrupt') validateInterruptResponse(result, request);\n",
          "",
        ),
    });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({
        key: "TURN_INTERRUPT",
        field: "consumer",
      }),
    );
  }, 15000);

  it.each([
    [
      "handler before validation",
      (source) =>
        source
          .replace(
            "      if (action === 'thread.interrupt') validateInterruptResponse(result, request);\n      if (notifyThreadActionFailure",
            "      if (notifyThreadActionFailure",
          )
          .replace(
            ")) return { ok: false, threadId, result };\n      return { ok: true",
            ")) return { ok: false, threadId, result };\n      if (action === 'thread.interrupt') validateInterruptResponse(result, request);\n      return { ok: true",
          ),
    ],
    [
      "fabricated validation result",
      (source) =>
        source.replace(
          "      if (action === 'thread.interrupt') validateInterruptResponse(result, request);",
          "      if (action === 'thread.interrupt') validateInterruptResponse({ ...result }, request);",
        ),
    ],
    [
      "fabricated validation request",
      (source) =>
        source.replace(
          "      if (action === 'thread.interrupt') validateInterruptResponse(result, request);",
          "      if (action === 'thread.interrupt') validateInterruptResponse(result, { ...request });",
        ),
    ],
  ])(
    "rejects TURN_INTERRUPT validation proof with %s",
    async (_label, mutateRuntime) => {
      const repoRoot = await createMutatedRealResultHandledShadow({ runtime: mutateRuntime });
      const report = await auditRpcContracts({ repoRoot });
      expect(report.invalidResponsePolicyEvidence).toContainEqual(
        expect.objectContaining({ key: "TURN_INTERRUPT", field: "consumer" }),
      );
    },
    15000,
  );

  it("accepts one exact helper hop from activeThreadRPC to runActiveThreadRPC", async () => {
    const repoRoot = await createRealResultHandledShadow({});
    const report = await auditRpcContracts({ repoRoot });
    expect(report.invalidResponsePolicyEvidence).toEqual([]);
  }, 30000);

  it.each([
    [
      "a shadow runActiveThreadRPC binding",
      (source) =>
        source.replace(
          "        Object.assign(runtime, { activeThreadRPC })",
          "        { const runActiveThreadRPC = async () => ({ ok: true }) }\n        Object.assign(runtime, { activeThreadRPC })",
        ),
    ],
    [
      "a computed helper call",
      (source) =>
        source.replace(
          "const outcome = await runActiveThreadRPC(action, rpc)",
          "const outcome = await ({ runActiveThreadRPC })['runActiveThreadRPC'](action, rpc)",
        ),
    ],
    [
      "a nested handler-call decoy",
      (source) =>
        source.replace(
          "const result = await rpc({})",
          "const decoy = () => notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })\n          const result = await rpc({})",
        ),
    ],
    [
      "two awaited rpc calls in the helper",
      (source) =>
        source.replace(
          "const result = await rpc({})",
          "const result = await rpc({})\n          await rpc({})",
        ),
    ],
    [
      "two awaited helper calls in the wrapper",
      (source) =>
        source.replace(
          "const outcome = await runActiveThreadRPC(action, rpc)",
          "const outcome = await runActiveThreadRPC(action, rpc)\n          await runActiveThreadRPC(action, rpc)",
        ),
    ],
    [
      "swapped wrapper arguments",
      (source) =>
        source.replace(
          "const outcome = await runActiveThreadRPC(action, rpc)",
          "const outcome = await runActiveThreadRPC(rpc, action)",
        ),
    ],
    [
      "the wrong handler action argument",
      (source) =>
        source.replace(
          "notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })",
          "notifyThreadActionFailure({ action: rpc, addWarning, notifyAction, result: result, threadId })",
        ),
    ],
    [
      "a fabricated handler result",
      (source) =>
        source
          .replace(
            "const result = await rpc({})",
            "const result = await rpc({})\n          const fabricatedResult = { ok: false }",
          )
          .replace(
            "notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })",
            "notifyThreadActionFailure({ action, addWarning, notifyAction, result: fabricatedResult, threadId })",
          ),
    ],
    [
      "an ignored helper outcome",
      (source) => source.replace("          if (!outcome.ok) return false\n", ""),
    ],
  ])(
    "rejects a single-helper TURN_INTERRUPT proof with %s",
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
      "the complete result flow inside if (false)",
      (source) =>
        source.replace(
          `            const result = await rpc({})
            if (notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })) return { ok: false, threadId, result }
            return { ok: true, threadId, result }`,
          `            if (false) {
              const result = await rpc({})
              if (notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })) return { ok: false, threadId, result }
              return { ok: true, threadId, result }
            }`,
        ),
    ],
    [
      "the complete result flow inside an arbitrary branch",
      (source) =>
        source.replace(
          `            const result = await rpc({})
            if (notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })) return { ok: false, threadId, result }
            return { ok: true, threadId, result }`,
          `            if (rpc.enabled) {
              const result = await rpc({})
              if (notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })) return { ok: false, threadId, result }
              return { ok: true, threadId, result }
            }`,
        ),
    ],
    [
      "an unconditional return before await rpc",
      (source) =>
        source.replace(
          "            const result = await rpc({})",
          "            return null\n            const result = await rpc({})",
        ),
    ],
    [
      "an unconditional throw before await rpc",
      (source) =>
        source.replace(
          "            const result = await rpc({})",
          "            throw new Error('unreachable result flow')\n            const result = await rpc({})",
        ),
    ],
  ])(
    "rejects an unreachable single-helper TURN_INTERRUPT proof with %s",
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
      "a labeled return before result",
      (source) =>
        source.replace(
          "            const result = await rpc({})",
          "            unreachable: return null\n            const result = await rpc({})",
        ),
    ],
    [
      "a bare nested block with a return before result",
      (source) =>
        source.replace(
          "            const result = await rpc({})",
          "            { return null }\n            const result = await rpc({})",
        ),
    ],
    [
      "an if (true) return before result",
      (source) =>
        source.replace(
          "            const result = await rpc({})",
          "            if (true) return null\n            const result = await rpc({})",
        ),
    ],
    [
      "a nested try-finally return before result",
      (source) =>
        source.replace(
          "            const result = await rpc({})",
          "            try {} finally { return null }\n            const result = await rpc({})",
        ),
    ],
    [
      "a top-level finally return overriding the result flow",
      (source) =>
        source.replace(
          `          } catch (error) {
            return { ok: false, threadId, result: null }
          }`,
          `          } catch (error) {
            return { ok: false, threadId, result: null }
          } finally {
            return null
          }`,
        ),
    ],
    [
      "a top-level finally without a return",
      (source) =>
        source.replace(
          `          } catch (error) {
            return { ok: false, threadId, result: null }
          }`,
          `          } catch (error) {
            return { ok: false, threadId, result: null }
          } finally {
            notifyAction('cleanup', 'info')
          }`,
        ),
    ],
  ])(
    "rejects a noncanonical single-helper try proof with %s",
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
});
