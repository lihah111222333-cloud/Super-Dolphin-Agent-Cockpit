import { skillsPageService } from "../../services/skillsPageService.js";
import {
  buildSkillMarkdown,
  normalizeSettingsCwd,
  normalizeSummarySuggestion,
  parseSkillMarkdown,
  skillNameFromDisplayName,
} from "../SkillsPageMarkdownModel.js";
import {
  emptySkillForm,
  findSkillForCitation,
  isMainSkillFile,
  normalizeSkillFileList,
  skillFileForItem,
} from "./SkillsPageCitationModel.js";
import { fileNameFromPath } from "./SkillsPageImportSummaryModel.js";
import {
  skillCitationFromLink,
  skillPreviewDir,
} from "./SkillMarkdownPreviewModel.js";
import {
  firstTextField,
  optionalObject,
  textFromValue,
  trimmedText,
} from "../dashboard/skillsDashboardModel.js";
import {
  listToText,
  textValue,
  wordListFromText,
} from "../../../shared/pageShared.js";

const { listSkillFiles, readSkill, suggestSkillSummary } = skillsPageService;

export function openCreateSkillEditor(ctx) {
  ctx.setPatch({
    activeSkillPath: "",
    editorForm: emptySkillForm(),
    editorOpen: true,
    skillFiles: [],
    summarySuggestion: "",
  });
  ctx.setError("");
  ctx.setNotice("");
}

export async function openEditSkill(ctx, skill) {
  const skillPath = skillFileForItem(skill);
  const skillDir = (skill?.dir || skillPreviewDir(skillPath)).toString().trim();
  if (!skillPath || !skillDir) {
    ctx.setError("skills/local/read: path is required");
    return;
  }
  ctx.setError("");
  ctx.setNotice("");
  ctx.setPatch({ summarySuggestion: "" });
  try {
    const cwd = normalizeSettingsCwd(ctx.projectPath);
    const [rawSkill, rawFiles] = await Promise.all([
      readSkill({ cwd, path: skillPath }),
      listSkillFiles({ cwd, dir: skillDir }),
    ]);
    ctx.setPatch({
      activeSkillPath: skillPath,
      editorForm: skillFormFromRaw(rawSkill, skill),
      editorOpen: true,
      skillFiles: normalizeSkillFileList(rawFiles),
    });
  } catch (err) {
    ctx.setError("读取技能失败，请重试。");
    throw err;
  }
}

export function skillFormFromRaw(rawSkill, skill) {
  const raw = optionalObject(rawSkill?.skill);
  if (!raw)
    throw new Error("skills/local/read response.skill must be an object");
  const content = textFromValue(raw.content);
  const parsed = parseSkillMarkdown(content, skill.name);
  return {
    name: parsed.name ? parsed.name : skill.name,
    displayName: parsed.displayName
      ? parsed.displayName
      : textFromValue(skill.title),
    description: parsed.description
      ? parsed.description
      : textFromValue(skill.description),
    keywords: listToText(
      parsed.triggerWords.length > 0 ? parsed.triggerWords : skill.tags,
    ),
    body: parsed.body,
    scope: skill.scope,
    personalType: skill.personalType,
  };
}

export async function openSkillFile(ctx, file) {
  const path = trimmedText(file?.path);
  if (!path) return;
  ctx.setError("");
  try {
    const raw = await readSkill({
      cwd: normalizeSettingsCwd(ctx.projectPath),
      path,
    });
    const skill = optionalObject(raw?.skill);
    if (!skill)
      throw new Error("skills/local/read response.skill must be an object");
    ctx.setForm((form) =>
      skillFormForOpenedFile(path, textFromValue(skill.content), form),
    );
    ctx.setPatch({ activeSkillPath: path });
  } catch (err) {
    ctx.setError("读取子文件失败，请重试。");
    throw err;
  }
}

export async function openSkillCitation(ctx, target, label = "") {
  const citation = skillCitationFromLink(target, label);
  if (!citation) return false;
  ctx.setError("");
  if (citation.kind === "conversation") {
    ctx.setNotice(
      "暂不支持会话跳转：" +
        (citation.conversationId || citation.raw || "未命名会话"),
    );
    return false;
  }
  const skill = findSkillForCitation(ctx.skills, citation);
  if (!skill) {
    ctx.setNotice(
      "未找到引用的技能：" +
        (citation.skillName ||
          citation.path ||
          citation.skillId ||
          citation.raw ||
          "未命名技能"),
    );
    return false;
  }
  await openEditSkill(ctx, skill);
  return true;
}

