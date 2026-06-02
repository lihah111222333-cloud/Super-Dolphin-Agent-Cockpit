import { computed, onMounted, reactive, ref, watch } from '../../../lib/vue.esm-browser.prod.js';
import { callAPI, copyTextToClipboard } from '../../services/api.js';
import { useSettingsScope } from './useSettingsScope.ts';

type LspPromptProjectStore = { state?: { active?: string } } | null;
type LspPromptSettingsProps = { projectStore?: LspPromptProjectStore };
type LspPromptNoticeState = { level: string; message: string };

function setupLspPromptSettings(props: LspPromptSettingsProps) {
  const PREF_KEY_SHOW_INJECTED_PROMPT = 'settings.showInjectedPromptInChat';
  const lspPromptHint = ref('') as { value: string };
  const lspPromptEffectiveHint = ref('') as { value: string };
  const lspPromptDefaultHint = ref('') as { value: string };
  const lspPromptUsingDefault = ref(true) as { value: boolean };
  const lspPromptLoading = ref(false) as { value: boolean };
  const lspPromptSaving = ref(false) as { value: boolean };
  const lspPromptNotice = reactive({ level: 'info', message: '' }) as LspPromptNoticeState;
  const showInjectedPromptInChat = ref(false) as { value: boolean };
  const showInjectedPromptSaving = ref(false) as { value: boolean };
  const currentScopeCwd = ref('') as { value: string };
  const { activeProjectCwd, withProjectCwd, parseBoolPreference } = useSettingsScope(props.projectStore);

  const lspPromptDisplayHint = computed(() => {
    const text = (lspPromptEffectiveHint.value || lspPromptDefaultHint.value || '').toString().trim();
    return text || '暂无可用提示词';
  });
  const lspPromptLineCount = computed(() => {
    const text = lspPromptDisplayHint.value;
    if (!text || text === '暂无可用提示词') return 0;
    return text.split('\n').length;
  });
  const lspPromptCharCount = computed(() => {
    const text = lspPromptDisplayHint.value;
    if (!text || text === '暂无可用提示词') return 0;
    return text.length;
  });

  function setLSPPromptNotice(level: string, message: string): void {
    lspPromptNotice.level = level || 'info';
    lspPromptNotice.message = (message || '').toString().trim();
  }

  async function loadLSPPromptHint(): Promise<void> {
    lspPromptLoading.value = true;
    try {
      const res = await callAPI('config/lspPromptHint/read', withProjectCwd({}));
      const hint = (res?.hint || '').toString();
      const defaultHint = (res?.defaultHint || '').toString();
      const overrideHint = (res?.overrideHint || '').toString();
      const usingDefault = Boolean(res?.usingDefault);
      lspPromptHint.value = overrideHint;
      lspPromptEffectiveHint.value = hint;
      lspPromptDefaultHint.value = defaultHint;
      lspPromptUsingDefault.value = usingDefault || overrideHint.trim() === '';
      setLSPPromptNotice('info', '');
    } catch (error: any) {
      setLSPPromptNotice('error', `加载失败：${error?.message || error}`);
    } finally {
      lspPromptLoading.value = false;
    }
  }

  async function loadCurrentScopeCwd(): Promise<void> {
    try {
      const cfg = await callAPI('config/read', {});
      currentScopeCwd.value = (cfg?.cwd || '').toString().trim();
    } catch {
      currentScopeCwd.value = '';
    }
  }

  async function saveLSPPromptHint(): Promise<void> {
    if (lspPromptSaving.value) return;
    lspPromptSaving.value = true;
    try {
      const res = await callAPI('config/lspPromptHint/write', withProjectCwd({
        hint: lspPromptHint.value,
      }));
      lspPromptEffectiveHint.value = (res?.hint || '').toString();
      lspPromptDefaultHint.value = (res?.defaultHint || lspPromptDefaultHint.value || '').toString();
      lspPromptHint.value = (res?.overrideHint || '').toString();
      lspPromptUsingDefault.value = Boolean(res?.usingDefault);
      if (lspPromptUsingDefault.value) {
        setLSPPromptNotice('info', '已恢复默认提示词');
      } else {
        setLSPPromptNotice('info', '提示词已保存');
      }
    } catch (error: any) {
      setLSPPromptNotice('error', `保存失败：${error?.message || error}`);
    } finally {
      lspPromptSaving.value = false;
    }
  }

  async function resetLSPPromptHint(): Promise<void> {
    if (lspPromptSaving.value) return;
    lspPromptHint.value = '';
    await saveLSPPromptHint();
  }

  async function copyEffectivePromptHint(): Promise<void> {
    const text = lspPromptDisplayHint.value;
    if (!text || text === '暂无可用提示词') {
      setLSPPromptNotice('error', '暂无可复制内容');
      return;
    }
    try {
      const ok = await copyTextToClipboard(text);
      if (ok) {
        setLSPPromptNotice('info', '已复制生效提示词');
      } else {
        setLSPPromptNotice('error', '复制失败');
      }
    } catch (error: any) {
      setLSPPromptNotice('error', `复制失败：${error?.message || error}`);
    }
  }

  async function loadInjectedPromptVisibility(): Promise<void> {
    try {
      const value = await callAPI('ui/preferences/get', withProjectCwd({ key: PREF_KEY_SHOW_INJECTED_PROMPT }));
      showInjectedPromptInChat.value = parseBoolPreference(value);
    } catch (error: any) {
      setLSPPromptNotice('error', `加载聊天注入显示开关失败：${error?.message || error}`);
    }
  }

  async function saveInjectedPromptVisibility(): Promise<void> {
    if (showInjectedPromptSaving.value) return;
    showInjectedPromptSaving.value = true;
    const next = Boolean(showInjectedPromptInChat.value);
    try {
      await callAPI('ui/preferences/set', withProjectCwd({ key: PREF_KEY_SHOW_INJECTED_PROMPT, value: next }));
      setLSPPromptNotice('info', next ? '聊天区已改为显示自动注入内容' : '聊天区已改为隐藏自动注入内容');
    } catch (error: any) {
      setLSPPromptNotice('error', `保存聊天注入显示开关失败：${error?.message || error}`);
      await loadInjectedPromptVisibility();
    } finally {
      showInjectedPromptSaving.value = false;
    }
  }

  onMounted(() => {
    loadLSPPromptHint();
    loadCurrentScopeCwd();
    loadInjectedPromptVisibility();
  });

  watch(
    () => activeProjectCwd.value,
    (next: string, prev: string) => {
      if (next === prev) return;
      loadLSPPromptHint();
      loadCurrentScopeCwd();
      loadInjectedPromptVisibility();
    },
  );

  return {
    lspPromptHint,
    lspPromptEffectiveHint,
    lspPromptDefaultHint,
    lspPromptUsingDefault,
    lspPromptLoading,
    lspPromptSaving,
    lspPromptNotice,
    showInjectedPromptInChat,
    showInjectedPromptSaving,
    currentScopeCwd,
    lspPromptDisplayHint,
    lspPromptLineCount,
    lspPromptCharCount,
    loadLSPPromptHint,
    saveLSPPromptHint,
    resetLSPPromptHint,
    copyEffectivePromptHint,
    saveInjectedPromptVisibility,
  };
}

