import { describe, expect, it } from 'vitest';
import { APP_COMMAND_IDS, defineAppCommandRegistry } from './appCommandRegistry.js';
import * as registryModule from './appCommandRegistry.js';

function commandDescriptor(overrides = {}) {
  return {
    id: APP_COMMAND_IDS.CHAT_NEW,
    labelKey: 'commands.chatNew',
    helpKey: 'commands.chatNewHelp',
    section: 'chat',
    defaultShortcut: { key: 'n', mod: true },
    editablePolicy: 'deny',
    repeatable: false,
    capabilityKey: 'chat.canCreate',
    ...overrides,
  };
}

describe('app command registry', () => {
  it('owns the single canonical descriptor list without executable bindings', () => {
    const registry = registryModule.APP_COMMAND_REGISTRY;

    expect(registry).toBeDefined();
    expect(registry.map(({ id }) => id)).toEqual(Object.values(APP_COMMAND_IDS));
    for (const descriptor of registry) {
      expect(descriptor).not.toHaveProperty('run');
      expect(descriptor).not.toHaveProperty('canExecute');
      expect(descriptor).not.toHaveProperty('disabledReason');
    }
  });

  it('publishes the fixed command identifiers as immutable descriptor keys', () => {
    expect(APP_COMMAND_IDS).toEqual({
      PALETTE_OPEN: 'command.palette.open',
      CHAT_NEW: 'chat.new',
      SETTINGS_OPEN: 'settings.open',
      SIDEBAR_TOGGLE: 'sidebar.toggle',
      TURN_INTERRUPT: 'turn.interrupt',
    });
    expect(Object.isFrozen(APP_COMMAND_IDS)).toBe(true);
  });

  it('returns immutable descriptor copies without executable bindings', () => {
    const source = commandDescriptor();
    const registry = defineAppCommandRegistry([source]);

    expect(registry).toEqual([source]);
    expect(registry[0]).not.toBe(source);
    expect(registry[0].defaultShortcut).not.toBe(source.defaultShortcut);
    expect(Object.isFrozen(registry)).toBe(true);
    expect(Object.isFrozen(registry[0])).toBe(true);
    expect(Object.isFrozen(registry[0].defaultShortcut)).toBe(true);
  });

  it('rejects duplicate command ids', () => {
    const descriptor = commandDescriptor();

    expect(() => defineAppCommandRegistry([descriptor, { ...descriptor }]))
      .toThrow('duplicate command id: chat.new');
  });

  it.each(['extra', 'run', 'handler'])(
    'rejects the unknown descriptor field %s',
    (field) => {
      expect(() => defineAppCommandRegistry([commandDescriptor({ [field]: () => {} })]))
        .toThrow(`unknown command descriptor field: ${field}`);
    },
  );

  it.each([
    ['id', ''],
    ['labelKey', 1],
    ['helpKey', false],
    ['section', ''],
    ['defaultShortcut', null],
    ['editablePolicy', 'sometimes'],
    ['repeatable', 'false'],
    ['capabilityKey', ''],
  ])('rejects an invalid %s descriptor value', (field, value) => {
    expect(() => defineAppCommandRegistry([commandDescriptor({ [field]: value })]))
      .toThrow('invalid command descriptor');
  });
});
