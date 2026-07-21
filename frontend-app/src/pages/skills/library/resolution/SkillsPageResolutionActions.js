import {
  firstTextField,
  lowerTrimmedText,
  optionalArray,
  trimmedText,
} from "../dashboard/skillsDashboardModel.js";
import {
  externalPersonalProjectResolutionConflict,
  firstResolutionSourceID,
  personalDeletedDriftResolutionConflict,
  resolutionActionHelp,
  resolutionActionLabel,
  resolutionActionUnsupported,
  resolutionSourceID,
  resolutionSourceScope,
  sameNameHasProjectSource,
  sameNamePersonalSources,
  sameNamePersonalVersionText,
  sameNameProjectSources,
  sameNameProjectVersionEntry,
  sameNameRenameEntry,
  sameNameResolutionConflict,
} from "./SkillsPageResolutionLabels.js";

function resolutionActionEntries(conflict) {
  const actions = optionalArray(conflict?.available_actions).filter(
    (action) => !resolutionActionUnsupported(action),
  );
  if (personalDeletedDriftResolutionConflict(conflict)) {
    return actions.map((action) => ({
      action,
      label:
        {
          sync_back_to_personal: "继续私人使用",
          confirm_delete_drifted_mirror: "使用项目共享版本，删除旧私人版本",
        }[action] || resolutionActionLabel(action),
      help: resolutionActionHelp(action),
    }));
  }
  if (externalPersonalProjectResolutionConflict(conflict)) {
    const allowed = externalPersonalProjectAllowedActions();
    const entries = [];
    for (const action of actions) {
      if (!allowed.has(action)) continue;
      entries.push(externalPersonalProjectActionEntry(action));
    }
    return entries;
  }
  if (!sameNameResolutionConflict(conflict)) {
    return actions.map((action) => ({
      action,
      help: resolutionActionHelp(action),
    }));
  }
  const entries = [];
  const personalSources = sameNamePersonalSources(conflict);
  const projectSources = sameNameProjectSources(conflict);
  const hasProjectSource = sameNameHasProjectSource(conflict);
  if (actions.includes("keep_selected")) {
    projectSources.forEach((source) =>
      entries.push(
        sameNameProjectVersionEntry(source, projectSources.length > 1),
      ),
    );
    personalSources.forEach((source) => {
      const versionText = sameNamePersonalVersionText(source, hasProjectSource);
      entries.push({
        action: "keep_selected",
        label: `用${versionText}，删除其他版本`,
        help: `保留这个${versionText}，删除其他同名版本。`,
        source,
        sourceID: resolutionSourceID(source),
      });
    });
  }
  if (actions.includes("rename_personal")) {
    [...projectSources, ...personalSources].forEach((source) => {
      entries.push(sameNameRenameEntry(source, projectSources.length > 1));
    });
  }
  return entries.length > 0
    ? entries
    : actions.map((action) => ({ action, help: resolutionActionHelp(action) }));
}
function externalPersonalProjectActionEntry(action) {
  return {
    action,
    label: externalPersonalProjectActionLabel(action),
    help: resolutionActionHelp(action),
  };
}
function externalPersonalProjectAllowedActions() {
  return new Set([
    "view_diff",
    "use_project_shared_skill",
    "use_external_provider_skill",
    "save_as_new_personal_skill",
  ]);
}
function externalPersonalProjectActionLabel(action) {
  return (
    {
      use_project_shared_skill: "使用项目共享版本，删除旧私人版本",
      use_external_provider_skill: "继续私人使用，替换项目共享版本",
    }[action] || resolutionActionLabel(action)
  );
}
function resolutionActionEntryLabel(entry) {
  return entry?.label || resolutionActionLabel(entry?.action || entry);
}
function resolutionActionEntryHelp(entry) {
  return entry?.help || resolutionActionHelp(entry?.action || entry);
}
function resolutionActionEntryTarget(actionEntry, providerEntry) {
  if (
    providerEntry?.merged_provider_entry &&
    actionEntry?.action === "view_unmanaged"
  ) {
    return {
      ...providerEntry,
      provider: "",
      source_path_id: "",
      sourcePathId: "",
    };
  }
  return actionEntry?.source ? actionEntry : providerEntry;
}
function resolutionSameNamePayloadFields(conflict, action, entry = null) {
  switch (action) {
    case "rename_personal":
    case "keep_selected": {
      const sources = optionalArray(conflict?.sources);
      const selected =
        entry?.source ||
        sources.find(
          (source) => resolutionSourceScope(source) === "personal",
        ) ||
        sources.find((source) => resolutionSourceScope(source) === "project");
      const keepSourceID =
        resolutionSourceID(selected) || firstResolutionSourceID(conflict);
      return keepSourceID ? { keep_source_id: keepSourceID } : {};
    }
    case "merge_manually": {
      const mergeContentHash = trimmedText(conflict?.merge_content_hash);
      return {
        keep_source_id: firstResolutionSourceID(conflict),
        merge_content_hash: mergeContentHash,
      };
    }
    default:
      return {};
  }
}
function resolutionActionAutoApplies(action) {
  return action === "keep_selected";
}
function resolutionActionAutoAppliesForConflict(action, conflict) {
  if (resolutionActionAutoApplies(action)) return true;
  if (action === "rename_personal") return true;
  if (externalPersonalProjectResolutionConflict(conflict)) {
    return (
      action === "use_project_shared_skill" ||
      action === "use_external_provider_skill" ||
      action === "save_as_new_personal_skill"
    );
  }
  return false;
}
function resolutionApplyKey(conflict, action, entry = null) {
  const source =
    firstTextField(
      entry,
      ["source_path_id", "provider", "sourceID"],
      "skill resolution entry",
    ) || resolutionSourceID(entry?.source);
  return `${trimmedText(conflict?.conflict_id)}:${trimmedText(source)}:${trimmedText(action)}`;
}
function previewItemPaths(item, action = "") {
  const normalizedAction = trimmedText(action || item?.action);
  const overwrite =
    normalizedAction === "canonical_overwrite_mirror" ||
    normalizedAction === "personal_overwrite_mirror";
  const importAction =
    normalizedAction === "import_to_personal_imported" ||
    normalizedAction === "import_to_project" ||
    normalizedAction === "takeover_provider_skill";
  const useProjectShared = normalizedAction === "use_project_shared_skill";
  const useExternal = normalizedAction === "use_external_provider_skill";
  const sourceLabel = overwrite || useProjectShared ? "本项目版本" : "外部版本";
  let targetLabel = overwrite ? "外部版本" : "本项目版本";
  if (importAction) targetLabel = "保存位置";
  if (useProjectShared) targetLabel = "外部版本";
  if (useExternal) targetLabel = "项目共享版本";
  return [
    [sourceLabel, item?.source_path],
    [targetLabel, item?.target_path],
  ]
    .map(([label, value]) => ({ label, value: trimmedText(value) }))
    .filter((itemPath) => itemPath.value);
}
function resolutionShortHash(value) {
  return trimmedText(value).slice(0, 8);
}
function resolutionManualSteps(conflict) {
  const kind = lowerTrimmedText(conflict?.kind);
  const actions = optionalArray(conflict?.available_actions);
  if (
    (kind === "same_name" || kind === "same_name_scope_conflict") &&
    !actions.includes("keep_selected") &&
    !actions.includes("rename_personal")
  ) {
    return [
      "要保留项目共享：编辑或删除同名私人技能。",
      "要保留私人使用：编辑项目共享技能改名，或删除项目共享技能。",
      "两边都要保留：把其中一个改成更明确的名字。",
    ];
  }
  return [];
}

export {
  resolutionActionEntries,
  externalPersonalProjectActionEntry,
  externalPersonalProjectAllowedActions,
  externalPersonalProjectActionLabel,
  resolutionActionEntryLabel,
  resolutionActionEntryHelp,
  resolutionActionEntryTarget,
  resolutionSameNamePayloadFields,
  resolutionActionAutoApplies,
  resolutionActionAutoAppliesForConflict,
  resolutionApplyKey,
  previewItemPaths,
  resolutionShortHash,
  resolutionManualSteps,
};
