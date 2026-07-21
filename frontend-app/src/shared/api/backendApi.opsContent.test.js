import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi, getVideoApiKey, setVideoApiKey } from "./backendApi.js";
import { guardedOpsContentResponse } from "./test-support/backendApi.opsContent.testSupport.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";

it("wraps prompt-section and thread read RPCs with stable payloads", async () => {
  const callAPI = vi.fn((method) => Promise.resolve(guardedOpsContentResponse(method)));
  const api = createBackendApi({ callAPI });

  await api.listPromptSections({ cwd: "/repo/app", prompt_id: "prompt-1" });
  await api.writePromptSection({
    cwd: "/repo/app",
    prompt_id: "prompt-1",
    section: "body",
  });
  await api.deletePromptSection({
    cwd: "/repo/app",
    prompt_id: "prompt-1",
    section: "body",
  });
  await api.getThreadMessages({
    threadId: "thread-1",
    limit: 20,
    before: "cursor-1",
  });
  await api.resolveThreadIdentity({ cwd: "/repo/app", thread_id: "thread-2" });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_SECTIONS_LIST, {
    cwd: "/repo/app",
    prompt_id: "prompt-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_SECTIONS_WRITE, {
    cwd: "/repo/app",
    prompt_id: "prompt-1",
    section: "body",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_SECTIONS_DELETE, {
    cwd: "/repo/app",
    prompt_id: "prompt-1",
    section: "body",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_MESSAGES, {
    threadId: "thread-1",
    limit: 20,
    before: "cursor-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_RESOLVE, {
    threadId: "thread-2",
  });

  expect(() => api.listPromptSections({ cwd: "", prompt_id: "prompt-1" })).toThrow("cwd is required");
  expect(() => api.writePromptSection({ cwd: "/repo/app", prompt_id: "" })).toThrow("prompt_id is required");
  expect(() => api.getThreadMessages({ threadId: "" })).toThrow("threadId is required");
  expect(() => api.getThreadMessages({ threadId: "thread-1", surprise: true })).toThrow(
    "thread/messages: unsupported payload field surprise",
  );
  expect(() => api.resolveThreadIdentity({})).toThrow("threadId is required");
});

it("wraps video API key RPCs with named facade methods", async () => {
  const getResponse = { configured: true, masked: "sk***ed" };
  const setResponse = { ok: true };
  const callAPI = vi
    .fn()
    .mockResolvedValueOnce(getResponse)
    .mockResolvedValueOnce(setResponse)
    .mockRejectedValueOnce(new Error("credential store unavailable"));
  const api = createBackendApi({ callAPI });

  await expect(api.getVideoApiKey()).resolves.toEqual(getResponse);
  await expect(
    api.setVideoApiKey({
      apiKey: " sk-test-key ",
      unexpectedUiOnlyField: "must-not-leak",
    }),
  ).resolves.toEqual(setResponse);
  await expect(api.setVideoApiKey({ apiKey: "sk-test-key-2" })).rejects.toThrow("credential store unavailable");

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.UI_VIDEO_GET_API_KEY, {});
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.UI_VIDEO_SET_API_KEY, {
    apiKey: "sk-test-key",
  });
  expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.UI_VIDEO_SET_API_KEY, {
    apiKey: "sk-test-key-2",
  });
  expectInvalidInputDoesNotCall(callAPI, () => api.setVideoApiKey({ apiKey: "" }), "apiKey is required");
  expect(typeof getVideoApiKey).toBe("function");
  expect(typeof setVideoApiKey).toBe("function");
});
