import { guardedBackendResponse } from "./backendApi.guardedResponse.testSupport.js";

export function threadCompactResponse(overrides = {}) {
  return {
    threadId: "thread-1",
    command: "/compact",
    beforeTokens: 1200,
    afterTokens: 640,
    compacted: true,
    ...overrides,
  };
}
export function threadConfigResponse(overrides = {}) {
  return {
    threadId: "thread-1",
    provider: "codex",
    supportsThreadOverride: true,
    override: { model: "gpt-5.5", effort: "high", approvals: "on-request" },
    effective: { model: "gpt-5.5", effort: "high", approvals: "on-request" },
    ...overrides,
  };
}
export function threadRecoverResponse(overrides = {}) {
  return {
    thread: { id: "thread-1", status: "recovering" },
    recovered: true,
    mode: "relaunch_resume",
    ...overrides,
  };
}
export function guardedThreadStateResponse(method) {
  return guardedBackendResponse(method);
}
