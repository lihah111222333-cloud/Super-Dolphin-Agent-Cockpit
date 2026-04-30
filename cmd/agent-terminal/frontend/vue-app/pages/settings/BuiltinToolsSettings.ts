import { computed, onMounted, reactive, ref, watch } from '../../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../../services/api.js';
import { useSettingsScope } from './useSettingsScope.ts';

type BuiltinToolsProjectStore = { state?: { active?: string } } | null;
type BuiltinToolsSettingsProps = { projectStore?: BuiltinToolsProjectStore };
type BuiltinToolView = { id: string; label: string; description?: string; enabled: boolean; provider?: string };
type BuiltinToolProviderNote = { provider: string; label: string; note: string };
type BuiltinToolsReadResult = { tools?: BuiltinToolView[]; providerNotes?: BuiltinToolProviderNote[] };
type BuiltinToolsNoticeState = { level: string; message: string };
type BuiltinToolGroup = {
  provider: string;
  label: string;
  tools: BuiltinToolView[];
  note: string;
  disabledCount: number;
};

// PROVIDER_LABELS gives each provider a short Chinese display name. Backend
// only emits "claude" today; codex appears solely via providerNotes.
const PROVIDER_LABELS: Record<string, string> = {
  claude: 'Claude 内置工具',
  codex: 'Codex 内置工具',
};

function setupBuiltinToolsSettings(props: BuiltinToolsSettingsProps) {
  const tools = ref([]) as { value: BuiltinToolView[] };
  const providerNotes = ref([]) as { value: BuiltinToolProviderNote[] };
  const loading = ref(false) as { value: boolean };
  const savingIds = reactive({} as Record<string, boolean>);
  const expanded = reactive({} as Record<string, boolean>);
  const notice = reactive({ level: 'info', message: '' }) as BuiltinToolsNoticeState;
  const { activeProjectCwd, withProjectCwd } = useSettingsScope(props.projectStore);

  function setNotice(level: string, message: string): void {
    notice.level = level || 'info';
    notice.message = (message || '').toString().trim();
  }

  function applyToolsPayload(payload: BuiltinToolsReadResult | null): void {
    const list = Array.isArray(payload?.tools) ? payload!.tools : [];
    tools.value = list.map((item) => ({
      id: (item.id || '').toString(),
      label: (item.label || item.id || '').toString(),
      description: (item.description || '').toString(),
      enabled: Boolean(item.enabled),
      provider: (item.provider || 'claude').toString(),
    }));
    const notes = Array.isArray(payload?.providerNotes) ? payload!.providerNotes : [];
    providerNotes.value = notes.map((entry) => ({
      provider: (entry.provider || '').toString(),
      label: (entry.label || '').toString(),
      note: (entry.note || '').toString(),
    })).filter((entry) => entry.provider && entry.note);
  }

  // groups merges tool rows and provider notes into per-provider accordion
  // entries the template iterates over. Order: providers that own tools first
  // (registry order), then info-only providers (codex).
  const groups = computed<BuiltinToolGroup[]>(() => {
    const byProvider = new Map<string, BuiltinToolView[]>();
    const order: string[] = [];
    for (const tool of tools.value) {
      const key = tool.provider || 'claude';
      if (!byProvider.has(key)) {
        byProvider.set(key, []);
        order.push(key);
      }
      byProvider.get(key)!.push(tool);
    }
    const noteByProvider = new Map<string, BuiltinToolProviderNote>();
    for (const note of providerNotes.value) {
      noteByProvider.set(note.provider, note);
      if (!byProvider.has(note.provider)) order.push(note.provider);
    }
    return order.map((provider) => {
      const groupTools = byProvider.get(provider) || [];
      const note = noteByProvider.get(provider);
      return {
        provider,
        label: (note?.label) || PROVIDER_LABELS[provider] || provider,
        tools: groupTools,
        note: note?.note || '',
        disabledCount: groupTools.filter((tool) => !tool.enabled).length,
      };
    });
  });

  const totalDisabledCount = computed(() => tools.value.filter((tool) => !tool.enabled).length);
  const totalToolCount = computed(() => tools.value.length);

  function toggleGroupExpanded(provider: string): void {
    if (!provider) return;
    expanded[provider] = !expanded[provider];
  }

  function isGroupExpanded(provider: string): boolean {
    return Boolean(expanded[provider]);
  }

  async function loadBuiltinTools(): Promise<void> {
    loading.value = true;
    try {
      const res = await callAPI('config/builtinTools/read', withProjectCwd({}));
      applyToolsPayload(res as BuiltinToolsReadResult);
      setNotice('info', '');
    } catch (error: any) {
      setNotice('error', `加载失败：${error?.message || error}`);
    } finally {
      loading.value = false;
    }
  }

  // Semantic: clicking a tool row means "flip its disabled state". The visible
  // checkbox tracks `!enabled` (checked = disabled), so a click toggles
  // `enabled` to its opposite, and we send the new enabled state to backend.
  async function toggleBuiltinTool(tool: BuiltinToolView): Promise<void> {
    const id = (tool?.id || '').toString();
    if (!id) return;
    if (savingIds[id]) return;
    const nextEnabled = !tool.enabled;
    savingIds[id] = true;
    const prevSnapshot = tools.value.map((item) => ({ ...item }));
    tools.value = tools.value.map((item) =>
      item.id === id ? { ...item, enabled: nextEnabled } : item,
    );
    try {
      const res = await callAPI('config/builtinTools/write', withProjectCwd({ id, enabled: nextEnabled }));
      applyToolsPayload(res as BuiltinToolsReadResult);
      setNotice('info', `${tool.label || id} 已${nextEnabled ? '启用' : '禁用'}`);
    } catch (error: any) {
      tools.value = prevSnapshot;
      setNotice('error', `保存失败：${error?.message || error}`);
    } finally {
      savingIds[id] = false;
    }
  }

  onMounted(() => {
    loadBuiltinTools();
  });

  watch(
    () => activeProjectCwd.value,
    (next: string, prev: string) => {
      if (next === prev) return;
      loadBuiltinTools();
    },
  );

  return {
    tools,
    providerNotes,
    groups,
    totalDisabledCount,
    totalToolCount,
    loading,
    savingIds,
    expanded,
    notice,
    loadBuiltinTools,
    toggleBuiltinTool,
    toggleGroupExpanded,
    isGroupExpanded,
  };
}

