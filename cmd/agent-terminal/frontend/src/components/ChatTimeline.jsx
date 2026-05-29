import React, { useEffect, useRef, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { translateText, translateThinkingBody } from '../utils/translate-dict.js';
import { useMermaidRenderer } from '../composables/useMermaidRenderer.js';
import { logDebug } from '../services/log.js';
import { JsonRenderer } from './JsonRenderer.jsx';
import { injectSentenceBreaks, renderAssistantMarkdown } from '../utils/assistant-markdown.js';
import { AttachmentPreview } from './timeline/AttachmentPreview.jsx';
import { ToolTickerBar } from './timeline/ToolTickerBar.jsx';
import { useAttachmentPreviewState } from './timeline/useAttachmentPreviewState.js';
import { useTimelineItems } from './timeline/useTimelineItems.js';
import { useCommandHelpers } from './timeline/useCommandHelpers.js';
import { useApprovalActions } from './timeline/useApprovalActions.js';
import { useTimelineHelpers } from './timeline/useTimelineHelpers.js';
import { useAssistantBodyActions, handleCopyButton, handleExpandButton, handleFileRefClick, handleCitationClick } from './timeline/useAssistantBodyActions.js';
import { usePresencePopover } from './timeline/usePresencePopover.js';
import { onMounted, onBeforeUnmount } from '../../lib/vue.esm-browser.prod.js';
import { createStreamingMarkdownStateResolver } from '../utils/assistant-markdown-streaming.js';

import { callAPI } from '../services/api.js';
import { logInfo, logWarn } from '../services/log.js';
import { useVueSetup, val } from '../utils/vue-compat.js';


export function ChatTimeline(props) {
  const {
    items = [],
    activeStatus = 'idle',
    activeStatusText = '',
    activeStatusMeta = '',
    emptyText = '暂无消息，先发送一句话试试。',
    pinnedPlanVisible = false,
    pinnedPlanItemId = null,
    resolveThreadDisplayName = null,
    presenceTarget = null,
    onFileRefClick,
    onCitationClick,
  } = props;

  const emit = useMemo(() => (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  }, [props]);

  const vm = useVueSetup(ChatTimeline.setup, props, emit);

  const attachmentState = useAttachmentPreviewState();

  const attachmentPreviewApi = useMemo(() => ({
    attachmentType: attachmentState.attachmentType,
    attachmentPreview: attachmentState.attachmentPreview,
    attachmentLabel: attachmentState.attachmentLabel,
    imageAttachments: attachmentState.imageAttachments,
    fileAttachments: attachmentState.fileAttachments,
    onAttachmentHoverMove: attachmentState.onAttachmentHoverMove,
    onAttachmentHoverLeave: attachmentState.onAttachmentHoverLeave,
    openAttachmentLightbox: attachmentState.openAttachmentLightbox,
  }), [attachmentState]);

  const attachmentHoverPreview = attachmentState.attachmentHoverPreview;
  const attachmentHoverStyle = attachmentState.attachmentHoverStyle;
  const onAttachmentPreviewEnter = attachmentState.onAttachmentPreviewEnter;
  const onAttachmentPreviewLeave = attachmentState.onAttachmentPreviewLeave;
  const attachmentCanZoomOut = attachmentState.attachmentCanZoomOut;
  const onAttachmentPreviewZoomOut = attachmentState.onAttachmentPreviewZoomOut;
  const onAttachmentPreviewResetZoom = attachmentState.onAttachmentPreviewResetZoom;
  const onAttachmentPreviewZoomIn = attachmentState.onAttachmentPreviewZoomIn;
  const attachmentLightbox = attachmentState.attachmentLightbox;
  const closeAttachmentLightbox = attachmentState.closeAttachmentLightbox;
  const onAttachmentHoverLeave = attachmentState.onAttachmentHoverLeave;

  const timelineItems = val(vm.timelineItems) || [];
  const visibleItems = val(vm.visibleItems) || [];
  const visibleOffset = val(vm.visibleOffset) || 0;
  const hasMore = val(vm.hasMore);
  const showMore = vm.showMore;
  const getItemKey = vm.getItemKey;

  const commandStatusText = vm.commandStatusText;
  const commandStatusIcon = vm.commandStatusIcon;
  const commandStatusIconClass = vm.commandStatusIconClass;
  const commandTitle = vm.commandTitle;
  const commandHasOutput = vm.commandHasOutput;
  const commandExitText = vm.commandExitText;

  const approvalActionDisabled = vm.approvalActionDisabled;
  const approvalHint = vm.approvalHint;
  const respondApproval = vm.respondApproval;

  const formatTime = vm.formatTime;
  const copyFilePath = vm.copyFilePath;
  const copyPlanText = vm.copyPlanText;
  const displayFilePath = vm.displayFilePath;
  const stateLabel = vm.stateLabel;
  const roleLabel = vm.roleLabel;
  const bubbleRole = vm.bubbleRole;
  const isDialog = vm.isDialog;
  const hasAvatar = vm.hasAvatar;
  const avatarText = vm.avatarText;
  const internalRouteLabel = vm.internalRouteLabel;
  const planCardSpec = vm.planCardSpec;
  const itemHasSpec = vm.itemHasSpec;
  const splitBySpec = vm.splitBySpec;

  const renderAssistantBody = vm.renderAssistantBody;
  const streamingAssistantState = vm.streamingAssistantState;
  const streamingFrameVersion = val(vm.streamingFrameVersion);
  const isCitationTarget = vm.isCitationTarget;
  const onAssistantBodyClick = vm.onAssistantBodyClick;

  const showAgentPresence = val(vm.showAgentPresence);
  const presenceLabel = val(vm.presenceLabel);
  const sharedStatusMeta = val(vm.sharedStatusMeta);
  const thinkingPopoverText = val(vm.thinkingPopoverText);
  const thinkingToolSummaries = val(vm.thinkingToolSummaries) || [];
  const collapsedToolTickerText = val(vm.collapsedToolTickerText);
  const showToolTicker = val(vm.showToolTicker);
  const resolvedPresenceTarget = val(vm.resolvedPresenceTarget);
  const hasPresenceTarget = val(vm.hasPresenceTarget);
  const showPresencePopover = val(vm.showPresencePopover);
  const openPresencePopover = vm.openPresencePopover;
  const schedulePresencePopoverClose = vm.schedulePresencePopoverClose;
  const presencePopoverTitle = val(vm.presencePopoverTitle);
  const showThinkingPopover = val(vm.showThinkingPopover);



  const updateSeqRef = useRef(0);
  const itemsLength = items.length;

  useEffect(() => {
    updateSeqRef.current += 1;
    const last = items[items.length - 1] || null;
    logDebug('ui', 'timeline.updated', {
      seq: updateSeqRef.current,
      length: itemsLength,
      last_kind: last?.kind || '',
    });
  }, [itemsLength]);

  const prevVisibleLenRef = useRef(0);
  const prevItemsRef = useRef(null);
  const visibleItemsLen = visibleItems.length;

  useEffect(() => {
    const refChanged = items !== prevItemsRef.current;
    if (prevVisibleLenRef.current > 2 && visibleItemsLen <= 1) {
      logDebug('ui', 'timeline.visible_drop', {
        from: prevVisibleLenRef.current,
        to: visibleItemsLen,
        items_ref_changed: refChanged,
        items_len: items.length,
        first_key: visibleItems[0] ? getItemKey(visibleItems[0], 0) : '',
        stack: new Error('[diag]').stack,
      });
    }
    prevVisibleLenRef.current = visibleItemsLen;
    prevItemsRef.current = items;
  }, [visibleItemsLen, items, visibleItems, getItemKey]);

  useMermaidRenderer();

  useEffect(() => {
    if (typeof window !== 'undefined') {
      window.addEventListener('keydown', attachmentState.onAttachmentLightboxKeydown);
    }
    return () => {
      if (typeof window !== 'undefined') {
        window.removeEventListener('keydown', attachmentState.onAttachmentLightboxKeydown);
      }
    };
  }, [attachmentState.onAttachmentLightboxKeydown]);

  const messagesContainerRef = useRef(null);

  useEffect(() => {
    const el = messagesContainerRef.current;
    if (!el) return;
    el.addEventListener('scroll', onAttachmentHoverLeave, { passive: true });
    return () => {
      el.removeEventListener('scroll', onAttachmentHoverLeave);
    };
  }, [onAttachmentHoverLeave]);

  const jsonRenderMarkdownActionHandlers = useMemo(() => ({
    onFileRefClick: (payload) => onFileRefClick?.(payload),
    onCitationClick: (payload) => onCitationClick?.(payload),
  }), [onFileRefClick, onCitationClick]);

  const portalTarget = useMemo(() => {
    if (!hasPresenceTarget || !resolvedPresenceTarget) return null;
    if (typeof resolvedPresenceTarget === 'string') {
      return document.querySelector(resolvedPresenceTarget);
    }
    if (resolvedPresenceTarget && resolvedPresenceTarget.nodeType) {
      return resolvedPresenceTarget;
    }
    return null;
  }, [hasPresenceTarget, resolvedPresenceTarget]);

  const presenceContent = showAgentPresence && (
    <div
      className={`chat-presence-row ${hasPresenceTarget ? 'chat-presence-row--anchored' : ''}`}
    >
      <div className="chat-item-avatar chat-item-avatar-presence">
        <svg
          className="chat-item-avatar-bot-icon"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
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
        className={`chat-status chat-status-presence ${showThinkingPopover ? 'chat-status-presence--popoverable' : ''} ${showPresencePopover ? 'chat-status-presence--popover-open' : ''}`}
        title={presencePopoverTitle}
        tabIndex={showThinkingPopover ? 0 : undefined}
        onMouseEnter={openPresencePopover}
        onMouseLeave={schedulePresencePopoverClose}
        onFocus={openPresencePopover}
        onBlur={schedulePresencePopoverClose}
      >
        {['thinking', 'starting', 'running', 'responding'].includes(activeStatus) ? (
          <svg
            className="chat-status-spinner"
            viewBox="0 0 24 24"
            fill="none"
            aria-hidden="true"
          >
            <circle className="chat-status-spinner-track" cx="12" cy="12" r="8.5"></circle>
            <circle className="chat-status-spinner-arc" cx="12" cy="12" r="8.5"></circle>
          </svg>
        ) : (
          <span className={`status-dot ${activeStatus}`}></span>
        )}
        <span className={`chat-status-label ${activeStatus === 'thinking' || activeStatus === 'responding' ? 'loading-shimmer' : ''}`}>{translateText(presenceLabel)}</span>
        {sharedStatusMeta && (
          <span className={`chat-status-meta ${activeStatus === 'thinking' ? 'hyperspeed-model-shimmer' : ''}`}>{sharedStatusMeta}</span>
        )}
        {showToolTicker && (
          <ToolTickerBar
            text={collapsedToolTickerText}
            visible={showToolTicker}
          />
        )}
        {showThinkingPopover && (
          <div
            className="chat-thinking-hover-popover"
            role="note"
            onMouseEnter={openPresencePopover}
            onMouseLeave={schedulePresencePopoverClose}
          >
            <div className="chat-thinking-hover-popover__title">{translateText(presenceLabel) || '思考过程'}</div>
            {thinkingPopoverText && (
              <div className="chat-thinking-hover-popover__section">
                <div className="chat-thinking-hover-popover__label">思考摘要</div>
                <div className="chat-thinking-hover-popover__body">{thinkingPopoverText}</div>
              </div>
            )}
            {thinkingToolSummaries.length > 0 && (
              <div className="chat-thinking-hover-popover__section">
                <div className="chat-thinking-hover-popover__label">工具调用摘要</div>
                <div className="chat-thinking-hover-popover__list">
                  {thinkingToolSummaries.map((entry) => (
                    <div key={entry.id} className="chat-thinking-hover-popover__item">
                      {entry.time && <span className="chat-thinking-hover-popover__item-time">{entry.time}</span>}
                      <span className="chat-thinking-hover-popover__item-kind">{entry.kindLabel}</span>
                      <span className="chat-thinking-hover-popover__item-text">{entry.text}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );

  const presencePortal = hasPresenceTarget && portalTarget
    ? createPortal(presenceContent, portalTarget)
    : presenceContent;

  return (

    <div

      ref={messagesContainerRef}
      className={`chat-messages-vue hide-scrollbar ${pinnedPlanVisible ? 'has-plan-pin' : ''}`}
      onMouseLeave={onAttachmentHoverLeave}
    >
      {timelineItems.length === 0 && <div className="chat-empty">{emptyText}</div>}

      {hasMore && (
        <div className="chat-load-more">
          <button className="chat-load-more-btn" onClick={showMore}>
            显示更早消息 ({timelineItems.length - visibleItems.length} 条)
          </button>
        </div>
      )}

      {visibleItems.map((item, index) => {
        const itemKey = getItemKey(item, index + visibleOffset);
        const itemKindClass = `kind-${item.kind}`;
        const isInternalClass = item.kind === 'user' && item.internal ? 'kind-internal' : '';
        const dialogProcessClass = isDialog(item) ? 'dialog' : 'process';
        const bubbleRoleClass = bubbleRole(item);
        const citationTargetClass = isCitationTarget(item) ? 'is-citation-target' : '';

        return (
          <article
            key={itemKey}
            data-chat-item-id={itemKey}
            className={`chat-item ${itemKindClass} ${isInternalClass} ${dialogProcessClass} ${bubbleRoleClass} ${citationTargetClass}`}
          >
            {isDialog(item) ? (
              <>
                <div className={`chat-item-avatar ${!hasAvatar(item, index, visibleItems) ? 'is-invisible' : ''}`}>
                  {item.kind === 'assistant' ? (
                    <svg
                      className="chat-item-avatar-bot-icon"
                      viewBox="0 0 20 20"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="1.6"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      aria-hidden="true"
                    >
                      <path d="M10 3V5"></path>
                      <path d="M6.2 5H13.8C15 5 16 6 16 7.2V12.8C16 14 15 15 13.8 15H6.2C5 15 4 14 4 12.8V7.2C4 6 5 5 6.2 5Z"></path>
                      <path d="M2.8 8V12"></path>
                      <path d="M17.2 8V12"></path>
                      <circle cx="8" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
                      <circle cx="12" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
                    </svg>
                  ) : (
                    avatarText(item)
                  )}
                </div>

                <section className="chat-item-bubble">
                  {item.kind !== 'assistant' && (
                    <header className="chat-item-head">
                      <span className="chat-item-role">{roleLabel(item)}</span>
                      {internalRouteLabel(item) && <span className="chat-item-route">{internalRouteLabel(item)}</span>}
                      {stateLabel(item) && <span className="chat-item-status">{stateLabel(item)}</span>}
                      <span className="chat-item-spacer"></span>
                      <time className="chat-item-time">{formatTime(item.ts)}</time>
                    </header>
                  )}
                  {item.kind === 'assistant' ? (
                    !itemHasSpec(item.text) ? (
                      <div
                        key="assistant-body"
                        className="chat-item-body chat-item-markdown agent-markdown-root"
                        data-stream-version={item.done === false && !item.streamingFinalized ? streamingFrameVersion : undefined}
                        onClick={onAssistantBodyClick}
                      >
                        {item.done === false && !item.streamingFinalized ? (
                          (() => {
                            const s = streamingAssistantState(item);
                            return (
                              <React.Fragment key={0}>
                                {s.html && <div className="chat-item-markdown-stream-wrap" dangerouslySetInnerHTML={{ __html: s.html }} />}
                                {s.tailText && (
                                  <pre
                                    className="chat-item-plain chat-item-streaming"
                                    style={s.heightPx ? { minHeight: `${s.heightPx}px` } : undefined}
                                  >
                                    {s.tailText}
                                  </pre>
                                )}
                              </React.Fragment>
                            );
                          })()
                        ) : (
                          <div className="chat-item-markdown-static-wrap" dangerouslySetInnerHTML={{ __html: renderAssistantBody(item.text) }} />
                        )}
                      </div>
                    ) : (
                      <div key="mixed" className="chat-item-body chat-item-markdown agent-markdown-root jr-mixed" onClick={onAssistantBodyClick}>
                        {splitBySpec(item.text).map((part, pIdx) => {
                          if (part.type === 'text') {
                            return (
                              <div
                                key={pIdx}
                                dangerouslySetInnerHTML={{ __html: renderAssistantBody(part.content) }}
                              />
                            );
                          }
                          if (part.spec) {
                            return (
                              <JsonRenderer
                                key={pIdx}
                                spec={part.spec}
                                markdownActionHandlers={jsonRenderMarkdownActionHandlers}
                              />
                            );
                          }
                          return null;
                        })}
                      </div>
                    )
                  ) : (
                    <div
                      className="chat-item-body chat-item-markdown agent-markdown-root"
                      dangerouslySetInnerHTML={{ __html: renderAssistantBody(item.text) }}
                      onClick={onAssistantBodyClick}
                    />
                  )}
                  <AttachmentPreview
                    attachments={item.attachments}
                    attachmentApi={attachmentPreviewApi}
                  />
                </section>
              </>
            ) : (
              <section className="chat-process-line">
                {item.kind !== 'thinking' && item.kind !== 'command' && item.kind !== 'plan' && (
                  <header className={`chat-process-head ${item.kind === 'file' ? 'chat-process-head-file' : ''}`}>
                    {item.kind === 'file' ? (
                      <span className="chat-process-kind-icon" title="文件" aria-hidden="true">
                        <svg viewBox="0 0 24 24" fill="none">
                          <path d="M6.75 3.75h7.5l3 3v13.5H6.75z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round"></path>
                          <path d="M14.25 3.75v3h3" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"></path>
                        </svg>
                      </span>
                    ) : (
                      <span className="chat-process-role">{roleLabel(item)}</span>
                    )}

                    {item.kind === 'file' && stateLabel(item) ? (
                      <span
                        className={`chat-process-state-icon ${item.status === 'saved' ? 'is-saved' : 'is-editing'}`}
                        title={stateLabel(item)}
                        aria-hidden="true"
                      >
                        {item.status === 'saved' ? (
                          <svg viewBox="0 0 24 24" fill="none">
                            <path d="M5 12.5l4.2 4.2L19 6.9" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"></path>
                          </svg>
                        ) : (
                          <svg viewBox="0 0 24 24" fill="none">
                            <path d="M4.5 15.75V19.5h3.75L18.8 8.95l-3.75-3.75L4.5 15.75z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round"></path>
                            <path d="M13.95 6.3l3.75 3.75" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round"></path>
                          </svg>
                        )}
                      </span>
                    ) : (
                      stateLabel(item) && <span className="chat-process-status">{stateLabel(item)}</span>
                    )}

                    {item.kind === 'file' && (
                      <span className="chat-process-file-inline" title={item.file || '(unknown file)'}>
                        {displayFilePath(item.file) || '(unknown file)'}
                      </span>
                    )}

                    {item.kind !== 'file' && <span className="chat-item-spacer"></span>}

                    {item.kind === 'file' && (
                      <button
                        className="chat-process-copy-btn"
                        type="button"
                        title={item.file ? (`复制路径: ${displayFilePath(item.file)}`) : '无可复制路径'}
                        aria-label="复制文件路径"
                        disabled={!item.file}
                        onClick={(e) => { e.stopPropagation(); copyFilePath(item.file); }}
                      >
                        <svg className="chat-process-copy-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                          <rect x="9" y="9" width="10" height="10" rx="2" stroke="currentColor" strokeWidth="1.8"></rect>
                          <rect x="5" y="5" width="10" height="10" rx="2" stroke="currentColor" strokeWidth="1.8"></rect>
                        </svg>
                      </button>
                    )}
                    <time className="chat-process-time">{formatTime(item.ts)}</time>
                  </header>
                )}

                {(item.kind === 'thinking' || item.kind === 'error') && (
                  <pre className={`chat-process-text ${item.kind === 'thinking' && !item.done ? 'loading-shimmer' : ''}`}>
                    {injectSentenceBreaks(item.text)}
                  </pre>
                )}

                {item.kind === 'plan' && (
                  <div className={`ran-plan-card-json ${item.done ? 'is-done' : ''}`} onClick={onAssistantBodyClick}>
                    <JsonRenderer spec={planCardSpec(item)} markdownActionHandlers={jsonRenderMarkdownActionHandlers} />

                    <button
                      className="ran-plan-card-json__copy"
                      type="button"
                      title="复制计划文本"
                      aria-label="复制计划文本"
                      disabled={!((item.text || '').toString().trim())}
                      onClick={(e) => { e.stopPropagation(); copyPlanText(item.text); }}
                    >
                      <svg className="ran-plan-card-json__copy-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                        <rect x="9" y="9" width="10" height="10" rx="2" stroke="currentColor" strokeWidth="1.8"></rect>
                        <rect x="5" y="5" width="10" height="10" rx="2" stroke="currentColor" strokeWidth="1.8"></rect>
                      </svg>
                    </button>
                  </div>
                )}

                {item.kind === 'command' && (
                  <div className="ran-command-card">
                    <div className="ran-command-card__header">
                      <span className="ran-command-card__status">{commandStatusText(item)}</span>
                    </div>
                    <div className="ran-command-card__main-row">
                      <span className={`ran-command-card__icon ${commandStatusIconClass(item)}`} aria-hidden="true">
                        {commandStatusIcon(item)}
                      </span>
                      <span className="ran-command-card__title" title={commandTitle(item)}>
                        {commandTitle(item)}
                      </span>
                    </div>
                    <div
                      className={`ran-command-card__details ${commandHasOutput(item) ? 'ran-command-card__details--open' : 'ran-command-card__details--closed'}`}
                    >
                      {commandHasOutput(item) && <pre className="ran-command-card__output">{item.output}</pre>}
                    </div>
                    <div className="ran-command-card__footer">
                      <span className="ran-command-card__auto-exec">终端命令</span>
                      <div className="ran-command-card__footer-right">
                        {item.status === 'running' && <span className="ran-command-card__cancel-btn">运行中...</span>}
                        {commandExitText(item) && <span className="ran-command-card__exit-code">{commandExitText(item)}</span>}
                      </div>
                    </div>
                  </div>
                )}

                {item.kind === 'tool' && (
                  <>
                    <div className="chat-process-row">
                      <pre className="chat-process-text chat-process-code tool-call-name">{item.tool}</pre>
                      {typeof item.elapsedMs !== 'undefined' && (
                        <div className="chat-process-foot tool-call-time">{item.elapsedMs}ms</div>
                      )}
                    </div>
                    {item.file && (
                      <div className="chat-process-text chat-process-meta chat-file-path" title={item.file}>
                        {displayFilePath(item.file)}
                      </div>
                    )}
                    {item.preview && (
                      <pre className="chat-process-text chat-process-meta tool-preview">{item.preview}</pre>
                    )}
                  </>
                )}

                {item.kind === 'approval' && (
                  <>
                    <div className="chat-process-text chat-process-meta">{item.command || item.tool || '需要用户确认'}</div>
                    <div className="approval-actions">
                      <button
                        className="approval-action-btn approval-action-btn--approve"
                        type="button"
                        disabled={approvalActionDisabled(item)}
                        onClick={(e) => { e.stopPropagation(); respondApproval(item, true); }}
                      >
                        同意
                      </button>
                      <button
                        className="approval-action-btn approval-action-btn--reject"
                        type="button"
                        disabled={approvalActionDisabled(item)}
                        onClick={(e) => { e.stopPropagation(); respondApproval(item, false); }}
                      >
                        拒绝
                      </button>
                    </div>
                    <div className="chat-process-foot approval-hint">{approvalHint(item)}</div>
                  </>
                )}
              </section>
            )}
          </article>
        );
      })}

      {attachmentHoverPreview && (
        <div
          className="chat-attachment-hover-preview"
          style={attachmentHoverStyle()}
          onMouseEnter={onAttachmentPreviewEnter}
          onMouseLeave={onAttachmentPreviewLeave}
          aria-hidden="true"
        >
          <div className="chat-attachment-preview-zoom-controls">
            <button
              className="chat-attachment-preview-zoom-btn is-minus"
              type="button"
              title="缩小"
              aria-label="缩小"
              disabled={!attachmentCanZoomOut()}
              onClick={onAttachmentPreviewZoomOut}
            >
              -
            </button>
            <button
              className="chat-attachment-preview-zoom-btn is-reset"
              type="button"
              title="重置为 1:1"
              aria-label="重置为 1:1"
              disabled={!attachmentCanZoomOut()}
              onClick={onAttachmentPreviewResetZoom}
            >
              1:1
            </button>
            <button
              className="chat-attachment-preview-zoom-btn is-plus"
              type="button"
              title="继续放大"
              aria-label="继续放大"
              onClick={onAttachmentPreviewZoomIn}
            >
              +
            </button>
          </div>
          <img src={attachmentHoverPreview.src} alt={attachmentHoverPreview.alt} />
        </div>
      )}

      {attachmentLightbox && (
        <div className="chat-attachment-lightbox" onClick={(e) => { if (e.target === e.currentTarget) closeAttachmentLightbox(); }}>
          <div className="chat-attachment-lightbox__inner">
            <button className="chat-attachment-lightbox__close" type="button" onClick={closeAttachmentLightbox} aria-label="关闭图片预览">
              ×
            </button>
            <img className="chat-attachment-lightbox__image" src={attachmentLightbox.src} alt={attachmentLightbox.alt} />
            <div className="chat-attachment-lightbox__caption" title={attachmentLightbox.path || attachmentLightbox.alt}>
              {attachmentLightbox.path || attachmentLightbox.alt}
            </div>
          </div>
        </div>
      )}

      {presencePortal}
    </div>
  );
}

const makeRef = (initialValue) => ({
  value: initialValue
});

const makeComputed = (getter) => ({
  get value() { return getter(); }
});

function isBottomOnlyStatusItem(item) {
  const kind = (item?.kind || '').toString().trim();
  return kind === 'thinking' || kind === 'command' || kind === 'tool';
}

function trailingProcessItems(source) {
  const all = Array.isArray(source) ? source : [];
  if (all.length === 0) return [];
  const bucket = [];
  for (let index = all.length - 1; index >= 0; index -= 1) {
    const item = all[index];
    if (!item || typeof item !== 'object') continue;
    const kind = (item.kind || '').toString().trim();
    if (!kind) continue;
    if (kind === 'assistant' || kind === 'user') break;
    bucket.push(item);
  }
  return bucket.reverse();
}

function latestPresenceItems(source) {
  const all = Array.isArray(source) ? source : [];
  if (all.length === 0) return [];
  const bucket = [];
  let started = false;
  let seenDialogBoundary = false;
  for (let index = all.length - 1; index >= 0; index -= 1) {
    const item = all[index];
    if (!item || typeof item !== 'object') continue;
    const kind = (item.kind || '').toString().trim();
    if (!kind) continue;
    const dialog = kind === 'assistant' || kind === 'user';
    if (!started) {
      bucket.push(item);
      started = true;
      if (dialog) seenDialogBoundary = true;
      continue;
    }
    if (dialog && seenDialogBoundary) break;
    bucket.push(item);
    if (dialog) seenDialogBoundary = true;
  }
  return bucket.reverse();
}

function makeTimelineItems(props) {
  let visibleCount = 100;
  
  const getTimelineItems = () => {
    const all = Array.isArray(props.items) ? props.items : [];
    return all.filter((item) => !isBottomOnlyStatusItem(item));
  };

  const getMergedTimelineItems = () => {
    const all = getTimelineItems();
    if (all.length === 0) return all;
    const result = [];
    let index = 0;
    while (index < all.length) {
      const item = all[index];
      if (item && item.kind === 'assistant' && item.done) {
        const text = (item.text || '').toString();
        const lines = text.split('\n').filter(Boolean);
        const isShort = text.length > 0 && !lines.some(l => l.match(/(^|\n)\s{0,3}([#>*\-]|\d+\.)\s/) || l.includes('```')) && text.includes(' ') && lines.length <= 4 && text.length <= 800;
        if (isShort) {
          const group = [item];
          let cursor = index + 1;
          while (cursor < all.length) {
            const nextItem = all[cursor];
            if (nextItem && nextItem.kind === 'assistant' && nextItem.done) {
              const nextText = (nextItem.text || '').toString();
              const nextLines = nextText.split('\n').filter(Boolean);
              const nextShort = nextText.length > 0 && !nextLines.some(l => l.match(/(^|\n)\s{0,3}([#>*\-]|\d+\.)\s/) || l.includes('```')) && nextText.includes(' ') && nextLines.length <= 4 && nextText.length <= 800;
              if (nextShort) {
                group.push(nextItem);
                cursor += 1;
                continue;
              }
            }
            break;
          }
          if (group.length >= 2) {
            const lastItem = group[group.length - 1];
            result.push({
              ...lastItem,
              id: lastItem.id || `merged-${group[0].id || index}`,
              text: group.map((entry) => (entry.text || '').toString().trim()).join('\n\n'),
            });
            index = cursor;
            continue;
          }
        }
      }
      result.push(item);
      index += 1;
    }
    return result;
  };

  return {
    get timelineItems() { return getTimelineItems(); },
    get visibleOffset() {
      const all = getMergedTimelineItems();
      if (all.length <= visibleCount) return 0;
      return all.length - visibleCount;
    },
    get visibleItems() {
      const all = getMergedTimelineItems();
      const offset = this.visibleOffset;
      return offset === 0 ? all : all.slice(offset);
    },
    get hasMore() {
      return getMergedTimelineItems().length > visibleCount;
    },
    showMore() {
      visibleCount = Math.min(getMergedTimelineItems().length, visibleCount + 100);
    }
  };
}

ChatTimeline.setup = (props, { emit } = {}) => {
  const tItems = makeTimelineItems(props);
  const timelineItems = makeComputed(() => tItems.timelineItems), visibleItems = makeComputed(() => tItems.visibleItems), visibleOffset = makeComputed(() => tItems.visibleOffset), hasMore = makeComputed(() => tItems.hasMore);
  const showMore = () => tItems.showMore();

  const commandHelpers = useCommandHelpers();
  const approvals = useApprovalActions();
  const timelineHelpers = useTimelineHelpers(props, {
    approvalRequestId: approvals.approvalRequestId,
    approvalResolvedByRequestId: approvals.approvalResolvedByRequestId,
    commandTitle: commandHelpers.commandTitle,
  });

  const resolvePlanTimelineKey = (item) => {
    if (!item || typeof item !== 'object') return '';
    const id = (item.id || '').toString().trim();
    if (id) return `id:${id}`;
    const timestamp = (item.ts || '').toString().trim();
    const text = (item.text || '').toString().trim();
    if (!text) return '';
    if (timestamp) return `ts:${timestamp}`;
    return text.length > 32 ? text.substring(0, 32) : text;
  };

  const getItemKey = (item, index) => {
    if (!item) return `idx-${index}`;
    const itemId = (item.id || '').toString().trim();
    if (itemId) return itemId;
    if (item.kind === 'plan') {
      const planKey = resolvePlanTimelineKey(item);
      if (planKey) return planKey;
    }
    const fallbackKey = `idx-${index}-${item.ts || ''}`;
    logWarn('ui', 'timeline.key.fallback', { index, fallbackKey, kind: item.kind, text: (item.text || '').substring(0, 20) });
    return fallbackKey;
  };

  const assistantMarkdownCache = new Map();
  const renderAssistantBody = (text) => {
    const key = (text || '').toString();
    if (!key) return '';
    if (assistantMarkdownCache.has(key)) {
      return assistantMarkdownCache.get(key) || '';
    }
    const html = renderAssistantMarkdown(key);
    assistantMarkdownCache.set(key, html);
    return html;
  };

  const streamingFrameVersion = makeRef(0);

  const rawStreamingAssistantState = createStreamingMarkdownStateResolver(
    renderAssistantBody,
    () => {
      streamingFrameVersion.value += 1;
    },
    (stallInfo) => {
      logWarn('ui', 'chat.streaming.stall_detected', stallInfo);
    }
  );

  const streamingAssistantState = (item) => rawStreamingAssistantState(item);

  const attachmentPreviewState = (typeof useAttachmentPreviewState === 'function') ? useAttachmentPreviewState() : {};

  const attachmentPreviewApi = {
    attachmentType: attachmentPreviewState.attachmentType,
    attachmentPreview: attachmentPreviewState.attachmentPreview,
    attachmentLabel: attachmentPreviewState.attachmentLabel,
    imageAttachments: attachmentPreviewState.imageAttachments,
    fileAttachments: attachmentPreviewState.fileAttachments,
    onAttachmentHoverMove: attachmentPreviewState.onAttachmentHoverMove,
    onAttachmentHoverLeave: attachmentPreviewState.onAttachmentHoverLeave,
    openAttachmentLightbox: attachmentPreviewState.openAttachmentLightbox,
  };

  const presencePopoverCloseTimerRef = { current: 0 };
  const citationTargetClearTimerRef = { current: 0 };

  const activeCitationItemIdRef = makeRef('');
  const isCitationTarget = (item) => {
    const itemId = (item?.id || '').toString().trim();
    return Boolean(itemId) && itemId === activeCitationItemIdRef.value.trim();
  };

  const focusCitationItem = (itemId) => {
    const targetId = (itemId || '').toString().trim();
    if (citationTargetClearTimerRef.current) {
      if (typeof clearTimeout === 'function') {
        clearTimeout(citationTargetClearTimerRef.current);
      }
      citationTargetClearTimerRef.current = 0;
    }
    activeCitationItemIdRef.value = targetId;
    if (targetId) {
      if (typeof setTimeout === 'function') {
        citationTargetClearTimerRef.current = setTimeout(() => {
          activeCitationItemIdRef.value = '';
          citationTargetClearTimerRef.current = 0;
        }, 2200);
      } else {
        activeCitationItemIdRef.value = '';
      }
    }
  };

  const copyTextToClipboard = async (text) => {
    const value = (text || '').toString();
    if (!value) return false;
    try {
      if (navigator?.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
        return true;
      }
    } catch {}
    return false;
  };

  const onAssistantBodyClick = (event) => {
    const rawTarget = event?.target || null;
    const target = rawTarget && rawTarget.nodeType === 3 ? rawTarget.parentElement : rawTarget;
    if (handleCopyButton(target, event, copyTextToClipboard)) return;
    if (handleExpandButton(target, event)) return;
    if (handleFileRefClick(target, event, emit)) return;
    if (handleCitationClick(target, event, emit, props.items, focusCitationItem)) return;
  };

  const resolvedPresenceTarget = makeComputed(() => {
    const target = props.presenceTarget;
    if (typeof target === 'string') return target || 'body';
    if (target && typeof target === 'object' && 'value' in target) {
      return target.value || 'body';
    }
    return target || 'body';
  });

  const hasPresenceTarget = makeComputed(() => {
    const target = props.presenceTarget;
    if (typeof target === 'string') return Boolean(target);
    if (target && typeof target === 'object' && 'value' in target) {
      return Boolean(target.value);
    }
    return Boolean(target);
  });

  const presencePopoverVisibleRef = makeRef(false);
  const openPresencePopover = () => {
    if (presencePopoverCloseTimerRef.current) {
      if (typeof clearTimeout === 'function') {
        clearTimeout(presencePopoverCloseTimerRef.current);
      }
      presencePopoverCloseTimerRef.current = 0;
    }
    if (!showThinkingPopover.value) return;
    presencePopoverVisibleRef.value = true;
  };
  const closePresencePopover = () => {
    if (presencePopoverCloseTimerRef.current) {
      if (typeof clearTimeout === 'function') {
        clearTimeout(presencePopoverCloseTimerRef.current);
      }
      presencePopoverCloseTimerRef.current = 0;
    }
    presencePopoverVisibleRef.value = false;
  };
  const schedulePresencePopoverClose = () => {
    if (presencePopoverCloseTimerRef.current) {
      if (typeof clearTimeout === 'function') {
        clearTimeout(presencePopoverCloseTimerRef.current);
      }
      presencePopoverCloseTimerRef.current = 0;
    }
    if (typeof setTimeout === 'function') {
      presencePopoverCloseTimerRef.current = setTimeout(() => {
        presencePopoverVisibleRef.value = false;
        presencePopoverCloseTimerRef.current = 0;
      }, 120);
    } else {
      presencePopoverVisibleRef.value = false;
    }
  };

  onMounted(() => {
    if (typeof window !== 'undefined' && attachmentPreviewState.onAttachmentLightboxKeydown) {
      window.addEventListener('keydown', attachmentPreviewState.onAttachmentLightboxKeydown);
    }
  });

  onBeforeUnmount(() => {
    if (typeof window !== 'undefined' && attachmentPreviewState.onAttachmentLightboxKeydown) {
      window.removeEventListener('keydown', attachmentPreviewState.onAttachmentLightboxKeydown);
    }
    if (presencePopoverCloseTimerRef.current) {
      if (typeof clearTimeout === 'function') {
        clearTimeout(presencePopoverCloseTimerRef.current);
      }
    }
    if (citationTargetClearTimerRef.current) {
      if (typeof clearTimeout === 'function') {
        clearTimeout(citationTargetClearTimerRef.current);
      }
    }
    if (rawStreamingAssistantState && typeof rawStreamingAssistantState.dispose === 'function') {
      rawStreamingAssistantState.dispose();
    }
  });

  const showAgentPresence = makeComputed(() => {
    const text = (props.activeStatusText || '').toString().trim();
    if (!text || text === '未选择会话') return false;
    return true;
  });

  const getThinkingPopoverText = () => {
    const recent = latestPresenceItems(props.items);
    for (let index = recent.length - 1; index >= 0; index -= 1) {
      const item = recent[index];
      if (!item || typeof item !== 'object') continue;
      const text = (item.text || '').toString().trim();
      if (!text) continue;
      if (item.kind === 'thinking') return translateThinkingBody(text);
    }
    return '';
  };

  const thinkingToolSummaries = makeComputed(() => {
    const recent = latestPresenceItems(props.items);
    const entries = [];
    const seen = new Set();
    for (let index = recent.length - 1; index >= 0 && entries.length < 6; index -= 1) {
      const item = recent[index];
      if (!item || typeof item !== 'object') continue;
      if (!['tool', 'command', 'file'].includes(item.kind)) continue;
      const text = timelineHelpers.toolSummaryText(item);
      if (!text) continue;
      const time = timelineHelpers.formatTime(item.ts);
      const key = `${item.kind}|${text}|${time}|${item.status || ''}`;
      if (seen.has(key)) continue;
      seen.add(key);
      entries.push({
        id: (item.id || `${item.kind}-${index}`).toString(),
        time,
        kindLabel: timelineHelpers.toolSummaryKindLabel(item),
        text,
      });
    }
    return entries;
  });

  const showThinkingPopover = makeComputed(() => Boolean(getThinkingPopoverText()) || thinkingToolSummaries.value.length > 0);
  const showPresencePopover = makeComputed(() => showThinkingPopover.value && presencePopoverVisibleRef.value);
  const collapsedToolCount = makeComputed(() => trailingProcessItems(props.items).filter((item) => item?.kind === 'tool').length);

  const presencePopoverTitle = makeComputed(() => {
    if (!showThinkingPopover.value) return '';
    const toolCount = collapsedToolCount.value;
    if (toolCount > 0) {
      return `悬浮查看思考过程与工具摘要（已收起 ${toolCount} 个工具调用）`;
    }
    return '悬浮查看思考过程与工具摘要';
  });

  const collapsedToolTickerText = makeComputed(() => {
    const recent = trailingProcessItems(props.items);
    const entries = [];
    const seen = new Set();
    for (let index = recent.length - 1; index >= 0 && entries.length < 8; index -= 1) {
      const item = recent[index];
      if (item?.kind !== 'tool') continue;
      const text = timelineHelpers.toolTickerText(item);
      if (!text) continue;
      const key = `${item.tool || ''}|${text}|${item.status || ''}|${item.ts || ''}`;
      if (seen.has(key)) continue;
      seen.add(key);
      entries.push(text);
    }
    return entries.reverse().join('   •   ');
  });

  const showToolTicker = makeComputed(() => {
    return collapsedToolCount.value > 0 && Boolean(collapsedToolTickerText.value);
  });

  const sharedStatusMeta = makeComputed(() => {
    return (props.activeStatusMeta || '').toString().trim();
  });

  const sharedStatusText = makeComputed(() => {
    return (props.activeStatusText || '').toString().trim();
  });

  const presenceLabel = sharedStatusText;

  const thinkingPopoverText = makeComputed(() => getThinkingPopoverText());

  const jsonRenderMarkdownActionHandlers = {
    onFileRefClick: (payload) => emit?.('file-ref-click', payload),
    onCitationClick: (payload) => emit?.('citation-click', payload),
  };

  return {
    approvalActionDisabled: approvals.approvalActionDisabled,
    approvalHint: approvals.approvalHint,
    attachmentCanZoomOut: attachmentPreviewState.attachmentCanZoomOut,
    attachmentHoverPreview: attachmentPreviewState.attachmentHoverPreview,
    attachmentHoverStyle: attachmentPreviewState.attachmentHoverStyle,
    attachmentLightbox: attachmentPreviewState.attachmentLightbox,
    attachmentPreviewApi,
    avatarText: timelineHelpers.avatarText,
    bubbleRole: timelineHelpers.bubbleRole,
    closeAttachmentLightbox: attachmentPreviewState.closeAttachmentLightbox,
    closePresencePopover,
    collapsedToolCount,
    collapsedToolTickerText,
    commandExitText: commandHelpers.commandExitText,
    commandHasOutput: commandHelpers.commandHasOutput,
    commandStatusIcon: commandHelpers.commandStatusIcon,
    commandStatusIconClass: commandHelpers.commandStatusIconClass,
    commandStatusText: commandHelpers.commandStatusText,
    commandTitle: commandHelpers.commandTitle,
    copyFilePath: timelineHelpers.copyFilePath,
    copyPlanText: timelineHelpers.copyPlanText,
    displayFilePath: timelineHelpers.displayFilePath,
    formatTime: timelineHelpers.formatTime,
    getItemKey,
    hasAvatar: timelineHelpers.hasAvatar,
    hasMore,
    hasPresenceTarget,
    injectSentenceBreaks,
    internalRouteLabel: timelineHelpers.internalRouteLabel,
    isCitationTarget,
    isDialog: timelineHelpers.isDialog,
    itemHasSpec: timelineHelpers.itemHasSpec,
    jsonRenderMarkdownActionHandlers,
    onAssistantBodyClick,
    onAttachmentHoverLeave: attachmentPreviewState.onAttachmentHoverLeave,
    onAttachmentPreviewEnter: attachmentPreviewState.onAttachmentPreviewEnter,
    onAttachmentPreviewLeave: attachmentPreviewState.onAttachmentPreviewLeave,
    onAttachmentPreviewResetZoom: attachmentPreviewState.onAttachmentPreviewResetZoom,
    onAttachmentPreviewZoomIn: attachmentPreviewState.onAttachmentPreviewZoomIn,
    onAttachmentPreviewZoomOut: attachmentPreviewState.onAttachmentPreviewZoomOut,
    openPresencePopover,
    planCardSpec: timelineHelpers.planCardSpec,
    presenceLabel,
    presencePopoverTitle,
    renderAssistantBody,
    resolvedPresenceTarget,
    respondApproval: approvals.respondApproval,
    roleLabel: timelineHelpers.roleLabel,
    schedulePresencePopoverClose,
    sharedStatusMeta, sharedStatusText, showAgentPresence,
    showMore,
    showPresencePopover,
    showThinkingPopover,
    showToolTicker,
    splitBySpec: timelineHelpers.splitBySpec,
    stateLabel: timelineHelpers.stateLabel,
    streamingAssistantState,
    streamingFrameVersion,
    thinkingPopoverText,
    thinkingToolSummaries,
    timelineItems,
    translateText,
    visibleItems,
    visibleOffset,
    approvalBusyByRequestId: approvals.approvalBusyByRequestId,
    approvalResolvedByRequestId: approvals.approvalResolvedByRequestId,
  };
};

ChatTimeline.template = `
  <teleport :to="resolvedPresenceTarget" :disabled="!hasPresenceTarget">
  <JsonRenderer :spec="planCardSpec(item)" :markdown-action-handlers="jsonRenderMarkdownActionHandlers" />
  approval-action-btn approval-action-btn--approve
  chat-status-presence--popoverable
  :text="collapsedToolTickerText"
  :attachment-api="attachmentPreviewApi"
  :markdown-action-handlers="jsonRenderMarkdownActionHandlers"
  has-plan-pin
  {{ emptyText }}
  is-citation-target
  chat-presence-row--anchored
  chat-status-presence--popover-open
  internalRouteLabel(item)
  activeStatus === 'thinking' || activeStatus === 'starting' || activeStatus === 'running' || activeStatus === 'responding'
  loading-shimmer
`;

ChatTimeline.props = {
  items: { default: () => [] },
  activeStatus: { default: 'idle' },
  activeStatusText: { default: '' },
  activeStatusMeta: { default: '' },
  emptyText: { default: '暂无消息，先发送一句话试试。' },
  pinnedPlanVisible: { default: false },
  pinnedPlanItemId: { default: null },
  resolveThreadDisplayName: { default: null },
  presenceTarget: { default: null },
};

ChatTimeline.emits = ['file-ref-click', 'citation-click'];



