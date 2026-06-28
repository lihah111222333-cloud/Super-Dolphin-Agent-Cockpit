import {
  resolutionActionHelp,
  resolutionActionLabel,
  resolutionProviderLabel,
} from './useSkillResolutionCopy.js';

export function requiresResolutionNewName(action) {
  return action === 'save_as_new_skill'
    || action === 'save_as_new_personal_skill'
    || action === 'rename_personal';
}

export function isResolutionViewAction(action) {
  return action === 'view_diff' || action === 'view_unmanaged';
}

export function isResolutionPreviewOnlyAction(action) {
  return false;
}

export function resolutionRequiresApply(action) {
  return !isResolutionViewAction(action) && !isResolutionPreviewOnlyAction(action);
}

export function defaultResolutionNewName(conflict, action) {
  const base = (conflict?.name || conflict?.skill_name || 'skill').toString().trim() || 'skill';
  return `${base}${action === 'save_as_new_personal_skill' ? '-private' : '-copy'}`;
}

const actionableResolutionActions = new Set([
  'view_diff',
  'view_unmanaged',
  'sync_back_to_canonical',
  'canonical_overwrite_mirror',
  'save_as_new_skill',
  'confirm_delete_drifted_mirror',
  'sync_back_to_personal',
  'personal_overwrite_mirror',
  'save_as_new_personal_skill',
  'import_to_personal_imported',
  'import_to_project',
  'takeover_provider_skill',
  'use_project_shared_skill',
  'use_external_provider_skill',
  'replace_provider_root_symlink',
  'rename_personal',
  'keep_selected',
]);

export function resolutionActionUnsupported(action) {
  return !actionableResolutionActions.has((action || '').toString().trim());
}

export function normalizeResolutionConflict(conflict) {
  const actions = Array.isArray(conflict?.available_actions) ? conflict.available_actions : [];
  return {
    ...conflict,
    available_actions: actions.filter((action) => !resolutionActionUnsupported(action)),
  };
}

export function resolutionSourceID(source) {
  return (source?.canonical_id || source?.canonicalID || '').toString().trim();
}

function resolutionSourceScope(source) {
  return (source?.scope || '').toString().trim().toLowerCase();
}

function resolutionSourcePersonalType(source) {
  return (source?.personal_type || source?.personalType || '').toString().trim().toLowerCase();
}

function sameNameProjectSources(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.filter((source) => resolutionSourceScope(source) === 'project');
}

function firstResolutionSourceID(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return resolutionSourceID(sources[0]);
}

export function resolutionSameNamePayloadFields(conflict, action, entry = null) {
  switch (action) {
    case 'rename_personal':
    case 'keep_selected': {
      const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
      const selected = entry?.source || sources.find((source) => resolutionSourceScope(source) === 'personal') || sources.find((source) => resolutionSourceScope(source) === 'project');
      return { keepSourceID: resolutionSourceID(selected) || firstResolutionSourceID(conflict) };
    }
    case 'merge_manually':
      return {
        keepSourceID: firstResolutionSourceID(conflict),
        mergeContentHash: (conflict?.merge_content_hash || conflict?.mergeContentHash || '').toString().trim(),
      };
    default:
      return {};
  }
}

export function resolutionManualSteps(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  const actions = Array.isArray(conflict?.available_actions) ? conflict.available_actions : [];
  if (kind === 'same_name' || kind === 'same_name_scope_conflict') {
    if (actions.includes('keep_selected') || actions.includes('rename_personal')) {
      return [];
    }
    return [
      '要保留项目共享：编辑或删除同名私人技能。',
      '要保留私人使用：编辑项目共享技能改名，或删除项目共享技能。',
      '两边都要保留：把其中一个改成更明确的名字。',
    ];
  }
  return [];
}

export function sameNameResolutionConflict(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  return kind === 'same_name' || kind === 'same_name_scope_conflict';
}

function sameNamePersonalSources(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.filter((source) => resolutionSourceScope(source) === 'personal');
}

function sameNameHasProjectSource(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.some((source) => resolutionSourceScope(source) === 'project');
}

function sameNamePersonalVersionText(source, hasProjectSource = false) {
  const suffix = hasProjectSource ? '私人版本' : '版本';
  const value = resolutionSourcePersonalType(source);
  return ({
    user: `自己创建的${suffix}`,
    agent: `自动生成的${suffix}`,
    imported: `导入的${suffix}`,
    hub: `市场下载的${suffix}`,
  }[value] || `私人${suffix}`);
}

function sameNameProjectVersionEntry(source) {
  return {
    action: 'keep_selected',
    label: '用项目共享版本，删除其他版本',
    help: '保留项目共享版本，删除其他同名版本。',
    source,
    sourceID: resolutionSourceID(source),
  };
}

