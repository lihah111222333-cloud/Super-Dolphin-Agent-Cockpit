import { computed, onMounted, reactive, ref, watch } from '../../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../../services/api.js';
import { useSettingsScope } from './useSettingsScope.ts';

type BuiltinToolsProjectStore = { state?: { active?: string } } | null;
type BuiltinToolsSettingsProps = { projectStore?: BuiltinToolsProjectStore };
type BuiltinToolView = { id: string; label: string; description?: string; enabled: boolean; provider?: string; replacedBy?: string; filterMode?: string };
type BuiltinToolsReadResult = { tools?: BuiltinToolView[] };
type BuiltinToolsNoticeState = { level: string; message: string };
type BuiltinToolGroup = {
  key: string;
  label: string;
  tools: BuiltinToolView[];
  note?: string;
  disabledCount: number;
  canToggle: boolean;
};

// PROVIDER_LABELS gives each provider a short Chinese display name.
const PROVIDER_LABELS: Record<string, string> = {
  claude: 'Claude 内置工具',
  codex: 'Codex 内置工具',
};

function setupBuiltinToolsSettings(props: BuiltinToolsSettingsProps) {
  const tools = ref([]) as { value: BuiltinToolView[] };
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
      replacedBy: item.replacedBy ? (item.replacedBy || '').toString() : undefined,
      filterMode: (item.filterMode || '').toString() || undefined,
    }));
  }

  // groups splits tools into three filter-mode buckets:
  //   hard       — disabled tools whose filterMode is "hard" (blocked at CLI startup)
  //   soft       — disabled tools whose filterMode is "soft" (suppressed via prompt)
  //   unfiltered — enabled tools (not yet filtered)
  const groups = computed<BuiltinToolGroup[]>(() => {
    const disabled = tools.value.filter((t) => !t.enabled || t.replacedBy);
    const hardDisabled = disabled.filter((t) => t.filterMode === 'hard');
    const softDisabled = disabled.filter((t) => t.filterMode !== 'hard');
    const notFiltered = tools.value.filter((t) => t.enabled && !t.replacedBy);

    const result: BuiltinToolGroup[] = [];

    if (hardDisabled.length > 0) {
      result.push({
        key: 'hard',
        label: `启动时过滤（${hardDisabled.length}）—— CLI 启动参数直接屏蔽`,
        tools: hardDisabled,
        disabledCount: hardDisabled.length,
        canToggle: true,
      });
    }
    if (softDisabled.length > 0) {
      result.push({
        key: 'soft',
        label: `软过滤提示（${softDisabled.length}）—— 通过 prompt 指令抑制`,
        tools: softDisabled,
        disabledCount: softDisabled.length,
        canToggle: true,
      });
    }
    if (notFiltered.length > 0) {
      result.push({
        key: 'unfiltered',
        label: `未过滤（${notFiltered.length}）`,
        tools: notFiltered,
        disabledCount: 0,
        canToggle: true,
      });
    }

    return result;
  });

  const filteredCount = computed(() => tools.value.filter((t) => t.replacedBy || !t.enabled).length);
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
    if (tool.replacedBy) return;
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
    groups,
    filteredCount,
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
    <div class="section-header">原生工具过滤</div>
    <div class="data-card-vue" data-testid="settings-builtin-tools-card">
      <div class="data-row-vue">
        <strong>原生工具过滤</strong>
        <span data-testid="settings-builtin-tools-summary">
          {{ loading ? '加载中...' : '已过滤 ' + filteredCount + ' / ' + totalToolCount }}
        </span>
      </div>
      <div class="settings-prompt-desc">
        过滤底层 CLI 原生工具
      </div>

      <div v-if="tools.length === 0 && !loading" class="settings-log-empty" data-testid="settings-builtin-tools-empty">
        暂无可配置的内置工具
      </div>
      <div v-else class="settings-builtin-tool-groups" data-testid="settings-builtin-tools-groups">
        <section
          v-for="group in groups"
          :key="group.key"
          class="settings-builtin-tool-group"
          :data-testid="'settings-builtin-tool-group-' + group.key"
        >
          <button
            type="button"
            class="settings-builtin-tool-group-head"
            :data-testid="'settings-builtin-tool-group-head-' + group.key"
            :aria-expanded="isGroupExpanded(group.key) ? 'true' : 'false'"
            @click="toggleGroupExpanded(group.key)"
          >
            <span class="settings-builtin-tool-group-chevron" :class="{ 'is-open': isGroupExpanded(group.key) }">▸</span>
            <span class="settings-builtin-tool-group-name">{{ group.label }}</span>
            <span class="settings-builtin-tool-group-summary">
              <template v-if="group.tools.length > 0">已过滤 {{ group.disabledCount }} / {{ group.tools.length }}</template>
              <template v-else>仅说明</template>
            </span>
          </button>
          <div v-if="isGroupExpanded(group.key)" class="settings-builtin-tool-group-body">
            <p v-if="group.note" class="settings-builtin-tool-group-note" :data-testid="'settings-builtin-tool-group-note-' + group.key">
              {{ group.note }}
            </p>
            <label
              v-for="tool in group.tools"
              :key="tool.id"
              class="settings-prompt-toggle"
              :class="{ 'is-disabled-tool': !tool.enabled || tool.replacedBy }"
              :data-testid="'settings-builtin-tool-' + tool.id"
            >
              <div class="settings-prompt-toggle-copy">
                <span class="settings-prompt-toggle-title">{{ tool.label }}</span>
                <span class="settings-prompt-toggle-desc">
{{ tool.description || tool.id }}<span v-if="tool.description" class="settings-builtin-tool-id"> · {{ tool.id }}</span><span v-if="tool.provider" class="settings-builtin-tool-provider"> [{{ tool.provider }}]</span><span v-if="tool.filterMode" class="settings-builtin-tool-filter-mode"> [{{ tool.filterMode }}]</span><span v-if="tool.replacedBy" class="settings-builtin-tool-replaced"> ← {{ tool.replacedBy }}</span>
                </span>
              </div>
              <input
                type="checkbox"
                class="settings-prompt-toggle-input"
                :data-testid="'settings-builtin-tool-input-' + tool.id"
                :checked="!tool.enabled || !!tool.replacedBy"
                :disabled="!!tool.replacedBy || savingIds[tool.id]"
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
