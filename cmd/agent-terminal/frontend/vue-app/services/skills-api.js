import { callAPI } from './api.js';

function trimString(value) {
  return (value || '').toString().trim();
}

function withOptionalCwd(payload = {}, cwd = '') {
  const resolvedCwd = trimString(cwd);
  return resolvedCwd ? { ...payload, cwd: resolvedCwd } : payload;
}

function withOptionalScope(payload = {}, scope = '') {
  const resolvedScope = trimString(scope);
  return resolvedScope ? { ...payload, scope: resolvedScope } : payload;
}

function withOptionalPersonalType(payload = {}, personalType = '') {
  const resolvedType = trimString(personalType);
  return resolvedType ? { ...payload, personal_type: resolvedType } : payload;
}

export async function writeSkill(cwd = '', path = '', content = '', scope = '', personalType = '') {
  return callAPI('skills/local/write', withOptionalPersonalType(withOptionalScope(withOptionalCwd({
    path: trimString(path),
    content: (content || '').toString(),
  }, cwd), scope), personalType));
}

export async function suggestSkillSummary(cwd = '', params = {}) {
  const payload = {
    name: trimString(params?.name),
    description: trimString(params?.description),
    content: (params?.content || '').toString(),
    scenario_words: Array.isArray(params?.scenarioWords) ? params.scenarioWords : [],
    scope: trimString(params?.scope),
  };
  const provider = trimString(params?.provider || params?.modelProvider);
  const model = trimString(params?.model);
  const modelProvider = trimString(params?.model_provider || params?.codexModelProvider);
  if (provider) payload.provider = provider;
  if (model) payload.model = model;
  if (modelProvider) payload.model_provider = modelProvider;
  const raw = await callAPI('skills/summary/suggest', withOptionalCwd(payload, cwd));
  return trimString(raw?.description);
}

export async function importSkills(cwd = '', paths = [], scope = '', personalType = '') {
  return callAPI('skills/local/importDir', withOptionalPersonalType(withOptionalScope(withOptionalCwd({
    paths: Array.isArray(paths) ? paths : [],
  }, cwd), scope), personalType));
}

export async function listSkillResolutions(cwd = '') {
  const raw = await callAPI('skills/resolution_list', withOptionalCwd({}, cwd));
  if (Array.isArray(raw?.items)) return raw.items;
  return Array.isArray(raw?.conflicts) ? raw.conflicts : [];
}

function resolutionPayload(params = {}) {
  const payload = {
    conflict_id: trimString(params?.conflictId ?? params?.conflict_id),
    action: trimString(params?.action),
  };
  const optionalFields = [
    ['name', params?.name],
    ['scope', params?.scope],
    ['personal_type', params?.personalType ?? params?.personal_type],
    ['provider', params?.provider],
    ['source_provider', params?.sourceProvider ?? params?.source_provider],
    ['source_path_id', params?.sourcePathId ?? params?.source_path_id],
    ['new_name', params?.newName ?? params?.new_name],
    ['keep_source_id', params?.keepSourceID ?? params?.keep_source_id],
    ['merge_content_hash', params?.mergeContentHash ?? params?.merge_content_hash],
    ['disable_policy_target', params?.disablePolicyTarget ?? params?.disable_policy_target],
  ];
  optionalFields.forEach(([key, value]) => {
    const text = trimString(value);
    if (text) payload[key] = text;
  });
  return payload;
}

export async function previewSkillResolution(params = {}) {
  return callAPI('skills/resolution_preview', withOptionalCwd(resolutionPayload(params), params?.cwd || ''));
}

export async function applySkillResolution(params = {}) {
  return callAPI('skills/resolution_apply', withOptionalCwd({
    ...resolutionPayload(params),
    preview_id: trimString(params?.previewId ?? params?.preview_id),
    preview_hash: trimString(params?.previewHash ?? params?.preview_hash),
  }, params?.cwd || ''));
}
