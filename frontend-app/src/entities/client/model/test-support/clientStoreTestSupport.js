export { createThreadMessageFixtures } from "./clientStoreMessageFixtures.js";
export { createDefaultBackendResponses } from "./clientStoreBackendFixtures.test-helper.js";

export function createDeferred() {
  let resolve;
  let reject;
  const promise = new Promise((ok, fail) => {
    resolve = ok;
    reject = fail;
  });
  return { promise, resolve, reject };
}
export function createDeferredThreadMessagesPage(page) {
  const response = createDeferred();
  return {
    promise: response.promise,
    resolvePage: (value) => response.resolve(page(value)),
  };
}
export async function flushPromises(count = 8) {
  for (let index = 0; index < count; index += 1) await Promise.resolve();
}
export async function flushAssistantDeltaBatch(advanceTimers) {
  advanceTimers(50);
  await flushPromises();
}
export function interruptSuccessResult({ expectedTurnId, requestId }) {
  return {
    ok: true,
    accepted: true,
    requestId,
    expectedTurnId,
    turnId: expectedTurnId,
    status: "interrupted",
    confirmed: true,
    mode: "interrupt_confirmed",
    interruptSent: true,
    stateBefore: "running",
    stateAfter: "idle",
    waitedMs: 1,
    activeObserved: true,
  };
}
export function createBoundCapabilities() {
  return [
    {
      kind: "skill",
      key: "skill:project::review:/repo/app/.agents/skills/review",
      name: "review",
      label: "Code Review",
      availability: "ready",
      ref: {
        name: "review",
        scope: "project",
        personalType: "",
        path: "/repo/app/.agents/skills/review",
      },
    },
    {
      kind: "mcp_tool",
      key: "mcp_tool:lsp:lsp_edit",
      name: "lsp_edit",
      label: "LSP Edit",
      serverName: "lsp",
      availability: "ready",
    },
  ];
}
