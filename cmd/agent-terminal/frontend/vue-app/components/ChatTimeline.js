// @ts-nocheck
import { watch, onMounted, onBeforeUnmount } from '../../lib/vue.esm-browser.prod.js';
import { translateText, translateThinkingBody } from '../utils/translate-dict.js';
import { useMermaidRenderer } from '../composables/useMermaidRenderer.js';
import { logDebug } from '../services/log.js';
import { JsonRenderer } from './JsonRenderer.js';
import { injectSentenceBreaks } from '../utils/assistant-markdown.js';
import { AttachmentPreview } from './timeline/AttachmentPreview.ts';
import { ToolTickerBar } from './timeline/ToolTickerBar.ts';
import { useAttachmentPreviewState } from './timeline/useAttachmentPreviewState.ts';
import { useTimelineItems } from './timeline/useTimelineItems.js';
import { useCommandHelpers } from './timeline/useCommandHelpers.js';
import { useApprovalActions } from './timeline/useApprovalActions.js';
import { useTimelineHelpers } from './timeline/useTimelineHelpers.js';
import { useAssistantBodyActions } from './timeline/useAssistantBodyActions.js';
import { usePresencePopover } from './timeline/usePresencePopover.js';

export const ChatTimeline = {
  name: 'ChatTimeline',
  components: { JsonRenderer, AttachmentPreview, ToolTickerBar },
  props: {
    items: { type: Array, default: () => [] },
    activeStatus: { type: String, default: 'idle' },
    activeStatusText: { type: String, default: '' },
    activeStatusMeta: { type: String, default: '' },
    emptyText: { type: String, default: '暂无消息，先发送一句话试试。' },
    pinnedPlanVisible: { type: Boolean, default: false },
    pinnedPlanItemId: { type: [String, Number], default: null },
    resolveThreadDisplayName: { type: Function, default: null },
    presenceTarget: { type: [String, Object], default: null },
  },
  emits: ['file-ref-click', 'citation-click'],
  setup(props, { emit }) {
    let updateSeq = 0;
    const {
      attachmentType,
      attachmentPreview,
      attachmentLabel,
      imageAttachments,
      fileAttachments,
      onAttachmentHoverMove,
      onAttachmentHoverLeave,
      onAttachmentPreviewEnter,
      onAttachmentPreviewLeave,
      onAttachmentPreviewZoomIn,
      onAttachmentPreviewZoomOut,
      onAttachmentPreviewResetZoom,
      attachmentCanZoomOut,
      attachmentHoverStyle,
      attachmentHoverPreview,
      attachmentLightbox,
      openAttachmentLightbox,
      closeAttachmentLightbox,
      onAttachmentLightboxKeydown,
    } = useAttachmentPreviewState();
    const attachmentPreviewApi = {
      attachmentType,
      attachmentPreview,
      attachmentLabel,
      imageAttachments,
      fileAttachments,
      onAttachmentHoverMove,
      onAttachmentHoverLeave,
      openAttachmentLightbox,
    };
    const {
      timelineItems,
      visibleItems,
      visibleOffset,
      hasMore,
      showMore,
      getItemKey,
      trailingProcessItems,
      latestPresenceItems,
    } = useTimelineItems(props);
    const {
      commandStatusText,
      commandStatusIcon,
      commandStatusIconClass,
      commandTitle,
      commandHasOutput,
      commandExitText,
    } = useCommandHelpers();
    const {
      approvalResolvedByRequestId,
      approvalRequestId,
      approvalActionDisabled,
      approvalHint,
      respondApproval,
    } = useApprovalActions();
    const {
      formatTime,
      copyTextToClipboard,
      copyFilePath,
      copyPlanText,
      displayFilePath,
      stateLabel,
      toolSummaryKindLabel,
      toolSummaryText,
      toolTickerText,
      roleLabel,
      bubbleRole,
      isDialog,
      hasAvatar,
      avatarText,
      internalRouteLabel,
      planCardSpec,
      itemHasSpec,
      splitBySpec,
    } = useTimelineHelpers(props, {
      approvalRequestId,
      approvalResolvedByRequestId,
      commandTitle,
    });
    const {
      renderAssistantBody,
      streamingAssistantState,
      streamingFrameVersion,
      isCitationTarget,
      onAssistantBodyClick,
    } = useAssistantBodyActions(props, emit, {
      copyTextToClipboard,
    });
    const {
      showAgentPresence,
      presenceLabel,
      sharedStatusText,
      sharedStatusMeta,
      thinkingPopoverText,
      thinkingToolSummaries,
      collapsedToolCount,
      collapsedToolTickerText,
      showToolTicker,
      resolvedPresenceTarget,
      hasPresenceTarget,
      showPresencePopover,
      openPresencePopover,
      closePresencePopover,
      schedulePresencePopoverClose,
      presencePopoverTitle,
      showThinkingPopover,
    } = usePresencePopover(props, {
      trailingProcessItems,
      latestPresenceItems,
      formatTime,
      toolSummaryKindLabel,
      toolSummaryText,
      toolTickerText,
      translateThinkingBody,
    });

    watch(
      () => props.items.length,
      (next, prev) => {
        updateSeq += 1;
        const delta = Math.abs((Number(next) || 0) - (Number(prev) || 0));
        if (updateSeq % 20 !== 0 && delta <= 1) return;
        const last = props.items[props.items.length - 1] || null;
        logDebug('ui', 'timeline.updated', {
          seq: updateSeq,
          length: next || 0,
          last_kind: last?.kind || '',
        });
      },
      { immediate: true },
    );

    // [DIAG] 检测 visibleItems 突降
    let prevVisibleLen = 0;
    let prevItemsRef = null;
    watch(
      () => visibleItems.value,
      (next) => {
        const nextLen = Array.isArray(next) ? next.length : 0;
        const itemsRef = props.items;
        const refChanged = itemsRef !== prevItemsRef;
        if (prevVisibleLen > 2 && nextLen <= 1) {
          logDebug('ui', 'timeline.visible_drop', {
            from: prevVisibleLen, to: nextLen,
            items_ref_changed: refChanged,
            items_len: Array.isArray(itemsRef) ? itemsRef.length : 0,
            first_key: next?.[0] ? getItemKey(next[0], 0) : '',
            stack: new Error('[diag]').stack,
          });
        }
        prevVisibleLen = nextLen;
        prevItemsRef = itemsRef;
      },
      { immediate: true },
    );

    // scroll 位置保持由 useAutoScroll 的 MutationObserver 处理（更快、更可靠）
    useMermaidRenderer();

    onMounted(() => {
      if (typeof window !== 'undefined') {
        window.addEventListener('keydown', onAttachmentLightboxKeydown);
      }
    });
    onBeforeUnmount(() => {
      if (typeof window !== 'undefined') {
        window.removeEventListener('keydown', onAttachmentLightboxKeydown);
      }
    });

    const jsonRenderMarkdownActionHandlers = {
      onFileRefClick: (payload) => emit('file-ref-click', payload),
      onCitationClick: (payload) => emit('citation-click', payload),
    };

    return {
      visibleItems,
      visibleOffset,
      timelineItems,
      hasMore,
      showMore,
      roleLabel,
      stateLabel,
      commandStatusText,
      commandStatusIcon,
      commandStatusIconClass,
      commandTitle,
      commandHasOutput,
      commandExitText,
      displayFilePath,
      attachmentPreviewApi,
      formatTime,
      bubbleRole,
      isDialog,
      hasAvatar,
      avatarText,
      internalRouteLabel,
      renderAssistantBody,
      onAssistantBodyClick,
      isCitationTarget,
      onAttachmentHoverLeave,
      onAttachmentPreviewEnter,
      onAttachmentPreviewLeave,
      onAttachmentPreviewZoomIn,
      onAttachmentPreviewZoomOut,
      onAttachmentPreviewResetZoom,
      attachmentCanZoomOut,
      attachmentHoverStyle,
      attachmentHoverPreview,
      attachmentLightbox,
      closeAttachmentLightbox,
      copyFilePath,
      copyPlanText,
      approvalActionDisabled,
      approvalHint,
      respondApproval,
      planCardSpec,
      itemHasSpec,
      streamingAssistantState,
      streamingFrameVersion,
      splitBySpec,
      jsonRenderMarkdownActionHandlers,
      getItemKey,

      showAgentPresence,
      presenceLabel,
      sharedStatusText,
      sharedStatusMeta,
      thinkingPopoverText,
      thinkingToolSummaries,
      collapsedToolCount,
      collapsedToolTickerText,
      showToolTicker,
      resolvedPresenceTarget,
      hasPresenceTarget,
      showPresencePopover,
      openPresencePopover,
      closePresencePopover,
      schedulePresencePopoverClose,
      presencePopoverTitle,
      showThinkingPopover,
      translateText,
      injectSentenceBreaks,
    };
  },
  template: `
    <div
      class="chat-messages-vue hide-scrollbar"
      :class="{ 'has-plan-pin': pinnedPlanVisible }"
      @mouseleave="onAttachmentHoverLeave"
      @scroll.passive="onAttachmentHoverLeave"
    >
      <div v-if="timelineItems.length === 0" class="chat-empty">{{ emptyText }}</div>

      <div v-if="hasMore" class="chat-load-more">
        <button class="chat-load-more-btn" @click="showMore">显示更早消息 ({{ timelineItems.length - visibleItems.length }} 条)</button>
      </div>

      <article
        v-for="(item, index) in visibleItems"
        :key="getItemKey(item, index + visibleOffset)"
        :data-chat-item-id="getItemKey(item, index + visibleOffset)"
        class="chat-item"
        :class="['kind-' + item.kind, item.kind === 'user' && item.internal ? 'kind-internal' : '', isDialog(item) ? 'dialog' : 'process', bubbleRole(item), isCitationTarget(item) ? 'is-citation-target' : '']"
      >
        <template v-if="isDialog(item)">
          <div class="chat-item-avatar" :class="{ 'is-invisible': !hasAvatar(item, index, visibleItems) }">
            <svg
              v-if="item.kind === 'assistant'"
              class="chat-item-avatar-bot-icon"
              viewBox="0 0 20 20"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M10 3V5"></path>
              <path d="M6.2 5H13.8C15 5 16 6 16 7.2V12.8C16 14 15 15 13.8 15H6.2C5 15 4 14 4 12.8V7.2C4 6 5 5 6.2 5Z"></path>
              <path d="M2.8 8V12"></path>
              <path d="M17.2 8V12"></path>
              <circle cx="8" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
              <circle cx="12" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
            </svg>
            <template v-else>{{ avatarText(item) }}</template>
          </div>

          <section class="chat-item-bubble">
            <header v-if="item.kind !== 'assistant'" class="chat-item-head">
              <span class="chat-item-role">{{ roleLabel(item) }}</span>
              <span v-if="internalRouteLabel(item)" class="chat-item-route">{{ internalRouteLabel(item) }}</span>
              <span v-if="stateLabel(item)" class="chat-item-status">{{ stateLabel(item) }}</span>
              <span class="chat-item-spacer"></span>
              <time class="chat-item-time">{{ formatTime(item.ts) }}</time>
            </header>
            <template v-if="item.kind === 'assistant'">
              <div
                v-if="!itemHasSpec(item.text)"
                key="assistant-body"
                class="chat-item-body chat-item-markdown agent-markdown-root"
                :data-stream-version="item.done === false && !item.streamingFinalized ? streamingFrameVersion : undefined"
                @click="onAssistantBodyClick"
              >
                <template v-if="item.done === false && !item.streamingFinalized">
                  <template v-for="s in [streamingAssistantState(item)]" :key="0">
                    <div v-if="s.html" class="chat-item-markdown-stream-wrap" v-html="s.html"></div>
                    <pre v-if="s.tailText" class="chat-item-plain chat-item-streaming" :style="s.heightPx ? { minHeight: s.heightPx + 'px' } : undefined">{{ s.tailText }}</pre>
                  </template>
                </template>
                <div v-else class="chat-item-markdown-static-wrap" v-html="renderAssistantBody(item.text)"></div>
              </div>
              <div v-else key="mixed" class="chat-item-body chat-item-markdown agent-markdown-root jr-mixed" @click="onAssistantBodyClick">
                <template v-for="(part, pIdx) in splitBySpec(item.text)" :key="pIdx">
                  <div v-if="part.type === 'text'" v-html="renderAssistantBody(part.content)"></div>
                  <JsonRenderer v-else-if="part.spec" :spec="part.spec" :markdown-action-handlers="jsonRenderMarkdownActionHandlers" />
                </template>
              </div>
            </template>
            <div
              v-else
              class="chat-item-body chat-item-markdown agent-markdown-root"
              v-html="renderAssistantBody(item.text)"
              @click="onAssistantBodyClick"
            ></div>
            <AttachmentPreview
              :attachments="item.attachments"
              :attachment-api="attachmentPreviewApi"
            />
          </section>
        </template>

        <section v-else class="chat-process-line">
          <header v-if="item.kind !== 'thinking' && item.kind !== 'command' && item.kind !== 'plan'" class="chat-process-head" :class="{ 'chat-process-head-file': item.kind === 'file' }">
            <template v-if="item.kind === 'file'">
              <span class="chat-process-kind-icon" title="文件" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none">
                  <path d="M6.75 3.75h7.5l3 3v13.5H6.75z" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"></path>
                  <path d="M14.25 3.75v3h3" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              </span>
            </template>
            <span v-else class="chat-process-role">{{ roleLabel(item) }}</span>
            <template v-if="item.kind === 'file' && stateLabel(item)">
              <span
                class="chat-process-state-icon"
                :class="item.status === 'saved' ? 'is-saved' : 'is-editing'"
                :title="stateLabel(item)"
                aria-hidden="true"
              >
                <svg v-if="item.status === 'saved'" viewBox="0 0 24 24" fill="none">
                  <path d="M5 12.5l4.2 4.2L19 6.9" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
                <svg v-else viewBox="0 0 24 24" fill="none">
                  <path d="M4.5 15.75V19.5h3.75L18.8 8.95l-3.75-3.75L4.5 15.75z" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"></path>
                  <path d="M13.95 6.3l3.75 3.75" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"></path>
                </svg>
              </span>
            </template>
            <span v-else-if="stateLabel(item)" class="chat-process-status">{{ stateLabel(item) }}</span>
            <template v-if="item.kind === 'file'">
              <span class="chat-process-file-inline" :title="item.file || '(unknown file)'">
                {{ displayFilePath(item.file) || '(unknown file)' }}
              </span>
            </template>
            <span v-else class="chat-item-spacer"></span>
            <button
              v-if="item.kind === 'file'"
              class="chat-process-copy-btn"
              type="button"
              :title="item.file ? ('复制路径: ' + displayFilePath(item.file)) : '无可复制路径'"
              aria-label="复制文件路径"
              :disabled="!item.file"
              @click.stop="copyFilePath(item.file)"
            >
              <svg class="chat-process-copy-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <rect x="9" y="9" width="10" height="10" rx="2" stroke="currentColor" stroke-width="1.8"></rect>
                <rect x="5" y="5" width="10" height="10" rx="2" stroke="currentColor" stroke-width="1.8"></rect>
              </svg>
            </button>
            <time class="chat-process-time">{{ formatTime(item.ts) }}</time>
          </header>

          <template v-if="item.kind === 'thinking' || item.kind === 'error'">
            <pre class="chat-process-text" :class="{ 'loading-shimmer': item.kind === 'thinking' && !item.done }">{{ injectSentenceBreaks(item.text) }}</pre>
          </template>

          <template v-else-if="item.kind === 'plan'">
            <div class="ran-plan-card-json" :class="{ 'is-done': item.done }" @click="onAssistantBodyClick">
              <JsonRenderer :spec="planCardSpec(item)" :markdown-action-handlers="jsonRenderMarkdownActionHandlers" />

              <button
                class="ran-plan-card-json__copy"
                type="button"
                :title="'复制计划文本'"
                aria-label="复制计划文本"
                :disabled="!((item.text || '').toString().trim())"
                @click.stop="copyPlanText(item.text)"
              >
                <svg class="ran-plan-card-json__copy-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                  <rect x="9" y="9" width="10" height="10" rx="2" stroke="currentColor" stroke-width="1.8"></rect>
                  <rect x="5" y="5" width="10" height="10" rx="2" stroke="currentColor" stroke-width="1.8"></rect>
                </svg>
              </button>
            </div>
          </template>

          <template v-else-if="item.kind === 'command'">
            <div class="ran-command-card">
              <div class="ran-command-card__header">
                <span class="ran-command-card__status">{{ commandStatusText(item) }}</span>
              </div>
              <div class="ran-command-card__main-row">
                <span class="ran-command-card__icon" :class="commandStatusIconClass(item)" aria-hidden="true">{{ commandStatusIcon(item) }}</span>
                <span class="ran-command-card__title" :title="commandTitle(item)">{{ commandTitle(item) }}</span>
              </div>
              <div
                class="ran-command-card__details"
                :class="commandHasOutput(item) ? 'ran-command-card__details--open' : 'ran-command-card__details--closed'"
              >
                <pre v-if="commandHasOutput(item)" class="ran-command-card__output">{{ item.output }}</pre>
              </div>
              <div class="ran-command-card__footer">
                <span class="ran-command-card__auto-exec">终端命令</span>
                <div class="ran-command-card__footer-right">
                  <span v-if="item.status === 'running'" class="ran-command-card__cancel-btn">运行中...</span>
                  <span v-if="commandExitText(item)" class="ran-command-card__exit-code">{{ commandExitText(item) }}</span>
                </div>
              </div>
            </div>
          </template>

          <template v-else-if="item.kind === 'tool'">
            <div class="chat-process-row">
              <pre class="chat-process-text chat-process-code tool-call-name">{{ item.tool }}</pre>
              <div v-if="typeof item.elapsedMs !== 'undefined'" class="chat-process-foot tool-call-time">{{ item.elapsedMs }}ms</div>
            </div>
            <div v-if="item.file" class="chat-process-text chat-process-meta chat-file-path" :title="item.file">{{ displayFilePath(item.file) }}</div>
            <pre v-if="item.preview" class="chat-process-text chat-process-meta tool-preview">{{ item.preview }}</pre>
          </template>

          <template v-else-if="item.kind === 'approval'">
            <div class="chat-process-text chat-process-meta">{{ item.command || item.tool || '需要用户确认' }}</div>
            <div class="approval-actions">
              <button
                class="approval-action-btn approval-action-btn--approve"
                type="button"
                :disabled="approvalActionDisabled(item)"
                @click.stop="respondApproval(item, true)"
              >同意</button>
              <button
                class="approval-action-btn approval-action-btn--reject"
                type="button"
                :disabled="approvalActionDisabled(item)"
                @click.stop="respondApproval(item, false)"
              >拒绝</button>
            </div>
            <div class="chat-process-foot approval-hint">{{ approvalHint(item) }}</div>
          </template>
        </section>
      </article>
      <div
        v-if="attachmentHoverPreview"
        class="chat-attachment-hover-preview"
        :style="attachmentHoverStyle()"
        @mouseenter="onAttachmentPreviewEnter"
        @mouseleave="onAttachmentPreviewLeave"
        aria-hidden="true"
      >
        <div class="chat-attachment-preview-zoom-controls">
          <button
            class="chat-attachment-preview-zoom-btn is-minus"
            type="button"
            title="缩小"
            aria-label="缩小"
            :disabled="!attachmentCanZoomOut()"
            @click="onAttachmentPreviewZoomOut"
          >-</button>
          <button
            class="chat-attachment-preview-zoom-btn is-reset"
            type="button"
            title="重置为 1:1"
            aria-label="重置为 1:1"
            :disabled="!attachmentCanZoomOut()"
            @click="onAttachmentPreviewResetZoom"
          >1:1</button>
          <button
            class="chat-attachment-preview-zoom-btn is-plus"
            type="button"
            title="继续放大"
            aria-label="继续放大"
            @click="onAttachmentPreviewZoomIn"
          >+</button>
        </div>
        <img
          :src="attachmentHoverPreview.src"
          :alt="attachmentHoverPreview.alt"
        />
      </div>
      <div v-if="attachmentLightbox" class="chat-attachment-lightbox" @click.self="closeAttachmentLightbox">
        <div class="chat-attachment-lightbox__inner">
          <button class="chat-attachment-lightbox__close" type="button" @click="closeAttachmentLightbox" aria-label="关闭图片预览">×</button>
          <img class="chat-attachment-lightbox__image" :src="attachmentLightbox.src" :alt="attachmentLightbox.alt" />
          <div class="chat-attachment-lightbox__caption" :title="attachmentLightbox.path || attachmentLightbox.alt">
            {{ attachmentLightbox.path || attachmentLightbox.alt }}
          </div>
        </div>
      </div>
      <teleport :to="resolvedPresenceTarget" :disabled="!hasPresenceTarget">
        <div
          v-if="showAgentPresence"
          class="chat-presence-row"
          :class="{ 'chat-presence-row--anchored': hasPresenceTarget }"
        >
          <div class="chat-item-avatar chat-item-avatar-presence">
            <svg
              class="chat-item-avatar-bot-icon"
              viewBox="0 0 20 20"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M10 3V5"></path>
              <path d="M6.2 5H13.8C15 5 16 6 16 7.2V12.8C16 14 15 15 13.8 15H6.2C5 15 4 14 4 12.8V7.2C4 6 5 5 6.2 5Z"></path>
              <path d="M2.8 8V12"></path>
              <path d="M17.2 8V12"></path>
              <circle cx="8" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
              <circle cx="12" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
            </svg>
          </div>
          <div
            class="chat-status chat-status-presence"
            :class="{ 'chat-status-presence--popoverable': showThinkingPopover, 'chat-status-presence--popover-open': showPresencePopover }"
            :title="presencePopoverTitle"
            :tabindex="showThinkingPopover ? 0 : undefined"
            @mouseenter="openPresencePopover"
            @mouseleave="schedulePresencePopoverClose"
            @focusin="openPresencePopover"
            @focusout="schedulePresencePopoverClose"
          >
            <svg
              v-if="activeStatus === 'thinking' || activeStatus === 'starting' || activeStatus === 'running' || activeStatus === 'responding'"
              class="chat-status-spinner"
              viewBox="0 0 24 24"
              fill="none"
              aria-hidden="true"
            >
              <circle class="chat-status-spinner-track" cx="12" cy="12" r="8.5"></circle>
              <circle class="chat-status-spinner-arc" cx="12" cy="12" r="8.5"></circle>
            </svg>
            <span v-else class="status-dot" :class="activeStatus"></span>
            <span class="chat-status-label" :class="{ 'loading-shimmer': activeStatus === 'thinking' || activeStatus === 'responding' }">{{ translateText(presenceLabel) }}</span>
            <span v-if="sharedStatusMeta" class="chat-status-meta" :class="{ 'hyperspeed-model-shimmer': activeStatus === 'thinking' }">{{ sharedStatusMeta }}</span>
            <ToolTickerBar
              v-if="showToolTicker"
              :text="collapsedToolTickerText"
              :visible="showToolTicker"
            />
            <div
              v-if="showThinkingPopover"
              class="chat-thinking-hover-popover"
              role="note"
              @mouseenter="openPresencePopover"
              @mouseleave="schedulePresencePopoverClose"
            >
              <div class="chat-thinking-hover-popover__title">{{ translateText(presenceLabel) || '思考过程' }}</div>
              <div v-if="thinkingPopoverText" class="chat-thinking-hover-popover__section">
                <div class="chat-thinking-hover-popover__label">思考摘要</div>
                <div class="chat-thinking-hover-popover__body">{{ thinkingPopoverText }}</div>
              </div>
              <div v-if="thinkingToolSummaries.length > 0" class="chat-thinking-hover-popover__section">
                <div class="chat-thinking-hover-popover__label">工具调用摘要</div>
                <div class="chat-thinking-hover-popover__list">
                  <div v-for="entry in thinkingToolSummaries" :key="entry.id" class="chat-thinking-hover-popover__item">
                    <span v-if="entry.time" class="chat-thinking-hover-popover__item-time">{{ entry.time }}</span>
                    <span class="chat-thinking-hover-popover__item-kind">{{ entry.kindLabel }}</span>
                    <span class="chat-thinking-hover-popover__item-text">{{ entry.text }}</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </teleport>
    </div>
  `,
};
