import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi } from "./backendApi.js";
import {
  workflowTemplateDetail,
  workflowTemplateSummary,
} from "./test-support/backendApi.workflow.testSupport.js";
it("rejects malformed memory prompt dashboard and UI responses", async () => {
  const memoryIdentity = {
    cwd: "/repo/app",
    target: "private",
    path: "feedback/tdd.md",
  };
  const memoryPair = {
    cwd: "/repo/app",
    targetA: "private",
    pathA: "a.md",
    targetB: "team",
    pathB: "b.md",
  };
  const cases = [
    {
      call: (api) => api.getMemoryEntry(memoryIdentity),
      response: { name: 7 },
    },
    {
      call: (api) =>
        api.upsertMemoryEntry({
          cwd: "/repo/app",
          target: "private",
          existingPath: "",
          name: "tdd-rule",
          description: "先写红测",
          type: "feedback",
          content: "规则",
        }),
      response: { name: 7 },
    },
    {
      call: (api) => api.mergeMemoryEntries(memoryPair),
      response: { name: 7 },
    },
    {
      call: (api) => api.deleteMemoryEntry(memoryIdentity),
      response: { deleted: "yes" },
    },
    {
      call: (api) => api.setMemoryAutoDreamIntent({ cwd: "/repo/app", enabled: true }),
      response: { ok: true, enabled: "yes" },
    },
    {
      call: (api) => api.ignoreMemorySimilarity(memoryPair),
      response: { ignored: true, key: 7 },
    },
    {
      call: (api) => api.startConsolidateMemorySimilarities({ cwd: "/repo/app" }),
      response: {
        jobId: "job-1",
        status: "succeeded",
        result: { merged: "1", ignored: 0, failed: 0, skipped: 0 },
      },
    },
    {
      call: (api) => api.getMemoryConsolidationStatus({ cwd: "/repo/app", jobId: "job-1" }),
      response: { jobId: "job-1", status: "unknown" },
    },
    {
      call: (api) => api.deleteSharedFile({ path: "scratch/work.json" }),
      response: { deleted: "yes" },
    },
    {
      call: (api) =>
        api.writeWorkflowMaterial({
          path: "workflow/material.md",
          content: "body",
        }),
      response: { path: 9 },
    },
    {
      call: (api) => api.listPromptAssets({ cwd: "/repo/app" }),
      response: { prompts: [{ id: "prompt-1", issues: "broken" }] },
    },
    {
      call: (api) => api.getDashboardPrompts({ cwd: "/repo/app" }),
      response: { prompts: [{ id: "7" }] },
    },
    {
      call: (api) => api.getPrompt({ cwd: "/repo/app", id: "main/reviewer" }),
      response: { prompt: { id: "main/reviewer", enabled: "true" } },
    },
    {
      call: (api) =>
        api.writePrompt({
          cwd: "/repo/app",
          id: "main/reviewer",
          name: "Reviewer",
          content: "Review",
          agentType: "main",
          tags: [],
          scope: "project",
          enabled: true,
        }),
      response: { prompt: { id: "main/reviewer", enabled: "true" } },
    },
    {
      call: (api) => api.deletePrompt({ cwd: "/repo/app", id: "main/reviewer" }),
      response: { ok: false },
    },
    {
      call: (api) =>
        api.draftPromptIntent({
          cwd: "/repo/app",
          kind: "expert",
          rawInput: "Review code",
        }),
      response: {
        requested_kind: "expert",
        inferred_kind: "expert",
        drafts: {},
      },
    },
    {
      call: (api) =>
        api.commitPromptIntent({
          cwd: "/repo/app",
          draftKey: "intent/expert/review",
        }),
      response: {
        draft_key: "intent/expert/review",
        prompt_key: 7,
        kind: "expert",
        status: "saved",
      },
    },
    {
      call: (api) =>
        api.discardPromptIntent({
          cwd: "/repo/app",
          draftKey: "intent/expert/review",
        }),
      response: { draft_key: "intent/expert/review", status: 7 },
    },
    {
      call: (api) =>
        api.dryRunPromptIntent({
          cwd: "/repo/app",
          draftKey: "intent/expert/review",
          kind: "expert",
          card: { title: "Reviewer" },
          question: "Review this",
        }),
      response: {
        would_use: true,
        action: "use",
        reasons: "because",
        disclaimer: "preview",
      },
    },
    {
      call: (api) => api.getPersonalizationProfile({ cwd: "/repo/app" }),
      response: {
        profile: {
          displayName: "",
          role: "",
          background: [],
          customInstructions: "",
        },
      },
    },
    {
      call: (api) =>
        api.savePersonalizationProfile({
          cwd: "/repo/app",
          profile: {
            displayName: "",
            role: "",
            background: "",
            customInstructions: "",
          },
        }),
      response: {
        profile: {
          displayName: "",
          role: "",
          background: [],
          customInstructions: "",
        },
      },
    },
  ];

  for (const item of cases) {
    const callAPI = vi.fn().mockResolvedValue(item.response);
    await expect(item.call(createBackendApi({ callAPI }))).rejects.toThrow();
    expect(callAPI).toHaveBeenCalledTimes(1);
  }
});

