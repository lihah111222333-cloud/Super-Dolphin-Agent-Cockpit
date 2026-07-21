import { skillsPageService } from "../../services/skillsPageService.js";
import { resolutionActionLabel } from "./SkillsPageResolutionLabels.js";
import { trimmedText } from "../dashboard/skillsDashboardModel.js";

const { applySkillResolution } = skillsPageService;

export async function autoApplyResolutionPreview(ctx, payload, items) {
  const proof = items[0];
  if (!proof?.preview_id || !proof?.preview_hash)
    throw new Error("缺少处理预览凭据");
  const report = await applySkillResolution(
    resolutionApplyPayload(payload, proof),
  );
  ctx.setPreview(null);
  ctx.setNamePrompt(null);
  ctx.setNameInput("");
  await ctx.refreshSkillSurface();
  applyResolutionReportFeedback(ctx, report);
  return true;
}
function resolutionApplyPayload(payload, proof) {
  return {
    ...payload,
    provider: proof.provider || payload.provider,
    source_provider: proof.source_provider || payload.source_provider,
    source_path_id: proof.source_path_id || payload.source_path_id,
    preview_id: proof.preview_id,
    preview_hash: proof.preview_hash,
  };
}
function resolutionApplyPartialFailure(report) {
  return Boolean(report?.partialFailure);
}
function resolutionApplyFollowUpAction(report) {
  return trimmedText(report?.followUpAction);
}
function resolutionApplyReportMessage(report) {
  if (!resolutionApplyPartialFailure(report)) return "已处理技能冲突";
  const followUpAction = resolutionApplyFollowUpAction(report);
  const followUp = followUpAction
    ? `，后续需要重试：${resolutionActionLabel(followUpAction)}`
    : "，请查看技能冲突列表并重试";
  return `技能冲突已部分处理${followUp}`;
}
function applyResolutionReportFeedback(ctx, report) {
  const message = resolutionApplyReportMessage(report);
  if (resolutionApplyPartialFailure(report)) {
    ctx.setNotice("");
    ctx.setError(message);
    return;
  }
  ctx.setError("");
  ctx.setNotice(message);
}
export async function confirmResolutionPreview(ctx) {
  const proof = Array.isArray(ctx.preview?.items) ? ctx.preview.items[0] : null;
  if (!ctx.preview?.requiresApply || !proof?.preview_id || !proof?.preview_hash)
    return;
  ctx.setActioning("confirm");
  try {
    const report = await applySkillResolution(
      resolutionApplyPayload(ctx.preview.payload, proof),
    );
    ctx.setPreview(null);
    ctx.setNamePrompt(null);
    ctx.setNameInput("");
    await ctx.refreshSkillSurface();
    applyResolutionReportFeedback(ctx, report);
  } catch (err) {
    ctx.setError("应用技能冲突处理失败，请重试。");
    throw err;
  } finally {
    ctx.setActioning("");
  }
}
