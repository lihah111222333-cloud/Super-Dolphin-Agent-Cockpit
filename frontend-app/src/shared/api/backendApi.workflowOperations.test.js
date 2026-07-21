import { expect, it, vi } from "vitest";
import { RPC_METHODS, createAndStartDag, createBackendApi, writeWorkflowMaterial } from "./backendApi.js";
import {
  callDagDashboardApis,
  dashboardDagNode,
  expectDagDashboardCalls,
} from "./test-support/backendApi.workflow.testSupport.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";
import { guardedWorkflowResponse } from "./test-support/backendApi.workflow.responses.js";

it("wraps DAG dashboard RPCs with the legacy payload shapes", async () => {
  const callAPI = vi.fn((method) => Promise.resolve(guardedWorkflowResponse(method)));
  const api = createBackendApi({ callAPI });

  await callDagDashboardApis(api);

  expectDagDashboardCalls(callAPI);
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.applyDagOps({ dagKey: "dag-1", ops: [] }),
    "baseVersion is required",
  );
  expectInvalidInputDoesNotCall(callAPI, () => api.getDagRun({ runKey: "" }), "runKey is required");
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.dispatchDagNode({
        dagKey: "dag-1",
        runId: 88,
        nodeKey: "draft",
        assignedTo: "",
      }),
    "assignedTo is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.terminateDagRun({ dagKey: "dag-1", runKey: "" }),
    "runKey is required",
  );
  expect(typeof createAndStartDag).toBe("function");
  expect(typeof writeWorkflowMaterial).toBe("function");
});

it("passes through representative DAG mutation responses", async () => {
  const dispatchResponse = {
    node: dashboardDagNode({ assigned_to: "codex-runner" }),
    wakeup_id: 88,
    enqueued: true,
  };
  const applyResponse = { newVersion: 12 };
  const callAPI = vi.fn().mockResolvedValueOnce(dispatchResponse).mockResolvedValueOnce(applyResponse);
  const api = createBackendApi({ callAPI });

  await expect(
    api.dispatchDagNode({
      dagKey: "dag-1",
      runId: 88,
      nodeKey: "draft",
      assignedTo: "codex-runner",
    }),
  ).resolves.toEqual(dispatchResponse);
  await expect(
    api.applyDagOps({
      dagKey: "dag-1",
      baseVersion: 11,
      ops: [{ op: "update_node", node_key: "draft", patch: { title: "Draft v2" } }],
    }),
  ).resolves.toEqual(applyResponse);
});

it("wraps cronjob RPCs with validated payload shapes", async () => {
  const callAPI = vi.fn((method) => Promise.resolve(guardedWorkflowResponse(method)));
  const api = createBackendApi({ callAPI });
  const cronPayload = {
    cwd: "/repo/app",
    name: "nightly",
    prompt: "run tests",
    scheduleExpr: "0 9 * * *",
    timezone: "Asia/Shanghai",
    provider: "codex",
    model: "gpt-5",
    config: {
      codexHome: "/codex",
      codexInstanceKey: "default",
      codexModelProvider: "openai",
    },
    skills: ["测试规范"],
    notifyChannel: "desktop",
    enabled: true,
    nextRunAt: "2026-07-05T01:00:00Z",
    maxAttempts: 2,
  };

  await api.listCronJobs({ limit: 25, cursor: "" });
  await api.getCronJob({ id: "job-1" });
  await api.createCronJob(cronPayload);
  await api.updateCronJob({
    ...cronPayload,
    id: "job-1",
    name: "nightly v2",
    enabled: false,
  });
  await api.deleteCronJob({ id: "job-1" });
  await api.runCronJobOnce({ id: "job-1" });
  await api.setCronJobEnabled({ id: "job-1", enabled: true });
  await api.listCronJobRuns({ jobId: "job-1", limit: 50 });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_LIST, {
    limit: 25,
    cursor: "",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_GET, {
    id: "job-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_CREATE, {
    cwd: "/repo/app",
    name: "nightly",
    prompt: "run tests",
    schedule_expr: "0 9 * * *",
    timezone: "Asia/Shanghai",
    provider: "codex",
    model: "gpt-5",
    config: {
      codexHome: "/codex",
      codexInstanceKey: "default",
      codexModelProvider: "openai",
    },
    skills: ["测试规范"],
    notify_channel: "desktop",
    enabled: true,
    next_run_at: "2026-07-05T01:00:00Z",
    max_attempts: 2,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_UPDATE, {
    id: "job-1",
    cwd: "/repo/app",
    name: "nightly v2",
    prompt: "run tests",
    schedule_expr: "0 9 * * *",
    timezone: "Asia/Shanghai",
    provider: "codex",
    model: "gpt-5",
    config: {
      codexHome: "/codex",
      codexInstanceKey: "default",
      codexModelProvider: "openai",
    },
    skills: ["测试规范"],
    notify_channel: "desktop",
    enabled: false,
    next_run_at: "2026-07-05T01:00:00Z",
    max_attempts: 2,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_DELETE, {
    id: "job-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_RUN_ONCE, {
    id: "job-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_SET_ENABLED, {
    id: "job-1",
    enabled: true,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_LIST_RUNS, {
    job_id: "job-1",
    limit: 50,
  });
  expectInvalidInputDoesNotCall(callAPI, () => api.createCronJob({ ...cronPayload, cwd: "" }), "cwd is required");
  expectInvalidInputDoesNotCall(callAPI, () => api.createCronJob({ ...cronPayload, name: "" }), "name is required");
  expectInvalidInputDoesNotCall(callAPI, () => api.createCronJob({ ...cronPayload, prompt: "" }), "prompt is required");
  expectInvalidInputDoesNotCall(
    callAPI,
    () => api.createCronJob({ ...cronPayload, scheduleExpr: "" }),
    "schedule_expr is required",
  );
  expectInvalidInputDoesNotCall(callAPI, () => api.updateCronJob({ ...cronPayload, id: "" }), "id is required");
  expect(() => api.getCronJob({ id: "" })).toThrow("id is required");
  expect(() => api.setCronJobEnabled({ id: "job-1", enabled: "true" })).toThrow("enabled must be boolean");
  expect(() => api.listCronJobRuns({ jobId: "" })).toThrow("job_id is required");
});
