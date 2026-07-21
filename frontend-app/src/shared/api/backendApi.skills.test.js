import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi } from "./backendApi.js";
import {
  callSkillEditorApis,
  expectSkillEditorCalls,
} from "./test-support/backendApi.skills.testSupport.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";

it("deletes skills with cwd, scope, and personal type", async () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  await api.deleteSkill({
    cwd: "/repo/app",
    name: "DocsSkill",
    scope: "personal",
    personalType: "user",
  });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_DELETE, {
    cwd: "/repo/app",
    name: "DocsSkill",
    scope: "personal",
    personal_type: "user",
  });
  expect(() => api.deleteSkill({ cwd: "/repo/app", name: "DocsSkill", scope: "system" })).toThrow(
    "scope must be project or personal",
  );
});

it("creates project skills through the dedicated internal skills/create RPC", async () => {
  const callAPI = vi.fn().mockResolvedValue({ path: "/repo/app/.agents/skills/DocsSkill/SKILL.md" });
  const api = createBackendApi({ callAPI });

  await api.createSkill({
    cwd: "/repo/app",
    name: "DocsSkill",
    content: '---\nname: "DocsSkill"\n---\n\n## Docs',
  });

  expect(RPC_METHODS.SKILLS_CREATE).toBe("skills/create");
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_CREATE, {
    cwd: "/repo/app",
    name: "DocsSkill",
    content: '---\nname: "DocsSkill"\n---\n\n## Docs',
  });
  expect(() => api.createSkill({ cwd: "/repo/app", name: "", content: "body" })).toThrow(
    "skills/create: name is required",
  );
  expect(() => api.createSkill({ cwd: "/repo/app", name: "DocsSkill" })).toThrow("skills/create: content is required");
});

it("wraps skill editor and import RPCs with legacy payload shapes", async () => {
  const importedSkill = {
    name: "a",
    dir: "/repo/app/.agents/skills/a",
    skill_file: "/repo/app/.agents/skills/a/SKILL.md",
    source: "/imports/a",
    files: 1,
    bytes: 10,
  };
  const callAPI = vi.fn((method) =>
    Promise.resolve(
      method === RPC_METHODS.SKILLS_SUMMARY_SUGGEST
        ? { description: "当你需要编写文档时使用。" }
        : method === RPC_METHODS.SKILLS_LOCAL_READ
          ? {
              skill: {
                path: "/repo/app/.agents/skills/docs/SKILL.md",
                content: "# DocsSkill",
              },
            }
          : method === RPC_METHODS.SKILLS_LOCAL_LIST_FILES
            ? {
                dir: "/repo/app/.agents/skills/docs",
                files: [
                  {
                    name: "SKILL.md",
                    path: "/repo/app/.agents/skills/docs/SKILL.md",
                    size: 10,
                    is_main: true,
                  },
                ],
              }
            : method === RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR
              ? {
                  requested: 1,
                  imported: [importedSkill],
                  skill: importedSkill,
                  mirror_publish: {},
                }
              : { ok: true },
    ),
  );
  const selectProjectDirs = vi.fn().mockResolvedValue(["/imports/a"]);
  const api = createBackendApi({ callAPI, selectProjectDirs });

  await callSkillEditorApis(api);
  await api.selectProjectDirs();

  expectSkillEditorCalls(callAPI);
  expect(selectProjectDirs).toHaveBeenCalledTimes(1);
});

it("normalizes skill summary suggestions to description text", async () => {
  const callAPI = vi.fn().mockResolvedValue({ description: " 当你需要部署服务时使用。 " });
  const api = createBackendApi({ callAPI });

  await expect(
    api.suggestSkillSummary({
      cwd: "/repo/app",
      name: "DeploySkill",
      description: "",
      content: "body",
      scenario_words: ["deploy"],
      scope: "project",
      provider: "codex",
      model: "gpt-5.5",
      codexModelProvider: "openrouter",
    }),
  ).resolves.toBe("当你需要部署服务时使用。");

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_SUMMARY_SUGGEST, {
    cwd: "/repo/app",
    name: "DeploySkill",
    description: "",
    content: "body",
    scenario_words: ["deploy"],
    scope: "project",
    provider: "codex",
    model: "gpt-5.5",
    model_provider: "openrouter",
  });
});

