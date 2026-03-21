/**
 * LspIdePage — AI-Friendly LSP IDE with 4 panels.
 *
 * Layout:
 *   ┌──────────┬──────────────┬───────────┐
 *   │ Tree     │ Code Viewer  │ Inspector │
 *   │ (symbols)│ (file content│ (hover /  │
 *   │          │  + line nums)│  typedef) │
 *   ├──────────┴──────────────┴───────────┤
 *   │ Search Results (grep output)        │
 *   ├─────────────────────────────────────┤
 *   │ Status Bar                          │
 *   └─────────────────────────────────────┘
 */
// @ts-nocheck
import {
    ref,
    reactive,
    computed,
    onMounted,
    onBeforeUnmount,
    watch,
    nextTick,
} from '../../lib/vue.esm-browser.prod.js';

import {
    lspReadFile,
    lspOpenFile,
    lspDiagnostics,
    lspGrep,
    lspDocumentSymbols,
    lspHover,
    lspReferences,
    lspDefinition,
} from '../services/lsp-api.js';

import { highlightCode } from '../utils/code-highlight.js';

import { logDebug, logInfo, logWarn } from '../services/log.js';
import { selectProjectDir } from '../services/api.js';

// ── Symbol kind icons (LSP spec) ──────────────────────────────
const SYMBOL_ICONS = {
    File: '📄', Module: '📦', Namespace: '📁', Package: '📦',
    Class: '🔶', Method: '⚡', Property: '🔹', Field: '🔹',
    Constructor: '🏗️', Enum: '🔢', Interface: '🔷', Function: '⚡',
    Variable: '📌', Constant: '🔒', String: '📝', Number: '#',
    Boolean: '☑️', Array: '[]', Object: '{}', Key: '🔑',
    Null: '∅', EnumMember: '·', Struct: '🔶', Event: '⚡',
    Operator: '±', TypeParameter: 'T',
};

function symbolIcon(kind) {
    return SYMBOL_ICONS[kind] || '·';
}

