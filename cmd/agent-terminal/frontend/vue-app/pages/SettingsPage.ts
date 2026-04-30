import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logError, logInfo, logWarn, onLogLevelChange, readLogBuffer, readLogLevel, setLogLevel } from '../services/log.js';
import { ProviderSettings } from './settings/ProviderSettings.ts';
import { LspPromptSettings } from './settings/LspPromptSettings.ts';
import { BuiltinToolsSettings } from './settings/BuiltinToolsSettings.ts';
import { useSettingsScope } from './settings/useSettingsScope.ts';
import {
  loadContextUsageThresholds,
  saveContextUsageThresholds,
  isValidThresholds,
  useContextUsageThresholds,
} from '../composables/useContextUsageThresholds.js';

type SettingsBuildInfo = { version?: string; runtime?: string; buildTime?: string; commit?: string };
type SettingsProjectStore = { state?: { active?: string } } | null;
type SettingsPageProps = { buildInfo: SettingsBuildInfo; projectStore?: SettingsProjectStore };
type SettingsNoticeState = { level: string; message: string };
type SettingsLogEntry = { seq?: string | number; ts?: string | number | Date; level?: string; scope?: string; event?: string };

function setupSettingsPage(props: SettingsPageProps, { emit }: { emit: (event: 'refresh') => void }) {
  const LOG_LIST_LIMIT = 14;
  const MIN_TURN_TIMEOUT_SEC = 30;
  const LOG_LEVEL_OPTIONS = [
    { value: 'debug', label: 'debug（最详细）' },
    { value: 'info', label: 'info（默认）' },
    { value: 'warn', label: 'warn' },
    { value: 'error', label: 'error（仅错误）' },
  ];
  const versionText = computed(() => `Agent Orchestrator ${props.buildInfo.version || 'dev'}`);
  const runtimeText = computed(() => props.buildInfo.runtime
    ? `Wails WebKit · Go Backend · ${props.buildInfo.runtime}`
    : 'Wails WebKit · Go Backend');
  const buildTimeText = computed(() => props.buildInfo.buildTime || '-');
  const commitText = computed(() => props.buildInfo.commit || '-');
  const logLevel = ref('info') as { value: string };
  const logEntries = ref([]) as { value: SettingsLogEntry[] };
  const { activeProjectCwd, withProjectCwd } = useSettingsScope(props.projectStore);

  let logRefreshTimer = 0;
  let unsubscribeLogLevel: () => void = () => {};

  const stallThreshold = ref(MIN_TURN_TIMEOUT_SEC) as { value: number };
  const stallLoading = ref(false) as { value: boolean };
  const stallNotice = reactive({ level: 'info', message: '' }) as SettingsNoticeState;

  // Phase 1: 上下文警报阈值（warn / danger / critical）
  const ctxThresholdsRef = useContextUsageThresholds();
  const ctxWarn = ref(ctxThresholdsRef.value[0]) as { value: number };
  const ctxDanger = ref(ctxThresholdsRef.value[1]) as { value: number };
  const ctxCritical = ref(ctxThresholdsRef.value[2]) as { value: number };
  const ctxLoading = ref(false) as { value: boolean };
  const ctxNotice = reactive({ level: 'info', message: '' }) as SettingsNoticeState;

  function syncCtxLocalsFromShared(): void {
    ctxWarn.value = ctxThresholdsRef.value[0];
    ctxDanger.value = ctxThresholdsRef.value[1];
    ctxCritical.value = ctxThresholdsRef.value[2];
  }

  async function loadContextThresholds(): Promise<void> {
    ctxLoading.value = true;
    try {
      await loadContextUsageThresholds();
      syncCtxLocalsFromShared();
      ctxNotice.level = 'info';
      ctxNotice.message = '';
    } catch (error: any) {
      ctxNotice.level = 'error';
      ctxNotice.message = `加载失败：${error?.message || error}`;
    } finally {
      ctxLoading.value = false;
    }
  }

  async function saveContextThresholds(): Promise<void> {
    const next = [Number(ctxWarn.value), Number(ctxDanger.value), Number(ctxCritical.value)];
    if (!isValidThresholds(next)) {
      ctxNotice.level = 'error';
      ctxNotice.message = '三个阈值必须在 (0, 100) 之间，且严格升序（例 70 / 85 / 95）';
      return;
    }
    ctxLoading.value = true;
    try {
      await saveContextUsageThresholds(next);
      ctxNotice.level = 'info';
      ctxNotice.message = `已保存：${next.join(' / ')}%（立即生效）`;
      syncCtxLocalsFromShared();
    } catch (error: any) {
      ctxNotice.level = 'error';
      ctxNotice.message = `保存失败：${error?.message || error}`;
    } finally {
      ctxLoading.value = false;
    }
  }

  function formatLogTime(value: string | number | Date | null | undefined): string {
    if (!value) return '--:--:--';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '--:--:--';
    return date.toLocaleTimeString('zh-CN', { hour12: false });
  }

  function refreshLogPanel(): void {
    logLevel.value = readLogLevel();
    const buffer = readLogBuffer() as SettingsLogEntry[];
    logEntries.value = buffer.slice(-LOG_LIST_LIMIT).reverse();
  }

  function applyLogLevelChange(next: string): void {
    const trimmed = (next || '').toString().trim();
    if (!trimmed || trimmed === logLevel.value) return;
    const ok = setLogLevel(trimmed);
    if (!ok) {
      // setLogLevel rejected unknown values; resync local select to truth
      logLevel.value = readLogLevel();
      return;
    }
    // setLogLevel will fire onLogLevelChange which mirrors back into
    // logLevel.value, but mirror eagerly so the <select> never blinks
    // back to the old value during the next paint.
    logLevel.value = trimmed;
    logInfo('page', 'settings.logLevel.change', { level: trimmed });
  }

  function setStallNotice(level: string, message: string): void {
    stallNotice.level = level || 'info';
    stallNotice.message = (message || '').toString().trim();
  }

  async function loadStallSettings(): Promise<void> {
    stallLoading.value = true;
    try {
      const thresholdRes = await callAPI('ui/preferences/get', withProjectCwd({ key: 'stallThresholdSec' }));
      if (typeof thresholdRes === 'number' && Number.isFinite(thresholdRes) && thresholdRes >= MIN_TURN_TIMEOUT_SEC) {
        stallThreshold.value = thresholdRes;
        setStallNotice('info', '');
        return;
      }
      if (thresholdRes == null) {
        logWarn('page', 'settings.stallThreshold.missing', {
          cwd: activeProjectCwd.value || '',
          fallbackSec: stallThreshold.value,
        });
        setStallNotice('info', '');
        return;
      }
      logWarn('page', 'settings.stallThreshold.invalid', {
        cwd: activeProjectCwd.value || '',
        value: thresholdRes,
        fallbackSec: stallThreshold.value,
      });
      setStallNotice('error', `加载到无效阈值，已保留当前值 ${stallThreshold.value}s`);
    } catch (error: any) {
      const message = error?.message || String(error);
      logError('page', 'settings.stallThreshold.load_failed', {
        cwd: activeProjectCwd.value || '',
        error: message,
      });
      setStallNotice('error', `加载失败：${message}`);
    } finally {
      stallLoading.value = false;
    }
  }

  async function saveStallSetting(key: string, value: number, label: string): Promise<void> {
    const num = parseInt(String(value), 10);
    if (Number.isNaN(num) || num < MIN_TURN_TIMEOUT_SEC) {
      setStallNotice('error', `${label}不能小于 ${MIN_TURN_TIMEOUT_SEC} 秒`);
      return;
    }
    try {
      await callAPI('ui/preferences/set', withProjectCwd({ key, value: num }));
      setStallNotice('info', `${label}已保存: ${num}s (${Math.round(num / 60)}分钟)`);
    } catch (error: any) {
      setStallNotice('error', `保存失败：${error?.message || error}`);
    }
  }

  async function saveStallThreshold(): Promise<void> {
    await saveStallSetting('stallThresholdSec', stallThreshold.value, '超时阈值');
  }

  const refresh = () => {
    logInfo('page', 'settings.refreshBuildInfo.click', {});
    emit('refresh');
  };

  onMounted(() => {
    logInfo('page', 'settings.mounted', {});
    refreshLogPanel();
    loadStallSettings();
    loadContextThresholds();
    // Subscribe to live log-level updates (covers cross-tab `storage`
    // events, devtools `window.AOLog.setLevel`, and other consumers).
    unsubscribeLogLevel = onLogLevelChange((level: string) => {
      logLevel.value = level;
    });
    // logEntries is the ring buffer; still poll it (buffer churn is
    // fast and not event-driven). logLevel itself no longer needs
    // polling because onLogLevelChange covers every mutation path.
    logRefreshTimer = window.setInterval(refreshLogPanel, 1000);
  });
  watch(
    () => activeProjectCwd.value,
    (next: string, prev: string) => {
      if (next === prev) return;
      loadStallSettings();
    },
  );
  onBeforeUnmount(() => {
    if (logRefreshTimer) {
      window.clearInterval(logRefreshTimer);
    }
    try {
      unsubscribeLogLevel();
    } catch {
      // ignore: subscriber lifecycle is best-effort
    }
    logInfo('page', 'settings.unmounted', {});
  });

  return {
    versionText,
    runtimeText,
    buildTimeText,
    commitText,
    logLevel,
    logEntries,
    LOG_LEVEL_OPTIONS,
    applyLogLevelChange,
    refresh,
    refreshLogPanel,
    formatLogTime,
    stallThreshold,
    stallLoading,
    stallNotice,
    loadStallSettings,
    saveStallThreshold,
    ctxWarn,
    ctxDanger,
    ctxCritical,
    ctxLoading,
    ctxNotice,
    loadContextThresholds,
    saveContextThresholds,
  };
}

