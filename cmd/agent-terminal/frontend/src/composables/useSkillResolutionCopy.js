function conflictScope(conflict) {
  return (conflict?.scope || '').toString().trim().toLowerCase();
}

function sourceScope(source) {
  return (source?.scope || '').toString().trim().toLowerCase();
}

function personalSources(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.filter((source) => sourceScope(source) === 'personal');
}

function hasProjectSource(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.some((source) => sourceScope(source) === 'project');
}

function pathLooksPersonalCanonical(path) {
  return (path || '').toString().trim().replace(/\\/g, '/').includes('/.super-dolphin/skills/personal/');
}

function personalMirrorAction(action) {
  return new Set([
    'sync_back_to_personal',
    'personal_overwrite_mirror',
    'save_as_new_personal_skill',
  ]).has((action || '').toString().trim());
}

function previewUsesPersonalCanonical(item, action = '') {
  return pathLooksPersonalCanonical(item?.target_path || item?.targetPath)
    || personalMirrorAction(action || item?.action);
}

export function resolutionKindLabel(kind) {
  const value = (kind || '').toString().trim().toLowerCase();
  return ({
    mirror_drift: '外部版本有改动',
    mirror_root_symlink: '旧技能目录需要接管',
    unmanaged_provider_skill: '发现外部技能',
    unmanaged: '发现外部技能',
    same_name: '同名技能',
    same_name_scope_conflict: '同名技能',
    canonical_deleted_with_drift: '旧版本需要处理',
    external_personal_project_same_name: '私人和项目同名',
  }[value] || '需要处理');
}

export function resolutionConflictKindLabel(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  if (kind === 'canonical_deleted_with_drift' && conflictScope(conflict) === 'personal') {
    return '旧私人版本需要处理';
  }
  return resolutionKindLabel(kind);
}

export function resolutionProviderLabel(provider) {
  const value = (provider || '').toString().trim();
  const normalized = value.toLowerCase();
  if (normalized === 'claude') return 'Claude';
  if (normalized === 'codex') return 'Codex';
  return value || '外部应用';
}

export function resolutionProviderEntryLabel(entry) {
  const label = (entry?.display_label || entry?.displayLabel || '').toString().trim();
  if (label) return label;
  return resolutionProviderLabel(entry?.provider);
}

export function resolutionConflictGuide(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  if (kind === 'same_name' || kind === 'same_name_scope_conflict') {
    if (!hasProjectSource(conflict) && personalSources(conflict).length > 1) {
      return '发现多个同名的私人技能。请选择保留哪一版，其他同名版本会被删除；也可以改名保存。';
    }
    return '发现多个同名技能。请选择保留哪一版，其他同名版本会被删除；也可以改名保存。';
  }
  if (kind === 'unmanaged_same_name' && conflictScope(conflict) === 'project') {
    return '项目里已有同名技能，外部版本和项目共享版本不一致。请选择保留哪一版，或另存为新技能。';
  }
  if (kind === 'external_personal_project_same_name') {
    return '检测到同名技能同时存在于私人使用和项目共享。请选择使用项目共享版本、继续私人使用，或另存为新私人技能。';
  }
  if (kind === 'unmanaged_provider_skill' || kind === 'unmanaged_same_name' || kind === 'unmanaged') {
    return '外部应用里有一个还没纳入管理的技能。可以导入后统一管理，或只保留在外部应用里。';
  }
  if (kind === 'canonical_deleted_with_drift') {
    if (conflictScope(conflict) === 'personal') {
      return '私人使用里的同名技能已经删除或改成项目共享，但 Claude/Codex 里还保留旧私人版本。请选择继续私人使用、另存为新私人技能，或删除旧私人版本。';
    }
    return '本项目里的技能已不存在，但外部应用里还有改过的版本。请选择恢复、另存或删除外部版本。';
  }
  if (kind === 'mirror_root_symlink') {
    return '外部应用的技能目录还是旧连接。接管后会改成由本项目管理的技能目录，并重新同步技能。';
  }
  return '外部应用里的技能和本项目管理的技能不一致。请选择下面一种处理方式。';
}

