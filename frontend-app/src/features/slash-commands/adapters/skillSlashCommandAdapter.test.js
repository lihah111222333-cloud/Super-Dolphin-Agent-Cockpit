import { describe, expect, it } from 'vitest';
import { adaptSkillCommands } from './skillSlashCommandAdapter.js';

const baseSkillInfo = {
  name: 'review',
  display_name: 'Code Review',
  dir: '/repo/.agents/skills/review',
  scope: 'project',
  description: 'Review code',
  summary: 'Review code carefully',
  trust: 'project',
};

describe('skillSlashCommandAdapter', () => {
  it('accepts the dashboard SkillInfo wire shape when omitempty fields are absent', () => {
    const commands = adaptSkillCommands({
      skills: [{
        name: 'review',
        display_name: 'Code Review',
        dir: '/repo/.agents/skills/review',
        scope: 'project',
        description: 'Review code',
        summary: 'Review code carefully',
        trust: 'project',
      }],
    });

    expect(commands).toEqual([
      expect.objectContaining({
        id: 'skill:project::review:/repo/.agents/skills/review',
        kind: 'skill',
        name: 'review',
        label: 'Code Review',
        keywords: [],
        payload: {
          capability: {
            kind: 'skill',
            key: 'skill:project::review:/repo/.agents/skills/review',
            name: 'review',
            label: 'Code Review',
            ref: {
              name: 'review',
              scope: 'project',
              personalType: '',
              path: '/repo/.agents/skills/review',
            },
          },
        },
      }),
    ]);
  });

  it('validates every optional SkillInfo producer field before adaptation', () => {
    expect(() => adaptSkillCommands({
      skills: [{
        ...baseSkillInfo,
        trigger_words: ['@review'],
        force_words: ['[skill:review]'],
        allowed_tools: ['file'],
        disable_model_invocation: true,
        content_hash: 'a'.repeat(64),
        replaces_native: { codex: ['review'] },
      }],
    })).not.toThrow();
  });

  it.each([
    ['name', { name: '' }],
    ['display_name', { display_name: 1 }],
    ['dir', { dir: '' }],
    ['scope', { scope: 'global' }],
    ['personal_type', { personal_type: 1 }],
    ['description', { description: undefined }],
    ['summary', { summary: undefined }],
    ['trigger_words', { trigger_words: [1] }],
    ['force_words', { force_words: 'force' }],
    ['trust', { trust: 'unknown' }],
    ['allowed_tools', { allowed_tools: [1] }],
    ['disable_model_invocation', { disable_model_invocation: 'true' }],
    ['content_hash', { content_hash: 123 }],
    ['replaces_native', { replaces_native: [] }],
  ])('fails fast when %s violates its wire contract', (_field, override) => {
    expect(() => adaptSkillCommands({
      skills: [{ ...baseSkillInfo, ...override }],
    })).toThrow(TypeError);
  });

  it('rejects invalid scope ownership and unknown producer fields', () => {
    expect(() => adaptSkillCommands({
      skills: [{ ...baseSkillInfo, personal_type: 'user' }],
    })).toThrow(/project scope must not set personal_type/);
    expect(() => adaptSkillCommands({
      skills: [{
        ...baseSkillInfo,
        scope: 'personal',
        personal_type: 'hub',
      }],
    })).toThrow(/personal scope requires a supported personal_type/);
    expect(() => adaptSkillCommands({
      skills: [{ ...baseSkillInfo, skill_file: '/unexpected/SKILL.md' }],
    })).toThrow(/unknown fields: skill_file/);
  });
});
