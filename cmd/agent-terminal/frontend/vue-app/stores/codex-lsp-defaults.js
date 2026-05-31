// @ts-nocheck
const CODEX_LSP_ENABLED_TOOLS = Object.freeze([
  'file',
  'grep',
  'inspect',
  'xref',
  'structure',
  'edit',
  'completion',
]);

const CODEX_LSP_MCP_TOOLS = Object.freeze(CODEX_LSP_ENABLED_TOOLS.map((name) => `mcp__lsp__${name}`));

function mergeStringDefaults(existing, defaults) {
  const out = [];
  const seen = new Set();
  for (const value of [...(Array.isArray(existing) ? existing : []), ...defaults]) {
    const item = (value || '').toString().trim();
    if (!item || seen.has(item)) continue;
    seen.add(item);
    out.push(item);
  }
  return out;
}

export function withCodexLspToolDefaults(config) {
  const base = config && typeof config === 'object' && !Array.isArray(config) ? config : {};
  return {
    ...base,
    enabledTools: mergeStringDefaults(base.enabledTools, CODEX_LSP_ENABLED_TOOLS),
    mcpTools: mergeStringDefaults(base.mcpTools, CODEX_LSP_MCP_TOOLS),
  };
}
