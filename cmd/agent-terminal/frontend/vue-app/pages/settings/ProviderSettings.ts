import { computed, onMounted, reactive, ref, watch } from '../../../lib/vue.esm-browser.prod.js';
import {
  appendCurrentOption,
  EFFORT_MODES,
  EFFORT_MODES_BY_PROVIDER,
  isClaudeOpusFamilyModel,
  MODEL_OPTIONS,
  MODEL_OPTIONS_BY_PROVIDER,
  normalizeProviderConfigValue,
} from '../../provider-config-options.js';
import { callAPI } from '../../services/api.js';
import { useSettingsScope } from './useSettingsScope.ts';

type ProviderSettingsProjectStore = { state?: { active?: string } } | null;
type ProviderSettingsProps = { projectStore?: ProviderSettingsProjectStore };
type ProviderNoticeState = { level: string; message: string };
type SandboxPayload = {
  type?: string;
  writableRoots?: string[];
  networkAccess?: boolean;
  access?: {
    type?: string;
    readableRoots?: string[];
    includePlatformDefaults?: boolean;
  };
} | null;

function setupProviderSettings(props: ProviderSettingsProps) {
  const PROVIDER_ACTIVE_PREF_KEY = 'settings.provider.active';
  const DEFAULT_PROVIDER_ID = 'codex';
  const DEFAULT_SUMMARY_MODE = 'detailed';
  const DEFAULT_APPROVAL_MODE = 'on-request';
  const DEFAULT_PERSONALITY = 'pragmatic';
  const activeProvider = ref(DEFAULT_PROVIDER_ID) as { value: string };
  const PROVIDER_OPTIONS = [
    { value: 'codex', label: 'Codex (默认)' },
    { value: 'claude', label: 'Claude 命令行 (原生)' },
  ];
  const SUMMARY_MODES = [
    { value: 'detailed', label: 'detailed（详细摘要，推荐）' },
    { value: 'auto', label: 'auto（自动）' },
    { value: 'concise', label: 'concise（简洁）' },
    { value: 'none', label: 'none（关闭）' },
  ];
  const APPROVAL_MODES = [
    { value: 'on-request', label: 'on-request（按需，默认）' },
    { value: 'untrusted', label: 'untrusted（始终询问）' },
    { value: 'on-failure', label: 'on-failure（失败后询问）' },
    { value: 'never', label: 'never（全部放行）' },
  ];
  const PERSONALITY_OPTIONS = [
    { value: 'pragmatic', label: 'pragmatic（务实高效，默认）' },
    { value: 'friendly', label: 'friendly（友好气氛）' },
    { value: 'none', label: 'none（默认风格）' },
  ];
  const SANDBOX_MODES = [
    { value: 'workspaceWrite', label: 'workspaceWrite（推荐）' },
    { value: 'readOnly', label: 'readOnly（只读）' },
    { value: 'dangerFullAccess', label: 'dangerFullAccess（无限制 ⚠️）' },
  ];
  const sandboxMode = ref('workspaceWrite') as { value: string };
  const writablePaths = ref('') as { value: string };
  const networkAccess = ref(false) as { value: boolean };
  const readOnlyMode = ref('fullAccess') as { value: string };
  const readablePaths = ref('') as { value: string };
  const sandboxNotice = reactive({ level: 'info', message: '' }) as ProviderNoticeState;
  const sandboxSaving = ref(false) as { value: boolean };
  const writablePathsError = ref('') as { value: string };
  const summaryMode = ref(DEFAULT_SUMMARY_MODE) as { value: string };
  const approvalMode = ref(DEFAULT_APPROVAL_MODE) as { value: string };
  const effortMode = ref('xhigh') as { value: string };
  const providerModel = ref('gpt-5.4') as { value: string };
  const personality = ref(DEFAULT_PERSONALITY) as { value: string };
  const { activeProjectCwd, withProjectCwd } = useSettingsScope(props.projectStore);
  let providerSettingsLoadSeq = 0;

  const normalizedActiveProvider = computed(() => normalizeProviderID(activeProvider.value));
  const providerModelOptions = computed(() =>
    appendCurrentOption(
      MODEL_OPTIONS_BY_PROVIDER[normalizedActiveProvider.value] || MODEL_OPTIONS,
      providerModel.value,
    )
  );
  const providerEffortOptions = computed(() => {
    const baseOptions = EFFORT_MODES_BY_PROVIDER[normalizedActiveProvider.value] || EFFORT_MODES;
    const filteredOptions = normalizedActiveProvider.value === 'claude' && !isClaudeOpusFamilyModel(providerModel.value)
      ? baseOptions.filter((item: { value: string }) => item.value !== 'max')
      : baseOptions;
    return appendCurrentOption(
      filteredOptions,
      normalizeProviderEffortValue(normalizedActiveProvider.value, providerModel.value, effortMode.value),
    );
  });

  function normalizeProviderID(value: unknown): string {
    const providerID = normalizeProviderConfigValue(value);
    return providerID || DEFAULT_PROVIDER_ID;
  }

  function providerDefaults(providerID: string) {
    return normalizeProviderID(providerID) === 'claude'
      ? { model: 'sonnet', effort: 'high' }
      : { model: 'gpt-5.4', effort: 'xhigh' };
  }

  function providerPreferenceKey(suffix: string, providerID = activeProvider.value): string {
    return `settings.provider.${normalizeProviderID(providerID)}.${suffix}`;
  }

  async function loadActiveProviderPreference(): Promise<string> {
    try {
      const value = await callAPI('ui/preferences/get', withProjectCwd({ key: PROVIDER_ACTIVE_PREF_KEY }));
      activeProvider.value = normalizeProviderID(typeof value === 'string' ? value : DEFAULT_PROVIDER_ID);
    } catch {
      activeProvider.value = DEFAULT_PROVIDER_ID;
    }
    return activeProvider.value;
  }

  async function persistActiveProviderPreference(providerID = activeProvider.value): Promise<string> {
    const normalizedProviderID = normalizeProviderID(providerID);
    await callAPI('ui/preferences/set', withProjectCwd({ key: PROVIDER_ACTIVE_PREF_KEY, value: normalizedProviderID }));
    activeProvider.value = normalizedProviderID;
    return normalizedProviderID;
  }

  async function readProviderPreference(suffix: string, providerID = activeProvider.value): Promise<any> {
    const primaryKey = providerPreferenceKey(suffix, providerID);
    try {
      const value = await callAPI('ui/preferences/get', withProjectCwd({ key: primaryKey }));
      if (value !== null && value !== undefined && value !== '') {
        return value;
      }
    } catch {
      // ignore
    }
    return null;
  }

  function buildProviderPreferenceSetCalls(suffix: string, value: string, providerID = activeProvider.value): Promise<any>[] {
    const normalizedProviderID = normalizeProviderID(providerID);
    return [
      callAPI('ui/preferences/set', withProjectCwd({ key: providerPreferenceKey(suffix, normalizedProviderID), value })),
    ];
  }

  function validateAbsPaths(raw: string): string {
    if (!raw.trim()) return '请至少填写一个绝对路径';
    const bad = raw.trim().split('\n').map((s) => s.trim()).filter((s) => s && !s.startsWith('/'));
    return bad.length ? `路径必须以 / 开头：${bad.join(', ')}` : '';
  }

  function normalizeProviderModelValue(providerID: string, value: unknown): string {
    const normalizedValue = normalizeProviderConfigValue(value);
    return normalizedValue || providerDefaults(providerID).model;
  }

  function normalizeProviderEffortValue(providerID: string, model: string, value: unknown): string {
    const normalizedProviderID = normalizeProviderID(providerID);
    const normalizedValue = normalizeProviderConfigValue(value).toLowerCase();
    const defaults = providerDefaults(normalizedProviderID);
    if (normalizedProviderID !== 'claude') {
      return EFFORT_MODES_BY_PROVIDER.codex.some((item) => item.value === normalizedValue)
        ? normalizedValue
        : defaults.effort;
    }
    switch (normalizedValue) {
      case 'max':
        return isClaudeOpusFamilyModel(model) ? 'max' : 'high';
      case 'high':
      case 'xhigh':
        return 'high';
      case 'medium':
        return 'medium';
      case 'low':
      case 'minimal':
        return 'low';
      default:
        return defaults.effort;
    }
  }

  function resetProviderSettingsState(providerID: string): void {
    const defaults = providerDefaults(providerID);
    sandboxMode.value = 'workspaceWrite';
    writablePaths.value = '';
    networkAccess.value = false;
    readOnlyMode.value = 'fullAccess';
    readablePaths.value = '';
    writablePathsError.value = '';
    summaryMode.value = DEFAULT_SUMMARY_MODE;
    approvalMode.value = DEFAULT_APPROVAL_MODE;
    effortMode.value = defaults.effort;
    providerModel.value = defaults.model;
    personality.value = DEFAULT_PERSONALITY;
  }

  function buildSandboxPayload(): Exclude<SandboxPayload, null> {
    const mode = sandboxMode.value;
    if (mode === 'dangerFullAccess') return { type: 'dangerFullAccess' };
    if (mode === 'readOnly') {
      if (readOnlyMode.value === 'restricted') {
        const roots = readablePaths.value.trim().split('\n').map((s) => s.trim()).filter(Boolean);
        return { type: 'readOnly', access: { type: 'restricted', readableRoots: roots, includePlatformDefaults: true } };
      }
      return { type: 'readOnly' };
    }
    const roots = writablePaths.value.trim().split('\n').map((s) => s.trim()).filter(Boolean);
    return { type: 'workspaceWrite', writableRoots: roots, networkAccess: networkAccess.value };
  }

  function applySandboxPayload(payload: SandboxPayload): void {
    if (!payload || typeof payload !== 'object') return;
    sandboxMode.value = payload.type || 'workspaceWrite';
    if (payload.type === 'workspaceWrite') {
      writablePaths.value = (payload.writableRoots || []).join('\n');
      networkAccess.value = Boolean(payload.networkAccess);
    } else if (payload.type === 'readOnly') {
      const acc = payload.access;
      readOnlyMode.value = acc?.type === 'restricted' ? 'restricted' : 'fullAccess';
      readablePaths.value = (acc?.readableRoots || []).join('\n');
    }
  }

  async function loadProviderSettingsFor(providerID: string): Promise<void> {
    const normalizedProviderID = normalizeProviderID(providerID);
    const requestSeq = ++providerSettingsLoadSeq;
    activeProvider.value = normalizedProviderID;
    resetProviderSettingsState(normalizedProviderID);
    sandboxNotice.level = 'info';
    sandboxNotice.message = '';

    try {
      const raw = await readProviderPreference('sandbox', normalizedProviderID);
      if (requestSeq !== providerSettingsLoadSeq || normalizeProviderID(activeProvider.value) !== normalizedProviderID) return;
      if (raw && typeof raw === 'string') applySandboxPayload(JSON.parse(raw));
      else if (raw && typeof raw === 'object') applySandboxPayload(raw);
    } catch {
      // ignore
    }

    const defaults = providerDefaults(normalizedProviderID);
    const [summaryValue, approvalValue, effortValue, modelValue, personalityValue] = await Promise.all([
      readProviderPreference('summary', normalizedProviderID),
      readProviderPreference('approvalPolicy', normalizedProviderID),
      readProviderPreference('effort', normalizedProviderID),
      readProviderPreference('model', normalizedProviderID),
      readProviderPreference('personality', normalizedProviderID),
    ]);
    if (requestSeq !== providerSettingsLoadSeq || normalizeProviderID(activeProvider.value) !== normalizedProviderID) return;

    summaryMode.value = normalizeProviderConfigValue(summaryValue) || DEFAULT_SUMMARY_MODE;
    approvalMode.value = normalizeProviderConfigValue(approvalValue) || DEFAULT_APPROVAL_MODE;
    const nextModel = normalizeProviderModelValue(normalizedProviderID, modelValue || defaults.model);
    providerModel.value = nextModel;
    effortMode.value = normalizeProviderEffortValue(normalizedProviderID, nextModel, effortValue || defaults.effort);
    personality.value = normalizeProviderConfigValue(personalityValue) || DEFAULT_PERSONALITY;
  }

  async function loadProviderSettings(): Promise<void> {
    const providerID = await loadActiveProviderPreference();
    await loadProviderSettingsFor(providerID);
  }

  async function onActiveProviderChange(): Promise<void> {
    try {
      const providerID = await persistActiveProviderPreference(activeProvider.value);
      await loadProviderSettingsFor(providerID);
    } catch (error: any) {
      sandboxNotice.level = 'error';
      sandboxNotice.message = `切换 Provider 失败：${error?.message || error}`;
    }
  }

  async function saveProviderSettings(): Promise<void> {
    if (sandboxMode.value === 'workspaceWrite') {
      writablePathsError.value = validateAbsPaths(writablePaths.value);
      if (writablePathsError.value) return;
    }
    if (sandboxSaving.value) return;
    sandboxSaving.value = true;
    try {
      const providerID = normalizedActiveProvider.value;
      const normalizedModel = normalizeProviderModelValue(providerID, providerModel.value);
      const normalizedEffort = normalizeProviderEffortValue(providerID, normalizedModel, effortMode.value);
      providerModel.value = normalizedModel;
      effortMode.value = normalizedEffort;
      const payload = buildSandboxPayload();
      await Promise.all([
        ...buildProviderPreferenceSetCalls('sandbox', JSON.stringify(payload), providerID),
        ...buildProviderPreferenceSetCalls('summary', summaryMode.value, providerID),
        ...buildProviderPreferenceSetCalls('approvalPolicy', approvalMode.value, providerID),
        ...buildProviderPreferenceSetCalls('effort', effortMode.value, providerID),
        ...buildProviderPreferenceSetCalls('model', providerModel.value, providerID),
        ...buildProviderPreferenceSetCalls('personality', personality.value, providerID),
      ]);
      sandboxNotice.level = 'info';
      sandboxNotice.message = `已保存：${providerModel.value} / ${effortMode.value} / ${personality.value}`;
    } catch (error: any) {
      sandboxNotice.level = 'error';
      sandboxNotice.message = `保存失败：${error?.message || error}`;
    } finally {
      sandboxSaving.value = false;
    }
  }

  onMounted(() => {
    void loadProviderSettings();
  });

  watch(
    () => activeProjectCwd.value,
    (next: string, prev: string) => {
      if (next === prev) return;
      void loadProviderSettings();
    },
  );

  watch(
    () => [normalizedActiveProvider.value, providerModel.value],
    ([providerID, model]) => {
      const normalizedModel = normalizeProviderModelValue(providerID, model);
      if (providerModel.value !== normalizedModel) {
        providerModel.value = normalizedModel;
      }
      const normalizedEffort = normalizeProviderEffortValue(providerID, normalizedModel, effortMode.value);
      if (effortMode.value !== normalizedEffort) {
        effortMode.value = normalizedEffort;
      }
    },
    { immediate: true },
  );

  return {
    activeProvider,
    normalizedActiveProvider,
    PROVIDER_OPTIONS,
    SANDBOX_MODES,
    SUMMARY_MODES,
    APPROVAL_MODES,
    EFFORT_MODES,
    MODEL_OPTIONS,
    providerEffortOptions,
    providerModelOptions,
    PERSONALITY_OPTIONS,
    sandboxMode,
    writablePaths,
    networkAccess,
    readOnlyMode,
    readablePaths,
    sandboxNotice,
    sandboxSaving,
    writablePathsError,
    summaryMode,
    approvalMode,
    effortMode,
    providerModel,
    personality,
    onActiveProviderChange,
    saveProviderSettings,
    loadProviderSettings,
    loadProviderSettingsFor,
  };
}

