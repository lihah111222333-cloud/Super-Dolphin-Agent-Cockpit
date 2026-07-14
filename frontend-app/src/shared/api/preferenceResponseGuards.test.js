import { describe, expect, it, vi } from 'vitest';
import { getPreference } from './backendApi.js';
import {
  assertPreferenceResponseShape,
  getValidatedPreference,
} from './preferenceResponseGuards.js';

vi.mock('./backendApi.js', () => ({
  getPreference: vi.fn().mockResolvedValue({ malformed: true }),
}));

const validPreferenceCases = [
  ['stallThresholdSec', 30],
  ['contextUsageAlerts.thresholds', [70, 85, 95]],
  ['settings.provider.active', 'codex'],
  ['settings.provider.codex.codexHome', '~/.codex'],
  ['settings.provider.codex.codexInstanceKey', 'default'],
  ['settings.provider.codex.codexModelProvider', 'openai'],
  ['settings.provider.codex.model', 'gpt-5.5'],
  ['settings.provider.codex.effort', 'xhigh'],
  ['settings.provider.codex.personality', 'pragmatic'],
  ['settings.provider.codex.sandbox', { type: 'workspaceWrite', writableRoots: ['/repo'], networkAccess: false }],
  ['settings.provider.codex.summary', 'detailed'],
  ['settings.provider.codex.approvalPolicy', 'on-request'],
  ['settings.showInjectedPromptInChat', true],
  ['settings.activePromptKey', 'main/reviewer'],
];

const malformedPreferenceCases = [
  ['stallThresholdSec', 29],
  ['stallThresholdSec', 30.5],
  ['contextUsageAlerts.thresholds', [70, 70, 95]],
  ['contextUsageAlerts.thresholds', [70, 85]],
  ['settings.provider.active', 'claude'],
  ['settings.provider.codex.codexHome', {}],
  ['settings.provider.codex.codexInstanceKey', 1],
  ['settings.provider.codex.codexModelProvider', false],
  ['settings.provider.codex.model', { value: 'gpt-5.5' }],
  ['settings.provider.codex.effort', 'max'],
  ['settings.provider.codex.personality', 'balanced'],
  ['settings.provider.codex.sandbox', { type: 'workspaceWrite', writableRoots: 'relative', networkAccess: 'yes' }],
  ['settings.provider.codex.sandbox', '{bad json'],
  ['settings.provider.codex.summary', 'verbose'],
  ['settings.provider.codex.approvalPolicy', 'always'],
  ['settings.showInjectedPromptInChat', 'true'],
  ['settings.activePromptKey', 42],
];

describe('assertPreferenceResponseShape', () => {
  it('rejects malformed UI_PREFERENCES_GET response before returning it', async () => {
    getPreference.mockClear();
    await expect(getValidatedPreference({
      key: 'settings.provider.codex.model',
    })).rejects.toThrow('invalid UI preference response for settings.provider.codex.model');
    expect(getPreference).toHaveBeenCalledTimes(1);
    expect(getPreference).toHaveBeenCalledWith({
      key: 'settings.provider.codex.model',
    });
  });

  it.each(validPreferenceCases)('returns the original valid value for %s', (key, value) => {
    expect(assertPreferenceResponseShape(key, value)).toBe(value);
  });

  it.each(validPreferenceCases)('accepts null backend-missing value for %s', (key) => {
    expect(assertPreferenceResponseShape(key, null)).toBeNull();
  });

  it.each(malformedPreferenceCases)('rejects malformed %s response', (key, value) => {
    expect(() => assertPreferenceResponseShape(key, value)).toThrow(`invalid UI preference response for ${key}`);
  });

  it('rejects malformed UI_PREFERENCES_GET responses for all 14 production keys', () => {
    expect(new Set(malformedPreferenceCases.map(([key]) => key)).size).toBe(14);
    for (const [key, value] of malformedPreferenceCases) {
      expect(() => assertPreferenceResponseShape(key, value)).toThrow(`invalid UI preference response for ${key}`);
    }
  });

  it('rejects unknown keys', () => {
    expect(() => assertPreferenceResponseShape('settings.unknown', 'value')).toThrow('unknown UI preference response key: settings.unknown');
  });

  it('allows only the exact tombstone when explicitly requested by a scoped lookup', () => {
    const tombstone = { cleared: true };
    expect(assertPreferenceResponseShape('settings.provider.codex.model', tombstone, { allowTombstone: true })).toBe(tombstone);
    expect(() => assertPreferenceResponseShape('settings.provider.codex.model', tombstone)).toThrow();
    expect(() => assertPreferenceResponseShape('settings.provider.codex.model', { cleared: true, extra: true }, { allowTombstone: true })).toThrow();
  });
});