export function resolutionActionHelp(action) {
  return ({
    view_diff: '只查看两个版本分别在哪里，不会修改文件。',
    view_unmanaged: '只查看外部技能位置，不会修改文件。',
    sync_back_to_canonical: '保留 Claude/Codex 里的修改，写回本项目管理的技能。',
    canonical_overwrite_mirror: '丢弃 Claude/Codex 里的修改，用本项目当前技能重新同步。',
    save_as_new_skill: '保留两边内容，把外部版本存成一个新的项目共享技能。',
    confirm_delete_drifted_mirror: '删除 Claude/Codex 里保留的旧版本，下次会按当前技能重新生成。',
    sync_back_to_personal: '保留 Claude/Codex 里的旧私人版本，恢复为私人使用。',
    personal_overwrite_mirror: '丢弃 Claude/Codex 里的修改，用私人技能重新同步。',
    save_as_new_personal_skill: '保留两边内容，把外部版本存成一个新的私人技能。',
    import_to_personal_imported: '把外部技能导入为私人使用，之后由本项目管理。',
    import_to_project: '把外部技能导入为项目共享，项目成员都能使用。',
    takeover_provider_skill: '把外部技能纳入当前作用域管理，后续统一同步。',
    use_project_shared_skill: '使用项目共享版本，删除 Claude/Codex 里的旧私人版本。',
    use_external_provider_skill: '继续私人使用，用 Claude/Codex 里的旧私人版本替换项目共享版本。',
    replace_provider_root_symlink: '移除旧连接，创建由本项目管理的技能目录，并重新同步技能。',
    rename_personal: '把选中的版本改名保存，两个版本都会保留。',
    keep_selected: '保留这个版本，删除其他同名版本。',
  }[action] || '执行前会先预览，不会直接写入。');
}

export function resolutionPreviewIntro(preview) {
  const action = (preview?.action || '').toString().trim();
  if (action === 'view_diff' || action === 'view_unmanaged') {
    return '下面只说明两个版本分别在哪里，不会修改文件。';
  }
  return '请先确认将要写入的位置，确认应用后才会修改文件。';
}

export function resolutionShortHash(hash) {
  return (hash || '').toString().trim().slice(0, 8);
}

export function resolutionPreviewItemPaths(item, action = '') {
  const paths = [];
  const sourcePath = (item?.source_path || item?.sourcePath || '').toString().trim();
  const targetPath = (item?.target_path || item?.targetPath || '').toString().trim();
  const normalizedAction = (action || item?.action || '').toString().trim();
  const providerLabel = resolutionProviderLabel(item?.provider || item?.source_provider);
  if (pathLooksPersonalCanonical(targetPath) || personalMirrorAction(normalizedAction)) {
    const sourceLabel = normalizedAction === 'personal_overwrite_mirror' ? '私人使用版本' : `${providerLabel} 里的旧私人版本`;
    let targetLabel = normalizedAction === 'personal_overwrite_mirror' ? `${providerLabel} 里的旧私人版本` : '私人使用版本';
    if (normalizedAction === 'save_as_new_personal_skill') targetLabel = '保存位置';
    if (sourcePath) paths.push({ label: sourceLabel, value: sourcePath });
    if (targetPath && targetPath !== sourcePath) paths.push({ label: targetLabel, value: targetPath });
    return paths;
  }
  const overwrite = normalizedAction === 'canonical_overwrite_mirror' || normalizedAction === 'personal_overwrite_mirror';
  const importAction = normalizedAction === 'import_to_personal_imported' || normalizedAction === 'import_to_project' || normalizedAction === 'takeover_provider_skill';
  const useProjectShared = normalizedAction === 'use_project_shared_skill';
  const useExternal = normalizedAction === 'use_external_provider_skill';
  const sourceLabel = overwrite || useProjectShared ? '本项目版本' : '外部版本';
  let targetLabel = overwrite ? '外部版本' : '本项目版本';
  if (importAction) targetLabel = '保存位置';
  if (useProjectShared) targetLabel = '外部版本';
  if (useExternal) targetLabel = '项目共享版本';
  if (sourcePath) paths.push({ label: sourceLabel, value: sourcePath });
  if (targetPath && targetPath !== sourcePath) paths.push({ label: targetLabel, value: targetPath });
  return paths;
}