function sameNameProjectVersionEntryForSource(source, multipleProjectSources = false) {
  if (!multipleProjectSources) return sameNameProjectVersionEntry(source);
  const leaf = resolutionSourcePathLeaf(source) || resolutionSourceID(source).replace(/^project\//, '') || '项目共享版本';
  return {
    action: 'keep_selected',
    label: `用项目共享版本：${leaf}，删除其他版本`,
    help: '保留这个项目共享版本，删除其他同名版本。',
    source,
    sourceID: resolutionSourceID(source),
  };
}

function sameNameRenameEntry(source, includeSourceLeaf = false) {
  return {
    action: 'rename_personal',
    label: `改名保存${sameNameSourceShortText(source, includeSourceLeaf)}`,
    help: '把这个版本改成新名称，原来的同名冲突会保留为不同技能。',
    source,
    sourceID: resolutionSourceID(source),
  };
}

function sameNameSourceShortText(source, includeSourceLeaf = false) {
  if (resolutionSourceScope(source) === 'project') {
    const leaf = includeSourceLeaf ? resolutionSourcePathLeaf(source) || resolutionSourceID(source).replace(/^project\//, '') : '';
    return leaf ? `项目共享版本：${leaf}` : '项目共享版本';
  }
  return sameNamePersonalVersionText(source, true);
}

function resolutionSourcePathLeaf(source) {
  const path = (source?.path || source?.skill_file || source?.skillFile || '').toString().trim().replace(/\\/g, '/');
  if (!path) return '';
  const parts = path.split('/').filter(Boolean);
  const leaf = parts[parts.length - 1] || '';
  if (leaf === 'SKILL.md' && parts.length > 1) return parts[parts.length - 2] || '';
  return leaf;
}

export function resolutionActionEntryLabel(entry) {
  return entry?.label || resolutionActionLabel(entry?.action || entry);
}

export function resolutionActionEntryHelp(entry) {
  return entry?.help || resolutionActionHelp(entry?.action || entry);
}

export function resolutionActionSectionTitle(conflict) {
  return sameNameResolutionConflict(conflict) ? '选择使用哪个版本' : '处理方式';
}

export function resolutionActionFootnote(conflict) {
  if (!sameNameResolutionConflict(conflict)) return '';
  return '处理后同名冲突会立即消失。';
}

export function resolutionActionEntryTarget(actionEntry, providerEntry) {
  if (providerEntry?.merged_provider_entry && actionEntry?.action === 'view_unmanaged') {
    return {
      ...providerEntry,
      provider: '',
      source_path_id: '',
      sourcePathId: '',
    };
  }
  return actionEntry?.source ? actionEntry : providerEntry;
}

function resolutionActionAutoApplies(action) {
  return action === 'keep_selected';
}

export function resolutionActionAutoAppliesForConflict(action, conflict) {
  if (resolutionActionAutoApplies(action)) return true;
  if (action === 'rename_personal') return true;
  if (externalPersonalProjectResolutionConflict(conflict)) {
    return action === 'use_project_shared_skill'
      || action === 'use_external_provider_skill'
      || action === 'save_as_new_personal_skill';
  }
  return false;
}

function personalDeletedDriftResolutionConflict(conflict) {
  return (conflict?.kind || '').toString().trim().toLowerCase() === 'canonical_deleted_with_drift'
    && (conflict?.scope || '').toString().trim().toLowerCase() === 'personal';
}

function personalDeletedDriftActionEntry(action) {
  const labels = {
    sync_back_to_personal: '继续私人使用',
    confirm_delete_drifted_mirror: '使用项目共享版本，删除旧私人版本',
  };
  const helps = {
    sync_back_to_personal: '恢复为私人使用，Claude/Codex 会继续读取这个私人版本。',
    confirm_delete_drifted_mirror: '删除 Claude/Codex 里的旧私人版本；如果项目共享里已有同名技能，会继续使用项目共享版本。',
  };
  return { action, label: labels[action] || resolutionActionLabel(action), help: helps[action] || resolutionActionHelp(action) };
}

function externalPersonalProjectActionEntry(action) {
  const labels = {
    use_project_shared_skill: '使用项目共享版本，删除旧私人版本',
    use_external_provider_skill: '继续私人使用，替换项目共享版本',
  };
  return { action, label: labels[action] || resolutionActionLabel(action), help: resolutionActionHelp(action) };
}

export function resolutionNamePromptHelpText(prompt) {
  if (!prompt) return '';
  return prompt.autoApply
    ? `${prompt.label}，会立刻处理这个冲突。`
    : `${prompt.label}，生成预览后再确认应用。`;
}

export function resolutionNamePromptButtonText(prompt, actioning) {
  if (!prompt) return '生成预览';
  if (actioning === prompt.applyKey) {
    return prompt.autoApply ? '处理中...' : '生成中...';
  }
  return prompt.autoApply ? prompt.label : '生成预览';
}

export function resolutionConflictNotFound(error) {
  return (error?.message || String(error || '')).includes('resolution conflict not found');
}

function resolutionConflictShapePart(value) {
  return (value || '').toString().trim().toLowerCase();
}

export function sameResolutionConflictShape(left, right) {
  return resolutionConflictShapePart(left?.name || left?.skill_name) === resolutionConflictShapePart(right?.name || right?.skill_name)
    && resolutionConflictShapePart(left?.kind) === resolutionConflictShapePart(right?.kind)
    && resolutionConflictShapePart(left?.scope) === resolutionConflictShapePart(right?.scope)
    && resolutionConflictShapePart(left?.personal_type || left?.personalType) === resolutionConflictShapePart(right?.personal_type || right?.personalType);
}

function groupedResolutionProviderEntries(conflict, entries) {
  if (!externalProviderResolutionConflict(conflict) || entries.length < 2) {
    return entries;
  }
  const groups = [];
  const byHash = new Map();
  for (const entry of entries) {
    const hash = (entry?.source_hash || entry?.sourceHash || '').toString().trim();
    if (!hash) return entries;
    if (!byHash.has(hash)) {
      const group = [];
      byHash.set(hash, group);
      groups.push(group);
    }
    byHash.get(hash).push(entry);
  }
  return groups.flatMap((group) => {
    if (group.length === 1) return group;
    const providers = group.map((entry) => (entry?.provider || '').toString().trim()).filter(Boolean);
    return [{
      ...group[0],
      provider_entries: group,
      provider_group: providers,
      display_label: providers.map(resolutionProviderLabel).join('、'),
      merged_provider_entry: true,
    }];
  });
}

function externalProviderResolutionConflict(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  return kind === 'unmanaged_provider_skill' || kind === 'unmanaged_same_name' || kind === 'unmanaged' || kind === 'external_personal_project_same_name';
}

function externalPersonalProjectResolutionConflict(conflict) {
  return (conflict?.kind || '').toString().trim().toLowerCase() === 'external_personal_project_same_name';
}

export function resolutionProviderEntries(conflict) {
  const entries = Array.isArray(conflict?.provider_entries) ? conflict.provider_entries : [];
  if (entries.length > 0) return groupedResolutionProviderEntries(conflict, entries);
  const provider = (conflict?.provider || conflict?.source_provider || '').toString().trim();
  if (!provider) return [{}];
  return [{
    provider,
    source_path_id: conflict?.source_path_id || '',
  }];
}

export function resolutionActionEntries(conflict) {
  const actions = (Array.isArray(conflict?.available_actions) ? conflict.available_actions : [])
    .filter((action) => !resolutionActionUnsupported(action));
  if (personalDeletedDriftResolutionConflict(conflict)) {
    return actions.map(personalDeletedDriftActionEntry);
  }
  if (externalPersonalProjectResolutionConflict(conflict)) {
    const allowed = new Set(['view_diff', 'use_project_shared_skill', 'use_external_provider_skill', 'save_as_new_personal_skill']);
    return actions.filter((action) => allowed.has(action)).map(externalPersonalProjectActionEntry);
  }
  if (!sameNameResolutionConflict(conflict)) {
    return actions.map((action) => ({ action }));
  }
  const entries = [];
  const personalSources = sameNamePersonalSources(conflict);
  const projectSources = sameNameProjectSources(conflict);
  const hasProjectSource = sameNameHasProjectSource(conflict);
  if (actions.includes('keep_selected') && projectSources.length > 0) {
    projectSources.forEach((source) => {
      entries.push(sameNameProjectVersionEntryForSource(source, projectSources.length > 1));
    });
  }
  if (actions.includes('keep_selected')) {
    personalSources.forEach((source) => {
      const versionText = sameNamePersonalVersionText(source, hasProjectSource);
      entries.push({
        action: 'keep_selected',
        label: `用${versionText}，删除其他版本`,
        help: `保留这个${versionText}，删除其他同名版本。`,
        source,
        sourceID: resolutionSourceID(source),
      });
    });
  }
  if (actions.includes('rename_personal')) {
    [...projectSources, ...personalSources].forEach((source) => {
      entries.push(sameNameRenameEntry(source, projectSources.length > 1));
    });
  }
  return entries;
}
