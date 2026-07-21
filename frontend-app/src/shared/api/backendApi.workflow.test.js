import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi, rollbackWorkflowTemplate, saveWorkflowTemplate } from "./backendApi.js";
import {
  dashboardDagNode,
  dashboardDagRun,
  workflowTemplateDetail,
  workflowTemplateDraft,
  workflowTemplateSummary,
} from "./test-support/backendApi.workflow.testSupport.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";
it("wraps workflow template RPC methods with canonical payloads", async () => {
  const callAPI = vi.fn((method) => {
    if (method === RPC_METHODS.WORKFLOW_TEMPLATES_LIST)
      return Promise.resolve({ templates: [workflowTemplateSummary()] });
    if (method === RPC_METHODS.WORKFLOW_TEMPLATES_GET) return Promise.resolve({ template: workflowTemplateDetail() });
    if (method === RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG)
      return Promise.resolve({ draft: workflowTemplateDraft() });
    if (method === RPC_METHODS.WORKFLOW_TEMPLATES_SAVE) return Promise.resolve({ template: workflowTemplateSummary() });
    if (method === RPC_METHODS.WORKFLOW_TEMPLATES_ROLLBACK)
      return Promise.resolve({
        template: workflowTemplateSummary({ version: 1 }),
      });
    return Promise.resolve({ ok: true });
  });
  const api = createBackendApi({ callAPI });

  await api.listWorkflowTemplates({
    category: "government-enterprise",
    business_flow: "meeting-review",
    output_type: "docx",
    supports_schedule: true,
    locale: "zh-CN",
  });
  await api.getWorkflowTemplate({
    templateId: "government-enterprise/meeting-minutes",
    version: 1,
  });
  await api.renderWorkflowTemplateDraft({
    templateId: "government-enterprise/meeting-minutes",
    version: 1,
    values: { title: "June meeting" },
    user_inputs: { reviewer: "office" },
    runtime_context: { locale: "zh-CN" },
    locale: "zh-CN",
  });
  await api.saveWorkflowTemplate({
    templateId: "government-enterprise/meeting-minutes",
    version: 2,
    category: "government-enterprise",
    trust: { level: "user", source: "save_as_template" },
    compatibility: { runtime: "dag-v2", node_types: ["agent"] },
    draft: { dag_key: "meeting_minutes_run" },
  });
  await api.rollbackWorkflowTemplate({
    templateId: "government-enterprise/meeting-minutes",
    version: 1,
  });

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.WORKFLOW_TEMPLATES_LIST, {
    category: "government-enterprise",
    business_flow: "meeting-review",
    output_type: "docx",
    supports_schedule: true,
    locale: "zh-CN",
  });
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.WORKFLOW_TEMPLATES_GET, {
    templateId: "government-enterprise/meeting-minutes",
    version: 1,
  });
  expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG, {
    templateId: "government-enterprise/meeting-minutes",
    version: 1,
    values: { title: "June meeting" },
    user_inputs: { reviewer: "office" },
    runtime_context: { locale: "zh-CN" },
    locale: "zh-CN",
  });
  expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.WORKFLOW_TEMPLATES_SAVE, {
    templateId: "government-enterprise/meeting-minutes",
    version: 2,
    category: "government-enterprise",
    trust: { level: "user", source: "save_as_template" },
    compatibility: { runtime: "dag-v2", node_types: ["agent"] },
    draft: { dag_key: "meeting_minutes_run" },
  });
  expect(callAPI).toHaveBeenNthCalledWith(5, RPC_METHODS.WORKFLOW_TEMPLATES_ROLLBACK, {
    templateId: "government-enterprise/meeting-minutes",
    version: 1,
  });
  expect(typeof saveWorkflowTemplate).toBe("function");
  expect(typeof rollbackWorkflowTemplate).toBe("function");
});

it("fails fast for invalid workflow template facade inputs", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(callAPI, () => api.getWorkflowTemplate({ templateId: "" }), "templateId is required");
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.renderWorkflowTemplateDraft({
        templateId: "",
      }),
    "templateId is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.renderWorkflowTemplateDraft({
        templateId: "government-enterprise/meeting-minutes",
        values: [],
      }),
    "values must be an object",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.renderWorkflowTemplateDraft({
        templateId: "government-enterprise/meeting-minutes",
        user_inputs: [],
      }),
    "user_inputs must be an object",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.renderWorkflowTemplateDraft({
        templateId: "government-enterprise/meeting-minutes",
        runtime_context: [],
      }),
    "runtime_context must be an object",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.saveWorkflowTemplate({
        templateId: "government-enterprise/meeting-minutes",
        version: 0,
        category: "government-enterprise",
        trust: {},
        compatibility: {},
        draft: {},
      }),
    "version must be a positive integer",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.saveWorkflowTemplate({
        templateId: "government-enterprise/meeting-minutes",
        version: 2,
        category: "government-enterprise",
        trust: [],
        compatibility: {},
        draft: {},
      }),
    "trust must be an object",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.rollbackWorkflowTemplate({
        templateId: "government-enterprise/meeting-minutes",
        version: 0,
      }),
    "version must be a positive integer",
  );
});

