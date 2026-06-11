const MEMORY_TYPE_INFO = Object.freeze({
  user: { category: 'preference', label: '偏好' },
  feedback: { category: 'preference', label: '偏好' },
  project: { category: 'project', label: '项目' },
  reference: { category: 'project', label: '项目' },
});

function textValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function firstText(...values) {
  for (const value of values) {
    const text = textValue(value);
    if (text) return text;
  }
  return '';
}

function numberOrNull(value) {
  if (value === null || value === undefined || value === '') return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function objectValue(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function normalizeMemorySnapshot(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('memory snapshot response must be an object');
  }
  return {
    overview: objectValue(response.overview),
    entries: [
      ...normalizeMemorySection(response.private, 'private'),
      ...normalizeMemorySection(response.team, 'team'),
    ],
  };
}

function normalizeMemorySection(section, target) {
  const value = objectValue(section);
  if (!Array.isArray(value.entries)) {
    throw new Error(`memory ${target} entries must be an array`);
  }
  return value.entries.map((item, index) => normalizeMemoryEntry(item, index, target));
}

function normalizeMemoryEntry(raw, index, target) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`memory ${target} entry ${index} must be an object`);
  }
  const path = textValue(raw.path);
  if (!path) throw new Error(`memory ${target} entry ${index} path is required`);
  const type = textValue(raw.type).toLowerCase();
  const typeInfo = MEMORY_TYPE_INFO[type];
  if (!typeInfo) throw new Error(`memory ${target} entry ${index} type is unsupported: ${type || '(empty)'}`);
  const name = firstText(raw.name, raw.title, path);
  if (!name) throw new Error(`memory ${target} entry ${index} name is required`);
  return {
    id: `${target}:${path}:${index}`,
    target,
    path,
    type,
    category: typeInfo.category,
    tag: typeInfo.label,
    name,
    title: firstText(raw.title, raw.name),
    description: firstText(raw.description, raw.summary),
    preview: firstText(raw.preview, raw.content, raw.text),
    updatedAt: firstText(raw.updatedAt, raw.updated_at, raw.createdAt, raw.created_at),
    source: textValue(raw.source),
    raw,
  };
}

function normalizeSimilarityGroups(value) {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error('memory health similarGroups must be an array');
  return value.map((item, index) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw new Error(`memory similar group ${index} must be an object`);
    }
    const group = {
      targetA: textValue(item.targetA || item.target_a),
      pathA: textValue(item.pathA || item.path_a),
      nameA: firstText(item.nameA, item.name_a),
      targetB: textValue(item.targetB || item.target_b),
      pathB: textValue(item.pathB || item.path_b),
      nameB: firstText(item.nameB, item.name_b),
      score: numberOrNull(item.score) ?? 0,
    };
    for (const key of ['targetA', 'pathA', 'targetB', 'pathB']) {
      if (!group[key]) throw new Error(`memory similar group ${index} ${key} is required`);
    }
    return group;
  });
}

function memoryHealth(overview, counts) {
  const health = overview?.health;
  if (!health || typeof health !== 'object' || Array.isArray(health)) return null;
  return {
    preferenceCount: numberOrNull(health.preferenceCount) ?? counts.preference,
    projectCount: numberOrNull(health.projectCount) ?? counts.project,
    maxPerCategory: numberOrNull(health.maxPerCategory) ?? 15,
    similarGroups: normalizeSimilarityGroups(health.similarGroups),
  };
}

export { MEMORY_TYPE_INFO, memoryHealth, normalizeMemoryEntry, normalizeMemorySection, normalizeMemorySnapshot, normalizeSimilarityGroups };
