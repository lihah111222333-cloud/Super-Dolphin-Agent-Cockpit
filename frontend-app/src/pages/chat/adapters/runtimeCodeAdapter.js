function normalizeRuntimeProjectPath(path) {
  const value = (path || '').toString().trim();
  if (!value) return '';
  if (value !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(value)) {
    return value.replace(/[\\/]+$/, '');
  }
  return value;
}

function runtimeCodeScopePayload(filePath, projectPath, projects, position = null) {
  const payload = { filePath };
  const line = Number(position?.line);
  const column = Number(position?.column);
  if (Number.isFinite(line) && line > 0) payload.line = Math.floor(line);
  if (position && Number.isFinite(column) && column >= 0) payload.column = Math.floor(column);
  const project = normalizeRuntimeProjectPath(projectPath);
  if (project) payload.project = project;
  const projectList = [];
  for (const rawProject of Array.isArray(projects) ? projects : []) {
    const normalizedProject = normalizeRuntimeProjectPath(rawProject);
    if (normalizedProject) projectList.push(normalizedProject);
  }
  if (projectList.length > 0) payload.projects = projectList;
  return payload;
}

function codeActionError(error, fallback) {
  return (error?.message || fallback).toString();
}

function normalizeCodeLocateOptions(result) {
  const options = [];
  const seen = new Set();
  const add = (value) => {
    const text = (value || '').toString().trim();
    if (!text || seen.has(text)) return;
    seen.add(text);
    options.push(text);
  };
  if (Array.isArray(result?.paths)) result.paths.forEach(add);
  if (Array.isArray(result?.matches)) {
    result.matches.forEach((match) => {
      if (typeof match === 'string') {
        add(match);
        return;
      }
      add(match?.path || match?.filePath || match?.relative);
    });
  }
  return options;
}

function emptyPathChoiceState() {
  return {
    open: false,
    file: null,
    options: [],
    truncated: false,
  };
}

function fileRefPosition(payload = {}) {
  const line = Number(payload.line ?? payload.lineStart);
  const column = Number(payload.column);
  return {
    line: Number.isFinite(line) && line > 0 ? Math.floor(line) : 1,
    column: Number.isFinite(column) && column >= 0 ? Math.floor(column) : 0,
  };
}

export {
  codeActionError,
  emptyPathChoiceState,
  fileRefPosition,
  normalizeCodeLocateOptions,
  runtimeCodeScopePayload,
};
