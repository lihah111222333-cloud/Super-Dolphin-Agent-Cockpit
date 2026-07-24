import { describe, expect, it } from 'vitest';
import { normalizeSkillsResponse } from './skillsDashboardModel.js';

const validSkillInfo = {
  name: 'backend',
  display_name: '后端',
  dir: '/repo/.agents/skills/backend',
  scope: 'project',
  description: 'Go backend guidance',
  summary: 'Backend summary',
  trigger_words: ['go'],
  trust: 'project',
};

describe('skillsDashboardModel SkillInfo contract', () => {
  it.each([
    ['name', { name: { secret: 'do-not-stringify' } }],
    ['dir', { dir: 42 }],
    ['description', { description: ['invalid'] }],
    ['summary', { summary: undefined }],
  ])('fails fast instead of coercing an invalid %s', (_field, override) => {
    expect(() => normalizeSkillsResponse({
      skills: [{ ...validSkillInfo, ...override }],
    })).toThrow(TypeError);
  });

  it('rejects producer fields outside contract.SkillInfo', () => {
    expect(() => normalizeSkillsResponse({
      skills: [{ ...validSkillInfo, skill_file: '/unexpected/SKILL.md' }],
    })).toThrow(/unknown fields: skill_file/);
  });
});
