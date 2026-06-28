// @ts-nocheck

import { ChatTimeline } from '../ChatTimeline.js';
import { JsonRenderer } from '../JsonRenderer.js';
import { resolveRenderedMarkdownAction } from '../../utils/assistant-markdown-click.js';

export const WorkspaceChatPanel = {
  name: 'WorkspaceChatPanel',
  components: {
    ChatTimeline,
    JsonRenderer,
  },
  props: {
    splitRatio: { type: Number, default: 60 },
    activePinnedPlan: { type: Object, default: null },
    noActiveThread: { type: Boolean, default: false },
    activeTimeline: { type: Array, default: () => [] },
    activeStatus: { type: String, default: '' },
    displayStatusText: { type: String, default: '' },
    activeStatusMeta: { type: String, default: '' },
    emptyText: { type: String, default: '暂无消息，先发送一句话试试。' },
    resolveThreadDisplayName: { type: Function, default: (value) => value },
    presenceTarget: { type: Object, default: null },
    pinnedPlanCardSpec: { type: Function, default: () => ({}) },
    selectedThreadId: { type: String, default: '' },
    isAtBottom: { type: Boolean, default: true },
  },
  emits: ['dismiss-pinned-plan', 'file-ref-click', 'citation-click', 'scroll-to-bottom', 'scroll-to-top'],
  setup(_props, { emit }) {
    function onDismissPinnedPlanClick(event) {
      if (typeof event?.stopPropagation === 'function') event.stopPropagation();
      emit('dismiss-pinned-plan');
    }

    function onPinnedPlanBodyClick(event) {
      const action = resolveRenderedMarkdownAction(event);
      if (!action) return;
      if (typeof event?.preventDefault === 'function') event.preventDefault();
      if (typeof event?.stopPropagation === 'function') event.stopPropagation();
      if (action.type === 'file-ref') {
        emit('file-ref-click', action.payload);
        return;
      }
      if (action.type === 'citation') {
        emit('citation-click', action.payload);
      }
    }

    const jsonRenderMarkdownActionHandlers = {
      onFileRefClick: (payload) => emit('file-ref-click', payload),
      onCitationClick: (payload) => emit('citation-click', payload),
    };

    return { emit, onPinnedPlanBodyClick, onDismissPinnedPlanClick, jsonRenderMarkdownActionHandlers };

  },
  template: `
    <div id="chat-panel" class="chat-panel-only" :style="{ flex: '0 0 ' + splitRatio + '%' }">

      <div v-if="noActiveThread" class="chat-messages-vue">
        <div class="diff-empty" data-testid="chat-empty-state">选择或启动一个 Agent 开始对话</div>
      </div>
      <ChatTimeline
        v-else
        :key="'timeline'"
        :items="activeTimeline"
        :active-status="activeStatus"
        :active-status-text="displayStatusText"
        :active-status-meta="activeStatusMeta"
        :empty-text="emptyText"
        :pinned-plan-visible="Boolean(activePinnedPlan)"
        :pinned-plan-item-id="activePinnedPlan ? activePinnedPlan.id : null"
        :resolve-thread-display-name="resolveThreadDisplayName"
        :presence-target="presenceTarget"
        @file-ref-click="emit('file-ref-click', $event)"
        @citation-click="emit('citation-click', $event)"
      />
      <button
        v-if="!noActiveThread"
        class="chat-scroll-toggle-btn"
        :class="{ 'is-at-bottom': isAtBottom }"
        :title="isAtBottom ? '滚动到顶部' : '滚动到底部'"
        :aria-label="isAtBottom ? '滚动到顶部' : '滚动到底部'"
        @click="isAtBottom ? $emit('scroll-to-top') : $emit('scroll-to-bottom')"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M18 15l-6-6-6 6"></path>
        </svg>
      </button>
    </div>

  `,
};