export const BuiltinToolsSettings = {
  name: 'BuiltinToolsSettings',
  props: {
    projectStore: { type: Object, default: null },
  },
  setup: setupBuiltinToolsSettings,
  template: `
    <div class="section-header">UPSTREAM BUILTIN TOOLS</div>
    <div class="data-card-vue" data-testid="settings-builtin-tools-card">
      <div class="data-row-vue">
        <strong>上游内置工具</strong>
        <span data-testid="settings-builtin-tools-summary">
          {{ loading ? '加载中...' : '已禁用 ' + totalDisabledCount + ' / ' + totalToolCount }}
        </span>
      </div>
      <div class="settings-prompt-desc">
        勾选 = 禁用该工具，Agent 会改走项目侧 MCP 等价品（如 lsp_file / lsp_grep / code_run）以保留审计。点击下方分组标题可展开查看。
      </div>
      <div v-if="tools.length === 0 && providerNotes.length === 0 && !loading" class="settings-log-empty" data-testid="settings-builtin-tools-empty">
        暂无可配置的内置工具
      </div>
      <div v-else class="settings-builtin-tool-groups" data-testid="settings-builtin-tools-groups">
        <section
          v-for="group in groups"
          :key="group.provider"
          class="settings-builtin-tool-group"
          :data-testid="'settings-builtin-tool-group-' + group.provider"
        >
          <button
            type="button"
            class="settings-builtin-tool-group-head"
            :data-testid="'settings-builtin-tool-group-head-' + group.provider"
            :aria-expanded="isGroupExpanded(group.provider) ? 'true' : 'false'"
            @click="toggleGroupExpanded(group.provider)"
          >
            <span class="settings-builtin-tool-group-chevron" :class="{ 'is-open': isGroupExpanded(group.provider) }">▸</span>
            <span class="settings-builtin-tool-group-name">{{ group.label }}</span>
            <span class="settings-builtin-tool-group-summary">
              <template v-if="group.tools.length > 0">已禁用 {{ group.disabledCount }} / {{ group.tools.length }}</template>
              <template v-else>仅说明</template>
            </span>
          </button>
          <div v-if="isGroupExpanded(group.provider)" class="settings-builtin-tool-group-body">
            <p v-if="group.note" class="settings-builtin-tool-group-note" :data-testid="'settings-builtin-tool-group-note-' + group.provider">
              {{ group.note }}
            </p>
            <label
              v-for="tool in group.tools"
              :key="tool.id"
              class="settings-prompt-toggle"
              :class="{ 'is-disabled-tool': !tool.enabled }"
              :data-testid="'settings-builtin-tool-' + tool.id"
            >
              <div class="settings-prompt-toggle-copy">
                <span class="settings-prompt-toggle-title">{{ tool.label }}</span>
                <span class="settings-prompt-toggle-desc">
                  {{ tool.description || tool.id }}<span v-if="tool.description" class="settings-builtin-tool-id"> · {{ tool.id }}</span>
                </span>
              </div>
              <input
                type="checkbox"
                class="settings-prompt-toggle-input"
                :data-testid="'settings-builtin-tool-input-' + tool.id"
                :checked="!tool.enabled"
                :disabled="loading || savingIds[tool.id]"
                @change="toggleBuiltinTool(tool)"
              />
            </label>
          </div>
        </section>
      </div>
      <div v-if="notice.message" class="settings-prompt-notice" data-testid="settings-builtin-tools-notice" :class="'is-' + notice.level">
        {{ notice.message }}
      </div>
    </div>
  `,
};