it("rejects malformed DAG and workflow template responses", async () => {
  const missingRunKey = dashboardDagRun();
  delete missingRunKey.run_key;
  const missingTemplateId = workflowTemplateSummary();
  delete missingTemplateId.id;
  const wrongRollbackTemplateID = workflowTemplateSummary({
    id: "government-enterprise/other-template",
    version: 1,
  });
  const wrongRollbackVersion = workflowTemplateSummary({ version: 99 });
  const missingRollbackTemplateID = workflowTemplateSummary({ version: 1 });
  delete missingRollbackTemplateID.id;
  const missingDraft = { template_id: "government-enterprise/meeting-minutes" };
  const missingPersistedVersion = workflowTemplateSummary();
  delete missingPersistedVersion.version;
  const cases = [
    {
      call: (api) => api.getDagDetail({ dagKey: "dag-1" }),
      response: { dag: null, nodes: [dashboardDagNode()] },
    },
    {
      call: (api) => api.getDagRuns({ dagKey: "dag-1", limit: 5 }),
      response: { runs: null },
    },
    {
      call: (api) => api.getDagRuns({ dagKey: "dag-1", limit: 5 }),
      response: { runs: [missingRunKey] },
    },
    {
      call: (api) => api.getDagRun({ runKey: "run-1" }),
      response: { run: null, nodes: [dashboardDagNode()] },
    },
    {
      call: (api) => api.listWorkflowTemplates({ category: "government-enterprise" }),
      response: { templates: null },
    },
    {
      call: (api) => api.listWorkflowTemplates({ category: "government-enterprise" }),
      response: { templates: [missingTemplateId] },
    },
    {
      call: (api) =>
        api.getWorkflowTemplate({
          templateId: "government-enterprise/meeting-minutes",
        }),
      response: { template: null },
    },
    {
      call: (api) =>
        api.renderWorkflowTemplateDraft({
          templateId: "government-enterprise/meeting-minutes",
          version: 2,
          values: {},
        }),
      response: { draft: missingDraft },
    },
    {
      call: (api) =>
        api.saveWorkflowTemplate({
          templateId: "government-enterprise/meeting-minutes",
          version: 2,
          category: "government-enterprise",
          trust: {},
          compatibility: {},
          draft: {},
        }),
      response: { template: missingPersistedVersion },
    },
    {
      call: (api) =>
        api.rollbackWorkflowTemplate({
          templateId: "government-enterprise/meeting-minutes",
          version: 1,
        }),
      response: { template: missingPersistedVersion },
    },
    {
      call: (api) =>
        api.rollbackWorkflowTemplate({
          templateId: "government-enterprise/meeting-minutes",
          version: 1,
        }),
      response: { template: missingRollbackTemplateID },
    },
    {
      call: (api) =>
        api.rollbackWorkflowTemplate({
          templateId: "government-enterprise/meeting-minutes",
          version: 1,
        }),
      response: { template: wrongRollbackTemplateID },
    },
    {
      call: (api) =>
        api.rollbackWorkflowTemplate({
          templateId: "government-enterprise/meeting-minutes",
          version: 1,
        }),
      response: { template: wrongRollbackVersion },
    },
  ];

  for (const item of cases) {
    const callAPI = vi.fn().mockResolvedValue(item.response);
    const api = createBackendApi({ callAPI });
    await expect(item.call(api)).rejects.toThrow();
    expect(callAPI).toHaveBeenCalledTimes(1);
  }
});