export const LspPromptSettings = {
  name: 'LspPromptSettings',
  props: {
    projectStore: { type: Object, default: null },
  },
  setup: setupLspPromptSettings,
  template: `
    <div class="section-header">PROMPT</div>
    <div class="data-card-vue settings-prompt-card" data-testid="settings-lsp-prompt-card">
      <div class="data-row-vue">
        <strong>自动注入提示词（LSP / Playwright / json-render）</strong>
        <span>{{ lspPromptLoading ? '加载中...' : (lspPromptUsingDefault ? '默认注入' : '自定义覆盖') }}</span>
      </div>
      <div class="settings-prompt-desc">下方“生效内容”是后端每轮实际注入文本；“覆盖编辑”用于调试，留空保存可恢复默认。</div>
      <div class="settings-prompt-meta" data-testid="settings-lsp-effective-cwd">
        当前作用 CWD：{{ currentScopeCwd || '未知' }}
      </div>
      <label class="settings-prompt-toggle" data-testid="settings-show-injected-toggle">
        <div class="settings-prompt-toggle-copy">
          <span class="settings-prompt-toggle-title">聊天区显示自动注入内容（调试）</span>
          <span class="settings-prompt-toggle-desc">开启后将保留每轮消息里的“已注入 ...”段。</span>
        </div>
        <input
          type="checkbox"
          class="settings-prompt-toggle-input"
          data-testid="settings-show-injected-toggle-input"
          v-model="showInjectedPromptInChat"
          @change="saveInjectedPromptVisibility"
          :disabled="lspPromptLoading || showInjectedPromptSaving"
        />
      </label>
      <div class="settings-prompt-meta">生效行数 {{ lspPromptLineCount }} · 字符 {{ lspPromptCharCount }}</div>
      <div class="settings-prompt-label">当前生效内容（只读）</div>
      <textarea
        class="settings-prompt-textarea settings-prompt-textarea-readonly"
        data-testid="settings-lsp-effective-output"
        rows="12"
        :value="lspPromptDisplayHint"
        readonly
      ></textarea>
      <div class="settings-prompt-label">自定义覆盖（可编辑，空=默认）</div>
      <textarea
        class="settings-prompt-textarea"
        data-testid="settings-lsp-prompt-input"
        rows="8"
        v-model="lspPromptHint"
        :placeholder="lspPromptDefaultHint || '请输入提示词'"
        :disabled="lspPromptLoading || lspPromptSaving"
      ></textarea>
      <div v-if="lspPromptNotice.message" class="settings-prompt-notice" data-testid="settings-lsp-prompt-notice" :class="'is-' + lspPromptNotice.level">
        {{ lspPromptNotice.message }}
      </div>
      <div class="settings-action-row settings-action-inline">
        <button class="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-refresh-button" @click="loadLSPPromptHint" :disabled="lspPromptSaving">刷新</button>
        <button class="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-copy-button" @click="copyEffectivePromptHint" :disabled="lspPromptLoading || lspPromptSaving">复制生效提示词</button>
        <button class="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-reset-button" @click="resetLSPPromptHint" :disabled="lspPromptLoading || lspPromptSaving">恢复默认</button>
        <button class="btn btn-primary btn-toolbar-sm" data-testid="settings-lsp-save-button" @click="saveLSPPromptHint" :disabled="lspPromptLoading || lspPromptSaving">
          {{ lspPromptSaving ? '保存中...' : '保存提示词' }}
        </button>
      </div>
    </div>
  `,
};
