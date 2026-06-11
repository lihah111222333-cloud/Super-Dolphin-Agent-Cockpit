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

function adaptSharedFile(raw, index = 0, fallback = {}) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`shared file item ${index} must be an object`);
  }
  const path = firstText(raw.path, fallback.path);
  if (!path) throw new Error(`shared file item ${index} path is required`);
  return {
    id: `${path}:${index}`,
    path,
    content: firstText(raw.content, fallback.content),
    updatedBy: firstText(raw.updated_by, raw.updatedBy, fallback.updatedBy, fallback.updated_by),
    updatedAt: firstText(raw.updated_at, raw.updatedAt, fallback.updatedAt, fallback.updated_at),
    createdAt: firstText(raw.created_at, raw.createdAt, fallback.createdAt, fallback.created_at),
  };
}

function adaptFinalOutputRefs(value) {
  if (value === undefined) return [];
  if (!Array.isArray(value)) throw new Error('shared files dashboard finalOutputRefs must be an array');
  return value.map((item, index) => {
    if (typeof item === 'string') {
      const path = textValue(item);
      if (!path) throw new Error(`final output ref ${index} path is required`);
      return { path, runKey: '', dagKey: '', sourceNodeKey: '' };
    }
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw new Error(`final output ref ${index} must be an object`);
    }
    const path = firstText(item.path, item.sharedfile?.path, item.sharedFile?.path, item.shared_file?.path);
    if (!path) throw new Error(`final output ref ${index} path is required`);
    return {
      path,
      runKey: firstText(item.runKey, item.run_key),
      dagKey: firstText(item.dagKey, item.dag_key),
      sourceNodeKey: firstText(item.sourceNodeKey, item.source_node_key),
    };
  });
}

function adaptSharedFileRetention(value) {
  if (value === undefined) {
    return { items: [], protectedCount: 0, cleanupCandidateCount: 0 };
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('shared files dashboard sharedFileRetention must be an object');
  }
  if (!Array.isArray(value.items)) {
    throw new Error('shared files dashboard sharedFileRetention.items must be an array');
  }
  return {
    items: value.items.map((item, index) => {
      if (!item || typeof item !== 'object' || Array.isArray(item)) {
        throw new Error(`shared file retention item ${index} must be an object`);
      }
      const path = textValue(item.path);
      if (!path) throw new Error(`shared file retention item ${index} path is required`);
      return {
        path,
        protected: Boolean(item.protected),
        cleanupCandidate: Boolean(item.cleanupCandidate),
        reason: textValue(item.reason),
        finalOutput: item.finalOutput || item.final_output || null,
      };
    }),
    protectedCount: Number(value.protectedCount) || 0,
    cleanupCandidateCount: Number(value.cleanupCandidateCount) || 0,
  };
}

function adaptSharedFilesDashboard(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('shared files dashboard response must be an object');
  }
  const rawFiles = Array.isArray(response.files) ? response.files : response.memory;
  if (!Array.isArray(rawFiles)) {
    throw new Error('shared files dashboard response files must be an array');
  }
  return {
    files: rawFiles.map((item, index) => adaptSharedFile(item, index)),
    finalOutputRefs: adaptFinalOutputRefs(response.finalOutputRefs),
    retention: adaptSharedFileRetention(response.sharedFileRetention),
  };
}

function adaptSharedFileDetail(response, fallbackFile = {}) {
  return adaptSharedFile(response || {}, 0, fallbackFile);
}

export { adaptFinalOutputRefs, adaptSharedFile, adaptSharedFileDetail, adaptSharedFileRetention, adaptSharedFilesDashboard };
