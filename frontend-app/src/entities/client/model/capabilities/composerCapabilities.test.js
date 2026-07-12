import { describe, expect, it } from 'vitest';
import {
  CAPABILITY_READY,
  CAPABILITY_STALE,
  CAPABILITY_UNVERIFIED,
  addComposerCapability,
  cloneComposerCapabilities,
  composerCapabilityRequestFields,
  composerCapabilitiesReady,
  normalizeComposerCapability,
  reconcileComposerCapabilities,
  removeComposerCapability,
  restoreComposerCapabilities,
  snapshotComposerCapabilities,
} from './composerCapabilities.js';

const skillCapability = (overrides = {}) => ({
  kind: 'skill',
  key: 'skill:project::review:/repo/.agents/skills/review',
  name: 'review',
  label: 'Code Review',
  availability: CAPABILITY_READY,
  ref: {
    name: 'review',
    scope: 'project',
    personalType: '',
    path: '/repo/.agents/skills/review',
  },
  ...overrides,
});

const toolCapability = (overrides = {}) => ({
  kind: 'mcp_tool',
  key: 'mcp_tool:lsp:lsp_edit',
  name: 'lsp_edit',
  label: 'LSP Edit',
  serverName: 'lsp',
  availability: CAPABILITY_READY,
  ...overrides,
});

describe('composerCapabilities', () => {
  it('deduplicates exact capabilities and restores snapshots as unverified', () => {
    const selected = addComposerCapability([], skillCapability());
    const duplicate = addComposerCapability(selected, selected[0]);
    expect(duplicate).toHaveLength(1);

    const snapshot = snapshotComposerCapabilities(duplicate);
    expect(snapshot[0]).not.toHaveProperty('availability');
    expect(restoreComposerCapabilities(snapshot)).toEqual([
      expect.objectContaining({ key: selected[0].key, availability: CAPABILITY_UNVERIFIED }),
    ]);
  });

  it('keeps insertion order, removes by exact key, and deeply clones identities', () => {
    const current = addComposerCapability(
      addComposerCapability([], skillCapability()),
      toolCapability(),
    );
    const cloned = cloneComposerCapabilities(current);

    expect(cloned.map((item) => item.key)).toEqual([
      'skill:project::review:/repo/.agents/skills/review',
      'mcp_tool:lsp:lsp_edit',
    ]);
    cloned[0].ref.path = '/changed';
    expect(current[0].ref.path).toBe('/repo/.agents/skills/review');
    expect(removeComposerCapability(current, current[0].key)).toEqual([current[1]]);
  });

  it.each([
    ['kind', skillCapability({ kind: 'prompt' })],
    ['key', skillCapability({ key: '' })],
    ['name', skillCapability({ name: '' })],
    ['label', skillCapability({ label: '' })],
    ['availability', skillCapability({ availability: 'unknown' })],
    ['skill ref', skillCapability({ ref: { name: 'review', scope: 'project', personalType: '' } })],
    ['skill ref name', skillCapability({ ref: { ...skillCapability().ref, name: '' } })],
    ['skill ref scope', skillCapability({ ref: { ...skillCapability().ref, scope: 'global' } })],
    ['MCP server', toolCapability({ serverName: '' })],
  ])('rejects an invalid %s', (_field, capability) => {
    expect(() => normalizeComposerCapability(capability)).toThrow();
  });

  it('reconciles only the requested category against a successful catalog', () => {
    const current = [skillCapability({ availability: CAPABILITY_UNVERIFIED }), toolCapability()];
    const reconciled = reconcileComposerCapabilities(current, {
      kind: 'skill',
      status: 'success',
      items: [{ payload: { capability: { key: current[0].key } } }],
    });

    expect(reconciled).toEqual([
      expect.objectContaining({ kind: 'skill', availability: CAPABILITY_READY }),
      expect.objectContaining({ kind: 'mcp_tool', availability: CAPABILITY_READY }),
    ]);
  });

  it('marks missing successful identities stale and non-success identities unverified', () => {
    expect(reconcileComposerCapabilities([skillCapability()], {
      kind: 'skill',
      status: 'success',
      items: [],
    })[0].availability).toBe(CAPABILITY_STALE);

    expect(reconcileComposerCapabilities([toolCapability()], {
      kind: 'mcp_tool',
      status: 'error',
      items: [],
    })[0].availability).toBe(CAPABILITY_UNVERIFIED);
  });

  it('reports readiness only when every selected capability is ready', () => {
    expect(composerCapabilitiesReady([])).toBe(true);
    expect(composerCapabilitiesReady([skillCapability(), toolCapability()])).toBe(true);
    expect(composerCapabilitiesReady([
      skillCapability({ availability: CAPABILITY_UNVERIFIED }),
    ])).toBe(false);
  });

  it('serializes ready skills and tools into the turn facade', () => {
    expect(composerCapabilityRequestFields([
      skillCapability({
        key: 'skill:project::review:/repo/skills/review',
        ref: {
          name: 'review',
          scope: 'project',
          personalType: '',
          path: '/repo/skills/review',
        },
      }),
      toolCapability(),
    ])).toEqual({
      selectedSkills: ['review'],
      selectedSkillRefs: [{
        name: 'review',
        scope: 'project',
        path: '/repo/skills/review',
      }],
      manualSkillSelection: true,
      enabledTools: ['lsp_edit'],
    });
  });

  it.each([CAPABILITY_STALE, CAPABILITY_UNVERIFIED])(
    'rejects a %s capability before RPC',
    (availability) => {
      expect(() => composerCapabilityRequestFields([
        toolCapability({
          key: 'mcp_tool:lsp:grep',
          name: 'grep',
          label: 'grep',
          availability,
        }),
      ])).toThrow(`composer capability mcp_tool:lsp:grep is ${availability}`);
    },
  );
});
