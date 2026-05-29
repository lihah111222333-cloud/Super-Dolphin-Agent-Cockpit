import { describe, expect, it } from 'vitest';
import { normalizeCodexSandboxPreference } from './stores/codex-sandbox-defaults.js';

describe('normalizeCodexSandboxPreference', () => {
  it('omits sandbox when no user preference is persisted', () => {
    expect(normalizeCodexSandboxPreference(undefined)).toBeNull();
    expect(normalizeCodexSandboxPreference(null)).toBeNull();
    expect(normalizeCodexSandboxPreference('')).toBeNull();
    expect(normalizeCodexSandboxPreference('undefined')).toBeNull();
    expect(normalizeCodexSandboxPreference({})).toBeNull();
    expect(normalizeCodexSandboxPreference(JSON.stringify({}))).toBeNull();
  });

  it('converts persisted workspace-write JSON to the canonical snake_case payload', () => {
    const sandbox = { type: 'workspaceWrite', writableRoots: ['/repo'], networkAccess: true };
    expect(normalizeCodexSandboxPreference(JSON.stringify(sandbox))).toEqual({
      mode: 'workspace-write',
      writable_roots: ['/repo'],
      network_access: true,
    });
  });

  it('preserves restricted read-only roots in the canonical snake_case payload', () => {
    expect(normalizeCodexSandboxPreference({
      type: 'readOnly',
      access: { type: 'restricted', readableRoots: ['/repo'], includePlatformDefaults: true },
    })).toEqual({
      mode: 'read-only',
      access: {
        type: 'restricted',
        readable_roots: ['/repo'],
        include_platform_defaults: true,
      },
    });
  });

  it('preserves full read-only and danger-full-access modes as canonical mode payloads', () => {
    expect(normalizeCodexSandboxPreference({ type: 'readOnly' })).toEqual({ mode: 'read-only' });
    expect(normalizeCodexSandboxPreference('dangerFullAccess')).toEqual({ mode: 'danger-full-access' });
  });

  it('fails fast when non-empty sandbox fields are missing a mode', () => {
    expect(() => normalizeCodexSandboxPreference({ writableRoots: ['/repo'] })).toThrow('invalid codex sandbox preference');
  });
});