export const LspIdePage = {
    name: 'LspIdePage',
    props: {
        projectCwd: { type: String, default: '' },
    },
    setup(props) {
        // ── State ─────────────────────────────────────────────────
        const filePathInput = ref('');
        const currentFile = ref('');
        const codeLines = ref([]);         // [{ num, text, html }]}]
        const codeOffset = ref(0);
        const totalLines = ref(0);
        const highlightedLine = ref(-1);

        const symbols = ref([]);           // [{ name, kind, line, col }]
        const symbolFilter = ref('');

        const searchQuery = ref('');
        const searchResults = ref([]);     // [{ file, line, col, text }]
        const activeResultIdx = ref(-1);
        const activeBottomTab = ref('search');
        const refsResults = ref([]);
        const activeRefIdx = ref(-1);

        const terminalOutput = reactive({
            command: '',
            stdout: '',
            stderr: '',
            exit_code: null,
            duration: 0,
            status: 'idle',
            warning: '',
        });

        const selectionState = reactive({
            file: '',
            line: -1,
            column: 0,
            symbol: '',
        });

        const inspectorContent = ref('');
        const inspectorLabel = ref('');

        const diagnostics = reactive({});  // { [file]: [{ line, severity, message }] }

        const statusText = ref('就绪');
        const statusState = ref('ok');     // ok | error | loading
        const cursorInfo = ref('');

        const codeViewerEl = ref(null);
        const isLoading = ref(false);
        const hoverState = reactive({
            visible: false,
            x: 0,
            y: 0,
            content: '',
            line: -1,
        });
        let hoverTimer = 0;
        let hoverRequestSeq = 0;


        // ── Computed ──────────────────────────────────────────────
        const filteredSymbols = computed(() => {
            const q = symbolFilter.value.toLowerCase();
            if (!q) return symbols.value;
            return symbols.value.filter(s =>
                s.name.toLowerCase().includes(q)
            );
        });

        const currentDiagnostics = computed(() => {
            return diagnostics[currentFile.value] || [];
        });

        const lineDiagMap = computed(() => {
            const map = {};
            for (const d of currentDiagnostics.value) {
                if (!map[d.line]) map[d.line] = [];
                map[d.line].push(d);
            }
            return map;
        });

        // ── Actions ──────────────────────────────────────────────

        async function openFile(filePath) {
            if (!filePath) return;
            const path = filePath.trim();
            if (!path) return;

            logInfo('lsp-ide', 'open_file', { path });
            isLoading.value = true;
            setStatus('loading', `打开 ${basename(path)}...`);

            // 1. Open in LSP server
            await lspOpenFile(path);

            // 2. Read content
            const result = await lspReadFile(path, 0, 300);
            if (!result.ok) {
                setStatus('error', `读取失败: ${result.error}`);
                isLoading.value = false;
                return;
            }

            currentFile.value = path;
            filePathInput.value = path;
            parseFileContent(result.data, path);

            // 3. Load symbols
            const symResult = await lspDocumentSymbols(path);
            if (symResult.ok) {
                parseSymbols(symResult.data);
            }

            // 4. Load diagnostics
            const diagResult = await lspDiagnostics(path);
            if (diagResult.ok) {
                parseDiagnostics(path, diagResult.data);
            }

            setStatus('ok', `${basename(path)} — ${totalLines.value} 行`);
            isLoading.value = false;
        }

        function parseFileContent(data, filePath = '') {
            // The LSP tool returns content as a string with line numbers
            const raw = typeof data === 'string' ? data : (data?.content || data?.text || JSON.stringify(data, null, 2));
            const lines = raw.split('\n');
            const highlightedLines = highlightCode(raw, filePath);
            codeLines.value = lines.map((text, i) => ({
                num: i + 1,
                text,
                html: highlightedLines[i] || '',
            }));
            totalLines.value = lines.length;
            codeOffset.value = 0;
        }

        function parseSymbols(data) {
            // Normalise various LSP symbol response formats
            const raw = typeof data === 'string' ? tryParseJSON(data) : data;
            if (Array.isArray(raw)) {
                symbols.value = raw.map(normalizeSymbol).filter(Boolean);
            } else if (raw?.symbols) {
                symbols.value = raw.symbols.map(normalizeSymbol).filter(Boolean);
            } else {
                symbols.value = [];
            }
        }

        function normalizeSymbol(s) {
            if (!s) return null;
            return {
                name: s.name || s.label || '?',
                kind: s.kind || s.type || 'Symbol',
                line: s.range?.start?.line ?? s.line ?? s.lsp_line ?? 0,
                col: s.range?.start?.character ?? s.col ?? s.lsp_col ?? 0,
                detail: s.detail || '',
            };
        }

        function parseDiagnostics(filePath, data) {
            const raw = typeof data === 'string' ? tryParseJSON(data) : data;
            const items = /** @type {Array<any>} */ ([]);
            const list = Array.isArray(raw) ? raw : (raw?.diagnostics || []);
            for (const d of list) {
                items.push({
                    line: d.range?.start?.line ?? d.line ?? 0,
                    severity: d.severity || 'error',
                    message: d.message || '',
                });
            }
            diagnostics[filePath] = items;
        }

        function onFilePathKeydown(e) {
            if (e.key === 'Enter') {
                openFile(filePathInput.value);
            }
        }

        function onOpenClick() {
            openFile(filePathInput.value);
        }

        async function onBrowseClick() {
            try {
                const dir = await selectProjectDir();
                if (dir) {
                    filePathInput.value = dir.endsWith('/') ? dir : dir + '/';
                    logInfo('ide', 'browse.selected', { dir });
                }
            } catch (err) {
                logWarn('ide', 'browse.failed', { error: err });
            }
        }

        async function onSymbolClick(sym) {
            highlightedLine.value = sym.line + 1; // line is 0-based
            scrollToLine(sym.line + 1);
            cursorInfo.value = `行 ${sym.line + 1}, 列 ${sym.col + 1}`;
            updateSelection({
                file: currentFile.value,
                line: sym.line,
                column: sym.col,
                symbol: sym.name,
            });

            // Trigger hover
            const result = await lspHover(currentFile.value, sym.line, sym.col);
            if (result.ok) {
                inspectorLabel.value = `Hover · ${sym.name}`;
                inspectorContent.value = formatInspectorData(result.data);
            }

            if (activeBottomTab.value === 'refs') {
                await loadReferencesForSelection();
            }
        }

        async function onLineClick(lineNum) {
            highlightedLine.value = lineNum;
            cursorInfo.value = `行 ${lineNum}`;
            const currentLine = codeLines.value.find((line) => line.num === lineNum);
            updateSelection({
                file: currentFile.value,
                line: lineNum - 1,
                column: Math.max(hoverColumnForLine(currentLine), 0),
                symbol: '',
            });
            if (activeBottomTab.value === 'refs') {
                await loadReferencesForSelection();
            }
        }

        function clearHoverTimer() {
            if (!hoverTimer) return;
            window.clearTimeout(hoverTimer);
            hoverTimer = 0;
        }

        function hideHover() {
            hoverState.visible = false;
            hoverState.content = '';
            hoverState.line = -1;
        }

        function hoverColumnForLine(line) {
            const text = (line?.text || '').toString();
            if (!text.trim()) return -1;
            const symbolIndex = text.search(/[A-Za-z_]/);
            if (symbolIndex >= 0) return symbolIndex;
            return text.search(/\S/);
        }

        function normalizePositionValue(value, fallback = 0) {
            const num = Number(value);
            return Number.isFinite(num) ? Math.trunc(num) : fallback;
        }

        function updateSelection(payload = {}) {
            selectionState.file = (payload.file || currentFile.value || '').toString();
            selectionState.line = normalizePositionValue(payload.line, selectionState.line);
            selectionState.column = Math.max(0, normalizePositionValue(payload.column, selectionState.column));
            selectionState.symbol = (payload.symbol || '').toString();
        }

        function normalizeLocationResult(r) {
            const line = normalizePositionValue(r.line ?? r.lsp_line ?? 0, 0);
            const col = normalizePositionValue(r.col ?? r.lsp_col ?? 0, 0);
            return {
                file: r.file || r.path || r.file_path || '?',
                line,
                col,
                text: r.text || r.content || r.match || '',
                displayLine: r.line != null ? line : line + 1,
                displayCol: r.col != null ? col : col + 1,
            };
        }

        async function showLineHover(line, mouseEvent) {
            if (!currentFile.value || !line || !line.text || !line.text.trim()) {
                hideHover();
                return;
            }

            const column = hoverColumnForLine(line);
            if (column < 0) {
                hideHover();
                return;
            }

            const requestSeq = ++hoverRequestSeq;
            const result = await lspHover(currentFile.value, line.num - 1, column);
            if (requestSeq !== hoverRequestSeq || !result.ok) {
                hideHover();
                return;
            }

            const content = formatInspectorData(result.data).trim();
            if (!content || content === '{}' || content === 'null' || content === 'undefined') {
                hideHover();
                return;
            }

            const rect = mouseEvent?.currentTarget?.getBoundingClientRect?.();
            const anchorX = rect ? rect.left + 96 : (mouseEvent?.clientX ?? 24);
            const anchorY = rect ? rect.top + 8 : (mouseEvent?.clientY ?? 24);
            hoverState.visible = true;
            hoverState.x = Math.max(12, Math.min(anchorX + 12, window.innerWidth - 424));
            hoverState.y = Math.max(12, Math.min(anchorY + 18, window.innerHeight - 224));
            hoverState.content = content;
            hoverState.line = line.num;
        }

        function onLineMouseEnter(event, line) {
            clearHoverTimer();
            if (!line?.text || !line.text.trim()) {
                hideHover();
                return;
            }
            hoverTimer = window.setTimeout(() => {
                showLineHover(line, event).catch(() => hideHover());
            }, 600);
        }

        function onLineMouseLeave() {
            clearHoverTimer();
            hoverRequestSeq += 1;
            hideHover();
        }

        function parseReferenceResults(data) {
            const raw = typeof data === 'string' ? tryParseJSON(data) : data;
            const list = Array.isArray(raw) ? raw : (raw?.references || raw?.results || raw?.matches || []);
            refsResults.value = list.map(normalizeLocationResult);
            activeRefIdx.value = -1;
        }

        // Shared navigation logic for search results and reference clicks.
        async function navigateToLocation(result, idx, activeIdx) {
            activeIdx.value = idx;
            if (result.file !== currentFile.value) {
                await openFile(result.file);
            }
            highlightedLine.value = result.displayLine;
            nextTick(() => scrollToLine(result.displayLine));
            updateSelection({
                file: result.file,
                line: Math.max(result.displayLine - 1, 0),
                column: Math.max(result.displayCol - 1, 0),
                symbol: '',
            });
            cursorInfo.value = `${basename(result.file)}:${result.displayLine}:${result.displayCol}`;
        }

        async function onRefClick(result, idx) {
            await navigateToLocation(result, idx, activeRefIdx);
        }

        async function loadReferencesForSelection() {
            const filePath = selectionState.file || currentFile.value;
            if (!filePath || selectionState.line < 0) {
                refsResults.value = [];
                activeRefIdx.value = -1;
                return;
            }

            setStatus('loading', '加载引用...');
            const result = await lspReferences(filePath, selectionState.line, selectionState.column || 0);
            if (!result.ok) {
                refsResults.value = [];
                activeRefIdx.value = -1;
                setStatus('error', `引用失败: ${result.error}`);
                return;
            }

            parseReferenceResults(result.data);
            activeBottomTab.value = 'refs';
            setStatus('ok', `找到 ${refsResults.value.length} 个引用`);
        }

        function applyTerminalOutput(payload = {}) {
            terminalOutput.command = (payload.command || '').toString();
            terminalOutput.stdout = (payload.stdout || '').toString();
            terminalOutput.stderr = (payload.stderr || '').toString();
            terminalOutput.exit_code = payload.exit_code ?? null;
            terminalOutput.duration = Number.isFinite(Number(payload.duration)) ? Math.trunc(Number(payload.duration)) : 0;
            terminalOutput.status = (payload.status || '').toString() || (payload.warning ? 'warning' : (payload.command ? 'ready' : 'idle'));
            terminalOutput.warning = (payload.warning || '').toString();
            activeBottomTab.value = 'term';
        }

        function buildTerminalText() {
            const parts = [];
            if (terminalOutput.command) parts.push(`$ ${terminalOutput.command}`);
            if (terminalOutput.stdout) parts.push(terminalOutput.stdout);
            if (terminalOutput.stderr) parts.push(terminalOutput.stderr);
            if (terminalOutput.warning) parts.push(`warning=${terminalOutput.warning}`);
            if (terminalOutput.exit_code !== null && terminalOutput.exit_code !== undefined) parts.push(`exit_code=${terminalOutput.exit_code}`);
            if (terminalOutput.duration) parts.push(`duration_ms=${terminalOutput.duration}`);
            return parts.filter(Boolean).join('\n');
        }

        function snapshotVisualIdeState() {
            return {
                file_path: currentFile.value,
                status: statusText.value,
                cursor: cursorInfo.value,
                inspector: inspectorLabel.value,
                active_bottom_tab: activeBottomTab.value,
                terminal: buildTerminalText(),
                exit_code: terminalOutput.exit_code,
            };
        }

        async function activateBottomTab(tab) {
            activeBottomTab.value = tab;
            if (tab === 'refs') {
                await loadReferencesForSelection();
            }
        }

        async function runTerminalAction(payload = {}) {
            applyTerminalOutput({
                command: (payload.command || payload.action || '').toString(),
                stdout: '',
                stderr: 'P2 placeholder: awaiting bridge support',
                exit_code: null,
                duration: Number.isFinite(Number(payload.duration)) ? Math.trunc(Number(payload.duration)) : 0,
                status: 'placeholder',
                warning: (payload.warning || 'bridge_pending').toString(),
            });
            return snapshotVisualIdeState();
        }

        async function findReferencesForVisualIde(payload = {}) {
            const nextFile = (payload.file_path || payload.path || payload.target || currentFile.value || '').toString().trim();
            if (nextFile && nextFile !== currentFile.value) {
                await openFile(nextFile);
            }

            updateSelection({
                file: nextFile || currentFile.value,
                line: Number.isFinite(Number(payload.line)) ? Math.max(0, Math.trunc(Number(payload.line))) : selectionState.line,
                column: Number.isFinite(Number(payload.column)) ? Math.max(0, Math.trunc(Number(payload.column))) : selectionState.column,
                symbol: payload.symbol || selectionState.symbol,
            });
            await loadReferencesForSelection();
            return snapshotVisualIdeState();
        }

        // ── Search ────────────────────────────────────────────────

        async function onSearch() {
            const q = searchQuery.value.trim();
            if (!q) return;

            logDebug('lsp-ide', 'search', { query: q });
            setStatus('loading', `搜索 "${q}"...`);

            const result = await lspGrep(q, {
                path: props.projectCwd || '',
                maxResults: 30,
            });

            if (!result.ok) {
                setStatus('error', `搜索失败: ${result.error}`);
                return;
            }

            parseSearchResults(result.data);
            setStatus('ok', `找到 ${searchResults.value.length} 个结果`);
        }

        function onSearchKeydown(e) {
            if (e.key === 'Enter') {
                onSearch();
            }
        }

        function parseSearchResults(data) {
            const raw = typeof data === 'string' ? tryParseJSON(data) : data;
            const list = Array.isArray(raw) ? raw : (raw?.results || raw?.matches || []);
            searchResults.value = list.map(normalizeLocationResult);
            activeResultIdx.value = -1;
        }

        async function onResultClick(result, idx) {
            await navigateToLocation(result, idx, activeResultIdx);
        }

        // ── Helpers ───────────────────────────────────────────────

        function scrollToLine(lineNum) {
            nextTick(() => {
                const el = document.querySelector(`[data-testid="lsp-line-${lineNum}"]`);
                if (el) {
                    el.scrollIntoView({ block: 'center', behavior: 'smooth' });
                }
            });
        }

        function setStatus(state, text) {
            statusState.value = state;
            statusText.value = text;
        }

        function formatInspectorData(data) {
            if (typeof data === 'string') return data;
            if (data?.contents?.value) return data.contents.value;
            if (data?.contents) return String(data.contents);
            if (data?.result) return typeof data.result === 'string' ? data.result : JSON.stringify(data.result, null, 2);
            return JSON.stringify(data, null, 2);
        }

        function lineClass(line) {
            const classes = ['lsp-code-line'];
            if ((line.text || '').trim() === '') classes.push('empty-line');
            if (line.num === highlightedLine.value) classes.push('highlighted');
            const diags = lineDiagMap.value[line.num - 1]; // diag lines are 0-based
            if (diags) {
                const hasError = diags.some(d => d.severity === 1 || d.severity === 'error');
                const hasWarn = diags.some(d => d.severity === 2 || d.severity === 'warning');
                if (hasError) classes.push('has-error', 'error-line');
                else if (hasWarn) classes.push('has-warning', 'warn-line');
            }
            return classes.join(' ');
        }


        function lineNumberClass(line) {
            const classes = ['lsp-line-num'];
            const diags = lineDiagMap.value[line.num - 1];
            if (diags) {
                const hasError = diags.some(d => d.severity === 1 || d.severity === 'error');
                const hasWarn = diags.some(d => d.severity === 2 || d.severity === 'warning');
                if (hasError) classes.push('has-error');
                else if (hasWarn) classes.push('has-warn');
            }
            return classes.join(' ');
        }

        function basename(path) {
            return path.split('/').pop() || path;
        }

        function tryParseJSON(str) {
            try { return JSON.parse(str); } catch { return str; }
        }

        // ── Lifecycle ─────────────────────────────────────────────

        onMounted(() => {
            logInfo('lsp-ide', 'page.mounted');
            if (props.projectCwd) {
                setStatus('ok', `工作区: ${props.projectCwd}`);
            }
            if (typeof window !== 'undefined') {
                const bridge = window.__AO_VISUAL_IDE__ || {};
                bridge.getState = () => snapshotVisualIdeState();
                bridge.findReferences = (payload = {}) => findReferencesForVisualIde(payload);
                bridge.setTerminalOutput = (payload = {}) => {
                    applyTerminalOutput(payload);
                    return snapshotVisualIdeState();
                };
                bridge.runAction = (payload = {}) => runTerminalAction(payload);
                bridge.activateBottomTab = (tab) => activateBottomTab((tab || '').toString() || 'search');
                window.__AO_VISUAL_IDE__ = bridge;
            }
        });

        onBeforeUnmount(() => {
            clearHoverTimer();
            hideHover();
            if (typeof window !== 'undefined' && window.__AO_VISUAL_IDE__) {
                delete window.__AO_VISUAL_IDE__.getState;
                delete window.__AO_VISUAL_IDE__.findReferences;
                delete window.__AO_VISUAL_IDE__.setTerminalOutput;
                delete window.__AO_VISUAL_IDE__.runAction;
                delete window.__AO_VISUAL_IDE__.activateBottomTab;
            }
            logInfo('lsp-ide', 'page.unmounted');
        });

        return {
            // State
            filePathInput,
            currentFile,
            codeLines,
            highlightedLine,
            symbols,
            symbolFilter,
            filteredSymbols,
            searchQuery,
            searchResults,
            activeResultIdx,
            inspectorContent,
            inspectorLabel,
            hoverState,
            activeBottomTab,
            refsResults,
            activeRefIdx,
            terminalOutput,
            statusText,
            statusState,
            cursorInfo,
            isLoading,
            // Methods
            onFilePathKeydown,
            onOpenClick,
            onBrowseClick,
            onSymbolClick,
            onLineClick,
            onLineMouseEnter,
            onLineMouseLeave,
            onSearch,
            onSearchKeydown,
            onResultClick,
            onRefClick,
            activateBottomTab,
            loadReferencesForSelection,
            buildTerminalText,
            lineClass,
            lineNumberClass,
            symbolIcon,
            basename,
        };
    },
    template: `
    <section id="page-ide" class="lsp-ide-page" data-testid="lsp-ide-page">

      <!-- ── Left: Symbol Tree ────────────────────────────── -->
      <div class="lsp-tree-panel" data-testid="lsp-tree-panel">
        <div class="lsp-tree-header">符号导航</div>
        <input
          class="lsp-tree-input"
          v-model="symbolFilter"
          placeholder="过滤符号..."
          data-testid="lsp-symbol-filter"
        />
        <div class="lsp-tree-list" data-testid="lsp-tree-list">
          <div
            v-for="(sym, i) in filteredSymbols"
            :key="sym.name + '-' + i"
            class="lsp-tree-item"
            :data-testid="'lsp-symbol-' + i"
            @click="onSymbolClick(sym)"
          >
            <span class="lsp-tree-item-icon">{{ symbolIcon(sym.kind) }}</span>
            <span>{{ sym.name }}</span>
            <span class="lsp-tree-item-kind">{{ sym.kind }}</span>
            <span class="lsp-tree-item-line">L{{ sym.line + 1 }}</span>
          </div>
          <div v-if="filteredSymbols.length === 0 && currentFile" class="lsp-loading" data-testid="lsp-tree-empty">
            暂无符号
          </div>
          <div v-if="!currentFile" class="lsp-loading" data-testid="lsp-tree-placeholder">
            打开文件后显示符号
          </div>
        </div>
      </div>

      <!-- ── Main: Code Viewer ────────────────────────────── -->
      <div class="lsp-code-panel" data-testid="lsp-code-panel">
        <div class="lsp-code-toolbar">
          <input
            class="lsp-code-path-input"
            v-model="filePathInput"
            placeholder="输入文件路径..."
            @keydown="onFilePathKeydown"
            data-testid="lsp-file-path-input"
          />
          <button
            class="lsp-code-btn"
            @click="onOpenClick"
            data-testid="lsp-open-btn"
          >打开</button>
          <button
            class="lsp-code-btn lsp-browse-btn"
            @click="onBrowseClick"
            data-testid="lsp-browse-btn"
            title="选择目录"
          >📁</button>
        </div>
        <div class="lsp-code-viewer" ref="codeViewerEl" data-testid="lsp-code-viewer">
          <div v-if="isLoading" class="lsp-loading" data-testid="lsp-code-loading">
            加载中...
          </div>
          <div v-else-if="codeLines.length === 0" class="lsp-code-empty" data-testid="lsp-code-empty">
            输入文件路径并按 Enter 打开文件
          </div>
          <table v-else class="lsp-code-table" data-testid="lsp-code-table">
            <tr
              v-for="line in codeLines"
              :key="line.num"
              :class="lineClass(line)"
              :data-testid="'lsp-line-' + line.num"
              @click="onLineClick(line.num)"
              @mouseenter="onLineMouseEnter($event, line)"
              @mouseleave="onLineMouseLeave"
            >
              <td :class="lineNumberClass(line)">{{ line.num }}</td>
              <td class="lsp-line-content" v-html="line.html"></td>
            </tr>
          </table>
        </div>
      </div>

      <!-- ── Right: Inspector ─────────────────────────────── -->
      <div class="lsp-inspector-panel" data-testid="lsp-inspector-panel">
        <div class="lsp-inspector-header">检查器</div>
        <div class="lsp-inspector-body" data-testid="lsp-inspector-body">
          <div v-if="!inspectorContent" class="lsp-inspector-empty" data-testid="lsp-inspector-empty">
            点击符号查看类型信息
          </div>
          <template v-else>
            <div class="lsp-inspector-section">
              <div class="lsp-inspector-label" data-testid="lsp-inspector-label">{{ inspectorLabel }}</div>
              <div data-testid="lsp-inspector-content">{{ inspectorContent }}</div>
            </div>
          </template>
        </div>
      </div>

      <!-- ── Bottom: Search Results ───────────────────────── -->
      <div class="lsp-results-panel" data-testid="lsp-results-panel">
        <div class="lsp-results-tabs" data-testid="lsp-results-tabs">
          <button class="lsp-results-tab" :class="{ active: activeBottomTab === 'search' }" @click="activateBottomTab('search')" data-testid="lsp-tab-search">SEARCH</button>
          <button class="lsp-results-tab" :class="{ active: activeBottomTab === 'refs' }" @click="activateBottomTab('refs')" data-testid="lsp-tab-refs">REFS</button>
          <button class="lsp-results-tab" :class="{ active: activeBottomTab === 'term' }" @click="activateBottomTab('term')" data-testid="lsp-tab-term">TERM</button>
        </div>

        <template v-if="activeBottomTab === 'search'">
          <div class="lsp-results-toolbar">
            <input
              class="lsp-results-search-input"
              v-model="searchQuery"
              placeholder="搜索代码... (Enter 执行)"
              @keydown="onSearchKeydown"
              data-testid="lsp-search-input"
            />
            <button
              class="lsp-code-btn"
              @click="onSearch"
              data-testid="lsp-search-btn"
            >搜索</button>
            <span class="lsp-results-count" v-if="searchResults.length" data-testid="lsp-results-count">
              {{ searchResults.length }} 个结果
            </span>
          </div>
          <div class="lsp-results-list" data-testid="lsp-results-list">
            <div
              v-for="(r, idx) in searchResults"
              :key="r.file + ':' + r.line + ':' + idx"
              class="lsp-result-item"
              :class="{ active: idx === activeResultIdx }"
              :data-testid="'lsp-result-' + idx"
              @click="onResultClick(r, idx)"
            >
              <span class="lsp-result-loc">{{ basename(r.file) }}:{{ r.displayLine }}:{{ r.displayCol }}</span>
              <span class="lsp-result-text">{{ r.text }}</span>
            </div>
            <div v-if="searchResults.length === 0" class="lsp-results-empty" data-testid="lsp-results-empty">
              输入关键词并按 Enter 搜索
            </div>
          </div>
        </template>

        <template v-else-if="activeBottomTab === 'refs'">
          <div class="lsp-results-toolbar">
            <button class="lsp-code-btn" @click="loadReferencesForSelection" data-testid="lsp-refs-refresh-btn">刷新</button>
            <span class="lsp-results-count" v-if="refsResults.length" data-testid="lsp-refs-count">{{ refsResults.length }} 个引用</span>
          </div>
          <div class="lsp-results-list" data-testid="lsp-refs-list">
            <div
              v-for="(r, idx) in refsResults"
              :key="r.file + ':' + r.line + ':' + idx"
              class="lsp-result-item"
              :class="{ active: idx === activeRefIdx }"
              :data-testid="'lsp-ref-' + idx"
              @click="onRefClick(r, idx)"
            >
              <span class="lsp-result-loc">{{ basename(r.file) }}:{{ r.displayLine }}:{{ r.displayCol }}</span>
              <span class="lsp-result-text">{{ r.text }}</span>
            </div>
            <div v-if="refsResults.length === 0" class="lsp-results-empty" data-testid="lsp-refs-empty">
              选择代码行或符号后查看引用
            </div>
          </div>
        </template>

        <template v-else>
          <div class="lsp-term-panel" data-testid="lsp-term-panel">
            <template v-if="terminalOutput.command || terminalOutput.stdout || terminalOutput.stderr || terminalOutput.warning">
              <div class="lsp-term-meta">
                <div class="lsp-term-command" data-testid="lsp-term-command">$ {{ terminalOutput.command || 'pending' }}</div>
                <div class="lsp-term-status" data-testid="lsp-term-status">
                  <span v-if="terminalOutput.duration">duration={{ terminalOutput.duration }}ms</span>
                  <span v-if="terminalOutput.exit_code !== null && terminalOutput.exit_code !== undefined">exit_code={{ terminalOutput.exit_code }}</span>
                  <span v-else>exit_code=pending</span>
                </div>
              </div>
              <pre class="lsp-term-output" :class="{ 'has-error': terminalOutput.stderr }" data-testid="lsp-term-output">{{ buildTerminalText() }}</pre>
            </template>
            <div v-else class="lsp-results-empty" data-testid="lsp-term-empty">
              TERM 输出将在扩展动作接入后显示
            </div>
          </div>
        </template>
      </div>

      <div
        v-if="hoverState.visible"
        class="lsp-hover-popover"
        :style="{ left: hoverState.x + 'px', top: hoverState.y + 'px' }"
        data-testid="lsp-hover-popover"
      >{{ hoverState.content }}</div>

      <!-- ── Status Bar ───────────────────────────────────── -->
      <div class="lsp-status-bar" data-testid="lsp-status-bar">
        <span
          class="lsp-status-indicator"
          :class="statusState"
          :data-testid="'lsp-status-' + statusState"
        ></span>
        <span class="lsp-status-text" data-testid="lsp-status-text">{{ statusText }}</span>
        <span class="lsp-status-cursor" v-if="cursorInfo" data-testid="lsp-cursor-info">{{ cursorInfo }}</span>
      </div>

    </section>
  `,
};
