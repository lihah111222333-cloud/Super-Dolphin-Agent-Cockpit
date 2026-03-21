// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick, reactive, ref } from '../lib/vue.esm-browser.prod.js';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logWarn: vi.fn(),
}));

import { useSkillPreview } from './composables/useSkillPreview.js';

const flushPromises = async () => {
  await Promise.resolve();
  await Promise.resolve();
};

function createSkillPreview({ text = '', threadId = '', revision = 0 } = {}) {
  const composer = {
    state: reactive({
      text,
      attachments: [],
    }),
  };
  const selectedThreadId = ref(threadId);
  const skillRevision = ref(revision);
  const vm = useSkillPreview({ composer, selectedThreadId, skillRevision });
  return {
    composer,
    selectedThreadId,
    skillRevision,
    ...vm,
  };
}

beforeEach(() => {
  vi.useFakeTimers();
  apiMock.callAPI.mockReset().mockResolvedValue({ matches: [] });
  globalThis.window = {
    setTimeout: (...args) => setTimeout(...args),
    clearTimeout: (id) => clearTimeout(id),
  };
});

afterEach(() => {
  vi.useRealTimers();
  delete globalThis.window;
});

describe('useSkillPreview', () => {
  it('returns false when no skills are selected or matched', () => {
    const vm = createSkillPreview();
    expect(vm.isComposerSkillSelected('deploy-skill')).toBe(false);
    expect(vm.composerEffectiveSelectedSkillNames.value).toEqual([]);
  });

  it('adds and removes a manually selected skill', () => {
    const vm = createSkillPreview();

    vm.toggleComposerSelectedSkill('deploy-skill');
    expect(vm.isComposerSkillSelected('deploy-skill')).toBe(true);
    expect(vm.composerEffectiveSelectedSkillNames.value).toEqual(['deploy-skill']);

    vm.toggleComposerSelectedSkill('deploy-skill');
    expect(vm.isComposerSkillSelected('deploy-skill')).toBe(false);
    expect(vm.composerEffectiveSelectedSkillNames.value).toEqual([]);
  });

  it('clears all manually selected skills', () => {
    const vm = createSkillPreview();

    vm.toggleComposerSelectedSkill('alpha');
    vm.toggleComposerSelectedSkill('beta');
    expect(vm.composerEffectiveSelectedSkillNames.value).toEqual(['alpha', 'beta']);

    vm.clearComposerSelectedSkills();
    expect(vm.composerEffectiveSelectedSkillNames.value).toEqual([]);
  });

  it('merges manual selection with force-matched skills when sending', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      matches: [
        { name: 'ForceSkill', matched_by: 'force', matched_terms: ['@force'] },
        { name: 'ManualSkill', matched_by: 'trigger', matched_terms: ['manual'] },
      ],
    });

    const vm = createSkillPreview();
    vm.toggleComposerSelectedSkill('ManualSkill');

    const result = await vm.resolveComposerSkillSelectionForSend('thread-1', 'please @force and manual');

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/match/preview', {
      threadId: 'thread-1',
      text: 'please @force and manual',
    });
    expect(result).toEqual({
      selectedSkills: ['ManualSkill', 'ForceSkill'],
      manualSkillSelection: true,
    });
    expect(vm.composerSkillMatches.value).toEqual([
      { name: 'ForceSkill', matchedBy: 'force', matchedTerms: ['@force'] },
      { name: 'ManualSkill', matchedBy: 'trigger', matchedTerms: ['manual'] },
    ]);
  });

  it('normalizes preview matches for send resolution with case-insensitive dedupe', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      matches: [
        { name: 'ForceSkill', matched_by: 'explicit', matched_terms: ['@force', '@FORCE'] },
        { name: 'forceskill', matchedBy: 'force', matchedTerms: ['@ignored'] },
        { skill: 'TriggerSkill', matched_by: 'trigger', matched_terms: ['term', 'TERM'] },
      ],
    });

    const vm = createSkillPreview();
    const result = await vm.resolveComposerSkillSelectionForSend('thread-1', 'please @force and term');

    expect(result).toEqual({
      selectedSkills: ['ForceSkill'],
      manualSkillSelection: false,
    });
    expect(vm.composerSkillMatches.value).toEqual([
      { name: 'ForceSkill', matchedBy: 'force', matchedTerms: ['@force'] },
      { name: 'TriggerSkill', matchedBy: 'trigger', matchedTerms: ['term'] },
    ]);
  });

  it('returns empty normalized matches when preview payload is missing an array', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ matches: null });

    const vm = createSkillPreview();
    vm.toggleComposerSelectedSkill('ManualSkill');

    const result = await vm.resolveComposerSkillSelectionForSend('thread-1', 'manual only');

    expect(result).toEqual({
      selectedSkills: ['ManualSkill'],
      manualSkillSelection: true,
    });
    expect(vm.composerSkillMatches.value).toEqual([]);
  });

  it('normalizes match class and reason labels', () => {
    const vm = createSkillPreview();

    expect(vm.composerSkillMatchClass({ matchedBy: 'FORCE' })).toBe('force');
    expect(vm.composerSkillMatchClass({ matchedBy: 'explicit' })).toBe('explicit');
    expect(vm.composerSkillMatchClass({ matchedBy: 'something-else' })).toBe('trigger');

    expect(vm.composerSkillMatchReason({ matchedBy: 'explicit', matchedTerms: [' Alpha ', '', 'Beta '] })).toBe('显式提及: Alpha / Beta');
    expect(vm.composerSkillMatchReason({ matchedBy: 'force', matchedTerms: [] })).toBe('强制词');
  });

  it('keeps effective selected skill names ordered and de-duplicated', async () => {
    const vm = createSkillPreview();

    vm.toggleComposerSelectedSkill('ManualSkill');
    vm.toggleComposerSelectedSkill('ForceSkill');
    vm.composerSkillMatches.value = [
      { name: 'ForceSkill', matchedBy: 'force', matchedTerms: ['@force'] },
      { name: 'OtherForce', matchedBy: 'force', matchedTerms: ['@other'] },
    ];
    await nextTick();

    expect(vm.composerEffectiveSelectedSkillNames.value).toEqual([
      'ManualSkill',
      'ForceSkill',
      'OtherForce',
    ]);
  });

  it('re-runs preview when skillRevision changes', async () => {
    const vm = createSkillPreview({ text: 'need preview', threadId: 'thread-1', revision: 1 });

    await vi.advanceTimersByTimeAsync(241);
    await flushPromises();
    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);

    apiMock.callAPI.mockReset().mockResolvedValueOnce({
      matches: [{ name: 'RefreshedSkill', matched_by: 'trigger', matched_terms: ['preview'] }],
    });

    vm.skillRevision.value = 2;
    await nextTick();
    await vi.advanceTimersByTimeAsync(241);
    await flushPromises();

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/match/preview', {
      threadId: 'thread-1',
      text: 'need preview',
    });
    expect(vm.composerSkillMatches.value).toEqual([
      { name: 'RefreshedSkill', matchedBy: 'trigger', matchedTerms: ['preview'] },
    ]);
  });
});
