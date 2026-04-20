import { callAPI } from './api.js';

export async function listSkills() {
  const raw = await callAPI('skills/list', {});
  return Array.isArray(raw?.skills) ? raw.skills : [];
}

export async function previewSkillMatches(params = {}) {
  const threadId = (params?.threadId || '').toString();
  const text = (params?.text || '').toString();
  return callAPI('skills/match/preview', { threadId, text });
}
