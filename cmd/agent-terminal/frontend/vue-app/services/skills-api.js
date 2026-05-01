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

export async function listSkills(cwd = '') {
  const raw = await callAPI('skills/list', withOptionalCwd({}, cwd));
  return Array.isArray(raw?.skills) ? raw.skills : [];
}

export async function previewSkillMatches(params = {}) {
  const threadId = (params?.threadId || '').toString();
  const text = (params?.text || '').toString();
  return callAPI('skills/match/preview', withOptionalCwd({ threadId, text }, params?.cwd || ''));
}

export async function writeSkill(cwd = '', path = '', content = '', scope = '') {
  return callAPI('skills/local/write', withOptionalScope(withOptionalCwd({
    path: trimString(path),
    content: (content || '').toString(),
  }, cwd), scope));
}

export async function importSkills(cwd = '', paths = [], scope = '') {
  return callAPI('skills/local/importDir', withOptionalScope(withOptionalCwd({
    paths: Array.isArray(paths) ? paths : [],
  }, cwd), scope));
}

export async function listPendingCandidates(cwd = '', limit = 20, offset = 0) {
  const raw = await callAPI('skills/candidate/list/pending', withOptionalCwd({ limit, offset }, cwd));
  return Array.isArray(raw?.candidates) ? raw.candidates : [];
}

export async function getCandidate(candidateId) {
  return callAPI('skills/candidate/get', { candidate_id: candidateId });
}

export async function approveCandidate(candidateId, approvedBy = '', reason = '', cwd = '') {
  return callAPI('skills/candidate/approve', withOptionalCwd({
    candidate_id: candidateId,
    approved_by: trimString(approvedBy),
    reason: trimString(reason),
  }, cwd));
}

export async function rejectCandidate(candidateId, reason = '', cwd = '') {
  return callAPI('skills/candidate/reject', withOptionalCwd({
    candidate_id: candidateId,
    reason: trimString(reason),
  }, cwd));
}