function skillFormForOpenedFile(path, content, form) {
  if (!isMainSkillFile(path)) return { ...form, body: content };
  const parsed = parseSkillMarkdown(content, form.name);
  return {
    ...form,
    name: parsed.name || form.name,
    displayName: parsed.displayName || parsed.name || form.displayName,
    description: parsed.description,
    keywords: listToText(parsed.triggerWords),
    body: parsed.body,
  };
}

export async function suggestSkillSummaryForEditor(ctx) {
  ctx.setPatch({ summarySuggesting: true, summarySuggestion: "" });
  ctx.setError("");
  try {
    const cwd = normalizeSettingsCwd(ctx.projectPath);
    const launchPreferences =
      typeof ctx.resolveLaunchPreferences === "function"
        ? await ctx.resolveLaunchPreferences(cwd)
        : null;
    const description = await suggestSkillSummary(
      skillSummaryRequest(cwd, ctx.state.editorForm, launchPreferences),
    );
    ctx.setPatch({
      summarySuggestion: normalizeSummarySuggestion(description),
    });
  } catch (err) {
    ctx.setError("生成简介失败，请重试。");
    throw err;
  } finally {
    ctx.setPatch({ summarySuggesting: false });
  }
}

function skillSummaryRequest(cwd, form, launchPreferences) {
  return {
    cwd,
    name: form.displayName || form.name,
    description: form.description,
    content: form.body,
    scenario_words: wordListFromText(form.keywords),
    scope: form.scope,
    provider: firstTextField(
      launchPreferences,
      ["modelProvider", "provider"],
      "launch preferences",
    ),
    model: textValue(launchPreferences?.model),
    codexModelProvider: textValue(
      launchPreferences?.config?.codexModelProvider,
    ),
  };
}

export async function saveSkillEditor(ctx) {
  ctx.setPatch({ saving: true });
  ctx.setError("");
  ctx.setNotice("");
  try {
    const payload = skillSavePayload(
      normalizeSettingsCwd(ctx.projectPath),
      ctx.state,
    );
    if (shouldCreateProjectSkill(ctx.state)) {
      await ctx.facade.createSkill({
        cwd: payload.cwd,
        name: payload.path,
        content: payload.content,
      });
    } else {
      await ctx.facade.writeSkill(payload);
    }
    ctx.setPatch({ editorOpen: false });
    await ctx.refreshSkillSurface();
    ctx.setNotice(skillSaveNotice(ctx.state, payload));
  } catch (err) {
    ctx.setError("保存失败，请重试。");
    throw err;
  } finally {
    ctx.setPatch({ saving: false });
  }
}

function shouldCreateProjectSkill(state) {
  return !state.activeSkillPath && state.editorForm.scope === "project";
}

function skillSaveNotice(state, payload) {
  if (state.activeSkillPath && !isMainSkillFile(state.activeSkillPath))
    return "文件已保存：" + (fileNameFromPath(payload.path) || payload.path);
  return "已保存";
}

function skillSavePayload(cwd, state) {
  const isMain =
    !state.activeSkillPath || isMainSkillFile(state.activeSkillPath);
  const displayName = state.editorForm.displayName.trim();
  const name =
    state.editorForm.name.trim() || skillNameFromDisplayName(displayName);
  if (isMain && !displayName) throw new Error("请先填写技能名称");
  if (isMain && !name) throw new Error("技能名称必须包含中文、英文或数字");
  const normalizedForm = isMain
    ? { ...state.editorForm, name, displayName }
    : state.editorForm;
  return {
    cwd,
    path: isMain ? state.activeSkillPath || name : state.activeSkillPath,
    content: isMain
      ? buildSkillMarkdown(normalizedForm)
      : state.editorForm.body,
    scope: state.editorForm.scope,
    personal_type:
      state.editorForm.scope === "personal"
        ? state.editorForm.personalType || "user"
        : "",
  };
}

export async function confirmDeleteSkill(ctx) {
  const skill = ctx.state.deleteTarget;
  const skillName = trimmedText(skill?.name);
  if (!skillName) {
    ctx.setError("skills/local/delete: name is required");
    return;
  }
  ctx.setPatch({ deleting: true });
  ctx.setError("");
  ctx.setNotice("");
  try {
    await ctx.facade.deleteSkill({
      cwd: normalizeSettingsCwd(ctx.projectPath),
      name: skillName,
      scope: skill.scope,
      personal_type: skill.personalType,
    });
    ctx.setPatch({ deleteTarget: null });
    await ctx.refreshSkillSurface();
    ctx.setNotice("已删除 " + skill.title);
  } catch (err) {
    ctx.setError("删除技能失败，请重试。");
    throw err;
  } finally {
    ctx.setPatch({ deleting: false });
  }
}
