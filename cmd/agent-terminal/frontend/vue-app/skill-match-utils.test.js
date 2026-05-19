// @ts-nocheck
import { describe, expect, it } from 'vitest';
import { skillNameKey } from './utils/skill-match-utils.js';

describe('skill-match-utils', () => {
  it('normalizes skill names for compatibility payload de-duplication', () => {
    expect(skillNameKey(' ForceSkill ')).toBe('forceskill');
    expect(skillNameKey('')).toBe('');
  });
});
