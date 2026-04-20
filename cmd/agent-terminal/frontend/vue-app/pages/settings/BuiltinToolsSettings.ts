import { onMounted, reactive, ref, watch } from '../../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../../services/api.js';
import { useSettingsScope } from './useSettingsScope.ts';

type BuiltinToolsProjectStore = { state?: { active?: string } } | null;
type BuiltinToolsSettingsProps = { projectStore?: BuiltinToolsProjectStore };
type BuiltinToolView = { id: string; label: string; description?: string; enabled: boolean };
type BuiltinToolsReadResult = { tools?: BuiltinToolView[] };
type BuiltinToolsNoticeState = { level: string; message: string };

function setupBuiltinToolsSettings(props: BuiltinToolsSettingsProps) {
  const tools = ref([]) as { value: BuiltinToolView[] };
  const loading = ref(false) as { value: boolean };
  const savingIds = reactive({} as Record<string, boolean>);
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
    }));
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

  async function toggleBuiltinTool(tool: BuiltinToolView): Promise<void> {
    const id = (tool?.id || '').toString();
    if (!id) return;
    if (savingIds[id]) return;
    const nextEnabled = !tool.enabled;
    savingIds[id] = true;
    // optimistic update
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
    loading,
    savingIds,
    notice,
    loadBuiltinTools,
    toggleBuiltinTool,
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
        <span>{{ loading ? '加载中...' : '按需启用上游 Agent 自带工具' }}</span>
      </div>
      <div class="settings-prompt-desc">
        启用后，上游 Agent 可能直接使用其原生工具（例如「读文件」），绕过项目提供的等价 MCP 工具。默认禁用写入/执行类工具以保留项目侧审计能力。
      </div>
      <div v-if="tools.length === 0 && !loading" class="settings-log-empty" data-testid="settings-builtin-tools-empty">
        暂无可配置的内置工具
      </div>
      <div v-else class="settings-builtin-tools-list" data-testid="settings-builtin-tools-list">
        <label
          v-for="tool in tools"
          :key="tool.id"
          class="settings-prompt-toggle"
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
            :checked="tool.enabled"
            :disabled="loading || savingIds[tool.id]"
            @change="toggleBuiltinTool(tool)"
          />
        </label>
      </div>
      <div v-if="notice.message" class="settings-prompt-notice" data-testid="settings-builtin-tools-notice" :class="'is-' + notice.level">
        {{ notice.message }}
      </div>
    </div>
  `,
};
