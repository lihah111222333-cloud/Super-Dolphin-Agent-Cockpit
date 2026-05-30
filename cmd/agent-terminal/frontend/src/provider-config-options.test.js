import { describe, expect, it } from 'vitest';

import { CODEX_IDENTITY_DEFAULTS, normalizeProviderConfigValue } from './provider-config-options.js';

describe('normalizeProviderConfigValue', () => {
  it('extracts scalar values from option-shaped objects', () => {
    expect(normalizeProviderConfigValue({ value: 'sonnet', label: 'Sonnet 4.7' })).toBe('sonnet');
    expect(normalizeProviderConfigValue({ model: 'opus[1m]' })).toBe('opus[1m]');
    expect(normalizeProviderConfigValue({ id: 'gpt-5.5' })).toBe('gpt-5.5');
  });

  it('drops accidental object/string artifacts', () => {
    expect(normalizeProviderConfigValue('[object Object]')).toBe('');
    expect(normalizeProviderConfigValue(' undefined ')).toBe('');
    expect(normalizeProviderConfigValue('null')).toBe('');
    expect(normalizeProviderConfigValue({ label: 'Sonnet 4.7' })).toBe('');
  });
});

describe('provider config defaults', () => {
  it('leaves Codex home empty so the backend can choose the app-managed home', () => {
    expect(CODEX_IDENTITY_DEFAULTS).toMatchObject({
      codexHome: '',
      codexInstanceKey: 'default',
      codexModelProvider: 'super-dolphin-relay',
    });
  });
});
