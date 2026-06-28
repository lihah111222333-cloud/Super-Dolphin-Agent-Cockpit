// @ts-nocheck

import { computed } from '../../../lib/vue.esm-browser.prod.js';
import { ProjectSelect } from '../ProjectSelect.js';

export function resolveProviderToggleLabel(useClaudeProvider = false) {
  return useClaudeProvider ? 'Claude' : 'Codex';
}

export const ChatToolbar = {
  name: 'ChatToolbar',
  components: {
    ProjectSelect,
  },
  props: {
    isCmd: { type: Boolean, default: false },
    activeStatus: { type: String, default: '' },
    displayStatusText: { type: String, default: '' },
    activeStatusMeta: { type: String, default: '' },
    useClaudeProvider: { type: Boolean, default: false },
    providerPreferenceReady: { type: Boolean, default: true },
    providerPreferenceError: { type: String, default: '' },
    selectedThreadId: { type: String, default: '' },
    threadConfigProvider: { type: String, default: '' },
    threadConfigSupportsOverride: { type: Boolean, default: false },
    threadConfigDraftModel: { type: String, default: '' },
    threadConfigDraftEffort: { type: String, default: '' },
    threadConfigLoading: { type: Boolean, default: false },
    threadConfigSaving: { type: Boolean, default: false },
    threadConfigNotice: { type: String, default: '' },
    threadConfigNoticeLevel: { type: String, default: 'info' },
    threadConfigMeta: { type: Object, default: () => ({ override: {}, effective: {} }) },

    canInterrupt: { type: Boolean, default: false },
    recoveringSelected: { type: Boolean, default: false },
    copyButtonLabel: { type: String, default: '' },
    projectOptions: { type: Array, default: () => [] },
    activeProject: { type: String, default: '.' },
    layoutMode: { type: String, default: '' },
    cmdCardCols: { type: Number, default: 3 },
    /** 窗口实际 CWD（绝对路径） */
    windowCwd: { type: String, default: '' },
    /** 完整展示文本（含 "活动项目：..." 等） */
    cwdDisplay: { type: String, default: '' },
  },
  emits: [
    'update-project',
    'add-project',
    'remove-project',
    'set-cmd-layout',
    'set-cmd-card-cols',
    'copy-thread-info',
    'stop-selected',
    'toggle-provider-mode',
    'launch-one',
    'recover-selected',
    'update-thread-config-model',
    'update-thread-config-effort',
    'save-thread-config',
    'restore-thread-config-inherit',
  ],
  setup(props, { emit }) {
    const providerToggleLabel = computed(() => resolveProviderToggleLabel(props.useClaudeProvider));
    const launchAgentLabel = computed(() => {
      return '新对话';
    });
    const launchAgentTitle = computed(() => {
      return '新对话：发送第一条消息时才会创建会话';
    });
    return {
      emit,
      launchAgentLabel,
      launchAgentTitle,
      providerToggleLabel,
    };
  },
  template: `
    <div class="chat-toolbar unified-toolbar" style="position:relative" data-testid="chat-toolbar">
      <ProjectSelect
        :model-value="activeProject"
        :options="projectOptions"
        @update:model-value="emit('update-project', $event)"
        @add-project="emit('add-project')"
        @remove-project="emit('remove-project', $event)"
      />

      <div class="layout-switch" v-if="isCmd">
        <button class="btn btn-ghost btn-xs" :class="{active: layoutMode==='overview'}" @click="emit('set-cmd-layout', 'overview')">A 紧凑</button>
        <button class="btn btn-ghost btn-xs" :class="{active: layoutMode==='chat'}" @click="emit('set-cmd-layout', 'chat')">B 对话</button>
        <button class="btn btn-ghost btn-xs" :class="{active: layoutMode==='mix'}" @click="emit('set-cmd-layout', 'mix')">C 混合</button>
      </div>

      <div class="layout-switch" v-if="isCmd">
        <button class="btn btn-ghost btn-xs" :class="{active: cmdCardCols===2}" @click="emit('set-cmd-card-cols', 2)">2列</button>
        <button class="btn btn-ghost btn-xs" :class="{active: cmdCardCols===3}" @click="emit('set-cmd-card-cols', 3)">3列</button>
      </div>

      <button
        v-if="!isCmd && selectedThreadId"
        class="btn btn-ghost btn-xs chat-toolbar-icon-btn"
        :aria-label="copyButtonLabel"
        :title="copyButtonLabel"
        @click="emit('copy-thread-info')"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <rect x="9" y="7" width="10" height="13" rx="2.2"></rect>
          <path d="M15 7V5.8A1.8 1.8 0 0 0 13.2 4H6.8A1.8 1.8 0 0 0 5 5.8V16.2A1.8 1.8 0 0 0 6.8 18H9"></path>
          <path d="M12 11.5h4"></path>
          <path d="M12 15h4"></path>
        </svg>
      </button>

      <button
        v-if="!isCmd && selectedThreadId"
        class="btn btn-ghost btn-xs chat-toolbar-icon-btn"
        :disabled="!canInterrupt"
        :aria-label="canInterrupt ? '停止' : '当前没有可中断任务'"
        :title="canInterrupt ? '中断当前执行' : '当前没有可中断任务'"
        @click="emit('stop-selected')"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="8"></circle>
          <rect x="9" y="9" width="6" height="6" rx="1.2"></rect>
        </svg>
      </button>
      <label
        v-if="false"
        class="provider-toggle"
        :class="{ active: useClaudeProvider }"
        data-testid="provider-toggle"
        title="切换 Claude / Codex provider"
      >
        <input
          type="checkbox"
          :checked="useClaudeProvider"
          @change="emit('toggle-provider-mode')"
          class="provider-toggle-input"
        />
        <span class="provider-toggle-track">
          <span class="provider-toggle-thumb"></span>
        </span>
        <span class="provider-toggle-label">{{ providerToggleLabel }}</span>
      </label>
      <button
        class="btn launch-agent-icon-btn"
        data-testid="launch-agent-button"
        :aria-label="launchAgentLabel"
        :title="launchAgentTitle"
        @click="emit('launch-one')"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M4 20h4l10-10a2.2 2.2 0 0 0-3.1-3.1L4.9 16.8 4 20z"></path>
          <path d="M13.8 7.2l3 3"></path>
        </svg>
      </button>
      <button
        v-if="!isCmd"
        class="btn btn-ghost btn-xs btn-warning chat-toolbar-icon-btn"
        data-testid="recover-agent-button"
        :disabled="recoveringSelected || !selectedThreadId"
        :aria-label="recoveringSelected ? '恢复中' : (selectedThreadId ? '进程恢复' : '请先选择会话')"
        :title="recoveringSelected ? '进程恢复中…' : (selectedThreadId ? '手动杀进程并恢复连接' : '请先选择会话')"
        @click="emit('recover-selected')"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M20 12a8 8 0 1 1-2.3-5.6"></path>
          <path d="M20 4v5h-5"></path>
          <path d="M12 9v6"></path>
          <path d="M9 12h6"></path>
        </svg>
      </button>



      <!-- flex spacer：消费剩余空间，把右侧整组推到右边缘 -->
      <div class="toolbar-filler" aria-hidden="true"></div>

      <!-- CWD 图标徽章：显示当前窗口工作目录 -->
      <div
        v-if="windowCwd"
        class="cwd-badge"
        :title="cwdDisplay || windowCwd"
        aria-label="当前工作目录"
      >
        <svg class="cwd-badge-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"></path>
        </svg>
        <span class="cwd-badge-text">{{ windowCwd.split('/').filter(Boolean).pop() || windowCwd }}</span>
      </div>
    </div>
  `,
};
