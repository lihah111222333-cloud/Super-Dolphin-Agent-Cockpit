import { describe, expect, it } from 'vitest';
import {
  isEditableShortcutTarget,
  matchesShortcut,
  resolveShortcut,
  shortcutConflict,
} from './shortcutModel.js';

function keyEvent(init = {}, target = null) {
  const event = new KeyboardEvent('keydown', {
    key: 'k',
    cancelable: true,
    ...init,
  });
  if (target) Object.defineProperty(event, 'target', { configurable: true, value: target });
  return event;
}

describe('shortcut model', () => {
  it('resolves the platform mod key into an immutable exact modifier shape', () => {
    expect(resolveShortcut({ key: 'K', mod: true }, 'darwin')).toEqual({
      key: 'k', meta: true, ctrl: false, alt: false, shift: false,
    });
    expect(resolveShortcut({ key: 'k', mod: true, alt: true }, 'linux')).toEqual({
      key: 'k', meta: false, ctrl: true, alt: true, shift: false,
    });
    expect(Object.isFrozen(resolveShortcut({ key: 'k', mod: true }, 'win32'))).toBe(true);
  });

  it.each([
    [{ key: '', mod: true }, 'darwin', 'invalid shortcut key'],
    [{ key: 'k', ctrl: 'true' }, 'darwin', 'invalid shortcut modifier: ctrl'],
    [{ key: 'k', mod: true, ctrl: true }, 'darwin', 'shortcut mod cannot be combined with meta or ctrl'],
    [{ key: 'k', extra: true }, 'darwin', 'unknown shortcut field: extra'],
    [{ key: 'k', mod: true }, 'android', 'unsupported shortcut platform: android'],
  ])('rejects malformed or ambiguous shortcut definitions', (shortcut, platform, message) => {
    expect(() => resolveShortcut(shortcut, platform)).toThrow(message);
  });

  it('matches key and every modifier exactly', () => {
    const shortcut = resolveShortcut({ key: 'k', mod: true }, 'darwin');

    expect(matchesShortcut(keyEvent({ metaKey: true }), shortcut)).toBe(true);
    expect(matchesShortcut(keyEvent({ metaKey: true, shiftKey: true }), shortcut)).toBe(false);
    expect(matchesShortcut(keyEvent({ ctrlKey: true }), shortcut)).toBe(false);
    expect(matchesShortcut(keyEvent({ key: 'p', metaKey: true }), shortcut)).toBe(false);
  });

  it('rejects prevented, composing, and IME 229 events', () => {
    const shortcut = resolveShortcut({ key: 'k', mod: true }, 'darwin');
    const prevented = keyEvent({ metaKey: true });
    prevented.preventDefault();
    const ime229 = keyEvent({ metaKey: true });
    Object.defineProperty(ime229, 'keyCode', { configurable: true, value: 229 });

    expect(matchesShortcut(prevented, shortcut)).toBe(false);
    expect(matchesShortcut(keyEvent({ isComposing: true, metaKey: true }), shortcut)).toBe(false);
    expect(matchesShortcut(ime229, shortcut)).toBe(false);
  });

  it('enforces repeat and editable target policy', () => {
    const shortcut = resolveShortcut({ key: 'k', mod: true }, 'darwin');
    const input = document.createElement('input');

    expect(matchesShortcut(keyEvent({ metaKey: true, repeat: true }), shortcut)).toBe(false);
    expect(matchesShortcut(keyEvent({ metaKey: true, repeat: true }), shortcut, { repeatable: true })).toBe(true);
    expect(matchesShortcut(keyEvent({ metaKey: true }, input), shortcut)).toBe(false);
    expect(matchesShortcut(keyEvent({ metaKey: true }, input), shortcut, { editablePolicy: 'allow' })).toBe(true);
  });

  it('recognizes editable controls and contenteditable ancestry', () => {
    const textarea = document.createElement('textarea');
    const editor = document.createElement('div');
    const child = document.createElement('span');
    editor.setAttribute('contenteditable', 'true');
    editor.append(child);

    expect(isEditableShortcutTarget(textarea)).toBe(true);
    expect(isEditableShortcutTarget(child)).toBe(true);
    expect(isEditableShortcutTarget(document.createElement('button'))).toBe(false);
    expect(isEditableShortcutTarget(null)).toBe(false);
  });

  it('detects only exact effective shortcut conflicts', () => {
    const darwinModK = resolveShortcut({ key: 'k', mod: true }, 'darwin');
    const darwinMetaK = resolveShortcut({ key: 'k', meta: true }, 'darwin');
    const darwinMetaShiftK = resolveShortcut({ key: 'k', meta: true, shift: true }, 'darwin');

    expect(shortcutConflict(darwinModK, darwinMetaK)).toBe(true);
    expect(shortcutConflict(darwinModK, darwinMetaShiftK)).toBe(false);
  });
});