export const ProviderSettings = {
  name: 'ProviderSettings',
  props: {
    projectStore: { type: Object, default: null },
  },
  setup: setupProviderSettings,
  template: `
    <div class="section-header">PROVIDER</div>

    <div class="data-card-vue" data-testid="settings-provider-sandbox-card">
      <div class="data-row-vue">
        <strong>Active Provider</strong>
        <span>当前生效的底层模型驱动</span>
      </div>
      <div class="settings-stall-row" style="margin-top:8px; margin-bottom:12px">
        <select v-model="activeProvider" class="settings-stall-input" data-testid="settings-provider-active-select" style="width:220px" @change="onActiveProviderChange">
          <option v-for="p in PROVIDER_OPTIONS" :key="p.value" :value="p.value">{{ p.label }}</option>
        </select>
      </div>

      <div class="data-row-vue">
        <strong>Sandbox Policy</strong>
        <span>新建 Thread 时生效的沙箱策略</span>
      </div>
      <div class="settings-stall-row" style="margin-top:8px">
        <select v-model="sandboxMode" class="settings-stall-input" data-testid="provider-sandbox-mode-select" style="width:220px">
          <option v-for="m in SANDBOX_MODES" :key="m.value" :value="m.value">{{ m.label }}</option>
        </select>
      </div>

      <template v-if="sandboxMode === 'workspaceWrite'">
        <div class="settings-prompt-label" style="margin-top:10px">可写目录（每行一个绝对路径，必填）</div>
        <textarea
          class="settings-prompt-textarea"
          data-testid="provider-writable-paths-input"
          rows="3"
          v-model="writablePaths"
          placeholder="/abs/path/to/workspace"
        ></textarea>
        <div v-if="writablePathsError" class="settings-prompt-notice is-error">{{ writablePathsError }}</div>
        <label class="settings-prompt-toggle" style="margin-top:8px">
          <div class="settings-prompt-toggle-copy">
            <span class="settings-prompt-toggle-title">允许网络访问</span>
          </div>
          <input type="checkbox" class="settings-prompt-toggle-input" v-model="networkAccess" />
        </label>
      </template>

      <template v-if="sandboxMode === 'readOnly'">
        <div class="settings-stall-row" style="margin-top:10px">
          <select v-model="readOnlyMode" class="settings-stall-input" style="width:160px">
            <option value="fullAccess">fullAccess（全量只读）</option>
            <option value="restricted">restricted（限定目录）</option>
          </select>
        </div>
        <template v-if="readOnlyMode === 'restricted'">
          <div class="settings-prompt-label" style="margin-top:8px">可读目录（每行一个绝对路径）</div>
          <textarea
            class="settings-prompt-textarea"
            rows="3"
            v-model="readablePaths"
            placeholder="/abs/path/to/read"
          ></textarea>
        </template>
      </template>

      <div class="settings-stall-row" style="margin-top:12px">
        <label class="settings-stall-label">模型（Model）</label>
        <select v-model="providerModel" class="settings-stall-input" data-testid="provider-model-select" style="width:260px">
          <option v-for="m in providerModelOptions" :key="m.value" :value="m.value">{{ m.label }}</option>
        </select>
      </div>
      <div class="settings-stall-row" style="margin-top:8px">
        <label class="settings-stall-label">推理力度（Effort）</label>
        <select v-model="effortMode" class="settings-stall-input" data-testid="provider-effort-mode-select" style="width:260px">
          <option v-for="m in providerEffortOptions" :key="m.value" :value="m.value">{{ m.label }}</option>
        </select>
      </div>
      <div class="settings-stall-row" style="margin-top:8px">
        <label class="settings-stall-label">回复风格（Personality）</label>
        <select v-model="personality" class="settings-stall-input" data-testid="provider-personality-select" style="width:260px">
          <option v-for="m in PERSONALITY_OPTIONS" :key="m.value" :value="m.value">{{ m.label }}</option>
        </select>
      </div>
      <div class="settings-stall-row" style="margin-top:8px">
        <label class="settings-stall-label">推理摘要（Summary）</label>
        <select v-model="summaryMode" class="settings-stall-input" data-testid="provider-summary-mode-select" style="width:260px">
          <option v-for="m in SUMMARY_MODES" :key="m.value" :value="m.value">{{ m.label }}</option>
        </select>
      </div>
      <div class="settings-stall-row" style="margin-top:8px">
        <label class="settings-stall-label">审批策略（ApprovalPolicy）</label>
        <select v-model="approvalMode" class="settings-stall-input" data-testid="provider-approval-mode-select" style="width:260px">
          <option v-for="m in APPROVAL_MODES" :key="m.value" :value="m.value">{{ m.label }}</option>
        </select>
      </div>

      <div v-if="sandboxNotice.message" class="settings-prompt-notice" :class="'is-' + sandboxNotice.level">{{ sandboxNotice.message }}</div>
      <div class="settings-action-row settings-action-inline" style="margin-top:10px">
        <button class="btn btn-secondary btn-toolbar-sm" @click="loadProviderSettings" :disabled="sandboxSaving">刷新</button>
        <button class="btn btn-primary btn-toolbar-sm" data-testid="provider-sandbox-save-button" @click="saveProviderSettings" :disabled="sandboxSaving">{{ sandboxSaving ? '保存中...' : '保存' }}</button>
      </div>
    </div>
  `,
};
