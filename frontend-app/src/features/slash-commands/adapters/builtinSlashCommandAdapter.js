import { normalizeSlashCommandItem } from '../model/slashCommandModel.js';

export function builtinSlashCommandItems(copy) {
  return [
    {
      id: 'builtin:new',
      kind: 'builtin',
      name: 'new',
      label: copy?.builtins?.newLabel,
      description: copy?.builtins?.newDescription,
      keywords: ['new', 'chat'],
      payload: { action: 'new' },
      disabled: false,
      disabledReason: '',
    },
    {
      id: 'builtin:clear',
      kind: 'builtin',
      name: 'clear',
      label: copy?.builtins?.clearLabel,
      description: copy?.builtins?.clearDescription,
      keywords: ['clear', 'reset'],
      payload: { action: 'clear' },
      disabled: false,
      disabledReason: '',
    },
  ].map(normalizeSlashCommandItem);
}
