// @ts-nocheck

import { ref } from '../../../lib/vue.esm-browser.prod.js';
import { parseAgentBadge } from '../../stores/thread-view.model.js';

export const ThreadRailSidePanel = {
  name: 'ThreadRailSidePanel',
  props: {
    showArchivedThreadList: { type: Boolean, default: false },
    activeChatThreadCount: { type: Number, default: 0 },
    archivedChatThreadCount: { type: Number, default: 0 },
    visibleChatThreadCards: { type: Array, default: () => [] },
    threadRailDragging: { type: Boolean, default: false },
    threadRailStyle: { type: [String, Object], default: () => ({}) },
    editingThreadId: { type: String, default: '' },
    editingAlias: { type: String, default: '' },
    renamingThreadId: { type: String, default: '' },
    setRenameInputRef: { type: Function, default: null },
    // Phase 1 遗留：会话上下文警报等级 map，键为 thread.id，值为 'normal'|'warn'|'danger'|'critical'
    tokenLevelByThreadId: { type: Object, default: () => ({}) },
  },
  emits: [
    'open-new-window',
    'toggle-archived-thread-list',
    'select-thread',
    'toggle-thread-pin',
    'toggle-thread-archive',
    'begin-inline-rename',
    'submit-inline-rename',
    'handle-inline-rename-enter',
    'cancel-inline-rename',
    'handle-inline-rename-blur',
    'update-editing-alias',
    'delete-stale-threads',
  ],
  setup(_, { emit }) {
    const confirmCleanMode = ref(false);
    return {
      emit,
      parseAgentBadge,
      confirmCleanMode,
      startClean() { confirmCleanMode.value = true; },
      cancelClean() { confirmCleanMode.value = false; },
      doClean(staleIds) { confirmCleanMode.value = false; emit('delete-stale-threads', staleIds); },
    };
  },
  template: `
    <aside
      class="thread-rail"
      :class="{ dragging: threadRailDragging }"
      :style="threadRailStyle"
      data-testid="thread-rail"
      :aria-label="showArchivedThreadList ? '归档会话列表' : '会话列表'"
    >
      <header class="thread-rail-header">
        <div class="thread-rail-header-main">
          <span
            class="thread-rail-kind-icon"
            role="img"
            :aria-label="showArchivedThreadList ? '归档列表' : '会话列表'"
            :title="showArchivedThreadList ? '归档列表' : '会话列表'"
          >
            <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M10 3V5"></path>
              <path d="M6.2 5H13.8C15 5 16 6 16 7.2V12.8C16 14 15 15 13.8 15H6.2C5 15 4 14 4 12.8V7.2C4 6 5 5 6.2 5Z"></path>
              <path d="M2.8 8V12"></path>
              <path d="M17.2 8V12"></path>
              <circle cx="8" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
              <circle cx="12" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
            </svg>
          </span>
          <span
            class="thread-rail-count-chip"
            role="img"
            :aria-label="showArchivedThreadList ? (archivedChatThreadCount + ' 个 Agent') : (activeChatThreadCount + ' 个 Agent')"
            :title="showArchivedThreadList ? (archivedChatThreadCount + ' 个 Agent') : (activeChatThreadCount + ' 个 Agent')"
          >
            <svg class="thread-rail-count-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M10 3V5"></path>
              <path d="M6.2 5H13.8C15 5 16 6 16 7.2V12.8C16 14 15 15 13.8 15H6.2C5 15 4 14 4 12.8V7.2C4 6 5 5 6.2 5Z"></path>
              <path d="M2.8 8V12"></path>
              <path d="M17.2 8V12"></path>
              <circle cx="8" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
              <circle cx="12" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
            </svg>
            <strong>{{ showArchivedThreadList ? archivedChatThreadCount : activeChatThreadCount }}</strong>
          </span>
        </div>
        <button
          type="button"
          class="btn btn-ghost btn-xs thread-rail-new-window-btn"
          data-testid="new-window-btn"
          aria-label="新窗口 (独立进程)"
          title="新窗口 (独立进程)"
          @click="emit('open-new-window')"
        >
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <line x1="8" y1="3" x2="8" y2="13"></line>
            <line x1="3" y1="8" x2="13" y2="8"></line>
          </svg>
        </button>
        <button
          v-if="showArchivedThreadList && !confirmCleanMode && visibleChatThreadCards.some(c => c.isStale)"
          type="button"
          class="btn btn-ghost btn-xs thread-rail-clean-btn"
          data-testid="thread-clean-stale-btn"
          aria-label="清理无用对话"
          title="清理无用对话"
          @click="startClean()"
        >
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M3 4h10"></path>
            <path d="M5 4V3a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v1"></path>
            <path d="M4 4l1 9a1 1 0 0 0 1 1h4a1 1 0 0 0 1-1l1-9"></path>
          </svg>
        </button>
        <button
          v-if="showArchivedThreadList && confirmCleanMode"
          type="button"
          class="btn btn-ghost btn-xs thread-rail-confirm-btn"
          data-testid="thread-clean-confirm-btn"
          @click="doClean(visibleChatThreadCards.filter(c => c.isStale).map(c => c.id))"
        >确认</button>
        <button
          v-if="showArchivedThreadList && confirmCleanMode"
          type="button"
          class="btn btn-ghost btn-xs thread-rail-cancel-btn"
          data-testid="thread-clean-cancel-btn"
          @click="cancelClean()"
        >取消</button>
        <button
          type="button"
          class="btn btn-ghost btn-xs thread-rail-switch-btn"
          data-testid="thread-archive-toggle"
          :class="{ active: showArchivedThreadList }"
          :aria-label="showArchivedThreadList ? '返回会话列表' : '打开归档列表'"
          :title="showArchivedThreadList ? '返回会话列表' : '打开归档列表'"
          @click="emit('toggle-archived-thread-list')"
        >
          <svg v-if="showArchivedThreadList" viewBox="0 0 16 16" fill="none" aria-hidden="true">
            <path d="M6 4L10 8L6 12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" transform="rotate(180 8 8)"></path>
          </svg>
          <svg v-else viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M2.2 3.3h11.6a.9.9 0 0 1 .9.9v1.7a.9.9 0 0 1-.9.9H2.2a.9.9 0 0 1-.9-.9V4.2a.9.9 0 0 1 .9-.9Z"></path>
            <path d="M3.4 6.8h9.2V12a1 1 0 0 1-1 1h-7.2a1 1 0 0 1-1-1V6.8Z"></path>
            <path d="M6.1 9.3h3.8" stroke-linecap="round"></path>
          </svg>
        </button>
      </header>
      <div v-if="visibleChatThreadCards.length === 0" class="thread-rail-empty" data-testid="thread-empty-state">
        {{ showArchivedThreadList ? '暂无归档会话' : '暂无会话，点击顶部「新对话」开始草稿' }}
      </div>
      <div v-else class="thread-rail-list hide-scrollbar" data-testid="thread-list">
        <div
          v-for="thread in visibleChatThreadCards"
          :key="thread.id"
          class="thread-rail-item"
          :class="{ active: thread.selected, archived: thread.isArchived }"
          :data-thread-id="thread.id"
          role="button"
          tabindex="0"
          @click="emit('select-thread', thread.id)"
          @keydown.enter.self.prevent="emit('select-thread', thread.id)"
          @keydown.space.self.prevent="emit('select-thread', thread.id)"
          :title="thread.name"
        >

          <div class="thread-rail-item-head" :class="{ editing: editingThreadId === thread.id }">
            <button
              v-if="editingThreadId !== thread.id"
              type="button"
              class="thread-rail-pin-btn"
              :class="{ active: thread.isPinned }"
              :aria-label="thread.isPinned ? '取消置顶会话' : '置顶会话'"
              :title="thread.isPinned ? '取消置顶' : '置顶'"
              @click.stop="emit('toggle-thread-pin', thread.id)"
            >
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                <path d="M9.5 2.5L13.5 6.5L10 10L8 14L2 8L6 6L9.5 2.5Z" stroke-linejoin="round"></path>
                <path d="M6 10L2.5 13.5" stroke-linecap="round"></path>
              </svg>
            </button>
            <template v-if="editingThreadId === thread.id">
              <input
                :ref="(el) => setRenameInputRef && setRenameInputRef(thread.id, el)"
                :value="editingAlias"
                class="thread-rail-alias-input"
                type="text"
                maxlength="64"
                aria-label="会话别名"
                placeholder="输入别名"
                :disabled="renamingThreadId === thread.id"
                @input="emit('update-editing-alias', $event.target.value)"
                @click.stop
                @keydown.enter.stop="emit('handle-inline-rename-enter', $event, thread.id)"
                @keydown.esc.prevent="emit('cancel-inline-rename', thread.id)"
                @blur="emit('handle-inline-rename-blur', $event, thread.id)"
              >

              <button
                type="button"
                class="thread-rail-save-btn"
                :data-rename-save-button-for="thread.id"
                :disabled="renamingThreadId === thread.id"
                title="保存别名"
                @mousedown.prevent
                @click.stop="emit('submit-inline-rename', thread.id)"
                @dblclick.stop.prevent="emit('submit-inline-rename', thread.id)"
              >保存</button>
            </template>
            <strong
              v-else
              class="thread-rail-name"
              @click.stop="emit('begin-inline-rename', thread.id)"
            ><span
              v-if="parseAgentBadge(thread.name).label"
              class="thread-agent-pill"
              :title="'智能路由：' + parseAgentBadge(thread.name).label"
            >{{ parseAgentBadge(thread.name).label }}</span>{{ parseAgentBadge(thread.name).name }}</strong>
            <button
              v-if="editingThreadId !== thread.id"
              type="button"
              class="thread-rail-archive-btn"
              :class="{ active: thread.isArchived }"
              :aria-label="thread.isArchived ? '恢复会话' : '归档会话'"
              :title="thread.isArchived ? '恢复' : '归档'"
              @click.stop="emit('toggle-thread-archive', thread.id)"
            >
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                <path d="M2.2 3.3h11.6a.9.9 0 0 1 .9.9v1.7a.9.9 0 0 1-.9.9H2.2a.9.9 0 0 1-.9-.9V4.2a.9.9 0 0 1 .9-.9Z"></path>
                <path d="M3.4 6.8h9.2V12a1 1 0 0 1-1 1h-7.2a1 1 0 0 1-1-1V6.8Z"></path>
                <path d="M6.1 9.3h3.8" stroke-linecap="round"></path>
              </svg>
            </button>

            <span v-if="editingThreadId !== thread.id && thread.provider" class="thread-cli-badge" :class="'cli-' + thread.provider">{{ thread.provider === 'claude' ? 'Claude' : 'Codex' }}</span>

            <span v-else-if="editingThreadId !== thread.id && (thread.agentTitle || thread.agentKey || thread.promptKey)" class="thread-agent-badge" :title="'路由 agent：' + (thread.agentKey || '-') + (thread.promptKey ? (' / prompt：' + thread.promptKey) : '')">{{ thread.agentTitle || thread.agentKey || thread.promptKey }}</span>

            <span v-if="editingThreadId !== thread.id && thread.cwdMismatch" class="thread-cwd-mismatch-badge" :title="thread.cwdMismatchReason || 'CWD 不匹配'">⚠ CWD</span>
            <span
              v-if="editingThreadId !== thread.id && tokenLevelByThreadId[thread.id] && tokenLevelByThreadId[thread.id] !== 'normal'"
              class="thread-context-usage-badge"
              :class="'is-token-' + tokenLevelByThreadId[thread.id]"
              :title="'上下文使用率已达 ' + tokenLevelByThreadId[thread.id] + ' 阈值'"
              data-testid="thread-rail-token-badge"
            >⚠</span>
          </div>
          <div v-if="thread.showId" class="thread-rail-item-id">{{ thread.id }}</div>
          <div class="thread-rail-item-meta">
            <span class="status-dot" :class="thread.status"></span>
            <span>{{ thread.statusHeader }}</span>
            <span v-if="thread.isStale" class="thread-stale-badge" :data-stale-reason="thread.staleReason">{{ thread.staleReason === 'expired' ? '超7天' : '空对话' }}</span>
          </div>
        </div>
      </div>
    </aside>
  `,
};
