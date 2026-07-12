import { resolveShortcut, shortcutConflict } from '../../../shared/keyboard/shortcutModel.js';

export const SHORTCUT_PREFERENCE_KEY = 'settings.shortcuts.bindings';

const OVERRIDE_FIELDS = Object.freeze(['key', 'meta', 'ctrl', 'alt', 'shift']);

function requiredLocalizedText(values, key, kind) {
  const value = values?.[key];
  if (typeof value !== 'string' || value.trim() === '') {
    throw new Error(`missing command ${kind}: ${key}`);
  }
  return value;
}

function assertExactOverride(id, shortcut) {
  if (!shortcut || typeof shortcut !== 'object' || Array.isArray(shortcut)) {
    throw new Error(`invalid shortcut override: ${id}`);
  }
  const fields = Object.keys(shortcut);
  if (fields.length !== OVERRIDE_FIELDS.length || fields.some((field) => !OVERRIDE_FIELDS.includes(field))) {
    throw new Error(`invalid shortcut override: ${id}`);
  }
}

function resolvedCommands(registry, overrides, platform) {
  return registry.map((descriptor) => ({
    id: descriptor.id,
    shortcut: resolveShortcut(overrides[descriptor.id] ?? descriptor.defaultShortcut, platform),
  }));
}

function assertNoShortcutConflicts(commands) {
  for (let leftIndex = 0; leftIndex < commands.length; leftIndex += 1) {
    for (let rightIndex = leftIndex + 1; rightIndex < commands.length; rightIndex += 1) {
      const left = commands[leftIndex];
      const right = commands[rightIndex];
      if (shortcutConflict(left.shortcut, right.shortcut)) {
        throw new Error(`shortcut conflict: ${left.id} <-> ${right.id}`);
      }
    }
  }
}

export function validateShortcutOverrides({ registry, overrides, platform }) {
  if (!overrides || typeof overrides !== 'object' || Array.isArray(overrides)) {
    throw new Error('shortcut overrides must be an object');
  }
  const knownIds = new Set(registry.map(({ id }) => id));
  const validated = {};
  for (const [id, shortcut] of Object.entries(overrides)) {
    if (!knownIds.has(id)) throw new Error(`unknown shortcut override: ${id}`);
    assertExactOverride(id, shortcut);
    validated[id] = resolveShortcut(shortcut, platform);
  }
  assertNoShortcutConflicts(resolvedCommands(registry, validated, platform));
  return Object.freeze(validated);
}

export function formatShortcutDisplay(shortcut, platform) {
  const resolved = resolveShortcut(shortcut, platform);
  const key = resolved.key.length === 1 ? resolved.key.toLocaleUpperCase() : resolved.key;
  if (platform === 'darwin') {
    return `${resolved.meta ? '⌘' : ''}${resolved.ctrl ? '⌃' : ''}${resolved.alt ? '⌥' : ''}${resolved.shift ? '⇧' : ''}${key}`;
  }
  const modifiers = [];
  if (resolved.meta) modifiers.push('Meta');
  if (resolved.ctrl) modifiers.push('Ctrl');
  if (resolved.alt) modifiers.push('Alt');
  if (resolved.shift) modifiers.push('Shift');
  return [...modifiers, key].join('+');
}

export function projectShortcutSettings({ registry, copy, platform, overrides }) {
  return Object.freeze(registry.map((descriptor) => {
    const defaultShortcut = resolveShortcut(descriptor.defaultShortcut, platform);
    const currentShortcut = overrides[descriptor.id] ?? defaultShortcut;
    return Object.freeze({
      id: descriptor.id,
      label: requiredLocalizedText(copy.labels, descriptor.labelKey, 'label'),
      help: descriptor.helpKey ? requiredLocalizedText(copy.help, descriptor.helpKey, 'help') : '',
      defaultShortcut,
      currentShortcut,
      defaultDisplay: formatShortcutDisplay(defaultShortcut, platform),
      currentDisplay: formatShortcutDisplay(currentShortcut, platform),
      isCustomized: Object.hasOwn(overrides, descriptor.id),
    });
  }));
}
