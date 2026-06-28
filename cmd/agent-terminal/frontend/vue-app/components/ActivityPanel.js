import { computed, ref } from '../../lib/vue.esm-browser.prod.js';

// 8 个聚合 LSP 工具，对应 mcp-lsp server 当前实际暴露的方法集。
// 早期按 LSP 协议方法逐个导出（lsp_hover / lsp_definition / ...）已合并成
// 这些聚合工具；前端按 normalizeToolName 后的短名匹配。
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
const GO_RUN_TOOL_NAMES = ['go_run'];
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

/**
 * @param {unknown} name
 * @returns {string}
 */
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

/**
 * @param {Record<string, number>} toolMap
 * @param {(name: string) => boolean} matcher
 * @returns {number}
 */
function sumToolCallsByMatcher(toolMap, matcher) {
  let sum = 0;
  for (const [rawName, value] of Object.entries(toolMap || {})) {
    const name = normalizeToolName(rawName);
    if (!name || !matcher(name)) continue;
    sum += Number(value) || 0;
  }
  return sum;
}

/**
 * @param {Record<string, number>} toolMap
 * @param {string[]} names
 * @returns {number}
 */
function sumToolCallsByNames(toolMap, names) {
  const expected = new Set((names || []).map((name) => normalizeToolName(name)).filter(Boolean));
  if (expected.size === 0) return 0;
  return sumToolCallsByMatcher(toolMap, (name) => expected.has(name));
}

/**
 * ActivityPanel — 右下角活动面板，与 ComposerBar 等高。
 *
 * 两个区域:
 *   - 行为记录 (stats): LSP(19) / JSON-Render / Playwright / go-run / 命令 / 文件 / 工具
 *   - 告警日志 (alerts): 只显示高优先级事件 (error / stall / abort)
 */
