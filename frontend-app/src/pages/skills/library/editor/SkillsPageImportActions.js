import { skillsPageService } from "../../services/skillsPageService.js";
import {
  normalizeSettingsCwd,
  parseSkillMarkdown,
} from "../SkillsPageMarkdownModel.js";
import {
  importedSkillFilePath,
  importedSkillSameNameConflictMessage,
  isImportedSkillSameNameConflictError,
  normalizeImportFailure,
  normalizeImportSummaryDraftScope,
  importNotice,
  skillFileBaseName,
} from "./SkillsPageImportSummaryModel.js";
import {
  optionalObject,
  textFromValue,
  trimmedText,
} from "../dashboard/skillsDashboardModel.js";
import { skillFormFromRaw } from "./SkillsPageEditorActions.js";

const {
  importSkillDirectories,
  readSkill,
  selectProjectDirs,
  suggestSkillSummary,
} = skillsPageService;

export async function confirmImportScope(ctx, scope) {
  ctx.setPatch({ importing: true });
  ctx.setError("");
  ctx.setNotice("");
  try {
    const paths = await selectProjectDirs();
    ctx.setPatch({ importScopeOpen: false });
    if (!Array.isArray(paths) || paths.length === 0) {
      ctx.setNotice("未选择目录");
      return;
    }
    const cwd = normalizeSettingsCwd(ctx.projectPath);
    const personalType = scope === "personal" ? "imported" : "";
    const result = await importSkillDirectories({
      cwd,
      paths,
      scope,
      personal_type: personalType,
    });
    const failures = Array.isArray(result?.failures)
      ? result.failures.map(normalizeImportFailure)
      : [];
    const importSummaryDrafts = await createImportSummaryDrafts(
      ctx,
      result?.imported,
      scope,
      personalType,
    );
    ctx.setPatch({ importSummaryDrafts });
    await ctx.refreshSkillSurface();
    ctx.setNotice(
      importNotice(
        Array.isArray(result?.imported) ? result.imported.length : 0,
        importSummaryDrafts,
        failures,
        scope,
      ),
    );
  } catch (err) {
    ctx.setError("导入目录失败，请重试。");
    throw err;
  } finally {
    ctx.setPatch({ importing: false });
  }
}

async function createImportSummaryDrafts(
  ctx,
  importedSkills,
  scope,
  personalType,
) {
  if (!Array.isArray(importedSkills) || importedSkills.length === 0) return [];
  const cwd = normalizeSettingsCwd(ctx.projectPath);
  const draftResults = await Promise.all(
    importedSkills.map((item, index) =>
      createImportSummaryDraft({ ctx, cwd, item, scope, personalType, index }),
    ),
  );
  return draftResults.filter(Boolean);
}

async function createImportSummaryDraft(options) {
  const { cwd, item, scope, personalType, index } = options;
  const skillFile = importedSkillFilePath(item);
  if (!skillFile) return null;
  const fallbackName = trimmedText(item?.name) || skillFileBaseName(skillFile);
  const baseDraft = {
    id: `${index}:${skillFile || fallbackName}`,
    name: fallbackName,
    skillFile,
    scope: normalizeImportSummaryDraftScope(scope),
    personalType,
    suggestion: "",
    status: "ready",
    error: "",
  };
  try {
    const raw = await readSkill({ cwd, path: skillFile });
    const skill = optionalObject(raw?.skill);
    if (!skill)
      throw new Error("skills/local/read response.skill must be an object");
    const parsed = parseSkillMarkdown(
      textFromValue(skill.content),
      fallbackName,
    );
    const currentDescription = trimmedText(parsed.description);
    if (currentDescription) return null;
    const suggestion = await suggestSkillSummary({
      cwd,
      name: parsed.name || fallbackName,
      description: currentDescription,
      content: parsed.body,
      scenario_words: parsed.triggerWords,
      scope: normalizeImportSummaryDraftScope(scope),
    });
    if (!suggestion) return null;
    return { ...baseDraft, name: parsed.name || fallbackName, suggestion };
  } catch (err) {
    if (isImportedSkillSameNameConflictError(err))
      return {
        ...baseDraft,
        status: "conflict",
        error: importedSkillSameNameConflictMessage(baseDraft),
      };
    return {
      ...baseDraft,
      status: "error",
      error: "技能已正常导入。可以稍后重试，或手动补充简介。",
    };
  }
}

function importSummaryEditorPatch(raw, draft) {
  const skillBase = {
    name: draft.name,
    title: draft.name,
    description: "",
    tags: [],
    scope: draft.scope,
    personalType: draft.personalType,
  };
  const editorForm = {
    ...skillFormFromRaw(raw, skillBase),
    scope: draft.scope,
    personalType: draft.personalType,
  };
  return {
    activeSkillPath: draft.skillFile,
    editorForm,
    editorOpen: true,
    skillFiles: [{ name: "SKILL.md", path: draft.skillFile, isMain: true }],
    summarySuggestion: "",
  };
}

export async function openImportSummaryDraft(ctx, draft) {
  if (!draft?.skillFile) return false;
  ctx.setError("");
  try {
    const raw = await readSkill({
      cwd: normalizeSettingsCwd(ctx.projectPath),
      path: draft.skillFile,
    });
    ctx.setPatch(importSummaryEditorPatch(raw, draft));
    return true;
  } catch (err) {
    ctx.setError("打开技能失败，请重试。");
    throw err;
  }
}

export async function applyImportSummaryDraft(ctx, draft) {
  if (!draft || draft.status !== "ready") return;
  const suggestion = trimmedText(draft.suggestion);
  if (!suggestion) return;
  const opened = await openImportSummaryDraft(ctx, draft);
  if (!opened) return;
  ctx.setForm((form) => ({ ...form, description: suggestion }));
  ctx.setPatch({
    importSummaryDrafts: ctx.state.importSummaryDrafts.map((item) =>
      item.id === draft.id ? { ...item, status: "applied" } : item,
    ),
  });
  ctx.setNotice("已采用简介建议，保存技能后生效。");
}

export function dismissImportSummaryDraft(ctx, draft) {
  ctx.setPatch({
    importSummaryDrafts: ctx.state.importSummaryDrafts.filter(
      (item) => item.id !== draft?.id,
    ),
  });
}