it("rejects malformed skill and datasource responses", async () => {
  const document = {
    documentId: 7,
    sourcePath: "/data/a.txt",
    fileName: "a.txt",
    extension: ".txt",
    sizeBytes: 12,
    contentHash: "hash",
    chunkCount: 1,
    totalChars: 4,
    status: "ready",
    errorMessage: "",
    createdAt: "2026-07-13T00:00:00Z",
    updatedAt: "2026-07-13T00:00:00Z",
  };
  const chunk = {
    id: 9,
    documentId: 7,
    chunkIndex: 0,
    content: "body",
    charCount: 4,
    byteCount: 4,
    embeddingModel: "",
    embeddingDim: 0,
    tokenCount: 1,
    createdAt: "2026-07-13T00:00:00Z",
  };
  const cases = [
    {
      call: (api) => api.listSkillFiles({ cwd: "/repo", dir: "/repo/.agents/skills/a" }),
      response: {
        dir: "/repo/.agents/skills/a",
        files: [
          {
            name: "SKILL.md",
            path: "/repo/.agents/skills/a/SKILL.md",
            size: 10,
            is_main: "yes",
          },
        ],
      },
    },
    {
      call: (api) =>
        api.importSkillDirectories({
          cwd: "/repo",
          paths: ["/tmp/a"],
          scope: "project",
        }),
      response: {
        requested: 1,
        imported: [
          {
            name: "a",
            dir: "/repo/.agents/skills/a",
            skill_file: "/repo/.agents/skills/a/SKILL.md",
            source: "/tmp/a",
            files: 1,
            bytes: "4",
          },
        ],
      },
    },
    {
      call: (api) =>
        api.suggestSkillSummary({
          cwd: "/repo",
          name: "a",
          description: "desc",
        }),
      response: { description: 1 },
    },
    {
      call: (api) => api.listSkillResolutions({ cwd: "/repo" }),
      response: {
        items: [
          {
            conflict_id: "c1",
            kind: "mirror_drift",
            name: "a",
            available_actions: [1],
          },
        ],
      },
    },
    {
      call: (api) =>
        api.previewSkillResolution({
          cwd: "/repo",
          conflictId: "c1",
          action: "view_diff",
        }),
      response: {
        conflict_id: "c1",
        kind: "mirror_drift",
        items: [{ action: "view_diff", preview_hash: 1 }],
      },
    },
    {
      call: (api) =>
        api.applySkillResolution({
          cwd: "/repo",
          conflict_id: "c1",
          action: "canonical_overwrite_mirror",
          previewId: "p1",
          previewHash: "h1",
        }),
      response: {
        Action: "canonical_overwrite_mirror",
        Name: "a",
        ResultingHash: "h1",
        PartialFailure: false,
        FollowUpAction: "",
      },
    },
    {
      call: (api) => api.listSkillTools({ cwd: "/repo", limit: 20 }),
      response: {
        tools: [
          {
            id: 1,
            cwd: "/repo",
            methodName: "read",
            description: "read",
            enabled: "yes",
            createdAt: "2026-07-13T00:00:00Z",
            updatedAt: "2026-07-13T00:00:00Z",
          },
        ],
      },
    },
    {
      call: (api) => api.listDatasourceDocuments({ keyword: "a", limit: 20 }),
      response: { documents: [{ ...document, status: false }] },
    },
    {
      call: (api) => api.getDatasourceDocument({ documentId: 7 }),
      response: {
        document,
        chunks: [{ ...chunk, documentId: 8 }],
        hasMore: false,
        nextCursor: 0,
      },
    },
    {
      call: (api) => api.listDatasourceChunks({ documentId: 7, limit: 20, cursor: 0 }),
      response: { chunks: [chunk], hasMore: false, nextCursor: "0" },
    },
    {
      call: (api) =>
        api.importDatasourceLocalFile({
          sourcePath: "/data/a.txt",
          pickerToken: "picker-token",
        }),
      response: {
        documentId: 7,
        sourcePath: "/data/a.txt",
        fileName: "a.txt",
        extension: ".txt",
        sizeBytes: 12,
        contentHash: "hash",
        chunkCount: 1,
        totalChars: 4,
        status: "ready",
        stale: true,
      },
    },
    {
      call: (api) =>
        api.updateDatasourceDocument({
          documentId: 7,
          sourcePath: "/data/a.txt",
          fileName: "a.txt",
          extension: ".txt",
          sizeBytes: 12,
        }),
      response: { ...document, updatedAt: 5 },
    },
    {
      call: (api) => api.deleteDatasourceDocument({ documentId: 7 }),
      response: { documentId: 7, deleted: "true" },
    },
    {
      call: (api) => api.deleteDatasourceDocument({ documentId: 7 }),
      response: { documentId: 7, deleted: false },
    },
    {
      call: (api) => api.deleteDatasourceDocument({ documentId: 7 }),
      response: { documentId: 8, deleted: true },
    },
  ];
  for (const item of cases) {
    const callAPI = vi.fn().mockResolvedValue(item.response);
    await expect(item.call(createBackendApi({ callAPI }))).rejects.toThrow();
    expect(callAPI).toHaveBeenCalledTimes(1);
  }
});
