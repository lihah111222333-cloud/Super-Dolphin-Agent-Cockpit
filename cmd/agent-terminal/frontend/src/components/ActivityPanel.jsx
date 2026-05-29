import React, { useState, useMemo, useCallback } from 'react';
import * as Vue from '../lib/vue.esm-browser.prod.js';

const LSP_TOOL_NAMES = [
  'grep',
  'file',
  'inspect',
  'xref',
  'structure',
  'edit',
  'completion',
  'format_preview',
];
const JSON_RENDER_TOOL_NAMES = ['json_render'];
const GO_RUN_TOOL_NAMES = ['go_run', 'code_run', 'code_run_test'];
const PLAYWRIGHT_TOOL_PREFIXES = ['mcp__playwright__', 'playwright_', 'browser_'];
const STAT_ICON_PATHS = {
  lsp: 'M8 7 3 12l5 5M16 7l5 5-5 5M13 4l-2 16',
  jsonRender: 'M9 5c-1.6 0-2 1.1-2 2.8V9c0 .9-.4 1.6-1 2 .6.4 1 1.1 1 2v1.2C7 15.9 7.4 17 9 17M15 5c1.6 0 2 1.1 2 2.8V9c0 .9.4 1.6 1 2-.6.4-1 1.1-1 2v1.2c0 1.7-.4 2.8-2 2.8',
  playwright: 'M3 6h18v12H3zM3 10h18M8 8h.01M12 8h.01M16 8h.01',
  goRun: 'M4 6h16v12H4zM10 9l5 3-5 3',
  command: 'M4 7h16v10H4zM8 11l2 2-2 2M12 15h4',
  file: 'M8 3h7l3 3v15H6V3h2zM15 3v4h4M9 13h6M9 17h6',
  tool: 'M14.5 6.5a3.4 3.4 0 0 0-4.7 4.7L4 17l3 3 5.8-5.8a3.4 3.4 0 0 0 4.7-4.7l-2.3 2.3-2.4-2.4 2.2-2.4z',
};

function normalizeToolName(name) {
  const raw = (name || '').toString().trim().toLowerCase();
  const mcpParts = raw.startsWith('mcp__') ? raw.split('__') : [];
  const withoutMCPServer = mcpParts.length >= 3 ? mcpParts.slice(2).join('__') : raw;
  const normalized = withoutMCPServer
    .replace(/[./:-]+/g, '_')
    .replace(/^functions_+/, '')
    .replace(/^function_+/, '')
    .replace(/^tools_+/, '')
    .replace(/^tool_+/, '')
    .replace(/_+/g, '_')
    .replace(/^_+|_+$/g, '');
  return canonicalLspToolName(normalized);
}

function canonicalLspToolName(name) {
  return ({
    lsp_file: 'file',
    lsp_grep: 'grep',
    lsp_inspect: 'inspect',
    lsp_xref: 'xref',
    lsp_structure: 'structure',
    lsp_edit: 'edit',
    lsp_completion: 'completion',
    lsp_format_preview: 'format_preview',
  })[name] || name;
}

function sumToolCallsByMatcher(toolMap, matcher) {
  let sum = 0;
  for (const [rawName, value] of Object.entries(toolMap || {})) {
    const name = normalizeToolName(rawName);
    if (!name || !matcher(name)) continue;
    sum += Number(value) || 0;
  }
  return sum;
}

function sumToolCallsByNames(toolMap, names) {
  const expected = new Set((names || []).map((name) => normalizeToolName(name)).filter(Boolean));
  if (expected.size === 0) return 0;
  return sumToolCallsByMatcher(toolMap, (name) => expected.has(name));
}