export const ActivityPanel = {
  name: 'ActivityPanel',
  props: {
    /** @type {{ lspCalls: number, commands: number, fileEdits: number, toolCalls: Record<string,number> }} */
    stats: { type: Object, default: () => ({}) },
    /** @type {Array<{ id: string, time: string, level: string, message: string }>} */
    alerts: { type: Array, default: () => [] },
    /** @type {Array<{ id: string, time: string, message: string, status: string, kind?: string, title?: string, command?: string, output?: string, exitCode?: number, multiline?: boolean }>} */
    processEvents: { type: Array, default: () => [] },
  },
  setup(props) {
    const expanded = ref(false);

    const toolCallMap = computed(() => props.stats?.toolCalls || {});
    const lspCount = computed(() => {
      const byList = sumToolCallsByNames(toolCallMap.value, LSP_TOOL_NAMES);
      if (byList > 0) return byList;
      return Number(props.stats?.lspCalls) || 0;
    });
    const jsonRenderCount = computed(() => sumToolCallsByNames(toolCallMap.value, JSON_RENDER_TOOL_NAMES));
    const playwrightCount = computed(() => sumToolCallsByMatcher(
      toolCallMap.value,
      (name) => PLAYWRIGHT_TOOL_PREFIXES.some((prefix) => name.startsWith(prefix)),
    ));
    const goRunCount = computed(() => sumToolCallsByNames(toolCallMap.value, GO_RUN_TOOL_NAMES));
    const cmdCount = computed(() => Number(props.stats?.commands) || 0);
    const fileCount = computed(() => Number(props.stats?.fileEdits) || 0);
    const totalTools = computed(() => {
      const map = toolCallMap.value;
      let sum = 0;
      for (const [, v] of Object.entries(map)) {
        sum += Number(v) || 0;
      }
      return sum;
    });

    const toolCallEntries = computed(() => {
      const map = toolCallMap.value;
      const merged = {};
      for (const [raw, value] of Object.entries(map)) {
        const short = normalizeToolName(raw) || raw;
        merged[short] = (merged[short] || 0) + (Number(value) || 0);
      }
      return Object.entries(merged)
        .map(([name, count]) => ({ name, count }))
        .filter((entry) => entry.count > 0)
        .sort((a, b) => b.count - a.count);
    });

    const recentAlerts = computed(() => {
      const items = props.alerts || [];
      return items.slice(-5).reverse();
    });

    const recentProcessEvents = computed(() => {
      const items = Array.isArray(props.processEvents) ? props.processEvents : [];
      return items.slice(0, 12);
    });

    const hasAlerts = computed(() => recentAlerts.value.length > 0);
    const hasProcessEvents = computed(() => recentProcessEvents.value.length > 0);
    const statItems = computed(() => ([
      { key: 'lsp', label: 'LSP (8 tools)', className: 'stat-lsp', value: lspCount.value },
      { key: 'jsonRender', label: 'JSON-Render', className: 'stat-json-render', value: jsonRenderCount.value },
      { key: 'playwright', label: 'Playwright', className: 'stat-playwright', value: playwrightCount.value },
      { key: 'goRun', label: 'go-run', className: 'stat-go-run', value: goRunCount.value },
      { key: 'command', label: '命令', className: 'stat-cmd', value: cmdCount.value },
      { key: 'file', label: '文件', className: 'stat-file', value: fileCount.value },
      { key: 'tool', label: '工具', className: 'stat-tool', value: totalTools.value },
    ]));

    function toggleExpand() {
      expanded.value = !expanded.value;
    }

    function statIconPath(key) {
      const name = (key || '').toString().trim();
      return STAT_ICON_PATHS[name] || STAT_ICON_PATHS.tool;
    }

    function statTooltip(item) {
      const label = (item?.label || '').toString().trim();
      const value = Number(item?.value) || 0;
      return `${label}: ${value}`;
    }

    function alertIcon(level) {
      if (level === 'error') return '✗';
      if (level === 'warning' || level === 'stall') return '⚠';
      return '●';
    }

    function alertClass(level) {
      if (level === 'error') return 'alert-error';
      if (level === 'warning' || level === 'stall') return 'alert-warning';
      return 'alert-info';
    }

    function processIcon(status) {
      if (status === 'done') return '✓';
      if (status === 'active') return '●';
      return '•';
    }

    function processClass(status) {
      if (status === 'done') return 'alert-info';
      if (status === 'failed') return 'alert-error';
      return 'alert-warning';
    }

    function isCommandEntry(entry) {
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
    }

    function commandStatusText(entry) {
      const status = (entry?.status || '').toString().trim();
      if (status === 'active') return '命令执行中';
      if (status === 'failed') return '命令执行失败';
      const msg = (entry?.message || '').toString();
      if (msg.includes('命令执行中') || msg.includes('Running command')) return '命令执行中';
      if (msg.includes('执行失败') || msg.includes('Errored command')) return '命令执行失败';
      return '已执行命令';
    }

    function commandStatusIcon(entry) {
      const status = (entry?.status || '').toString().trim();
      if (status === 'active') return '◌';
      if (status === 'failed') return '✕';
      return '✓';
    }

    function commandStatusIconClass(entry) {
      const status = (entry?.status || '').toString().trim();
      if (status === 'active') return 'ran-command-card__icon--running ran-command-card__icon--spinning';
      if (status === 'failed') return 'ran-command-card__icon--error';
      return 'ran-command-card__icon--done';
    }

    function commandTitle(entry) {
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
    }

    function commandOutput(entry) {
      const output = (entry?.output || '').toString();
      if (output.trim()) return output;
      const msg = (entry?.message || '').toString();
      if (!msg.includes('\n')) return '';
      const lines = msg.split('\n').map((line) => line.toString());
      if (lines.length <= 1) return '';
      const payload = lines.slice(1).filter((line) => !line.trim().startsWith('$ ')).join('\n').trim();
      return payload;
    }

    function commandExitText(entry) {
      const code = Number(entry?.exitCode);
      if (!Number.isFinite(code)) return '';
      return `退出码 ${Math.trunc(code)}`;
    }

    return {
      expanded,
      lspCount,
      jsonRenderCount,
      playwrightCount,
      goRunCount,
      cmdCount,
      fileCount,
      totalTools,
      toolCallEntries,
      recentAlerts,
      recentProcessEvents,
      hasAlerts,
      hasProcessEvents,
      statItems,
      toggleExpand,
      statIconPath,
      statTooltip,
      alertIcon,
      alertClass,
      processIcon,
      processClass,
      isCommandEntry,
      commandStatusText,
      commandStatusIcon,
      commandStatusIconClass,
      commandTitle,
      commandOutput,
      commandExitText,
    };
  },
  template: `
    <div class="activity-panel" :class="{ expanded }">
      <div class="activity-stats" @click="toggleExpand" title="点击展开工具详情">
        <span
          v-for="item in statItems"
          :key="item.key"
          class="stat stat-icon-item"
          :class="item.className"
        >
          <span class="stat-icon" :title="statTooltip(item)" role="img" :aria-label="item.label">
            <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
              <path :d="statIconPath(item.key)"></path>
            </svg>
          </span>
          <strong>{{ item.value }}</strong>
        </span>
      </div>

      <div v-if="expanded && toolCallEntries.length > 0" class="activity-tool-detail">
        <span
          v-for="entry in toolCallEntries"
          :key="entry.name"
          class="tool-entry"
        >{{ entry.name }}:<strong>{{ entry.count }}</strong></span>
      </div>

      <div class="activity-alerts" :class="{ empty: !hasAlerts && !hasProcessEvents }">
        <template v-if="hasProcessEvents">
          <div
            v-for="(entry, idx) in recentProcessEvents"
            :key="entry.id + '-' + idx"
            class="alert-line"
            :class="[processClass(entry.status), { 'alert-line-command': isCommandEntry(entry) }]"
          >
            <span class="alert-time">{{ entry.time }}</span>
            <template v-if="isCommandEntry(entry)">
              <div class="activity-command-entry ran-command-card">
                <div class="ran-command-card__header">
                  <span class="ran-command-card__status">{{ commandStatusText(entry) }}</span>
                </div>
                <div class="ran-command-card__main-row">
                  <span class="ran-command-card__icon" :class="commandStatusIconClass(entry)" aria-hidden="true">{{ commandStatusIcon(entry) }}</span>
                  <span class="ran-command-card__title" :title="commandTitle(entry)">{{ commandTitle(entry) }}</span>
                </div>
                <div
                  class="ran-command-card__details"
                  :class="commandOutput(entry) ? 'ran-command-card__details--open' : 'ran-command-card__details--closed'"
                >
                  <pre v-if="commandOutput(entry)" class="ran-command-card__output activity-command-output">{{ commandOutput(entry) }}</pre>
                </div>
                <div class="ran-command-card__footer">
                  <span class="ran-command-card__auto-exec">终端命令</span>
                  <div class="ran-command-card__footer-right">
                    <span v-if="entry.status === 'active'" class="ran-command-card__cancel-btn">运行中...</span>
                    <span v-if="commandExitText(entry)" class="ran-command-card__exit-code">{{ commandExitText(entry) }}</span>
                  </div>
                </div>
              </div>
            </template>
            <template v-else>
              <span class="alert-icon">{{ processIcon(entry.status) }}</span>
              <span class="alert-msg" :class="{ 'alert-msg-wrap': Boolean(entry.multiline) }">{{ entry.message }}</span>
            </template>
          </div>
        </template>
        <template v-if="hasAlerts">
          <div
            v-for="alert in recentAlerts"
            :key="alert.id"
            class="alert-line"
            :class="alertClass(alert.level)"
          >
            <span class="alert-time">{{ alert.time }}</span>
            <span class="alert-icon">{{ alertIcon(alert.level) }}</span>
            <span class="alert-msg">{{ alert.message }}</span>
          </div>
        </template>
        <div v-if="!hasAlerts && !hasProcessEvents" class="alert-empty">无告警</div>
      </div>
    </div>
  `,
};
