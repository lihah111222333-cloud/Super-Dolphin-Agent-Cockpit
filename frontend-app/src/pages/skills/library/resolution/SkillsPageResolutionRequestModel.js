import { skillsPageService } from "../../services/skillsPageService.js";
import { normalizeSettingsCwd } from "../SkillsPageMarkdownModel.js";
import {
  defaultResolutionNewName,
  isResolutionViewAction,
  requiresResolutionNewName,
  resolutionActionLabel,
  resolutionActionUnsupported,
  resolutionProviderEntries,
  resolutionRequiresApply,
} from "./SkillsPageResolutionLabels.js";
import {
  resolutionActionAutoAppliesForConflict,
  resolutionActionEntryTarget,
  resolutionApplyKey,
  resolutionSameNamePayloadFields,
} from "./SkillsPageResolutionActions.js";
import { firstTextField, trimmedText } from "../dashboard/skillsDashboardModel.js";
import { autoApplyResolutionPreview } from "./SkillsPageResolutionApplyModel.js";

const { previewSkillResolution } = skillsPageService;

export async function runResolutionPipeline(ctx) {
  const request = resolutionRequestFromAction(ctx);
  if (!request.ok) return request.value;
  if (request.prompt) {
    promptResolutionNewName(ctx, request.prompt);
    return false;
  }
  return previewAndMaybeApplyResolution(
    ctx,
    request.payload,
    request.action,
    request.conflict,
    request.applyKey,
  );
}
function resolutionRequestFromAction({
  actionOrEntry,
  conflict,
  entry,
  newName,
  projectPath,
}) {
  const conflictID = trimmedText(conflict?.conflict_id);
  const actionEntry =
    typeof actionOrEntry === "string"
      ? { action: actionOrEntry }
      : actionOrEntry;
  if (
    !actionEntry ||
    typeof actionEntry !== "object" ||
    Array.isArray(actionEntry)
  )
    return { ok: false, value: false };
  const action = trimmedText(actionEntry.action);
  if (!conflictID || !action) return { ok: false, value: false };
  if (resolutionActionUnsupported(action))
    return { ok: false, value: false, unsupported: action };
  const providerEntry = resolutionActionEntryTarget(
    actionEntry,
    entry || resolutionProviderEntries(conflict)[0],
  );
  const applyKey = resolutionApplyKey(conflict, action, providerEntry);
  const trimmedNewName = trimmedText(newName);
  if (requiresResolutionNewName(action) && !trimmedNewName)
    return {
      ok: true,
      prompt: { action, applyKey, conflict, entry: providerEntry },
    };
  return {
    ok: true,
    action,
    applyKey,
    conflict,
    payload: resolutionPayload({
      action,
      actionEntry,
      conflict,
      conflictID,
      projectPath,
      providerEntry,
      trimmedNewName,
    }),
  };
}
function resolutionPayload(options) {
  const {
    action,
    actionEntry,
    conflict,
    conflictID,
    projectPath,
    providerEntry,
    trimmedNewName,
  } = options;
  const provider = trimmedText(providerEntry?.provider);
  const fallbackProvider = trimmedText(conflict?.provider);
  const payload = {
    cwd: normalizeSettingsCwd(projectPath),
    conflict_id: conflictID,
    action,
    name: firstTextField(
      conflict,
      ["name", "skill_name"],
      "skill resolution conflict",
    ),
    scope: trimmedText(conflict?.scope),
    personal_type: trimmedText(conflict?.personal_type),
    provider: provider || fallbackProvider,
    source_provider:
      provider || trimmedText(conflict?.source_provider) || fallbackProvider,
    source_path_id:
      trimmedText(providerEntry?.source_path_id) ||
      trimmedText(conflict?.source_path_id),
    ...resolutionSameNamePayloadFields(conflict, action, actionEntry),
  };
  if (trimmedNewName) payload.new_name = trimmedNewName;
  return payload;
}
function promptResolutionNewName(ctx, prompt) {
  if (resolutionActionUnsupported(prompt.action)) {
    ctx.setNotice(
      "暂不支持该技能冲突操作：" + resolutionActionLabel(prompt.action),
    );
    return;
  }
  ctx.setPreview(null);
  ctx.setNamePrompt({
    ...prompt,
    autoApply: resolutionActionAutoAppliesForConflict(
      prompt.action,
      prompt.conflict,
    ),
  });
  ctx.setNameInput(defaultResolutionNewName(prompt.conflict, prompt.action));
  ctx.setNotice("请输入新技能名称后继续。");
}
async function previewAndMaybeApplyResolution(
  ctx,
  payload,
  action,
  conflict,
  applyKey,
) {
  ctx.setActioning(applyKey);
  ctx.setError("");
  let result;
  try {
    const preview = await previewSkillResolution(payload);
    const items = Array.isArray(preview?.items) ? preview.items : [];
    if (resolutionActionAutoAppliesForConflict(action, conflict)) {
      result = await autoApplyResolutionPreview(ctx, payload, items);
    } else {
      ctx.setPreview({
        ...preview,
        action,
        payload,
        items,
        requiresApply: resolutionRequiresApply(action),
      });
      ctx.setNotice(
        isResolutionViewAction(action)
          ? "已生成处理预览"
          : "已生成处理预览，请确认应用。",
      );
      result = true;
    }
  } catch (err) {
    ctx.setError("处理技能冲突失败，请重试。");
    throw err;
  } finally {
    ctx.setActioning("");
  }
  return result;
}
export async function confirmResolutionName(options) {
  const {
    nameInput,
    namePrompt,
    runAction,
    setError,
    setNameInput,
    setNamePrompt,
  } = options;
  if (!namePrompt) return;
  const newName = nameInput.trim();
  if (!newName) {
    setError("请输入新技能名称。");
    return;
  }
  if (
    await runAction(
      namePrompt.conflict,
      namePrompt.action,
      namePrompt.entry,
      newName,
    )
  ) {
    setNamePrompt(null);
    setNameInput("");
  }
}
