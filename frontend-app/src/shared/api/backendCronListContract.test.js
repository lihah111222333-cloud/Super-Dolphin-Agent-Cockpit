import { describe, expect, it, vi } from "vitest";
import { createBackendApi } from "./backendApi.js";
import { createBackendResponseValidators } from "./backendResponseValidators.js";
import { RPC_METHODS } from "./backend/backendRpcMethods.js";

describe("cron list page contract", () => {
  const validate =
    createBackendResponseValidators(RPC_METHODS)[RPC_METHODS.CRONJOB_LIST];

  it("maps only exact limit/cursor request fields and rejects omissions or aliases", async () => {
    const callAPI = vi
      .fn()
      .mockResolvedValue({ jobs: [], next_cursor: "", has_more: false });
    const api = createBackendApi({ callAPI });

    await api.listCronJobs({ limit: 2, cursor: "" });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_LIST, {
      limit: 2,
      cursor: "",
    });

    for (const invalid of [
      {},
      { limit: 2 },
      { cursor: "" },
      { limit: 0, cursor: "" },
      { limit: 101, cursor: "" },
      { limit: 2, cursor: 1 },
      { limit: 2, cursor: "", nextCursor: "" },
      { limit: 2, cursor: "", stale: true },
    ]) {
      expect(() => api.listCronJobs(invalid)).toThrow();
    }
    expect(callAPI).toHaveBeenCalledTimes(1);
  });

  it("requires exact snake_case response fields", () => {
    const response = { jobs: [], next_cursor: "", has_more: false };
    expect(validate(RPC_METHODS.CRONJOB_LIST, response)).toBe(response);
    for (const key of Object.keys(response)) {
      const missing = { ...response };
      delete missing[key];
      expect(() => validate(RPC_METHODS.CRONJOB_LIST, missing)).toThrow();
    }
    expect(() =>
      validate(RPC_METHODS.CRONJOB_LIST, {
        jobs: [],
        nextCursor: "",
        has_more: false,
      }),
    ).toThrow();
    expect(() =>
      validate(RPC_METHODS.CRONJOB_LIST, {
        jobs: [],
        next_cursor: "",
        has_more: false,
        stale: true,
      }),
    ).toThrow();
    expect(() =>
      validate(RPC_METHODS.CRONJOB_LIST, {
        jobs: [],
        next_cursor: "cursor",
        has_more: false,
      }),
    ).toThrow();
    expect(() =>
      validate(RPC_METHODS.CRONJOB_LIST, {
        jobs: [],
        next_cursor: "",
        has_more: true,
      }),
    ).toThrow();
  });
});
