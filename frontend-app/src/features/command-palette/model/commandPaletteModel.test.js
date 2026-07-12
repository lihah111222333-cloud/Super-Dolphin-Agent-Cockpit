import { describe, expect, it } from 'vitest';

import {
  filterCommandPaletteItems,
  groupCommandPaletteItems,
  isSubsequenceMatch,
  projectCommandPaletteItems,
} from './commandPaletteModel.js';

const copy = Object.freeze({
  labels: Object.freeze({
    'commands.palette.open': 'Open command palette',
    'commands.chat.new': 'New chat',
    'commands.settings.open': 'Open settings',
    'commands.sidebar.toggle': 'Toggle sidebar',
  }),
  help: Object.freeze({
    'commands.chat.newHelp': 'Start an empty conversation',
    'commands.settings.openHelp': 'Open application preferences',
  }),
  sections: Object.freeze({
    application: 'Application',
    chat: 'Chat',
    navigation: 'Navigation',
  }),
});

const commands = Object.freeze([
  Object.freeze({
    id: 'command.palette.open',
    labelKey: 'commands.palette.open',
    section: 'application',
    shortcut: Object.freeze({ key: 'k', meta: false, ctrl: true, alt: false, shift: false }),
  }),
  Object.freeze({
    id: 'chat.new',
    labelKey: 'commands.chat.new',
    helpKey: 'commands.chat.newHelp',
    section: 'chat',
    shortcut: Object.freeze({ key: 'n', meta: false, ctrl: true, alt: false, shift: false }),
  }),
  Object.freeze({
    id: 'settings.open',
    labelKey: 'commands.settings.open',
    helpKey: 'commands.settings.openHelp',
    section: 'navigation',
    shortcut: Object.freeze({ key: ',', meta: false, ctrl: true, alt: false, shift: false }),
  }),
  Object.freeze({
    id: 'sidebar.toggle',
    labelKey: 'commands.sidebar.toggle',
    section: 'navigation',
    shortcut: Object.freeze({ key: 'b', meta: false, ctrl: true, alt: false, shift: false }),
    canExecute: () => false,
    disabledReason: 'Sidebar is locked',
  }),
]);

describe('command palette model', () => {
  it('matches case-insensitive subsequences without accepting reordered characters', () => {
    expect(isSubsequenceMatch('Open Settings', 'sett')).toBe(true);
    expect(isSubsequenceMatch('Open Settings', 'opst')).toBe(true);
    expect(isSubsequenceMatch('Open Settings', 'stpo')).toBe(false);
  });

  it('filters projected commands by label, help, and id subsequences', () => {
    const items = projectCommandPaletteItems(commands, copy);

    expect(filterCommandPaletteItems(items, 'opng').map(({ id }) => id)).toEqual(['settings.open']);
    expect(filterCommandPaletteItems(items, 'empty').map(({ id }) => id)).toEqual(['chat.new']);
    expect(filterCommandPaletteItems(items, 'sdt').map(({ id }) => id)).toEqual(['sidebar.toggle']);
  });

  it('keeps first-seen section order and command order stable', () => {
    const groups = groupCommandPaletteItems(projectCommandPaletteItems(commands, copy));

    expect(groups.map(({ section }) => section)).toEqual(['application', 'chat', 'navigation']);
    expect(groups[2].items.map(({ id }) => id)).toEqual(['settings.open', 'sidebar.toggle']);
  });

  it('projects disabled state and its visible reason without executing capabilities', () => {
    const item = projectCommandPaletteItems(commands, copy).find(({ id }) => id === 'sidebar.toggle');

    expect(item).toMatchObject({
      disabled: true,
      disabledReason: 'Sidebar is locked',
      label: 'Toggle sidebar',
      sectionLabel: 'Navigation',
    });
  });
});