export function resolutionPreviewItemSummary(item, action = '') {
  const provider = resolutionProviderLabel(item?.provider || item?.source_provider);
  switch ((action || item?.action || '').toString().trim()) {
    case 'view_diff':
      return previewUsesPersonalCanonical(item, action)
        ? `${provider} 里还保留旧私人版本，需要选择继续私人使用、另存或删除。`
        : `${provider} 里的版本和本项目管理的版本不一致。`;
    case 'sync_back_to_canonical':
      return `将把 ${provider} 里的版本写回本项目管理的技能。`;
    case 'sync_back_to_personal':
      return `将把 ${provider} 里的旧私人版本恢复为私人使用。`;
    case 'canonical_overwrite_mirror':
    case 'personal_overwrite_mirror':
      return `将用本项目管理的技能覆盖 ${provider} 里的版本。`;
    case 'save_as_new_skill':
    case 'save_as_new_personal_skill':
      return previewUsesPersonalCanonical(item, action)
        ? `将把 ${provider} 里的旧私人版本另存为新私人技能。`
        : `将把 ${provider} 里的版本另存为新技能。`;
    case 'confirm_delete_drifted_mirror':
      return previewUsesPersonalCanonical(item, action)
        ? `将删除 ${provider} 里的旧私人版本。`
        : `将删除 ${provider} 里的旧版本。`;
    case 'import_to_personal_imported':
      return `将把 ${provider} 里的技能导入为私人使用。`;
    case 'import_to_project':
      return `将把 ${provider} 里的技能导入为项目共享。`;
    case 'takeover_provider_skill':
      return `将把 ${provider} 里的技能纳入本项目管理。`;
    case 'use_project_shared_skill':
      return `将使用项目共享版本，删除 ${provider} 里的旧私人版本。`;
    case 'use_external_provider_skill':
      return `将继续私人使用，用 ${provider} 里的旧私人版本替换项目共享版本。`;
    case 'replace_provider_root_symlink':
      return `将接管 ${provider} 的技能目录，并重新同步本项目管理的技能。`;
    default:
      return `${provider} 里的版本和本项目管理的版本不一致。`;
  }
}

export function resolutionActionLabel(action) {
  return ({
    view_diff: '查看两个版本',
    view_unmanaged: '查看外部位置',
    sync_back_to_canonical: '用外部修改更新本项目',
    canonical_overwrite_mirror: '用本项目内容覆盖外部版本',
    save_as_new_skill: '另存为新技能',
    confirm_delete_drifted_mirror: '删除旧版本',
    sync_back_to_personal: '继续私人使用',
    personal_overwrite_mirror: '用私人技能覆盖外部版本',
    save_as_new_personal_skill: '另存为新私人技能',
    import_to_personal_imported: '导入到私人使用',
    import_to_project: '导入到项目共享',
    takeover_provider_skill: '纳入管理',
    use_project_shared_skill: '使用项目共享版本，删除旧私人版本',
    use_external_provider_skill: '继续私人使用，替换项目共享版本',
    replace_provider_root_symlink: '接管外部技能目录',
    rename_personal: '改名保存',
    rename_personal_type: '更改私人技能类型',
    merge_manually: '手动合并',
    keep_selected: '用选中的版本，删除其他版本',
  }[action] || '处理');
}
