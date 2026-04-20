// @ts-nocheck
import { callAPI } from './api.js';

export async function listSkills() {
  const raw = await callAPI('skills/list', {});
  return Array.isArray(raw?.skills) ? raw.skills : [];
}

export async function previewSkillMatches({ threadId = '', text = '', input = [] } = {}) {
  const payload = {
    threadId: (threadId || '').toString().trim(),
    text: (text || '').toString(),
  };
  if (Array.isArray(input) && input.length > 0) {
    payload.input = input;
  }
  const raw = await callAPI('skills/match/preview', payload);
  return Array.isArray(raw?.matches) ? raw.matches : [];
}