it("does not duplicate skill summary retry at the frontend facade", async () => {
  const callAPI = vi.fn().mockRejectedValueOnce(new Error("parse skill summary suggestion: invalid character"));
  const api = createBackendApi({ callAPI });

  await expect(
    api.suggestSkillSummary({
      cwd: "/repo/app",
      name: "部署技能",
      description: "",
      content: "body",
      scenario_words: ["deploy"],
      scope: "project",
    }),
  ).rejects.toThrow("parse skill summary suggestion");

  expect(callAPI).toHaveBeenCalledTimes(1);
});

it("wraps skill resolution preview and apply payloads", async () => {
  const callAPI = vi.fn((method) =>
    Promise.resolve(
      {
        [RPC_METHODS.SKILLS_RESOLUTION_LIST]: { items: [] },
        [RPC_METHODS.SKILLS_RESOLUTION_PREVIEW]: {
          conflict_id: "c1",
          kind: "mirror_drift",
          items: [{ action: "view_diff", preview_id: "p1", preview_hash: "h1" }],
        },
        [RPC_METHODS.SKILLS_RESOLUTION_APPLY]: {
          action: "canonical_overwrite_mirror",
          name: "DocsSkill",
          resultingHash: "h1",
          partialFailure: false,
          followUpAction: "",
        },
      }[method],
    ),
  );
  const api = createBackendApi({ callAPI });

  await api.listSkillResolutions({ cwd: "/repo/app" });
  await api.previewSkillResolution({
    cwd: "/repo/app",
    conflictId: "c1",
    action: "view_diff",
    sourceProvider: "codex",
    sourcePathId: "codex:docs",
  });
  await api.applySkillResolution({
    cwd: "/repo/app",
    conflict_id: "c1",
    action: "canonical_overwrite_mirror",
    previewId: "p1",
    previewHash: "h1",
  });

  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_RESOLUTION_LIST, {
    cwd: "/repo/app",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_RESOLUTION_PREVIEW, {
    cwd: "/repo/app",
    conflict_id: "c1",
    action: "view_diff",
    source_provider: "codex",
    source_path_id: "codex:docs",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_RESOLUTION_APPLY, {
    cwd: "/repo/app",
    conflict_id: "c1",
    action: "canonical_overwrite_mirror",
    preview_id: "p1",
    preview_hash: "h1",
  });
});

it("rejects skill resolution apply without preview proof", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });
  const validApplyPayload = {
    cwd: "/repo/app",
    conflict_id: "c1",
    action: "canonical_overwrite_mirror",
    previewId: "p1",
    previewHash: "h1",
  };

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.applySkillResolution({
        ...validApplyPayload,
        previewId: "",
      }),
    "preview_id is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.applySkillResolution({
        ...validApplyPayload,
        previewHash: "",
      }),
    "preview_hash is required",
  );
});

it("rejects skill resolution payloads without required conflict fields", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });
  const validPreviewPayload = {
    cwd: "/repo/app",
    conflictId: "c1",
    action: "canonical_overwrite_mirror",
  };
  const validApplyPayload = {
    ...validPreviewPayload,
    previewId: "p1",
    previewHash: "h1",
  };

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.previewSkillResolution({
        ...validPreviewPayload,
        conflictId: "",
      }),
    "conflict_id is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.previewSkillResolution({
        ...validPreviewPayload,
        action: "",
      }),
    "action is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.applySkillResolution({
        ...validApplyPayload,
        conflictId: "",
      }),
    "conflict_id is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.applySkillResolution({
        ...validApplyPayload,
        action: "",
      }),
    "action is required",
  );
});
