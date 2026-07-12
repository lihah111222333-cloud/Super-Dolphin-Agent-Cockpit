import { describe, expect, it } from 'vitest';

import { defineAppCommandRegistry } from '../../../app/commands/appCommandRegistry.js';
import {
  formatShortcutDisplay,
  projectShortcutSettings,
  validateShortcutOverrides,
} from './shortcutSettingsModel.js';

const registry = defineAppCommandRegistry([
  {
    id: 'chat.new',
    labelKey: 'commands.chat.new',
    helpKey: 'commands.chat.newHelp',
    section: 'chat',
    defaultShortcut: { key: 'n', mod: true },
  },
  {
    id: 'settings.open',
    labelKey: 'commands.settings.open',
    helpKey: 'commands.settings.openHelp',
    section: 'navigation',
    defaultShortcut: { key: ',', mod: true },
  },
]);

const copy = Object.freeze({
  labels: Object.freeze({
    'commands.chat.new': 'New chat',
    'commands.settings.open': 'Open settings',
  }),
  help: Object.freeze({
    'commands.chat.newHelp': 'Start an empty conversation',
    'commands.settings.openHelp': 'Open application preferences',
  }),
});

function shortcut(key, overrides = {}) {
  return { key, meta: false, ctrl: false, alt: false, shift: false, ...overrides };
}

describe('shortcut settings model', () => {
  it('projects registry-owned labels, help, defaults, and platform-resolved displays', () => {
    const items = projectShortcutSettings({
      registry,
      copy,
      platform: 'darwin',
      overrides: { 'chat.new': shortcut('m', { meta: true }) },
    });

    expect(items[0]).toMatchObject({
      id: 'chat.new',
      label: 'New chat',
      help: 'Start an empty conversation',
      defaultDisplay: '⌘N',
      currentDisplay: '⌘M',
      isCustomized: true,
    });
    expect(items[1]).toMatchObject({
      id: 'settings.open',
      label: 'Open settings',
      defaultDisplay: '⌘,',
      currentDisplay: '⌘,',
      isCustomized: false,
    });
    expect(formatShortcutDisplay(shortcut('k', { ctrl: true, shift: true }), 'linux')).toBe('Ctrl+Shift+K');
  });

  it('rejects an unknown preference id as one invalid override set', () => {
    expect(() => validateShortcutOverrides({
      registry,
      overrides: { 'unknown.command': shortcut('x', { ctrl: true }) },
      platform: 'linux',
    })).toThrow('unknown shortcut override: unknown.command');
  });

  it('rejects an effective duplicate after defaults and overrides resolve for the platform', () => {
    expect(() => validateShortcutOverrides({
      registry,
      overrides: {
        'settings.open': shortcut('n', { ctrl: true }),
      },
      platform: 'linux',
    })).toThrow('shortcut conflict: chat.new <-> settings.open');
  });
});
