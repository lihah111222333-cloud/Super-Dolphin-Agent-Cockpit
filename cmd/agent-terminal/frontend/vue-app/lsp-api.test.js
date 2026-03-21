// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import {
  lspReadFile,
  lspOpenFile,
  lspDiagnostics,
  lspGrep,
  lspAstSearch,
  lspDocumentSymbols,
  lspWorkspaceSymbols,
  lspHover,
  lspDefinition,
  lspReferences,
} from './services/lsp-api.js';

beforeEach(() => {
  apiMock.callAPI.mockReset();
});

describe('lsp-api service wrappers', () => {
  it('forwards file and search requests with normalized params', async () => {
    apiMock.callAPI.mockResolvedValue('ok');

    await expect(lspReadFile('/tmp/a.js', 10, 20)).resolves.toEqual({ ok: true, data: 'ok' });
    expect(apiMock.callAPI).toHaveBeenCalledWith('lsp/gui_file', {
      action: 'read_file',
      file_path: '/tmp/a.js',
      offset: 10,
      limit: 20,
    });

    await lspOpenFile('/tmp/a.js');
    expect(apiMock.callAPI).toHaveBeenCalledWith('lsp/gui_file', {
      action: 'open_file',
      file_path: '/tmp/a.js',
    });

    await lspDiagnostics('');
    expect(apiMock.callAPI).toHaveBeenCalledWith('lsp/gui_file', {
      action: 'diagnostics',
      file_path: '',
    });

    await lspGrep('needle', { path: '/repo', glob: '*.js', regex: true, caseSensitive: true, maxResults: 12 });
    expect(apiMock.callAPI).toHaveBeenCalledWith('lsp/gui_grep', {
      action: 'text_search',
      query: 'needle',
      path: '/repo',
      glob: '*.js',
      regex: true,
      case_sensitive: true,
      max_results: 12,
    });
  });

  it('forwards structure and inspect requests', async () => {
    apiMock.callAPI.mockResolvedValue({ ok: 1 });

    await lspAstSearch('func main', 'go');
    expect(apiMock.callAPI).toHaveBeenCalledWith('lsp/gui_grep', {
      action: 'ast_search',
      symbol: 'func main',
      language: 'go',
    });

    await lspDocumentSymbols('/tmp/a.js');
    expect(apiMock.callAPI).toHaveBeenCalledWith('lsp/gui_structure', {
      action: 'document_symbol',
      file_path: '/tmp/a.js',
    });

    await lspWorkspaceSymbols('openFile');
    expect(apiMock.callAPI).toHaveBeenCalledWith('lsp/gui_structure', {
      action: 'workspace_symbol',
      query: 'openFile',
    });

    await lspHover('/tmp/a.js', 1, 2);
    expect(apiMock.callAPI).toHaveBeenCalledWith('lsp/gui_inspect', {
      action: 'hover',
      file_path: '/tmp/a.js',
      line: 1,
      column: 2,
    });

    await lspDefinition('/tmp/a.js', 3, 4);
    expect(apiMock.callAPI).toHaveBeenCalledWith('lsp/gui_inspect', {
      action: 'definition',
      file_path: '/tmp/a.js',
      line: 3,
      column: 4,
    });

    await lspReferences('/tmp/a.js', 5, 6);
    expect(apiMock.callAPI).toHaveBeenCalledWith('lsp/gui_xref', {
      action: 'references',
      file_path: '/tmp/a.js',
      line: 5,
      column: 6,
    });
  });

  it('converts call failures into { ok: false, error } results', async () => {
    apiMock.callAPI.mockRejectedValueOnce(new Error('network down'));

    await expect(lspOpenFile('/tmp/b.js')).resolves.toEqual({ ok: false, error: 'network down' });
  });
});
