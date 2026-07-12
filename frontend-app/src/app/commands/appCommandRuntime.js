import { resolveShortcut, shortcutConflict } from '../../shared/keyboard/shortcutModel.js';

const COMMAND_BINDING_FIELDS = new Set(['run', 'canExecute', 'disabledReason']);
const EXECUTED_COMMAND_RESULT = Object.freeze({ executed: true, reason: '' });

function disabledCommandResult(reason) {
  return { executed: false, reason };
}

function assertCommandBinding(id, binding) {
  if (!binding || typeof binding !== 'object' || Array.isArray(binding)) {
    throw new Error(`missing command handler: ${id}`);
  }
  for (const field of Object.keys(binding)) {
    if (!COMMAND_BINDING_FIELDS.has(field)) {
      throw new Error(`unknown command binding field: ${id}.${field}`);
    }
  }
  if (typeof binding.run !== 'function') throw new Error(`invalid command handler: ${id}.run`);
  if ('canExecute' in binding && typeof binding.canExecute !== 'function') {
    throw new Error(`invalid command handler: ${id}.canExecute`);
  }
  if ('disabledReason' in binding && typeof binding.disabledReason !== 'string') {
    throw new Error(`invalid command disabled reason: ${id}`);
  }
}

function assertNoEffectiveShortcutConflicts(commands) {
  for (let leftIndex = 0; leftIndex < commands.length; leftIndex += 1) {
    for (let rightIndex = leftIndex + 1; rightIndex < commands.length; rightIndex += 1) {
      assertDistinctShortcuts(commands[leftIndex], commands[rightIndex]);
    }
  }
}

function assertDistinctShortcuts(left, right) {
  if (shortcutConflict(left.shortcut, right.shortcut)) {
    throw new Error(`shortcut conflict: ${left.id} <-> ${right.id}`);
  }
}

export function createAppCommandRuntime({ registry, bindings, overrides = {}, platform }) {
  const knownCommandIds = new Set(registry.map(({ id }) => id));
  for (const id of Object.keys(bindings)) {
    if (!knownCommandIds.has(id)) throw new Error(`unknown command binding: ${id}`);
  }
  for (const descriptor of registry) assertCommandBinding(descriptor.id, bindings[descriptor.id]);
  for (const id of Object.keys(overrides)) {
    if (!knownCommandIds.has(id)) throw new Error(`unknown shortcut override: ${id}`);
  }

  const commands = registry.map((descriptor) => {
    const binding = bindings[descriptor.id];
    return Object.freeze({
      id: descriptor.id,
      labelKey: descriptor.labelKey,
      helpKey: descriptor.helpKey,
      section: descriptor.section,
      shortcut: resolveShortcut(overrides[descriptor.id] ?? descriptor.defaultShortcut, platform),
      editablePolicy: descriptor.editablePolicy,
      repeatable: descriptor.repeatable,
      capabilityKey: descriptor.capabilityKey,
      canExecute: binding.canExecute,
      disabledReason: binding.disabledReason,
      run: binding.run,
    });
  });
  assertNoEffectiveShortcutConflicts(commands);

  const frozenCommands = Object.freeze(commands);
  const byId = new Map(frozenCommands.map((command) => [command.id, command]));
  return Object.freeze({
    commands: frozenCommands,
    execute(id) {
      const command = byId.get(id);
      if (!command) throw new Error(`unknown command: ${id}`);
      if (command.canExecute && !command.canExecute()) {
        return disabledCommandResult(command.disabledReason);
      }
      command.run();
      return EXECUTED_COMMAND_RESULT;
    },
  });
}
