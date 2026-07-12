export const APP_COMMAND_IDS = Object.freeze({
  PALETTE_OPEN: 'command.palette.open',
  CHAT_NEW: 'chat.new',
  SETTINGS_OPEN: 'settings.open',
  SIDEBAR_TOGGLE: 'sidebar.toggle',
  TURN_INTERRUPT: 'turn.interrupt',
});

const COMMAND_DESCRIPTOR_FIELDS = new Set([
  'id',
  'labelKey',
  'helpKey',
  'section',
  'defaultShortcut',
  'editablePolicy',
  'repeatable',
  'capabilityKey',
]);

const SHORTCUT_FIELDS = new Set(['key', 'mod', 'meta', 'ctrl', 'alt', 'shift']);

function isNonBlankString(value) {
  return typeof value === 'string' && value.trim() === value && value.length > 0;
}

function assertExactDefaultShortcut(shortcut) {
  if (!shortcut || typeof shortcut !== 'object' || Array.isArray(shortcut)) {
    throw new Error('invalid command descriptor');
  }
  for (const field of Object.keys(shortcut)) {
    if (!SHORTCUT_FIELDS.has(field)) throw new Error(`unknown command shortcut field: ${field}`);
  }
  if (!isNonBlankString(shortcut.key) || shortcut.key.length > 32) {
    throw new Error('invalid command descriptor');
  }
  for (const field of ['mod', 'meta', 'ctrl', 'alt', 'shift']) {
    if (field in shortcut && typeof shortcut[field] !== 'boolean') {
      throw new Error('invalid command descriptor');
    }
  }
  if (shortcut.mod === true && ('meta' in shortcut || 'ctrl' in shortcut)) {
    throw new Error('invalid command descriptor');
  }
}

function assertExactCommandDescriptor(descriptor) {
  if (!descriptor || typeof descriptor !== 'object' || Array.isArray(descriptor)) {
    throw new Error('invalid command descriptor');
  }
  for (const field of Object.keys(descriptor)) {
    if (!COMMAND_DESCRIPTOR_FIELDS.has(field)) {
      throw new Error(`unknown command descriptor field: ${field}`);
    }
  }
  if (!isNonBlankString(descriptor.id)) {
    throw new Error('invalid command descriptor');
  }
  if ([...descriptor.id].length > 128) {
    throw new Error('command id must be non-blank, trimmed, and contain at most 128 characters');
  }
  if (!isNonBlankString(descriptor.labelKey)
    || !isNonBlankString(descriptor.section)) {
    throw new Error('invalid command descriptor');
  }
  if ('helpKey' in descriptor && !isNonBlankString(descriptor.helpKey)) {
    throw new Error('invalid command descriptor');
  }
  if ('editablePolicy' in descriptor && !['allow', 'deny'].includes(descriptor.editablePolicy)) {
    throw new Error('invalid command descriptor');
  }
  if ('repeatable' in descriptor && typeof descriptor.repeatable !== 'boolean') {
    throw new Error('invalid command descriptor');
  }
  if ('capabilityKey' in descriptor && !isNonBlankString(descriptor.capabilityKey)) {
    throw new Error('invalid command descriptor');
  }
  assertExactDefaultShortcut(descriptor.defaultShortcut);
}

export function defineAppCommandRegistry(descriptors) {
  if (!Array.isArray(descriptors)) throw new Error('command descriptors must be an array');
  const ids = new Set();
  const result = descriptors.map((descriptor) => {
    assertExactCommandDescriptor(descriptor);
    if (ids.has(descriptor.id)) throw new Error(`duplicate command id: ${descriptor.id}`);
    ids.add(descriptor.id);
    return Object.freeze({
      id: descriptor.id,
      labelKey: descriptor.labelKey,
      helpKey: descriptor.helpKey,
      section: descriptor.section,
      defaultShortcut: Object.freeze({ ...descriptor.defaultShortcut }),
      editablePolicy: descriptor.editablePolicy,
      repeatable: descriptor.repeatable,
      capabilityKey: descriptor.capabilityKey,
    });
  });
  return Object.freeze(result);
}

export const APP_COMMAND_REGISTRY = defineAppCommandRegistry([
  {
    id: APP_COMMAND_IDS.PALETTE_OPEN,
    labelKey: 'commands.palette.open',
    helpKey: 'commands.palette.openHelp',
    section: 'application',
    defaultShortcut: { key: 'k', mod: true },
  },
  {
    id: APP_COMMAND_IDS.CHAT_NEW,
    labelKey: 'commands.chat.new',
    helpKey: 'commands.chat.newHelp',
    section: 'chat',
    defaultShortcut: { key: 'n', mod: true },
  },
  {
    id: APP_COMMAND_IDS.SETTINGS_OPEN,
    labelKey: 'commands.settings.open',
    helpKey: 'commands.settings.openHelp',
    section: 'navigation',
    defaultShortcut: { key: ',', mod: true },
  },
  {
    id: APP_COMMAND_IDS.SIDEBAR_TOGGLE,
    labelKey: 'commands.sidebar.toggle',
    helpKey: 'commands.sidebar.toggleHelp',
    section: 'navigation',
    defaultShortcut: { key: 'b', mod: true },
  },
  {
    id: APP_COMMAND_IDS.TURN_INTERRUPT,
    labelKey: 'commands.turn.interrupt',
    helpKey: 'commands.turn.interruptHelp',
    section: 'chat',
    defaultShortcut: { key: 'Escape' },
  },
]);
