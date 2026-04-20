import { callAPI } from './api.js';

export async function listSkills(cwd = '') {
  const resolvedCwd = (cwd || '').toString().trim();
  const raw = await callAPI('skills/list', resolvedCwd ? { cwd: resolvedCwd } : {});
  return Array.isArray(raw?.skills) ? raw.skills : [];
}

export async function previewSkillMatches(params = {}) {
  const threadId = (params?.threadId || '').toString();
  const text = (params?.text || '').toString();
  const cwd = (params?.cwd || '').toString().trim();
  return callAPI('skills/match/preview', cwd ? { threadId, text, cwd } : { threadId, text });
}
