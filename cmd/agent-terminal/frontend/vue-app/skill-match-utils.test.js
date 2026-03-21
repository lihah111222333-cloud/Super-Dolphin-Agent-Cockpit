// @ts-nocheck
import { describe, expect, it } from 'vitest';
import {
  normalizeSkillPreviewMatches,
  skillNameKey,
  collectForceMatchedSkillNames,
  mergeSkillNameLists,
  normalizeComposerSkillMatchType,
  composerSkillMatchClass,
  composerSkillMatchReason,
  buildSkillPreviewSignature,
} from './utils/skill-match-utils.js';

describe('skill-match-utils', () => {
  it('normalizes preview matches with case-insensitive dedupe and force promotion', () => {
    expect(normalizeSkillPreviewMatches([
      { name: 'ForceSkill', matched_by: 'explicit', matched_terms: ['@force', '@FORCE', ''] },
      { name: 'forceskill', matchedBy: 'force', matchedTerms: ['@ignored'] },
      { skill: 'TriggerSkill', matched_by: 'trigger', matched_terms: ['term', 'TERM'] },
    ])).toEqual([
      { name: 'ForceSkill', matchedBy: 'force', matchedTerms: ['@force'] },
      { name: 'TriggerSkill', matchedBy: 'trigger', matchedTerms: ['term'] },
    ]);
  });

  it('collects force-matched names and merges lists without duplicates', () => {
    const matches = [
      { name: 'ForceSkill', matchedBy: 'force' },
      { name: 'ExplicitOnly', matchedBy: 'explicit' },
      { name: 'OtherForce', matchedBy: 'force' },
      { name: 'forceSkill', matchedBy: 'force' },
    ];

    expect(skillNameKey(' ForceSkill ')).toBe('forceskill');
    expect(collectForceMatchedSkillNames(matches)).toEqual(['ForceSkill', 'OtherForce']);
    expect(mergeSkillNameLists(['ManualSkill', 'ForceSkill'], ['forceskill', 'OtherForce'], null)).toEqual([
      'ManualSkill',
      'ForceSkill',
      'OtherForce',
    ]);
  });

  it('normalizes type, class and reason labels', () => {
    expect(normalizeComposerSkillMatchType(' FORCE ')).toBe('force');
    expect(normalizeComposerSkillMatchType('explicit')).toBe('explicit');
    expect(normalizeComposerSkillMatchType('anything')).toBe('trigger');
    expect(composerSkillMatchClass({ matchedBy: 'EXPLICIT' })).toBe('explicit');
    expect(composerSkillMatchReason({ matchedBy: 'explicit', matchedTerms: [' Alpha ', '', 'Beta '] })).toBe('显式提及: Alpha / Beta');
    expect(composerSkillMatchReason({ matchedBy: 'force', matchedTerms: null })).toBe('强制词');
  });

  it('builds stable preview signatures', () => {
    expect(buildSkillPreviewSignature([
      { name: 'ForceSkill', matchedBy: 'force', matchedTerms: ['@force'] },
      { name: 'TriggerSkill', matchedBy: 'trigger', matchedTerms: ['Term', 'Second'] },
    ])).toBe('forceskill:force:@force;triggerskill:trigger:term|second');
    expect(buildSkillPreviewSignature([])).toBe('');
  });
});
