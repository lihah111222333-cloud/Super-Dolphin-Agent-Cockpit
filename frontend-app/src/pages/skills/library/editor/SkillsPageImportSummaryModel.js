import {
  firstTextField,
  textFromValue,
  trimmedText,
} from "../dashboard/skillsDashboardModel.js";

function importedSkillFilePath(item) {
  return firstTextField(item, ["skill_file", "path"], "imported skill item");
}
function isImportedSkillSameNameConflictError(error) {
  const message = textFromValue(error?.message || error).toLowerCase();
  return (
    message.includes("skill same-name conflict") ||
    message.includes("err_skill_same_name_conflict") ||
    message.includes("skill path is not in effective skill set")
  );
}
function importedSkillSameNameConflictMessage(draft) {
  return draft.scope === "personal"
    ? "已导入，但和项目共享技能同名，暂未启用。请在冲突提示中选择使用哪个版本。"
    : "已导入，但与现有技能同名，暂未启用。请在冲突提示中选择使用哪个版本。";
}
function skillFileBaseName(path) {
  const clean = trimmedText(path)
    .replace(/[\\/]+SKILL\.md$/i, "")
    .replace(/\\/g, "/");
  const parts = clean.split("/").filter(Boolean);
  return textFromValue(parts[parts.length - 1]);
}
function fileNameFromPath(path) {
  const clean = trimmedText(path)
    .replace(/[\\/]+$/g, "")
    .replace(/\\/g, "/");
  const parts = clean.split("/").filter(Boolean);
  return textFromValue(parts[parts.length - 1]);
}
function normalizeImportSummaryDraftScope(scope) {
  return scope === "personal" ? "personal" : "project";
}
function importSummaryDraftStatusCount(drafts, status) {
  return drafts.filter((draft) => draft.status === status).length;
}
function importSummaryPanelTitle(drafts) {
  const conflictCount = importSummaryDraftStatusCount(drafts, "conflict");
  const errorCount = importSummaryDraftStatusCount(drafts, "error");
  const readyCount = drafts.filter(
    (draft) => draft.status === "ready" || draft.status === "applied",
  ).length;
  if (conflictCount > 0 && readyCount === 0) return "导入后需要处理";
  if (conflictCount > 0) return "导入后的简介建议和同名处理";
  if (errorCount > 0 && readyCount === 0) return "导入后可补充简介";
  return "导入后的简介建议";
}
function importSummaryPanelHint(drafts) {
  const conflictCount = importSummaryDraftStatusCount(drafts, "conflict");
  const errorCount = importSummaryDraftStatusCount(drafts, "error");
  const readyCount = drafts.filter(
    (draft) => draft.status === "ready" || draft.status === "applied",
  ).length;
  if (conflictCount > 0 && readyCount === 0)
    return "同名技能需要先选择使用哪个版本。";
  if (conflictCount > 0)
    return "简介建议采用并保存后生效；同名技能需要选择使用哪个版本。";
  if (errorCount > 0 && readyCount === 0)
    return "技能已正常导入，可以稍后手动补充简介。";
  return "还没有写入技能，采用并保存后生效。";
}
function importSummaryDraftMessage(drafts) {
  const readyCount = importSummaryDraftStatusCount(drafts, "ready");
  const conflictCount = importSummaryDraftStatusCount(drafts, "conflict");
  const errorCount = importSummaryDraftStatusCount(drafts, "error");
  const parts = [];
  if (readyCount > 0)
    parts.push(`已生成 ${readyCount} 条简介建议，采用后再保存。`);
  if (conflictCount > 0) parts.push(`${conflictCount} 个同名技能待处理。`);
  if (errorCount > 0) parts.push(`${errorCount} 个技能可手动补充简介。`);
  return parts.join("，");
}
function duplicateImportFailureMessage(message) {
  const raw = trimmedText(message);
  const existsMatch = raw.match(/^skill already exists:\s*(.+)$/i);
  if (existsMatch)
    return `${trimmedText(existsMatch[1]) || "该技能"} 已存在，未重复导入。`;
  if (/^source is inside skills root:/i.test(raw))
    return "这个目录已经在技能管理中，未重复导入。";
  return "";
}
function normalizeImportFailure(item) {
  const source = trimmedText(item?.source);
  const rawMessage = trimmedText(item?.error) || "未知错误";
  const duplicateMessage = duplicateImportFailureMessage(rawMessage);
  return {
    duplicate: Boolean(duplicateMessage),
    duplicateName: trimmedText(
      rawMessage.match(/^skill already exists:\s*(.+)$/i)?.[1],
    ),
    message: duplicateMessage || rawMessage,
    source,
  };
}
function summarizeImportFailureNames(names) {
  if (names.length <= 3) return names.join("、");
  return `${names.slice(0, 3).join("、")} 等 ${names.length} 个`;
}
function duplicateImportNotice(scope, duplicateFailures) {
  const names = [];
  for (const item of duplicateFailures) {
    if (item.duplicateName) names.push(item.duplicateName);
  }
  const prefix =
    normalizeImportSummaryDraftScope(scope) === "personal"
      ? "私人使用里已存在"
      : "项目共享里已存在";
  return names.length > 0
    ? `${prefix}：${summarizeImportFailureNames(names)}，未重复导入。`
    : `${prefix} ${duplicateFailures.length} 个技能，未重复导入。`;
}
function importNotice(importedCount, drafts, failures, scope) {
  const parts = [];
  if (importedCount > 0) parts.push(`已导入 ${importedCount} 个技能目录`);
  const draftMessage = importSummaryDraftMessage(drafts);
  if (draftMessage) parts.push(draftMessage);
  const duplicateFailures = failures.filter((failure) => failure.duplicate);
  if (duplicateFailures.length > 0)
    parts.push(duplicateImportNotice(scope, duplicateFailures));
  const otherFailures = failures.filter((failure) => !failure.duplicate);
  if (otherFailures.length > 0)
    parts.push(
      `${otherFailures.length} 个目录导入失败：${otherFailures[0].source || otherFailures[0].message}`,
    );
  return parts.length > 0 ? parts.join("，") : "未导入任何技能目录";
}

export {
  importedSkillFilePath,
  isImportedSkillSameNameConflictError,
  importedSkillSameNameConflictMessage,
  skillFileBaseName,
  fileNameFromPath,
  normalizeImportSummaryDraftScope,
  importSummaryDraftStatusCount,
  importSummaryPanelTitle,
  importSummaryPanelHint,
  importSummaryDraftMessage,
  duplicateImportFailureMessage,
  normalizeImportFailure,
  summarizeImportFailureNames,
  duplicateImportNotice,
  importNotice,
};
