import { describe, expect, it } from 'vitest';
import { normalizeCodexSandboxPreference } from './stores/codex-sandbox-defaults.js';

describe('normalizeCodexSandboxPreference', () => {
  it('defaults clean Codex launches to workspace write', () => {
    expect(normalizeCodexSandboxPreference(undefined)).toEqual({
      'workspace-write': null,
    });
  });

  it('converts persisted sandbox JSON to Codex thread/start shorthand', () => {
    const sandbox = { type: 'workspaceWrite', writableRoots: ['/repo'], networkAccess: true };
    expect(normalizeCodexSandboxPreference(JSON.stringify(sandbox))).toEqual({
      'workspace-write': null,
    });
  });

  it('preserves read-only and danger full access modes', () => {
    expect(normalizeCodexSandboxPreference({ type: 'readOnly' })).toEqual({ 'read-only': null });
    expect(normalizeCodexSandboxPreference('dangerFullAccess')).toEqual({ 'danger-full-access': null });
  });
});
