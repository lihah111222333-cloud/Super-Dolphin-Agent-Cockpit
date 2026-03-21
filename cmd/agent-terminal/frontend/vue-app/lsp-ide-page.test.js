// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const lspMock = vi.hoisted(() => ({
  lspReadFile: vi.fn(),
  lspOpenFile: vi.fn(),
  lspDiagnostics: vi.fn(),
  lspGrep: vi.fn(),
  lspDocumentSymbols: vi.fn(),
  lspHover: vi.fn(),
  lspReferences: vi.fn(),
  lspDefinition: vi.fn(),
}));

vi.mock('./services/lsp-api.js', () => ({
  lspReadFile: lspMock.lspReadFile,
  lspOpenFile: lspMock.lspOpenFile,
  lspDiagnostics: lspMock.lspDiagnostics,
  lspGrep: lspMock.lspGrep,
  lspDocumentSymbols: lspMock.lspDocumentSymbols,
  lspHover: lspMock.lspHover,
  lspReferences: lspMock.lspReferences,
  lspDefinition: lspMock.lspDefinition,
}));

vi.mock('./utils/code-highlight.js', () => ({
  highlightCode: (raw) => raw.split('\n'),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  selectProjectDir: vi.fn(),
}));

import { LspIdePage } from './pages/LspIdePage.js';

function createLspIdePage(props = { projectCwd: '/repo' }) {
  return LspIdePage.setup(props);
}

beforeEach(() => {
  Object.values(lspMock).forEach((mock) => mock.mockReset());
  globalThis.document = {
    querySelector: vi.fn(() => null),
  };
  globalThis.window = {
    __AO_VISUAL_IDE__: {},
  };
});

afterEach(() => {
  delete globalThis.document;
  delete globalThis.window;
});

describe('LspIdePage', () => {
  it('exports the page component and setup function', () => {
    expect(LspIdePage.name).toBe('LspIdePage');
    expect(typeof LspIdePage.setup).toBe('function');
  });

  it('filters symbols from the setup return surface', () => {
    const vm = createLspIdePage();

    vm.symbols.value = [
      { name: 'AlphaWorker', kind: 'Function', line: 0, col: 0 },
      { name: 'BetaStore', kind: 'Variable', line: 1, col: 2 },
    ];
    vm.symbolFilter.value = 'beta';

    expect(vm.filteredSymbols.value.map((item) => item.name)).toEqual(['BetaStore']);
    expect(typeof vm.onSearch).toBe('function');
    expect(typeof vm.onSymbolClick).toBe('function');
  });

  it('searches the workspace and stores normalized search results', async () => {
    lspMock.lspGrep.mockResolvedValueOnce({
      ok: true,
      data: [{ file: 'src/main.js', line: 12, col: 4, text: 'needle result' }],
    });

    const vm = createLspIdePage({ projectCwd: '/repo' });
    vm.searchQuery.value = 'needle';

    await vm.onSearch();

    expect(lspMock.lspGrep).toHaveBeenCalledWith('needle', {
      path: '/repo',
      maxResults: 30,
    });
    expect(vm.searchResults.value).toEqual([
      expect.objectContaining({
        file: 'src/main.js',
        line: 12,
        col: 4,
        text: 'needle result',
      }),
    ]);
    expect(vm.statusState.value).toBe('ok');
  });

  it('updates highlighted symbol selection and loads hover data', async () => {
    lspMock.lspHover.mockResolvedValueOnce({
      ok: true,
      data: { contents: { value: 'function openFile(path: string)' } },
    });

    const vm = createLspIdePage();
    vm.currentFile.value = 'src/main.js';

    await vm.onSymbolClick({ name: 'openFile', line: 4, col: 2 });

    expect(lspMock.lspHover).toHaveBeenCalledWith('src/main.js', 4, 2);
    expect(vm.highlightedLine.value).toBe(5);
    expect(vm.cursorInfo.value).toBe('行 5, 列 3');
    expect(vm.inspectorLabel.value).toBe('Hover · openFile');
    expect(vm.inspectorContent.value).toContain('function openFile(path: string)');
  });
});
