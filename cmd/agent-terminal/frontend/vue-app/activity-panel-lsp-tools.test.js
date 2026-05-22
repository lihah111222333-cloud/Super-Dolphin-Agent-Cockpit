import { describe, expect, it } from 'vitest';
import { ActivityPanel } from './components/ActivityPanel.js';

describe('ActivityPanel LSP tool counts', () => {
  it('counts format_preview aliases as LSP tools and keeps details visible', () => {
    const vm = ActivityPanel.setup({
      stats: {
        toolCalls: {
          format_preview: 1,
          mcp__lsp__lsp_format_preview: 1,
          'mcp__lsp-tools__lsp_grep': 1,
        },
      },
      alerts: [],
      processEvents: [],
    });

    expect(vm.lspCount.value).toBe(3);
    expect(vm.totalTools.value).toBe(3);
    expect(vm.toolCallEntries.value).toEqual([
      { name: 'format_preview', count: 2 },
      { name: 'grep', count: 1 },
    ]);
    expect(vm.statItems.value.find((item) => item.key === 'lsp')).toEqual(
      expect.objectContaining({ label: 'LSP (8 tools)', value: 3 }),
    );
  });
});
