import { test, expect } from '@playwright/test';
import { CALL_API_METHOD_ID, installMockBackend } from './support/mock-backend.js';

const FILE_PATH = '/workspace/go-project/internal/service.go';
const FILE_CONTENT = [
    'package service',
    '',
    'import "fmt"',
    '',
    'type Service struct{}',
    '',
    'func handleRequest() error {',
    '    value := "ok"',
    '',
    '    fmt.Println(value)',
    '    return nil',
    '}',
    '',
    'func main() {',
    '    _ = handleRequest()',
    '}',
].join('\n');

const LSP_FIXTURE = {
    filePath: FILE_PATH,
    content: FILE_CONTENT,
    diagnostics: [
        { line: 9, severity: 'error', message: 'fmt.Println should be guarded' },
        { line: 14, severity: 'warning', message: 'handleRequest usage should be checked' },
    ],
    symbols: [
        { name: 'Service', kind: 'Struct', line: 4, col: 5 },
        { name: 'handleRequest', kind: 'Function', line: 6, col: 5 },
        { name: 'main', kind: 'Function', line: 13, col: 5 },
    ],
    references: [
        { file: FILE_PATH, line: 7, col: 1, text: 'func handleRequest() error {' },
        { file: FILE_PATH, line: 15, col: 9, text: '    _ = handleRequest()' },
    ],
    grep: {
        handleRequest: [
            { file: FILE_PATH, line: 7, col: 1, text: 'func handleRequest() error {' },
            { file: FILE_PATH, line: 15, col: 9, text: '    _ = handleRequest()' },
        ],
    },
};

async function installIdeLspMock(page) {
    await page.addInitScript(({ callApiId, fixture }) => {
        const install = () => {
            const backend = globalThis.__AO_E2E_BACKEND__;
            if (!backend || typeof backend.byId !== 'function' || backend.__AO_LSP_GUI_PATCHED__) return false;
            const originalById = backend.byId.bind(backend);
            backend.__AO_LSP_GUI_PATCHED__ = true;
            backend.byId = async (methodId, ...args) => {
                if (methodId !== callApiId) return originalById(methodId, ...args);
                const [method, rawParams = {}] = args;
                const params = rawParams && typeof rawParams === 'object' ? rawParams : {};

                if (method === 'lsp/gui_file') {
                    if (params.action === 'open_file') return { ok: true };
                    if (params.action === 'read_file') return params.file_path === fixture.filePath ? fixture.content : '';
                    if (params.action === 'diagnostics') return fixture.diagnostics;
                }
                if (method === 'lsp/gui_structure' && params.action === 'document_symbol') return fixture.symbols;
                if (method === 'lsp/gui_inspect' && params.action === 'hover') {
                    if (Number(params.line) === 6) return { contents: { value: 'func handleRequest() error' } };
                    if (Number(params.line) === 13) return { contents: { value: 'func main()' } };
                    return {};
                }
                if (method === 'lsp/gui_xref' && params.action === 'references') return fixture.references;
                if (method === 'lsp/gui_grep' && params.action === 'text_search') return fixture.grep[(params.query || '').toString()] || [];
                return originalById(methodId, ...args);
            };
            return true;
        };

        if (!install()) {
            const timer = setInterval(() => {
                if (install()) clearInterval(timer);
            }, 0);
        }
    }, { callApiId: CALL_API_METHOD_ID, fixture: LSP_FIXTURE });
}

test('LSP IDE page supports P2 syntax highlighting, hover, refs, term, and locked CSS markers', async ({ page }) => {
    await installMockBackend(page, {
        threads: [{ id: 'thread-ide-1', name: 'IDE 测试线程', alias: 'IDE 测试线程', cwd: '/workspace/go-project' }],
        activeThreadId: 'thread-ide-1',

    });
    await installIdeLspMock(page);

    await page.goto('/');
    await page.getByTestId('nav-ide').click();
    await expect(page.getByTestId('lsp-ide-page')).toBeVisible();

    await page.getByTestId('lsp-file-path-input').fill(FILE_PATH);
    await page.getByTestId('lsp-open-btn').click();

    await expect(page.getByTestId('lsp-code-table')).toBeVisible();
    await expect(page.locator('[data-testid="lsp-line-7"] .hljs')).toBeVisible();
    await expect(page.getByTestId('lsp-line-9')).toHaveClass(/empty-line/);

    const errorLineNum = page.getByTestId('lsp-line-10').locator('td').first();
    const warnLineNum = page.getByTestId('lsp-line-15').locator('td').first();
    const errorMarker = await errorLineNum.evaluate((el) => getComputedStyle(el, '::after').content.split('"').join(''));
    const warnMarker = await warnLineNum.evaluate((el) => getComputedStyle(el, '::after').content.split('"').join(''));
    expect(errorMarker).toBe('E');
    expect(errorMarker).toBe('E');
    expect(warnMarker).toBe('W');

    await page.getByTestId('lsp-line-7').hover();
    await expect(page.getByTestId('lsp-hover-popover')).toContainText('handleRequest');
    await page.mouse.move(1, 1);
    await expect(page.getByTestId('lsp-hover-popover')).toBeHidden();

    await expect(page.getByTestId('lsp-tab-search')).toBeVisible();
    await expect(page.getByTestId('lsp-tab-refs')).toBeVisible();
    await expect(page.getByTestId('lsp-tab-term')).toBeVisible();

    await page.locator('[data-testid^="lsp-symbol-"]').filter({ hasText: 'handleRequest' }).first().click();
    await page.getByTestId('lsp-tab-refs').click();
    await expect(page.getByTestId('lsp-ref-0')).toBeVisible();
    await page.getByTestId('lsp-ref-1').click();
    await expect(page.getByTestId('lsp-cursor-info')).toContainText('service.go:15:9');

    await page.getByTestId('lsp-tab-search').click();
    await page.getByTestId('lsp-search-input').fill('handleRequest');
    await page.getByTestId('lsp-search-btn').click();
    await expect(page.getByTestId('lsp-result-0')).toBeVisible();
    await page.evaluate(() => window['__AO_VISUAL_IDE__'].runAction({ action: 'test', command: 'go test ./...' }));
    await expect(page.getByTestId('lsp-term-panel')).toBeVisible();
    await expect(page.getByTestId('lsp-term-output')).toContainText('P2 placeholder: awaiting bridge support');
});