export function ActivityPanel({
  stats = {},
  alerts = [],
  processEvents = [],
}) {
  const [expanded, setExpanded] = useState(false);

  const toolCallMap = useMemo(() => stats?.toolCalls || {}, [stats?.toolCalls]);

  const lspCount = useMemo(() => {
    const byList = sumToolCallsByNames(toolCallMap, LSP_TOOL_NAMES);
    if (byList > 0) return byList;
    return Number(stats?.lspCalls) || 0;
  }, [toolCallMap, stats?.lspCalls]);

  const jsonRenderCount = useMemo(() => sumToolCallsByNames(toolCallMap, JSON_RENDER_TOOL_NAMES), [toolCallMap]);

  const playwrightCount = useMemo(() => sumToolCallsByMatcher(
    toolCallMap,
    (name) => PLAYWRIGHT_TOOL_PREFIXES.some((prefix) => name.startsWith(prefix)),
  ), [toolCallMap]);

  const goRunCount = useMemo(() => sumToolCallsByNames(toolCallMap, GO_RUN_TOOL_NAMES), [toolCallMap]);
  const cmdCount = useMemo(() => Number(stats?.commands) || 0, [stats?.commands]);
  const fileCount = useMemo(() => Number(stats?.fileEdits) || 0, [stats?.fileEdits]);

  const totalTools = useMemo(() => {
    let sum = 0;
    for (const [, v] of Object.entries(toolCallMap)) {
      sum += Number(v) || 0;
    }
    return sum;
  }, [toolCallMap]);

  const toolCallEntries = useMemo(() => {
    const merged = {};
    for (const [raw, value] of Object.entries(toolCallMap)) {
      const short = normalizeToolName(raw) || raw;
      merged[short] = (merged[short] || 0) + (Number(value) || 0);
    }
    return Object.entries(merged)
      .map(([name, count]) => ({ name, count }))
      .filter((entry) => entry.count > 0)
      .sort((a, b) => b.count - a.count);
  }, [toolCallMap]);

  const recentAlerts = useMemo(() => {
    const items = alerts || [];
    return items.slice(-5).reverse();
  }, [alerts]);

  const recentProcessEvents = useMemo(() => {
    const items = Array.isArray(processEvents) ? processEvents : [];
    return items.slice(0, 12);
  }, [processEvents]);

  const hasAlerts = recentAlerts.length > 0;
  const hasProcessEvents = recentProcessEvents.length > 0;

  const statItems = useMemo(() => [
    { key: 'lsp', label: 'LSP (8 tools)', className: 'stat-lsp', value: lspCount },
    { key: 'jsonRender', label: 'JSON-Render', className: 'stat-json-render', value: jsonRenderCount },
    { key: 'playwright', label: 'Playwright', className: 'stat-playwright', value: playwrightCount },
    { key: 'goRun', label: 'go-run', className: 'stat-go-run', value: goRunCount },
    { key: 'command', label: '命令', className: 'stat-cmd', value: cmdCount },
    { key: 'file', label: '文件', className: 'stat-file', value: fileCount },
    { key: 'tool', label: '工具', className: 'stat-tool', value: totalTools },
  ], [lspCount, jsonRenderCount, playwrightCount, goRunCount, cmdCount, fileCount, totalTools]);

  const toggleExpand = useCallback(() => {
    setExpanded((prev) => !prev);
  }, []);

  const statIconPath = useCallback((key) => {
    const name = (key || '').toString().trim();
    return STAT_ICON_PATHS[name] || STAT_ICON_PATHS.tool;
  }, []);

  const statTooltip = useCallback((item) => {
    const label = (item?.label || '').toString().trim();
    const value = Number(item?.value) || 0;
    return `${label}: ${value}`;
  }, []);

  const alertIcon = useCallback((level) => {
    if (level === 'error') return '✗';
    if (level === 'warning' || level === 'stall') return '⚠';
    return '●';
  }, []);

  const alertClass = useCallback((level) => {
    if (level === 'error') return 'alert-error';
    if (level === 'warning' || level === 'stall') return 'alert-warning';
    return 'alert-info';
  }, []);

  const processIcon = useCallback((status) => {
    if (status === 'done') return '✓';
    if (status === 'active') return '●';
    return '•';
  }, []);

  const processClass = useCallback((status) => {
    if (status === 'done') return 'alert-info';
    if (status === 'failed') return 'alert-error';
    return 'alert-warning';
  }, []);

  const isCommandEntry = useCallback((entry) => {
    const kind = (entry?.kind || '').toString().trim();
    if (kind === 'command') return true;
    if (kind === 'tool' || kind === 'file' || kind === 'approval' || kind === 'thinking') return false;
    if ((entry?.command || '').toString().trim()) return true;
    if ((entry?.output || '').toString().trim()) return true;
    if (Number.isFinite(Number(entry?.exitCode))) return true;
    const msg = (entry?.message || '').toString();
    if (!msg) return false;
    if (msg.includes('命令执行')) return true;
    if (msg.includes('Running command') || msg.includes('Ran command') || msg.includes('Errored command')) return true;
    if (msg.includes('\n$ ') || msg.startsWith('$ ')) return true;
    return false;
  }, []);

  const commandStatusText = useCallback((entry) => {
    const status = (entry?.status || '').toString().trim();
    if (status === 'active') return '命令执行中';
    if (status === 'failed') return '命令执行失败';
    const msg = (entry?.message || '').toString();
    if (msg.includes('命令执行中') || msg.includes('Running command')) return '命令执行中';
    if (msg.includes('执行失败') || msg.includes('Errored command')) return '命令执行失败';
    return '已执行命令';
  }, []);

  const commandStatusIcon = useCallback((entry) => {
    const status = (entry?.status || '').toString().trim();
    if (status === 'active') return '◌';
    if (status === 'failed') return '✕';
    return '✓';
  }, []);

  const commandStatusIconClass = useCallback((entry) => {
    const status = (entry?.status || '').toString().trim();
    if (status === 'active') return 'ran-command-card__icon--running ran-command-card__icon--spinning';
    if (status === 'failed') return 'ran-command-card__icon--error';
    return 'ran-command-card__icon--done';
  }, []);

  const commandTitle = useCallback((entry) => {
    const title = (entry?.title || entry?.message || '').toString().trim();
    if (title.startsWith('$ ')) return title;
    const command = (entry?.command || '').toString().trim();
    if (command) return `$ ${command}`;
    if (title.includes('\n')) {
      const lines = title.split('\n').map((line) => line.trim()).filter(Boolean);
      const candidate = lines.find((line) => line.startsWith('$ '));
      if (candidate) return candidate;
    }
    if (title && !title.includes('命令执行')) return title;
    return '终端命令';
  }, []);

  const commandOutput = useCallback((entry) => {
    const output = (entry?.output || '').toString();
    if (output.trim()) return output;
    const msg = (entry?.message || '').toString();
    if (!msg.includes('\n')) return '';
    const lines = msg.split('\n').map((line) => line.toString());
    if (lines.length <= 1) return '';
    const payload = lines.slice(1).filter((line) => !line.trim().startsWith('$ ')).join('\n').trim();
    return payload;
  }, []);

  const commandExitText = useCallback((entry) => {
    const code = Number(entry?.exitCode);
    if (!Number.isFinite(code)) return '';
    return `退出码 ${Math.trunc(code)}`;
  }, []);

  return (
    <div className={`activity-panel ${expanded ? 'expanded' : ''}`}>
      <div className="activity-stats" onClick={toggleExpand} title="点击展开工具详情">
        {statItems.map((item) => (
          <span
            key={item.key}
            className={`stat stat-icon-item ${item.className}`}
          >
            <span className="stat-icon" title={statTooltip(item)} role="img" aria-label={item.label}>
              <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
                <path d={statIconPath(item.key)}></path>
              </svg>
            </span>
            <strong>{item.value}</strong>
          </span>
        ))}
      </div>

      {expanded && toolCallEntries.length > 0 && (
        <div className="activity-tool-detail">
          {toolCallEntries.map((entry) => (
            <span key={entry.name} className="tool-entry">
              {entry.name}:<strong>{entry.count}</strong>
            </span>
          ))}
        </div>
      )}

      <div className={`activity-alerts ${(!hasAlerts && !hasProcessEvents) ? 'empty' : ''}`}>
        {hasProcessEvents && (
          <>
            {recentProcessEvents.map((entry, idx) => {
              const isCmd = isCommandEntry(entry);
              return (
                <div
                  key={`${entry.id || idx}-${idx}`}
                  className={`alert-line ${processClass(entry.status)} ${isCmd ? 'alert-line-command' : ''}`}
                >
                  <span className="alert-time">{entry.time}</span>
                  {isCmd ? (
                    <div className="activity-command-entry ran-command-card">
                      <div className="ran-command-card__header">
                        <span className="ran-command-card__status">{commandStatusText(entry)}</span>
                      </div>
                      <div className="ran-command-card__main-row">
                        <span className={`ran-command-card__icon ${commandStatusIconClass(entry)}`} aria-hidden="true">
                          {commandStatusIcon(entry)}
                        </span>
                        <span className="ran-command-card__title" title={commandTitle(entry)}>
                          {commandTitle(entry)}
                        </span>
                      </div>
                      {commandOutput(entry) && (
                        <div className="ran-command-card__details ran-command-card__details--open">
                          <pre className="ran-command-card__output activity-command-output">{commandOutput(entry)}</pre>
                        </div>
                      )}
                      <div className="ran-command-card__footer">
                        <span className="ran-command-card__auto-exec">终端命令</span>
                        <div className="ran-command-card__footer-right">
                          {entry.status === 'active' && <span className="ran-command-card__cancel-btn">运行中...</span>}
                          {commandExitText(entry) && <span className="ran-command-card__exit-code">{commandExitText(entry)}</span>}
                        </div>
                      </div>
                    </div>
                  ) : (
                    <>
                      <span className="alert-icon">{processIcon(entry.status)}</span>
                      <span className={`alert-msg ${entry.multiline ? 'alert-msg-wrap' : ''}`}>{entry.message}</span>
                    </>
                  )}
                </div>
              );
            })}
          </>
        )}
        {hasAlerts && (
          <>
            {recentAlerts.map((alert) => (
              <div
                key={alert.id}
                className={`alert-line ${alertClass(alert.level)}`}
              >
                <span className="alert-time">{alert.time}</span>
                <span className="alert-icon">{alertIcon(alert.level)}</span>
                <span className="alert-msg">{alert.message}</span>
              </div>
            ))}
          </>
        )}
        {!hasAlerts && !hasProcessEvents && <div className="alert-empty">无告警</div>}
      </div>
    </div>
  );
}

