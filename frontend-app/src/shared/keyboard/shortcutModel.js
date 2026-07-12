const SHORTCUT_FIELDS = new Set(['key', 'mod', 'meta', 'ctrl', 'alt', 'shift']);
const RESOLVED_SHORTCUT_FIELDS = new Set(['key', 'meta', 'ctrl', 'alt', 'shift']);
const SHORTCUT_POLICY_FIELDS = new Set(['repeatable', 'editablePolicy']);
const SUPPORTED_PLATFORMS = new Set(['darwin', 'linux', 'win32']);

function normalizedKey(value) {
  if (typeof value !== 'string' || value.trim() !== value || value.length < 1 || value.length > 32) {
    throw new Error('invalid shortcut key');
  }
  return value.toLowerCase();
}

function assertBooleanField(source, field) {
  if (field in source && typeof source[field] !== 'boolean') {
    throw new Error(`invalid shortcut modifier: ${field}`);
  }
}

function assertShortcutDefinition(shortcut) {
  if (!shortcut || typeof shortcut !== 'object' || Array.isArray(shortcut)) {
    throw new Error('invalid shortcut');
  }
  for (const field of Object.keys(shortcut)) {
    if (!SHORTCUT_FIELDS.has(field)) throw new Error(`unknown shortcut field: ${field}`);
  }
  for (const field of ['mod', 'meta', 'ctrl', 'alt', 'shift']) assertBooleanField(shortcut, field);
  if (shortcut.mod === true && ('meta' in shortcut || 'ctrl' in shortcut)) {
    throw new Error('shortcut mod cannot be combined with meta or ctrl');
  }
  normalizedKey(shortcut.key);
}

function assertResolvedShortcut(shortcut) {
  if (!shortcut || typeof shortcut !== 'object' || Array.isArray(shortcut)) {
    throw new Error('invalid resolved shortcut');
  }
  for (const field of Object.keys(shortcut)) {
    if (!RESOLVED_SHORTCUT_FIELDS.has(field)) throw new Error(`unknown resolved shortcut field: ${field}`);
  }
  if (Object.keys(shortcut).length !== RESOLVED_SHORTCUT_FIELDS.size) {
    throw new Error('invalid resolved shortcut');
  }
  normalizedKey(shortcut.key);
  for (const field of ['meta', 'ctrl', 'alt', 'shift']) assertBooleanField(shortcut, field);
}

function normalizedShortcutPolicy(policy) {
  if (!policy || typeof policy !== 'object' || Array.isArray(policy)) {
    throw new Error('invalid shortcut policy');
  }
  for (const field of Object.keys(policy)) {
    if (!SHORTCUT_POLICY_FIELDS.has(field)) throw new Error(`unknown shortcut policy field: ${field}`);
  }
  if ('repeatable' in policy && typeof policy.repeatable !== 'boolean') {
    throw new Error('invalid shortcut repeatable policy');
  }
  if ('editablePolicy' in policy && !['allow', 'deny'].includes(policy.editablePolicy)) {
    throw new Error('invalid shortcut editable policy');
  }
  return {
    editablePolicy: policy.editablePolicy ?? 'deny',
    repeatable: policy.repeatable ?? false,
  };
}

export function resolveShortcut(shortcut, platform) {
  assertShortcutDefinition(shortcut);
  if (!SUPPORTED_PLATFORMS.has(platform)) throw new Error(`unsupported shortcut platform: ${platform}`);
  const platformMod = shortcut.mod === true;
  return Object.freeze({
    key: normalizedKey(shortcut.key),
    meta: platformMod ? platform === 'darwin' : (shortcut.meta ?? false),
    ctrl: platformMod ? platform !== 'darwin' : (shortcut.ctrl ?? false),
    alt: shortcut.alt ?? false,
    shift: shortcut.shift ?? false,
  });
}

export function isEditableShortcutTarget(target) {
  if (!(target instanceof Element)) return false;
  const tagName = target.tagName.toLowerCase();
  if (['input', 'textarea', 'select', 'option'].includes(tagName)) return true;
  return Boolean(target.closest('[contenteditable]:not([contenteditable="false"])'));
}

export function matchesShortcut(event, shortcut, policy = {}) {
  assertResolvedShortcut(shortcut);
  const resolvedPolicy = normalizedShortcutPolicy(policy);
  if (!event || event.defaultPrevented || event.isComposing) return false;
  const keyCode = Number(event.keyCode || event.which || 0);
  if (keyCode === 229 || event.key === 'Process' || event.key === 'Unidentified') return false;
  if (event.repeat && !resolvedPolicy.repeatable) return false;
  if (resolvedPolicy.editablePolicy === 'deny' && isEditableShortcutTarget(event.target)) return false;
  return event.key.toLowerCase() === shortcut.key
    && Boolean(event.metaKey) === shortcut.meta
    && Boolean(event.ctrlKey) === shortcut.ctrl
    && Boolean(event.altKey) === shortcut.alt
    && Boolean(event.shiftKey) === shortcut.shift;
}

export function shortcutConflict(left, right) {
  assertResolvedShortcut(left);
  assertResolvedShortcut(right);
  return left.key === right.key
    && left.meta === right.meta
    && left.ctrl === right.ctrl
    && left.alt === right.alt
    && left.shift === right.shift;
}
