import { computed, onMounted, reactive, ref, watch } from '../../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../../services/api.js';
import { useSettingsScope } from './useSettingsScope.ts';

type BuiltinToolsProjectStore = { state?: { active?: string } } | null;
type BuiltinToolsSettingsProps = { projectStore?: BuiltinToolsProjectStore };
type BuiltinToolView = { id: string; label: string; description?: string; enabled: boolean; provider?: string; replacedBy?: string; filterMode?: string; enforcement?: string };
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
  claude: 'Claude',
  codex: 'Codex',
};

const ENFORCEMENT_LABELS: Record<string, string> = {
  'native-hard': '启动前已关闭',
  'effect-hard': '已限制为只读',
  'soft-audit': '仅提醒使用项目工具',
};

const GROUP_NOTES: Record<string, string> = {
  'native-hard': '模型启动前就看不到这些能力。',
  'effect-hard': 'Codex 暂不支持单独关闭这类能力，已限制为只读，避免它直接改文件或执行命令。',
  'soft-audit': 'Codex 暂不支持可靠关闭这类能力，只能提示模型优先使用本项目工具；这不是强制拦截。',
};

function enforcementBucket(tool: BuiltinToolView): string {
  const enforcement = (tool.enforcement || '').toString().trim();
  if (enforcement) return enforcement;
  if (tool.filterMode === 'hard') return 'native-hard';
  return 'soft-audit';
}

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
	      enforcement: (item.enforcement || '').toString() || undefined,
	    }));
	  }

	  function pushGroup(result: BuiltinToolGroup[], key: string, label: string, items: BuiltinToolView[], note?: string): void {
	    if (items.length === 0) return;
	    result.push({
	      key,
	      label: `${label}（${items.length}）`,
	      tools: items,
	      note,
	      disabledCount: items.length,
	      canToggle: true,
	    });
	  }

	  // groups splits tools by the actual backend enforcement tier, not only by
	  // the provider-declared filterMode. Codex can be native-hard, effect-hard,
	  // or soft-audit depending on the disabled tool combination.
	  const groups = computed<BuiltinToolGroup[]>(() => {
	    const disabled = tools.value.filter((t) => !t.enabled || t.replacedBy);
	    const nativeHard = disabled.filter((t) => enforcementBucket(t) === 'native-hard');
	    const effectHard = disabled.filter((t) => enforcementBucket(t) === 'effect-hard');
	    const softAudit = disabled.filter((t) => enforcementBucket(t) === 'soft-audit');
	    const notFiltered = tools.value.filter((t) => t.enabled && !t.replacedBy);

	    const result: BuiltinToolGroup[] = [];

	    pushGroup(result, 'native-hard', ENFORCEMENT_LABELS['native-hard'], nativeHard, GROUP_NOTES['native-hard']);
	    pushGroup(result, 'effect-hard', ENFORCEMENT_LABELS['effect-hard'], effectHard, GROUP_NOTES['effect-hard']);
	    pushGroup(result, 'soft-audit', ENFORCEMENT_LABELS['soft-audit'], softAudit, GROUP_NOTES['soft-audit']);
	    if (notFiltered.length > 0) {
	      result.push({
	        key: 'unfiltered',
	        label: `保持可用（${notFiltered.length}）`,
	        tools: notFiltered,
	        disabledCount: 0,
	        canToggle: true,
      });
    }

    return result;
  });

	  const filteredCount = computed(() => tools.value.filter((t) => t.replacedBy || !t.enabled).length);
	  const totalToolCount = computed(() => tools.value.length);

	  function groupSummary(group: BuiltinToolGroup): string {
	    if (group.key === 'unfiltered') return `可用 ${group.tools.length} 项`;
	    return `已管控 ${group.disabledCount} 项`;
	  }

	  function toolStatusLabel(tool: BuiltinToolView): string {
	    if (tool.replacedBy) return '已由项目工具接管';
	    if (tool.enabled) return '保持可用';
	    return ENFORCEMENT_LABELS[enforcementBucket(tool)] || '已管控';
	  }

	  function toolMetaText(tool: BuiltinToolView): string {
	    const parts: string[] = [];
	    const description = (tool.description || '').toString().trim();
	    if (description) parts.push(description);
	    const provider = PROVIDER_LABELS[(tool.provider || '').toString()] || (tool.provider || '').toString().trim();
	    if (provider) parts.push(provider);
	    parts.push(toolStatusLabel(tool));
	    return parts.join(' · ');
	  }

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
	    groupSummary,
	    toolMetaText,
	  };
	}

export const BuiltinToolsSettings = {
  name: 'BuiltinToolsSettings',
  props: {
    projectStore: { type: Object, default: null },
  },
  setup: setupBuiltinToolsSettings,
  template: `
	    <div class="section-header">模型内置能力</div>
	    <div class="data-card-vue" data-testid="settings-builtin-tools-card">
	      <div class="data-row-vue">
	        <strong>内置能力开关</strong>
	        <span data-testid="settings-builtin-tools-summary">
	          {{ loading ? '加载中...' : '已管控 ' + filteredCount + ' / ' + totalToolCount }}
	        </span>
	      </div>
	      <div class="settings-prompt-desc">
	        默认管控与本项目文件、命令、编排、计划、权限、插件管理重复，或会绕过项目治理的能力。
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
	              {{ groupSummary(group) }}
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
	                  {{ toolMetaText(tool) }}
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