ActivityPanel.setup = function(props) {
  const toolCallMap = Vue.computed(() => props.stats?.toolCalls || {});

  const lspCount = Vue.computed(() => {
    const byList = sumToolCallsByNames(toolCallMap.value, LSP_TOOL_NAMES);
    if (byList > 0) return byList;
    return Number(props.stats?.lspCalls) || 0;
  });

  const jsonRenderCount = Vue.computed(() => sumToolCallsByNames(toolCallMap.value, JSON_RENDER_TOOL_NAMES));

  const playwrightCount = Vue.computed(() => sumToolCallsByMatcher(
    toolCallMap.value,
    (name) => PLAYWRIGHT_TOOL_PREFIXES.some((prefix) => name.startsWith(prefix)),
  ));

  const goRunCount = Vue.computed(() => sumToolCallsByNames(toolCallMap.value, GO_RUN_TOOL_NAMES));
  const cmdCount = Vue.computed(() => Number(props.stats?.commands) || 0);
  const fileCount = Vue.computed(() => Number(props.stats?.fileEdits) || 0);

  const totalTools = Vue.computed(() => {
    let sum = 0;
    for (const [, v] of Object.entries(toolCallMap.value)) {
      sum += Number(v) || 0;
    }
    return sum;
  });

  const toolCallEntries = Vue.computed(() => {
    const merged = {};
    for (const [raw, value] of Object.entries(toolCallMap.value)) {
      const short = normalizeToolName(raw) || raw;
      merged[short] = (merged[short] || 0) + (Number(value) || 0);
    }
    return Object.entries(merged)
      .map(([name, count]) => ({ name, count }))
      .filter((entry) => entry.count > 0)
      .sort((a, b) => b.count - a.count);
  });

  const statItems = Vue.computed(() => [
    { key: 'lsp', label: 'LSP (8 tools)', className: 'stat-lsp', value: lspCount.value },
    { key: 'jsonRender', label: 'JSON-Render', className: 'stat-json-render', value: jsonRenderCount.value },
    { key: 'playwright', label: 'Playwright', className: 'stat-playwright', value: playwrightCount.value },
    { key: 'goRun', label: 'go-run', className: 'stat-go-run', value: goRunCount.value },
    { key: 'command', label: '命令', className: 'stat-cmd', value: cmdCount.value },
    { key: 'file', label: '文件', className: 'stat-file', value: fileCount.value },
    { key: 'tool', label: '工具', className: 'stat-tool', value: totalTools.value },
  ]);

  return {
    lspCount,
    totalTools,
    toolCallEntries,
    statItems,
  };
};
