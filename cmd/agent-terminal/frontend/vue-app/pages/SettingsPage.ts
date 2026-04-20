import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logError, logInfo, logWarn, readLogBuffer, readLogLevel } from '../services/log.js';
import { ProviderSettings } from './settings/ProviderSettings.ts';
import { LspPromptSettings } from './settings/LspPromptSettings.ts';
import { BuiltinToolsSettings } from './settings/BuiltinToolsSettings.ts';
import { useSettingsScope } from './settings/useSettingsScope.ts';

type SettingsBuildInfo = { version?: string; runtime?: string; buildTime?: string; commit?: string };
type SettingsProjectStore = { state?: { active?: string } } | null;
type SettingsPageProps = { buildInfo: SettingsBuildInfo; projectStore?: SettingsProjectStore };
type SettingsNoticeState = { level: string; message: string };
type SettingsLogEntry = { seq?: string | number; ts?: string | number | Date; level?: string; scope?: string; event?: string };

function setupSettingsPage(props: SettingsPageProps, { emit }: { emit: (event: 'refresh') => void }) {
  const LOG_LIST_LIMIT = 14;
  const MIN_TURN_TIMEOUT_SEC = 30;
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

  const stallThreshold = ref(MIN_TURN_TIMEOUT_SEC) as { value: number };
  const stallLoading = ref(false) as { value: boolean };
  const stallNotice = reactive({ level: 'info', message: '' }) as SettingsNoticeState;

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
    logInfo('page', 'settings.unmounted', {});
  });

  return {
    versionText,
    runtimeText,
    buildTimeText,
    commitText,
    logLevel,
    logEntries,
    refresh,
    refreshLogPanel,
    formatLogTime,
    stallThreshold,
    stallLoading,
    stallNotice,
    loadStallSettings,
    saveStallThreshold,
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

        <ProviderSettings :project-store="projectStore" />
        <LspPromptSettings :project-store="projectStore" />
        <BuiltinToolsSettings :project-store="projectStore" />

        <div class="section-header">UI LOG</div>
        <div class="data-card-vue settings-log-card" data-testid="settings-log-card">
          <div class="data-row-vue">
            <strong>日志级别</strong>
            <span>{{ logLevel }}</span>
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
