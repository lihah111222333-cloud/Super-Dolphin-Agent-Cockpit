import { describe, expect, it } from 'vitest';
import {
  appendCurrentModelOption,
  canonicalizeModelValue,
  effortOptionsForProvider,
  isClaudeOpusFamilyModel,
  MODEL_DEFAULTS_BY_PROVIDER,
  modelOptionFor,
  normalizeProviderKey,
} from './providerCatalog.js';

describe('providerCatalog', () => {
  it('owns provider defaults and preserves the codex fallback for unknown providers', () => {
    expect(MODEL_DEFAULTS_BY_PROVIDER.codex).toEqual({ model: 'gpt-5.5', effort: 'xhigh' });
    expect(MODEL_DEFAULTS_BY_PROVIDER.claude).toEqual({ model: 'sonnet', effort: 'high' });
    expect(normalizeProviderKey('unsupported')).toBe('codex');
  });

  it('canonicalizes only known Claude aliases and retains unknown models', () => {
    expect(canonicalizeModelValue('claude', 'claude-opus-4-7')).toBe('opus');
    expect(canonicalizeModelValue('claude', 'vendor/custom-model')).toBe('vendor/custom-model');
    expect(modelOptionFor('claude', 'vendor/custom-model')).toMatchObject({ value: 'vendor/custom-model', label: 'vendor/custom-model' });
    expect(appendCurrentModelOption('claude', 'vendor/custom-model')).toContainEqual({ value: 'vendor/custom-model', label: 'vendor/custom-model', short: 'vendor/custom-model' });
  });

  it('keeps Claude max capability limited to the Opus family while retaining per-surface labels', () => {
    expect(isClaudeOpusFamilyModel('best')).toBe(true);
    expect(isClaudeOpusFamilyModel('claude-opus-4-6')).toBe(true);
    expect(isClaudeOpusFamilyModel('sonnet')).toBe(false);
    expect(effortOptionsForProvider('claude', { max: 'max（仅 Opus）' })[0]).toEqual({ value: 'max', label: 'max（仅 Opus）' });
  });
});
