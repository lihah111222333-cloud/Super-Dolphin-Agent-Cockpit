import { describe, expect, it } from 'vitest';
import { adaptSkillCommands } from './skillSlashCommandAdapter.js';

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
});
