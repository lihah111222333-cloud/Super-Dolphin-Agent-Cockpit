import { expect } from "vitest";
import { RPC_METHODS } from "../backendApi.js";

export async function callSkillEditorApis(api) {
  await api.readSkill({
    cwd: "/repo/app",
    path: "/repo/app/.agents/skills/docs/SKILL.md",
  });
  await api.listSkillFiles({
    cwd: "/repo/app",
    dir: "/repo/app/.agents/skills/docs",
  });
  await api.writeSkill({
    cwd: "/repo/app",
    path: "DocsSkill",
    content: "---",
    scope: "personal",
    personalType: "user",
  });
  await api.importSkillDirectories({
    cwd: "/repo/app",
    paths: ["/imports/a"],
    scope: "personal",
    personal_type: "imported",
  });
  await api.suggestSkillSummary({
    cwd: "/repo/app",
    name: "DocsSkill",
    description: "",
    content: "body",
    scenario_words: ["docs"],
    scope: "project",
  });
}

export function expectSkillEditorCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_READ, {
    cwd: "/repo/app",
    path: "/repo/app/.agents/skills/docs/SKILL.md",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_LIST_FILES, {
    cwd: "/repo/app",
    dir: "/repo/app/.agents/skills/docs",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_WRITE, {
    cwd: "/repo/app",
    path: "DocsSkill",
    content: "---",
    scope: "personal",
    personal_type: "user",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR, {
    cwd: "/repo/app",
    paths: ["/imports/a"],
    scope: "personal",
    personal_type: "imported",
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_SUMMARY_SUGGEST, {
    cwd: "/repo/app",
    name: "DocsSkill",
    description: "",
    content: "body",
    scenario_words: ["docs"],
    scope: "project",
  });
}
