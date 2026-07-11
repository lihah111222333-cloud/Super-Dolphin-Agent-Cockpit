import { describe, expect, it } from 'vitest';
import {
  normalizeSlashCommandItem,
  parseSlashCommandTrigger,
  rankSlashCommandItems,
  replaceSlashCommandTrigger,
  slashCommandOptionId,
} from './slashCommandModel.js';
import { builtinSlashCommandItems } from '../adapters/builtinSlashCommandAdapter.js';

const commandItem = (overrides = {}) => ({
  id: 'skill:review',
  kind: 'skill',
  name: 'review',
  label: 'Code Review',
  description: 'Review code',
  keywords: ['audit'],
  payload: { capability: { kind: 'skill', name: 'review' } },
  disabled: false,
  disabledReason: '',
  ...overrides,
});

describe('parseSlashCommandTrigger', () => {
  it.each([
    ['/', { leading: '', query: '', raw: '/' }],
    ['  /review', { leading: '  ', query: 'review', raw: '/review' }],
  ])('opens only for the first non-whitespace slash: %s', (draft, expected) => {
    expect(parseSlashCommandTrigger(draft)).toEqual(expected);
  });

  it.each(['hello /review', 'https://example.com/a', '/review now', '/review\nnext', 'C:/repo'])(
    'does not open for %s', (draft) => {
      expect(parseSlashCommandTrigger(draft)).toBeNull();
    },
  );

  it('preserves leading whitespace while replacing the trigger', () => {
    expect(replaceSlashCommandTrigger('  /review', 'Review this change')).toBe('  Review this change');
  });

  it('rejects replacement when no trigger is active', () => {
    expect(() => replaceSlashCommandTrigger('hello', 'replacement')).toThrow(
      'slash command trigger is not active',
    );
  });

  it('rejects a non-string replacement instead of silently clearing the draft', () => {
    expect(() => replaceSlashCommandTrigger('/review', null)).toThrow(
      'slash command replacement must be a string',
    );
  });
});

describe('normalizeSlashCommandItem', () => {
  it('normalizes strings and deduplicates keywords', () => {
    expect(normalizeSlashCommandItem(commandItem({
      id: ' skill:review ',
      keywords: ['audit', ' audit ', 'review'],
    }))).toEqual(commandItem({ keywords: ['audit', 'review'] }));
  });

  it.each([
    ['id', { id: '' }],
    ['kind', { kind: 'unknown' }],
    ['name', { name: '' }],
    ['label', { label: '' }],
    ['description', { description: null }],
    ['keywords', { keywords: 'audit' }],
    ['payload', { payload: null }],
    ['disabled', { disabled: 'false' }],
    ['disabledReason', { disabledReason: null }],
  ])('rejects an invalid %s field', (_field, overrides) => {
    expect(() => normalizeSlashCommandItem(commandItem(overrides))).toThrow();
  });
});

describe('rankSlashCommandItems', () => {
  it('orders by match quality and then builtin, skill, prompt, automation, tool', () => {
    const items = [
      commandItem({ id: 'mcp_tool:lsp:review', kind: 'mcp_tool', label: 'Review tool' }),
      commandItem({ id: 'prompt:review', kind: 'prompt', name: 'review-notes', label: 'Review notes' }),
      commandItem(),
      commandItem({
        id: 'builtin:new', kind: 'builtin', name: 'new', label: 'New chat',
        description: '', keywords: ['review'], payload: { action: 'new' },
      }),
    ];
    expect(rankSlashCommandItems(items, 'review').map((item) => item.id)).toEqual([
      'skill:review',
      'prompt:review',
      'mcp_tool:lsp:review',
      'builtin:new',
    ]);
  });

  it('preserves input order when rank and category are equal', () => {
    const items = [
      commandItem({ id: 'skill:first', label: 'Review first' }),
      commandItem({ id: 'skill:second', label: 'Review second' }),
    ];
    expect(rankSlashCommandItems(items, 'review').map((item) => item.id)).toEqual([
      'skill:first',
      'skill:second',
    ]);
  });
});

it('creates a stable DOM-safe option id', () => {
  expect(slashCommandOptionId(commandItem({ id: 'mcp_tool:lsp/lsp.edit' })))
    .toBe('slash-command-mcp_tool-lsp-lsp-edit');
});

it('creates only the approved immediate built-in commands', () => {
  const copy = {
    builtins: {
      newLabel: 'New chat',
      newDescription: 'Create a new draft',
      clearLabel: 'Clear input',
      clearDescription: 'Clear the composer',
    },
  };
  expect(builtinSlashCommandItems(copy)).toEqual([
    expect.objectContaining({ id: 'builtin:new', payload: { action: 'new' }, disabled: false }),
    expect.objectContaining({ id: 'builtin:clear', payload: { action: 'clear' }, disabled: false }),
  ]);
});
