/**
 * LSP GUI API helpers — thin wrappers around callAPI for the IDE page.
 *
 * All methods return normalised results; errors are caught and returned
 * as { error: string } objects so the UI can render inline feedback.
 */
import { callAPI } from './api.js';

// ── File Operations ────────────────────────────────────────────

/**
 * Read a file with line-numbered content.
 * @param {string} filePath  Absolute path
 * @param {number} [offset=0] Start line for paging (0-based page offset, not a coordinate)
 * @param {number} [limit=150] Max lines
 * @returns {Promise<object>}
 */
export async function lspReadFile(filePath, offset = 0, limit = 150) {
  return safeCall('lsp/gui_file', {
    action: 'read_file',
    file_path: filePath,
    offset,
    limit,
  });
}

/**
 * Open / sync a file in the LSP server.
 * @param {string} filePath
 * @returns {Promise<object>}
 */
export async function lspOpenFile(filePath) {
  return safeCall('lsp/gui_file', {
    action: 'open_file',
    file_path: filePath,
  });
}

/**
 * Get diagnostics for a file or workspace.
 * @param {string} [filePath]
 * @returns {Promise<object>}
 */
export async function lspDiagnostics(filePath) {
  return safeCall('lsp/gui_file', {
    action: 'diagnostics',
    file_path: filePath || '',
  });
}

// ── Search ─────────────────────────────────────────────────────

/**
 * Text or regex search across the workspace.
 * @param {string} query
 * @param {object} [opts]
 * @param {string} [opts.path]        Search root
 * @param {string} [opts.glob]        Glob filter
 * @param {boolean} [opts.regex]      Regex mode
 * @param {boolean} [opts.caseSensitive]
 * @param {number}  [opts.maxResults]
 * @returns {Promise<object>}
 */
export async function lspGrep(query, opts = {}) {
  return safeCall('lsp/gui_grep', {
    action: 'text_search',
    query,
    path: opts.path || '',
    glob: opts.glob || '',
    regex: opts.regex || false,
    case_sensitive: opts.caseSensitive ?? false,
    max_results: opts.maxResults || 30,
  });
}

/**
 * AST-based symbol search.
 * @param {string} symbol
 * @param {string} [language]
 * @returns {Promise<object>}
 */
export async function lspAstSearch(symbol, language) {
  return safeCall('lsp/gui_grep', {
    action: 'ast_search',
    symbol,
    language: language || '',
  });
}

// ── Structure ──────────────────────────────────────────────────

/**
 * Get document symbols (functions, classes, etc.) for a file.
 * @param {string} filePath
 * @returns {Promise<object>}
 */
export async function lspDocumentSymbols(filePath) {
  return safeCall('lsp/gui_structure', {
    action: 'document_symbol',
    file_path: filePath,
  });
}

/**
 * Search workspace-wide symbols.
 * @param {string} query
 * @returns {Promise<object>}
 */
export async function lspWorkspaceSymbols(query) {
  return safeCall('lsp/gui_structure', {
    action: 'workspace_symbol',
    query,
  });
}

// ── Inspect ────────────────────────────────────────────────────

/**
 * Hover info at a position.
 * @param {string} filePath
 * @param {number} line     0-based
 * @param {number} column   0-based
 * @returns {Promise<object>}
 */
export async function lspHover(filePath, line, column) {
  return safeCall('lsp/gui_inspect', {
    action: 'hover',
    file_path: filePath,
    line,
    column,
  });
}

/**
 * Go-to-definition at a position.
 * @param {string} filePath
 * @param {number} line     0-based
 * @param {number} column   0-based
 * @returns {Promise<object>}
 */
export async function lspDefinition(filePath, line, column) {
  return safeCall('lsp/gui_inspect', {
    action: 'definition',
    file_path: filePath,
    line,
    column,
  });
}

/**
 * Find references at a position.
 * @param {string} filePath
 * @param {number} line     0-based
 * @param {number} column   0-based
 * @returns {Promise<object>}
 */
export async function lspReferences(filePath, line, column) {
  return safeCall('lsp/gui_xref', {
    action: 'references',
    file_path: filePath,
    line,
    column,
  });
}

// ── Internal ───────────────────────────────────────────────────

async function safeCall(method, params) {
  try {
    const result = await callAPI(method, params);
    return { ok: true, data: result };
  } catch (err) {
    return { ok: false, error: String(err?.message || err) };
  }
}
