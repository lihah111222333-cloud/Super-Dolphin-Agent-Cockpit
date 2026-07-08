import { describe, expect, it } from 'vitest';
import {
  codexLaunchConfigFromPreferences,
  isPreferenceAbsent,
  isPreferenceTombstone,
  knownProviderName,
  normalizeCodexIdentityValue,
  normalizeProviderConfigValue,
  normalizeProviderRuntimeConfig,
  providerDisplayDefaultConfig,
  requireActiveProviderPreference,
  requireProviderPreferenceValue } from './providerRuntimeConfig.js';

describe('providerRuntimeConfig', () => {
  it('normalizes provider preference values from primitive and object-shaped payloads', () => {
    expect(normalizeProviderConfigValue(' gpt-5.5 ')).toBe('gpt-5.5');
    expect(normalizeProviderConfigValue({ value: ' codex ', label: 'Codex' })).toBe('codex');
    expect(normalizeProviderConfigValue({ model: ' gpt-5.5 ' })).toBe('gpt-5.5');
    expect(normalizeProviderConfigValue({ label: 'ignored' })).toBe('');
    expect(normalizeCodexIdentityValue(true)).toBe('');
  });

  it('keeps runtime provider fail-fast at the active preference boundary', () => {
    expect(requireActiveProviderPreference('codex', 'startThread')).toBe('codex');
    expect(knownProviderName('bad-provider')).toBe('');
    expect(() => requireActiveProviderPreference('', 'startThread')).toThrow('settings.provider.active preference is required');
    expect(() => requireActiveProviderPreference('claude', 'startThread')).toThrow('current desktop UI supports codex only');
  });

  it('normalizes provider runtime config and display defaults', () => {
    expect(providerDisplayDefaultConfig('codex')).toEqual({ model: 'gpt-5.5', effort: 'xhigh' });
    expect(providerDisplayDefaultConfig('missing')).toEqual({ model: 'gpt-5.5', effort: 'xhigh' });
    expect(normalizeProviderRuntimeConfig({
      model: { value: ' gpt-5.5 ' },
      effort: { value: ' xhigh ' },
      codexModelProvider: { value: ' openai ' },
    }, 'codex')).toEqual({
      provider: 'codex',
      model: 'gpt-5.5',
      effort: 'xhigh',
      codexModelProvider: 'openai',
    });
  });

  it('builds complete Codex launch identity config and rejects partial identity', () => {
    expect(codexLaunchConfigFromPreferences({
      codexHome: '',
      codexInstanceKey: '',
      codexModelProvider: '',
    })).toBeNull();
    expect(codexLaunchConfigFromPreferences({
      codexHome: '~/.codex',
      codexInstanceKey: 'default',
      codexModelProvider: 'openai',
    })).toEqual({
      codexHome: '~/.codex',
      codexInstanceKey: 'default',
      codexModelProvider: 'openai',
    });
    expect(() => codexLaunchConfigFromPreferences({
      codexHome: '~/.codex',
      codexInstanceKey: '',
      codexModelProvider: 'openai',
    })).toThrow('codexHome and codexInstanceKey');
  });

  it('allows Codex launch identity to omit model provider for config.toml resolution', () => {
    expect(codexLaunchConfigFromPreferences({
      codexHome: '~/.codex',
      codexInstanceKey: 'default',
      codexModelProvider: '',
    })).toEqual({
      codexHome: '~/.codex',
      codexInstanceKey: 'default',
    });
    expect(() => codexLaunchConfigFromPreferences({
      codexHome: '~/.codex',
      codexInstanceKey: '',
      codexModelProvider: '',
    })).toThrow('codexHome and codexInstanceKey');
    expect(() => codexLaunchConfigFromPreferences({
      codexHome: '',
      codexInstanceKey: 'default',
      codexModelProvider: '',
    })).toThrow('codexHome and codexInstanceKey');
  });

  it('classifies preference tombstones and required values', () => {
    expect(isPreferenceTombstone({ cleared: true })).toBe(true);
    expect(isPreferenceTombstone({ cleared: false })).toBe(false);
    expect(isPreferenceAbsent(null)).toBe(true);
    expect(isPreferenceAbsent('   ')).toBe(true);
    expect(requireProviderPreferenceValue({ value: 'sonnet' }, 'model', 'provider.config')).toBe('sonnet');
    expect(() => requireProviderPreferenceValue('', 'model', 'provider.config')).toThrow('model preference is required');
  });
});
