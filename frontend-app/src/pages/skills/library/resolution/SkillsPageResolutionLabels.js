import { skillResolutionResponseSchema } from "../SkillsPageMarkdownModel.js";
import {
  firstTextField,
  lowerTrimmedText,
  optionalArray,
  textFromValue,
  trimmedText,
} from "../dashboard/skillsDashboardModel.js";

export function normalizeResolutionResponse(response) {
  const result = skillResolutionResponseSchema.safeParse(response);
  if (!result.success) {
    if (!response || typeof response !== "object")
      throw new Error("skill resolutions response must be an object");
    throw new Error("skill resolutions response items must be an array");
  }
  const parsed = result.data;
  if (Array.isArray(parsed)) return parsed;
  if (Array.isArray(parsed.items)) return parsed.items;
  return parsed.conflicts;
}
function resolutionKindLabel(kind) {
  return (
    {
      mirror_drift: "外部版本有改动",
      unmanaged_provider_skill: "发现外部技能",
      unmanaged: "发现外部技能",
      same_name: "同名技能",
      same_name_scope_conflict: "同名技能",
      canonical_deleted_with_drift: "旧版本需要处理",
      external_personal_project_same_name: "私人和项目同名",
    }[lowerTrimmedText(kind)] || "需要处理"
  );
}
function resolutionActionLabel(action) {
  return (
    {
      view_diff: "查看两个版本",
      view_unmanaged: "查看外部位置",
      sync_back_to_canonical: "用外部修改更新本项目",
      canonical_overwrite_mirror: "用本项目内容覆盖外部版本",
      save_as_new_skill: "另存为新技能",
      confirm_delete_drifted_mirror: "删除旧版本",
      sync_back_to_personal: "继续私人使用",
      personal_overwrite_mirror: "用私人技能覆盖外部版本",
      save_as_new_personal_skill: "另存为新私人技能",
      import_to_personal_imported: "导入到私人使用",
      import_to_project: "导入到项目共享",
      takeover_provider_skill: "纳入管理",
      use_project_shared_skill: "使用项目共享版本，删除旧私人版本",
      use_external_provider_skill: "继续私人使用，替换项目共享版本",
      replace_provider_root_symlink: "接管外部技能目录",
      rename_personal: "改名保存",
      keep_selected: "用选中的版本，删除其他版本",
    }[trimmedText(action)] || "处理"
  );
}
function resolutionActionHelp(action) {
  const help = {
    view_diff: "只查看两个版本分别在哪里，不会修改文件。",
    view_unmanaged: "查看外部技能位置，不写入文件。",
    sync_back_to_canonical: "把外部修改同步回当前管理的技能。",
    canonical_overwrite_mirror:
      "用当前项目共享技能覆盖 Claude/Codex 中的外部版本。",
    save_as_new_skill: "保留两边内容，把外部版本保存成新的项目共享技能。",
    confirm_delete_drifted_mirror: "删除 Claude/Codex 里保留的旧版本。",
    sync_back_to_personal: "恢复为私人使用，外部运行时会继续读取这个私人版本。",
    personal_overwrite_mirror: "用当前私人技能覆盖 Claude/Codex 中的外部版本。",
    save_as_new_personal_skill: "保留两边内容，把外部版本保存成新的私人技能。",
    import_to_personal_imported: "把外部技能导入到私人使用。",
    import_to_project: "把外部技能导入到项目共享。",
    takeover_provider_skill: "把外部技能纳入当前技能管理。",
    use_project_shared_skill: "使用项目共享版本，并删除同名旧私人版本。",
    use_external_provider_skill: "继续私人使用，并替换项目共享版本。",
    replace_provider_root_symlink: "用当前技能根目录接管外部技能目录。",
    rename_personal: "把选中的版本改名保存，两个版本都会保留。",
    keep_selected: "保留选中的版本，删除其他同名版本。",
  }[trimmedText(action)];
  return textFromValue(help);
}
function resolutionConflictGuide(conflict) {
  const kind = lowerTrimmedText(conflict?.kind);
  if (sameNameResolutionConflict(conflict)) {
    if (
      !sameNameHasProjectSource(conflict) &&
      sameNamePersonalSources(conflict).length > 1
    ) {
      return "发现多个同名的私人技能。请选择保留哪一版，其他同名版本会被删除；也可以改名保存。";
    }
    return "发现多个同名技能。请选择保留哪一版，其他同名版本会被删除；也可以改名保存。";
  }
  if (kind === "external_personal_project_same_name") {
    return "检测到同名技能同时存在于私人使用和项目共享。请选择使用项目共享版本、继续私人使用，或另存为新私人技能。";
  }
  if (
    kind === "unmanaged_provider_skill" ||
    kind === "unmanaged_same_name" ||
    kind === "unmanaged"
  ) {
    return "外部应用里有一个还没纳入管理的技能。可以导入后统一管理，或只保留在外部应用里。";
  }
  if (kind === "canonical_deleted_with_drift") {
    if (lowerTrimmedText(conflict?.scope) === "personal") {
      return (
        "私人使用里的同名技能已经删除或改成项目共享，但 Claude/Codex 里还保留旧私人版本。" +
        "请选择继续私人使用、另存为新私人技能，或删除旧私人版本。"
      );
    }
    return "本项目里的技能已不存在，但外部应用里还有改过的版本。请选择恢复、另存或删除外部版本。";
  }
  if (kind === "mirror_root_symlink") {
    return "外部应用的技能目录还是旧连接。接管后会改成由本项目管理的技能目录，并重新同步技能。";
  }
  return "外部应用里的技能和本项目管理的技能不一致。请选择下面一种处理方式。";
}
function resolutionPreviewIntro(preview) {
  const action = trimmedText(preview?.action);
  if (isResolutionViewAction(action))
    return "下面只说明两个版本分别在哪里，不会修改文件。";
  return "请先确认将要写入的位置，确认应用后才会修改文件。";
}
function requiresResolutionNewName(action) {
  return (
    action === "save_as_new_skill" ||
    action === "save_as_new_personal_skill" ||
    action === "rename_personal"
  );
}
function isResolutionViewAction(action) {
  return action === "view_diff" || action === "view_unmanaged";
}
function resolutionRequiresApply(action) {
  return !isResolutionViewAction(action);
}
function defaultResolutionNewName(conflict, action) {
  const base =
    firstTextField(
      conflict,
      ["name", "skill_name"],
      "skill resolution conflict",
    ) || "skill";
  return `${base}${action === "save_as_new_personal_skill" ? "-private" : "-copy"}`;
}
const actionableResolutionActions = new Set([
  "view_diff",
  "view_unmanaged",
  "sync_back_to_canonical",
  "canonical_overwrite_mirror",
  "save_as_new_skill",
  "confirm_delete_drifted_mirror",
  "sync_back_to_personal",
  "personal_overwrite_mirror",
  "save_as_new_personal_skill",
  "import_to_personal_imported",
  "import_to_project",
  "takeover_provider_skill",
  "use_project_shared_skill",
  "use_external_provider_skill",
  "replace_provider_root_symlink",
  "rename_personal",
  "keep_selected",
]);
function resolutionActionUnsupported(action) {
  return !actionableResolutionActions.has(trimmedText(action));
}
function resolutionSourceID(source) {
  return firstTextField(
    source,
    ["canonical_id", "source_id"],
    "skill resolution source",
  );
}
function resolutionSourceScope(source) {
  return lowerTrimmedText(source?.scope);
}
function resolutionSourcePersonalType(source) {
  return lowerTrimmedText(source?.personal_type);
}
function resolutionSourcePathLeaf(source) {
  const path = firstTextField(
    source,
    ["path", "skill_file"],
    "skill resolution source",
  ).replace(/\\/g, "/");
  if (!path) return "";
  const parts = path.split("/").filter(Boolean);
  const leaf = parts[parts.length - 1];
  return leaf === "SKILL.md" && parts.length > 1
    ? textFromValue(parts[parts.length - 2])
    : textFromValue(leaf);
}
function sameNameResolutionConflict(conflict) {
  const kind = lowerTrimmedText(conflict?.kind);
  return kind === "same_name" || kind === "same_name_scope_conflict";
}
function sameNameProjectSources(conflict) {
  const sources = optionalArray(conflict?.sources);
  return sources.filter(
    (source) => resolutionSourceScope(source) === "project",
  );
}
function sameNamePersonalSources(conflict) {
  const sources = optionalArray(conflict?.sources);
  return sources.filter(
    (source) => resolutionSourceScope(source) === "personal",
  );
}
function sameNameHasProjectSource(conflict) {
  const sources = optionalArray(conflict?.sources);
  return sources.some((source) => resolutionSourceScope(source) === "project");
}
function firstResolutionSourceID(conflict) {
  const sources = optionalArray(conflict?.sources);
  return resolutionSourceID(sources[0]);
}
function sameNamePersonalVersionText(source, hasProjectSource = false) {
  const suffix = hasProjectSource ? "私人版本" : "版本";
  const value = resolutionSourcePersonalType(source);
  return (
    {
      user: `自己创建的${suffix}`,
      agent: `自动生成的${suffix}`,
      imported: `导入的${suffix}`,
      hub: `市场下载的${suffix}`,
    }[value] || `私人${suffix}`
  );
}
function sameNameSourceShortText(source, includeSourceLeaf = false) {
  if (resolutionSourceScope(source) === "project") {
    const leaf = includeSourceLeaf
      ? resolutionSourcePathLeaf(source) ||
        resolutionSourceID(source).replace(/^project\//, "")
      : "";
    return leaf ? `项目共享版本：${leaf}` : "项目共享版本";
  }
  return sameNamePersonalVersionText(source, true);
}
function sameNameProjectVersionEntry(source, multipleProjectSources = false) {
  const leaf = multipleProjectSources
    ? resolutionSourcePathLeaf(source) ||
      resolutionSourceID(source).replace(/^project\//, "")
    : "";
  return {
    action: "keep_selected",
    label: leaf
      ? `用项目共享版本：${leaf}，删除其他版本`
      : "用项目共享版本，删除其他版本",
    help: "保留这个项目共享版本，删除其他同名版本。",
    source,
    sourceID: resolutionSourceID(source),
  };
}
function sameNameRenameEntry(source, includeSourceLeaf = false) {
  return {
    action: "rename_personal",
    label: `改名保存${sameNameSourceShortText(source, includeSourceLeaf)}`,
    help: "把这个版本改成新名称，原来的同名冲突会保留为不同技能。",
    source,
    sourceID: resolutionSourceID(source),
  };
}
function personalDeletedDriftResolutionConflict(conflict) {
  return (
    lowerTrimmedText(conflict?.kind) === "canonical_deleted_with_drift" &&
    lowerTrimmedText(conflict?.scope) === "personal"
  );
}
function externalPersonalProjectResolutionConflict(conflict) {
  return (
    lowerTrimmedText(conflict?.kind) === "external_personal_project_same_name"
  );
}
function resolutionProviderLabel(provider) {
  return (
    { codex: "Codex", claude: "Claude" }[lowerTrimmedText(provider)] ||
    trimmedText(provider)
  );
}
function resolutionProviderEntryLabel(entry) {
  const label = trimmedText(entry?.display_label);
  if (label) return label;
  const group = [];
  for (const provider of optionalArray(entry?.provider_group)) {
    const providerLabel = resolutionProviderLabel(provider);
    if (providerLabel) group.push(providerLabel);
  }
  if (group.length > 0) return group.join("、");
  const provider = firstTextField(
    entry,
    ["provider", "source_provider"],
    "skill resolution provider entry",
  );
  return resolutionProviderLabel(provider) || "外部版本";
}
function resolutionProviderEntries(conflict) {
  const entries = optionalArray(conflict?.provider_entries);
  if (entries.length > 0) return entries;
  const provider = firstTextField(
    conflict,
    ["provider", "source_provider"],
    "skill resolution conflict",
  );
  if (!provider) return [{}];
  return [{ provider, source_path_id: trimmedText(conflict?.source_path_id) }];
}

export {
  resolutionKindLabel,
  resolutionActionLabel,
  resolutionActionHelp,
  resolutionConflictGuide,
  resolutionPreviewIntro,
  requiresResolutionNewName,
  isResolutionViewAction,
  resolutionRequiresApply,
  defaultResolutionNewName,
  actionableResolutionActions,
  resolutionActionUnsupported,
  resolutionSourceID,
  resolutionSourceScope,
  resolutionSourcePersonalType,
  resolutionSourcePathLeaf,
  sameNameResolutionConflict,
  sameNameProjectSources,
  sameNamePersonalSources,
  sameNameHasProjectSource,
  firstResolutionSourceID,
  sameNamePersonalVersionText,
  sameNameSourceShortText,
  sameNameProjectVersionEntry,
  sameNameRenameEntry,
  personalDeletedDriftResolutionConflict,
  externalPersonalProjectResolutionConflict,
  resolutionProviderLabel,
  resolutionProviderEntryLabel,
  resolutionProviderEntries,
};
