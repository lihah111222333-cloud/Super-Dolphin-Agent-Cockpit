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
    expect(() =>
      validate(RPC_METHODS.CRONJOB_LIST, {
        jobs: [{
          id: "job-1", name: "nightly", prompt: "run tests", schedule_type: "cron", schedule_expr: "0 9 * * *",
          provider: "codex", cwd: "/repo/app", enabled: "true", failure_count: 0, max_attempts: 2,
        }],
        next_cursor: "",
        has_more: false,
      }),
    ).toThrow();
  });

  it("validates every non-void cron response and rejects malformed mutations before consumers receive success", async () => {
    const job = {
      id: "job-1", name: "nightly", prompt: "run tests", schedule_type: "cron", schedule_expr: "0 9 * * *",
      provider: "codex", cwd: "/repo/app", enabled: true, failure_count: 0, max_attempts: 2,
    };
    const run = { id: "run-1", job_id: "job-1", status: "completed" };
    const validResponses = new Map([
      [RPC_METHODS.CRONJOB_GET, job], [RPC_METHODS.CRONJOB_CREATE, job], [RPC_METHODS.CRONJOB_UPDATE, job],
      [RPC_METHODS.CRONJOB_RUN_ONCE, job], [RPC_METHODS.CRONJOB_DELETE, { deleted: true, id: "job-1" }],
      [RPC_METHODS.CRONJOB_SET_ENABLED, { id: "job-1", enabled: true }], [RPC_METHODS.CRONJOB_LIST_RUNS, { runs: [run] }],
    ]);
    const validators = createBackendResponseValidators(RPC_METHODS);
    for (const [method, response] of validResponses) {
      const request = method === RPC_METHODS.CRONJOB_DELETE
        ? { id: "job-1" }
        : method === RPC_METHODS.CRONJOB_SET_ENABLED ? { id: "job-1", enabled: true } : undefined;
      expect(validators[method](method, response, request)).toBe(response);
    }

    const malformed = new Map([
      [RPC_METHODS.CRONJOB_GET, { ...job, enabled: "true" }], [RPC_METHODS.CRONJOB_CREATE, { ...job, extra: true }],
      [RPC_METHODS.CRONJOB_UPDATE, { ...job, failure_count: "0" }], [RPC_METHODS.CRONJOB_RUN_ONCE, { ...job, skills: ["ok", 1] }],
      [RPC_METHODS.CRONJOB_DELETE, { deleted: false, id: "job-1" }], [RPC_METHODS.CRONJOB_SET_ENABLED, { id: "job-1", enabled: "true" }],
      [RPC_METHODS.CRONJOB_LIST_RUNS, { runs: [{ ...run, status: 1 }] }],
    ]);
    for (const [method, response] of malformed) expect(() => validators[method](method, response)).toThrow();

    const api = createBackendApi({ callAPI: vi.fn((method) => Promise.resolve(malformed.get(method) ?? { jobs: [], next_cursor: "", has_more: false })) });
    await expect(api.createCronJob({
      cwd: "/repo/app", name: "nightly", prompt: "run tests", scheduleExpr: "0 9 * * *",
      timezone: "Asia/Seoul", provider: "codex", model: "gpt-5", config: {}, skills: [],
      notifyChannel: "desktop", enabled: true, nextRunAt: "", maxAttempts: 2,
    })).rejects.toThrow();
    await expect(api.getCronJob({ id: "job-1" })).rejects.toThrow();
    await expect(api.updateCronJob({
      id: "job-1", cwd: "/repo/app", name: "nightly", prompt: "run tests", scheduleExpr: "0 9 * * *",
      timezone: "Asia/Seoul", provider: "codex", model: "gpt-5", config: {}, skills: [],
      notifyChannel: "desktop", enabled: true, nextRunAt: "", maxAttempts: 2,
    })).rejects.toThrow();
    await expect(api.deleteCronJob({ id: "job-1" })).rejects.toThrow();
    await expect(api.runCronJobOnce({ id: "job-1" })).rejects.toThrow();
    await expect(api.setCronJobEnabled({ id: "job-1", enabled: true })).rejects.toThrow();
    await expect(api.listCronJobRuns({ jobId: "job-1", limit: 1 })).rejects.toThrow();

    const correlationApi = createBackendApi({
      callAPI: vi.fn((method) => Promise.resolve(
        method === RPC_METHODS.CRONJOB_DELETE
          ? { deleted: true, id: "other-job" }
          : { id: "job-1", enabled: false },
      )),
    });
    await expect(correlationApi.deleteCronJob({ id: "job-1" })).rejects.toThrow(/id must equal request id/);
    await expect(correlationApi.setCronJobEnabled({ id: "job-1", enabled: true })).rejects.toThrow(/enabled must equal request enabled/);
    const setEnabledIDMismatchApi = createBackendApi({
      callAPI: vi.fn().mockResolvedValue({ id: "other-job", enabled: true }),
    });
    await expect(setEnabledIDMismatchApi.setCronJobEnabled({ id: "job-1", enabled: true })).rejects.toThrow(/id must equal request id/);

    const successApi = createBackendApi({
      callAPI: vi.fn((method) => Promise.resolve(
        method === RPC_METHODS.CRONJOB_DELETE
          ? { deleted: true, id: "job-1" }
          : { id: "job-1", enabled: true },
      )),
    });
    await expect(successApi.deleteCronJob({ id: "job-1" })).resolves.toEqual({ deleted: true, id: "job-1" });
    await expect(successApi.setCronJobEnabled({ id: "job-1", enabled: true })).resolves.toEqual({ id: "job-1", enabled: true });
  });
});