export const SettingsPage = {
  name: 'SettingsPage',
  components: { ProviderSettings, LspPromptSettings, BuiltinToolsSettings },
  props: {
    buildInfo: { type: Object, required: true },
    projectStore: { type: Object, required: false, default: null },
  },
  emits: ['refresh'],
  setup: setupSettingsPage,
  template: `
    <section id="page-settings" class="page active" data-testid="settings-page">
      <div class="panel-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>设置</h2></div>
      </div>

      <div class="panel-body" data-testid="settings-panel-body">
        <div class="section-header">ABOUT</div>
        <div class="data-card-vue" data-testid="settings-about-card">
          <div class="data-row-vue"><strong>版本</strong><span>{{ versionText }}</span></div>
          <div class="data-row-vue"><strong>运行时</strong><span>{{ runtimeText }}</span></div>
          <div class="data-row-vue"><strong>构建时间</strong><span>{{ buildTimeText }}</span></div>
          <div class="data-row-vue"><strong>Commit</strong><span>{{ commitText }}</span></div>
        </div>
        <div class="settings-action-row">
          <button class="btn btn-secondary" data-testid="settings-refresh-build-button" @click="refresh">刷新构建信息</button>
        </div>

        <div class="section-header">TURN TRACKER</div>
        <div class="data-card-vue settings-stall-card" data-testid="settings-stall-card">
          <div class="data-row-vue">
            <strong>统一超时阈值</strong>
            <span>统一控制 Stall 检测、Watchdog 与流读取超时</span>
          </div>
          <div class="settings-stall-row">
            <input
              type="number"
              class="settings-stall-input"
              data-testid="settings-stall-threshold-input"
              v-model.number="stallThreshold"
              min="30"
              step="30"
              :disabled="stallLoading"
            />
            <span class="settings-stall-unit">秒 ({{ Math.round(stallThreshold / 60) }} 分钟)</span>
            <button class="btn btn-primary btn-toolbar-sm" data-testid="settings-stall-threshold-save-button" @click="saveStallThreshold" :disabled="stallLoading">保存</button>
          </div>
          <div v-if="stallNotice.message" class="settings-prompt-notice" data-testid="settings-stall-notice" :class="'is-' + stallNotice.level">
            {{ stallNotice.message }}
          </div>
        </div>

        <div class="section-header">CONTEXT USAGE ALERT</div>
        <div class="data-card-vue" data-testid="settings-ctx-thresholds-card">
          <div class="data-row-vue">
            <strong>上下文使用率警报阈值</strong>
            <span>分别对应 warn / danger / critical 三档颜色与顶部横幅</span>
          </div>
          <div class="settings-stall-row">
            <input type="number" class="settings-stall-input" data-testid="settings-ctx-warn-input" v-model.number="ctxWarn" min="1" max="99" :disabled="ctxLoading" />
            <span class="settings-stall-unit">% warn</span>
            <input type="number" class="settings-stall-input" data-testid="settings-ctx-danger-input" v-model.number="ctxDanger" min="1" max="99" :disabled="ctxLoading" />
            <span class="settings-stall-unit">% danger</span>
            <input type="number" class="settings-stall-input" data-testid="settings-ctx-critical-input" v-model.number="ctxCritical" min="1" max="99" :disabled="ctxLoading" />
            <span class="settings-stall-unit">% critical</span>
            <button class="btn btn-primary btn-toolbar-sm" data-testid="settings-ctx-thresholds-save-button" @click="saveContextThresholds" :disabled="ctxLoading">保存</button>
          </div>
          <div v-if="ctxNotice.message" class="settings-prompt-notice" data-testid="settings-ctx-thresholds-notice" :class="'is-' + ctxNotice.level">
            {{ ctxNotice.message }}
          </div>
        </div>

        <ProviderSettings :project-store="projectStore" />
        <LspPromptSettings :project-store="projectStore" />
        <BuiltinToolsSettings :project-store="projectStore" />

        <div class="section-header">UI LOG</div>
        <div class="data-card-vue settings-log-card" data-testid="settings-log-card">
          <div class="data-row-vue">
            <strong>日志级别</strong>
            <span>{{ logLevel }}</span>
          </div>
          <div class="settings-stall-row" style="margin-top:8px; margin-bottom:12px">
            <select
              class="settings-stall-input"
              data-testid="settings-log-level-select"
              style="width:220px"
              :value="logLevel"
              @change="applyLogLevelChange($event.target.value)"
            >
              <option v-for="opt in LOG_LEVEL_OPTIONS" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
            </select>
            <span class="settings-stall-unit">立即生效（跨 tab 同步）</span>
          </div>
          <div class="settings-action-row">
            <button class="btn btn-secondary btn-toolbar-sm" data-testid="settings-log-refresh-button" @click="refreshLogPanel">刷新日志</button>
          </div>
          <div v-if="logEntries.length === 0" class="settings-log-empty" data-testid="settings-log-empty">暂无日志</div>
          <div v-else class="settings-log-list" data-testid="settings-log-list">
            <div
              v-for="entry in logEntries"
              :key="entry.seq"
              class="settings-log-item"
            >
              <span class="settings-log-time">{{ formatLogTime(entry.ts) }}</span>
              <span class="settings-log-level" :class="'is-' + entry.level">{{ entry.level }}</span>
              <span class="settings-log-event">{{ entry.scope }}.{{ entry.event }}</span>
            </div>
          </div>
        </div>
      </div>
    </section>
  `,
};
