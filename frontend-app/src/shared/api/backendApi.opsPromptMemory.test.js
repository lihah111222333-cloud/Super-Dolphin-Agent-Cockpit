import { expect, it, vi } from "vitest";
import { createBackendApi } from "./backendApi.js";
import { callMemoryCenterApis, callPromptFacadeMethods } from "./test-support/backendApi.opsPromptMemory.testSupport.js";
import {
  expectMemoryCenterCalls,
  expectMemoryCenterValidation,
  expectPromptFacadeCalls,
  expectPromptFacadeValidation,
} from "./test-support/backendApi.opsPromptMemory.assertions.js";
import { guardedOpsPromptMemoryResponse } from "./test-support/backendApi.opsPromptMemory.responses.js";

it("wraps prompt RPCs with legacy payload shapes", async () => {
  const callAPI = vi.fn((method) => Promise.resolve(guardedOpsPromptMemoryResponse(method)));
  const api = createBackendApi({ callAPI });

  await callPromptFacadeMethods(api);

  expectPromptFacadeCalls(callAPI);
  expectPromptFacadeValidation(api);
});

it("wraps memory center RPCs with the legacy payload shapes", async () => {
  const callAPI = vi.fn((method) => Promise.resolve(guardedOpsPromptMemoryResponse(method)));
  const api = createBackendApi({ callAPI });

  await callMemoryCenterApis(api);

  expectMemoryCenterCalls(callAPI);
  expectMemoryCenterValidation(api);
});

it("rejects malformed memory target payloads before calling the backend", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expect(() =>
    api.getMemoryEntry({
      cwd: "/repo/app",
      target: "",
      path: "feedback/tdd.md",
    }),
  ).toThrow("ui/memory/entry/get: target must be private or team");
  expect(() =>
    api.deleteMemoryEntry({
      cwd: "/repo/app",
      target: "public",
      path: "feedback/tdd.md",
    }),
  ).toThrow("ui/memory/entry/delete: target must be private or team");
  expect(() =>
    api.upsertMemoryEntry({
      cwd: "/repo/app",
      target: "global",
      name: "tdd-rule",
      description: "先写红测",
      type: "feedback",
      content: "规则",
    }),
  ).toThrow("ui/memory/entry/upsert: target must be private or team");
  expect(() =>
    api.mergeMemoryEntries({
      cwd: "/repo/app",
      targetA: "private",
      pathA: "a.md",
      targetB: "global",
      pathB: "b.md",
    }),
  ).toThrow("ui/memory/entry/merge: targetB must be private or team");
  expect(() =>
    api.ignoreMemorySimilarity({
      cwd: "/repo/app",
      targetA: "global",
      pathA: "a.md",
      targetB: "team",
      pathB: "b.md",
    }),
  ).toThrow("ui/memory/similarity/ignore: targetA must be private or team");
  expect(callAPI).not.toHaveBeenCalled();
});
