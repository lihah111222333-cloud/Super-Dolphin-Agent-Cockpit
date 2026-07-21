import { expect } from "vitest";
import { RPC_METHODS } from "../backendApi.js";

export async function callDagDashboardApis(api) {
  await api.listDags({ status: "running", keyword: "build", limit: 7 });
  await api.getDagDetail({ dagKey: "dag-1" });
  await api.getDagRuns({ dagKey: "dag-1", status: "running", limit: 5 });
  await api.getDagRun({ runKey: "run-1" });
  await api.startDag({
    dagKey: "dag-1",
    triggerSource: "manual",
    idempotencyKey: "ui-123",
  });
  await api.createAndStartDag({
    dagKey: "dag-created",
    title: "Created DAG",
    description: "Created from template",
    finalNodeKey: "final",
    metadata: { source: "ui-template" },
    idempotencyKey: "ui-create-123",
    nodes: [
      {
        nodeKey: "draft",
        title: "Draft",
        nodeType: "agent",
        assignedTo: "codex-runner",
        dependsOn: [],
        config: { prompt: "draft" },
      },
    ],
  });
  await api.writeWorkflowMaterial({
    path: "reports/workflows/uploads/dag-1/material.md",
    content: "source text",
  });
  await api.dispatchDagNode({
    dagKey: "dag-1",
    runId: 88,
    nodeKey: "draft",
    assignedTo: "codex-runner",
  });
  await api.terminateDagRun({
    dagKey: "dag-1",
    runKey: "run-1",
    reason: "user_requested",
  });
  await api.deleteDag({ dagKey: "dag-1" });
  await api.applyDagOps({
    dagKey: "dag-1",
    baseVersion: 11,
    ops: [{ op: "update_node", node_key: "draft", patch: { title: "Draft v2" } }],
  });
}

export function dashboardDagNode(overrides = {}) {
  return {
    id: 11,
    dag_key: "dag-1",
    node_key: "draft",
    title: "Draft",
    status: "ready",
    created_at: "2026-07-13T00:00:00Z",
    updated_at: "2026-07-13T00:00:01Z",
    ...overrides,
  };
}

export function dashboardDagSummary(overrides = {}) {
  return {
    id: 7,
    dag_key: "dag-1",
    version: 3,
    title: "Release workflow",
    status: "active",
    schedule_enabled: false,
    created_at: "2026-07-13T00:00:00Z",
    updated_at: "2026-07-13T00:00:01Z",
    ...overrides,
  };
}

export function dashboardDagRun(overrides = {}) {
  return {
    id: 31,
    run_key: "run-1",
    dag_key: "dag-1",
    dag_version_snapshot: 3,
    status: "running",
    started_at: "2026-07-13T00:00:00Z",
    budget_used: 2,
    created_at: "2026-07-13T00:00:00Z",
    updated_at: "2026-07-13T00:00:01Z",
    ...overrides,
  };
}

export function expectDagDashboardCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAGS, {
    status: "running",
    keyword: "build",
    limit: 7,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_DETAIL, {
    dagKey: "dag-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_RUNS, {
    dagKey: "dag-1",
    status: "running",
    limit: 5,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_RUN, {
    runKey: "run-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_START, {
    dagKey: "dag-1",
    triggerSource: "manual",
    idempotencyKey: "ui-123",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_CREATE_AND_START, {
    dagKey: "dag-created",
    title: "Created DAG",
    description: "Created from template",
    finalNodeKey: "final",
    metadata: { source: "ui-template" },
    idempotencyKey: "ui-create-123",
    nodes: [
      {
        nodeKey: "draft",
        title: "Draft",
        nodeType: "agent",
        assignedTo: "codex-runner",
        dependsOn: [],
        config: { prompt: "draft" },
      },
    ],
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_WORKFLOW_MATERIAL_WRITE, {
    path: "reports/workflows/uploads/dag-1/material.md",
    content: "source text",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE, {
    dagKey: "dag-1",
    runId: 88,
    nodeKey: "draft",
    assignedTo: "codex-runner",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_TERMINATE, {
    dagKey: "dag-1",
    runKey: "run-1",
    reason: "user_requested",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_DELETE, {
    dagKey: "dag-1",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_APPLY_OPS, {
    dagKey: "dag-1",
    baseVersion: 11,
    ops: [{ op: "update_node", node_key: "draft", patch: { title: "Draft v2" } }],
  });
}

export function workflowTemplateDetail(overrides = {}) {
  return {
    id: "government-enterprise/meeting-minutes",
    version: 2,
    title: { zh: "会议纪要", en: "Meeting minutes" },
    description: { zh: "生成会议纪要" },
    category: "government-enterprise",
    business_flow: "meeting-review",
    output_types: ["docx"],
    tags: ["meeting"],
    estimated_nodes: 2,
    requires_review: true,
    supports_schedule: false,
    trust: { level: "builtin", source: "repository" },
    compatibility: {
      runtime: "dag-v2",
      node_types: ["agent"],
      required_capabilities: [],
    },
    ui_schema: [],
    dag_template: {
      dag_key_template: "meeting-minutes",
      title_template: "会议纪要",
      description_template: "生成会议纪要",
      trigger: "manual",
      final_node_key: "final",
      nodes: [],
    },
    validation: {
      require_review_before_final: true,
      require_final_node_key: true,
    },
    final_output: {
      node_key: "final",
      kind: "file",
      path_template: "reports/final.docx",
    },
    ...overrides,
  };
}

export function workflowTemplateDraft(overrides = {}) {
  return {
    template_id: "government-enterprise/meeting-minutes",
    template_version: 2,
    dag_key: "meeting-minutes",
    title: "会议纪要",
    description: "生成会议纪要",
    trigger: "manual",
    final_node_key: "final",
    review_node_key: "review",
    nodes: [
      {
        node_key: "draft",
        title: "起草",
        node_type: "agent",
        assigned_to: "codex",
        depends_on: [],
        config: {},
      },
    ],
    final_output: {
      node_key: "final",
      kind: "file",
      path_template: "reports/final.docx",
    },
    metadata: {},
    ...overrides,
  };
}

export function workflowTemplateSummary(overrides = {}) {
  return {
    id: "government-enterprise/meeting-minutes",
    version: 2,
    title: { zh: "会议纪要", en: "Meeting minutes" },
    description: { zh: "生成会议纪要" },
    category: "government-enterprise",
    business_flow: "meeting-review",
    output_types: ["docx"],
    tags: ["meeting"],
    estimated_nodes: 2,
    requires_review: true,
    supports_schedule: false,
    final_node_key: "final",
    trust: { level: "builtin", source: "repository" },
    compatibility: {
      runtime: "dag-v2",
      node_types: ["agent"],
      required_capabilities: [],
    },
    available_versions: [1, 2],
    ...overrides,
  };
}

export function cronJobResponse() {
  return {
    id: "job-1",
    name: "nightly",
    prompt: "run tests",
    schedule_type: "cron",
    schedule_expr: "0 9 * * *",
    provider: "codex",
    cwd: "/repo/app",
    enabled: true,
    failure_count: 0,
    max_attempts: 2,
  };
}

export function cronRunResponse() {
  return { id: "run-1", job_id: "job-1", status: "completed" };
}
