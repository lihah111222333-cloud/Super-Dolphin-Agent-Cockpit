import { parseSharedFileDetailResponse, parseSharedFilesDashboardResponse } from '../shared/api/backendSchemas.js';

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

function adaptSharedFilesDashboard(response) {
  return parseSharedFilesDashboardResponse(response);
}

function detailResponseFile(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('shared file detail response must be an object');
  }
  if (Object.prototype.hasOwnProperty.call(response, 'file')) {
    if (!response.file || typeof response.file !== 'object' || Array.isArray(response.file)) {
      throw new Error('shared file detail file must be an object');
    }
    return response.file;
  }
  return response;
}

function adaptSharedFileDetail(response, fallbackFile = {}) {
  const rawDetail = detailResponseFile(response);
  const detail = parseSharedFileDetailResponse(rawDetail);
  const adapted = adaptSharedFile(detail, 0);
  return {
    ...adapted,
    updatedBy: firstText(adapted.updatedBy, fallbackFile.updatedBy, fallbackFile.updated_by),
    updatedAt: firstText(adapted.updatedAt, fallbackFile.updatedAt, fallbackFile.updated_at),
    createdAt: firstText(adapted.createdAt, fallbackFile.createdAt, fallbackFile.created_at),
  };
}

export { adaptSharedFile, adaptSharedFileDetail, adaptSharedFilesDashboard };