it("accepts nullable prompt intent slices without normalizing them and rejects scalar replacements", async () => {
  const draftResponse = {
    draft_key: "intent/expert/review",
    requested_kind: "expert",
    inferred_kind: "expert",
    status: "ready_to_save",
    confidence: 0.9,
    scope: "project",
    issues: null,
    card: {
      kind: "expert",
      title: "Review expert",
      summary: "Review code carefully.",
      hit_examples: null,
      miss_examples: null,
    },
  };
  const dryRunResponse = {
    would_use: false,
    action: "none",
    reasons: null,
    disclaimer: "Preview only.",
  };
  const draftCall = (api) =>
    api.draftPromptIntent({
      cwd: "/repo/app",
      kind: "expert",
      rawInput: "Review code",
    });
  const dryRunCall = (api) =>
    api.dryRunPromptIntent({
      cwd: "/repo/app",
      draftKey: "intent/expert/review",
      kind: "expert",
      card: { title: "Reviewer" },
      question: "Review this",
    });

  await expect(draftCall(createBackendApi({ callAPI: vi.fn().mockResolvedValue(draftResponse) }))).resolves.toEqual(
    draftResponse,
  );
  await expect(dryRunCall(createBackendApi({ callAPI: vi.fn().mockResolvedValue(dryRunResponse) }))).resolves.toEqual(
    dryRunResponse,
  );

  const invalidResponses = [
    { call: draftCall, response: { ...draftResponse, issues: "broken" } },
    {
      call: draftCall,
      response: {
        ...draftResponse,
        issues: [],
        card: { ...draftResponse.card, hit_examples: "broken" },
      },
    },
    {
      call: draftCall,
      response: {
        ...draftResponse,
        issues: [],
        card: { ...draftResponse.card, miss_examples: "broken" },
      },
    },
    { call: dryRunCall, response: { ...dryRunResponse, reasons: "broken" } },
  ];
  for (const item of invalidResponses) {
    const callAPI = vi.fn().mockResolvedValue(item.response);
    await expect(item.call(createBackendApi({ callAPI }))).rejects.toThrow();
    expect(callAPI).toHaveBeenCalledTimes(1);
  }
});

it("accepts null workflow template tags from list get and save responses", async () => {
  const callAPI = vi.fn((method) => {
    if (method === RPC_METHODS.WORKFLOW_TEMPLATES_LIST) {
      return Promise.resolve({
        templates: [workflowTemplateSummary({ tags: null })],
      });
    }
    if (method === RPC_METHODS.WORKFLOW_TEMPLATES_GET) {
      return Promise.resolve({
        template: workflowTemplateDetail({ tags: null }),
      });
    }
    if (method === RPC_METHODS.WORKFLOW_TEMPLATES_SAVE) {
      return Promise.resolve({
        template: workflowTemplateSummary({ tags: null }),
      });
    }
    throw new Error(`unexpected method ${method}`);
  });
  const api = createBackendApi({ callAPI });

  await expect(api.listWorkflowTemplates({ category: "government-enterprise" })).resolves.toMatchObject({
    templates: [{ tags: null }],
  });
  await expect(
    api.getWorkflowTemplate({
      templateId: "government-enterprise/meeting-minutes",
    }),
  ).resolves.toMatchObject({
    template: { tags: null },
  });
  await expect(
    api.saveWorkflowTemplate({
      templateId: "government-enterprise/meeting-minutes",
      version: 2,
      category: "government-enterprise",
      trust: {},
      compatibility: {},
      draft: {},
    }),
  ).resolves.toMatchObject({ template: { tags: null } });
  expect(callAPI).toHaveBeenCalledTimes(3);

  const invalidAPI = createBackendApi({
    callAPI: vi.fn().mockResolvedValue({
      templates: [workflowTemplateSummary({ tags: {} })],
    }),
  });
  await expect(invalidAPI.listWorkflowTemplates({ category: "government-enterprise" })).rejects.toThrow(
    "tags must be an array of strings",
  );
});
