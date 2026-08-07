import { memo, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Bot, CheckCircle2, Copy, File } from 'lucide-react';
import Bubble from '@ant-design/x/es/bubble';
import { isApprovalMessage } from '../../../features/approval/model/approvalDecision.js';
import { runUIAction } from '../../../shared/ui/runUIAction.js';
import { publicErrorForRemoteTerminal } from '../../../shared/ui/publicError.js';
import { useSmoothStreamingText } from '../hooks/useSmoothStreamingText.js';
import { ChatApprovalMessage } from './ChatApprovalMessage.jsx';
import { AssistantMessageActions, ReasoningTrace } from './ChatReasoningTrace.jsx';
import { copyTextToClipboard } from '../services/chatCodeService.js';
import { MarkdownImagePreview, MessageContent } from '../markdown/MarkdownMessage.jsx';
import { isReasoningMessage } from './chatReasoningModel.js';
import { basenameFromPath, firstText, trimmedText } from '../markdown/markdownMessageModel.js';
import { resolveAttachmentImageSrc } from './timelineMessageModel.js';

const IMAGE_ATTACHMENT_LABEL = '\u56fe\u7247\u9644\u4ef6';
const SYNC_HISTORY_LABEL = '\u6b63\u5728\u540c\u6b65\u4f1a\u8bdd\u5386\u53f2';
const ASSISTANT_THINKING_LABEL = '\u601d\u8003\u4e2d';
const TERMINAL_STATUS_LABELS = {
  interrupted: '\u5df2\u4e2d\u65ad',
  cancelled: '\u5df2\u53d6\u6d88',
};
const COPY_DIAGNOSTICS_ACTION = 'copy_diagnostics';

function safeDiagnosticField(value) {
  return (value === null || value === undefined ? '' : String(value)).replace(/\s+/g, ' ').trim();
}

function terminalDiagnosticText(error) {
  return [
    `Diagnostic ID: ${safeDiagnosticField(error.diagnosticId)}`,
    `Code: ${safeDiagnosticField(error.code)}`,
    `Title: ${safeDiagnosticField(error.title)}`,
    `Message: ${safeDiagnosticField(error.message)}`,
  ].join('\n');
}

function TerminalPublicError({ error, processInternal = false }) {
  const publicError = publicErrorForRemoteTerminal(error);
  const [copyState, setCopyState] = useState('idle');
  const resetTimerRef = useRef(null);
  useEffect(() => () => {
    if (resetTimerRef.current) window.clearTimeout(resetTimerRef.current);
  }, []);
  const diagnosticId = safeDiagnosticField(publicError.diagnosticId);
  const canCopyDiagnostics = diagnosticId.length > 0
    && publicError.recoveryActions.includes(COPY_DIAGNOSTICS_ACTION);
  const scheduleReset = (delay) => {
    if (resetTimerRef.current) window.clearTimeout(resetTimerRef.current);
    resetTimerRef.current = window.setTimeout(() => {
      resetTimerRef.current = null;
      setCopyState('idle');
    }, delay);
  };
  const copyDiagnostics = () => runUIAction('turn.terminal.copy-diagnostics', async () => {
    setCopyState('copying');
    try {
      await copyTextToClipboard(terminalDiagnosticText(publicError));
      setCopyState('copied');
      scheduleReset(1800);
    } catch (copyError) {
      setCopyState('failed');
      scheduleReset(2200);
      throw copyError;
    }
  }, { retryable: true });
  let copyLabel = '\u590d\u5236\u8bca\u65ad\u4fe1\u606f';
  if (copyState === 'copying') copyLabel = '\u6b63\u5728\u590d\u5236';
  if (copyState === 'copied') copyLabel = '\u5df2\u590d\u5236';
  if (copyState === 'failed') copyLabel = '\u590d\u5236\u5931\u8d25\uff0c\u8bf7\u91cd\u8bd5';
  return (
    <div role="alert" className="turn-terminal-error">
      <strong>{publicError.title}</strong>
      <span>{publicError.message}</span>
      {diagnosticId ? <code className="turn-terminal-diagnostic-id">{'\u8bca\u65ad ID\uff1a'}{diagnosticId}</code> : null}
      {canCopyDiagnostics && !processInternal ? (
        <div className="turn-terminal-actions" aria-label={'\u7ec8\u6001\u9519\u8bef\u6062\u590d\u64cd\u4f5c'}>
          <button
            type="button"
            className={`message-copy${copyState === 'copied' ? ' is-copied' : ''}${copyState === 'failed' ? ' is-failed' : ''}`}
            disabled={copyState === 'copying'}
            aria-label={`\u590d\u5236\u8bca\u65ad\u4fe1\u606f ${diagnosticId}`}
            title={'\u590d\u5236\u8131\u654f\u8bca\u65ad\u4fe1\u606f'}
            onClick={() => { void copyDiagnostics(); }}
          >
            {copyState === 'copied' ? <CheckCircle2 size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
            <span aria-live="polite">{copyLabel}</span>
          </button>
        </div>
      ) : null}
    </div>
  );
}

function attachmentFilePath(att) {
  return firstText(att?.path, att?.url, att?.previewUrl).trim();
}

function attachmentFileLabel(att) {
  const path = attachmentFilePath(att);
  return firstText(att?.name, basenameFromPath(path), path);
}

function UserAttachmentFilePill({ att, index, actions }) {
  const path = attachmentFilePath(att);
  const label = attachmentFileLabel(att);
  const openPath = actions?.onOpenPath;
  const fileRef = actions?.onFileRef;
  const openFile = typeof openPath === 'function' ? openPath : fileRef;
  const content = (
    <>
      <File size={12} />
      <span>{label}</span>
    </>
  );
  if (!path || !openFile) {
    return <span key={firstText(att?.path, att?.name, index)} className="user-attachment-file-pill">{content}</span>;
  }
  return (
    <button
      key={firstText(att?.path, att?.name, index)}
      type="button"
      className="user-attachment-file-pill"
      title={path}
      aria-label={`\u6253\u5f00\u6587\u4ef6 ${label}`}
      onClick={() => openFile({ path, line: 1, column: 0, raw: label })}
    >
      {content}
    </button>
  );
}

function UserMessageAttachments({ attachments, actions }) {
  if (!Array.isArray(attachments) || attachments.length === 0) return null;
  const images = [];
  const files = [];
  for (const att of attachments) {
    if (!att) continue;
    const src = resolveAttachmentImageSrc(att);
    if (src) {
      images.push({ ...att, _resolvedSrc: src });
    } else {
      files.push(att);
    }
  }
  if (images.length === 0 && files.length === 0) return null;
  return (
    <div className="user-message-attachments">
      {images.length > 0 ? (
        <div className="user-attachment-gallery">
          {images.map((att, idx) => (
            <MarkdownImagePreview
              key={firstText(att.path, att.previewUrl, att.url, idx)}
              src={att._resolvedSrc}
              label={firstText(att.name, basenameFromPath(firstText(att.path, att.previewUrl, att.url)), IMAGE_ATTACHMENT_LABEL)}
            />
          ))}
        </div>
      ) : null}
      {files.length > 0 ? (
        <div className="user-attachment-file-list">
          {files.map((att, idx) => (
            <UserAttachmentFilePill key={firstText(att.path, att.name, idx)} att={att} index={idx} actions={actions} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

const CHAT_ASSISTANT_AVATAR = (
  <span className="chat-bubble-avatar" aria-hidden="true">
    <Bot size={16} strokeWidth={1.9} />
  </span>
);

const TimelineMessage = memo(function TimelineMessage({
  message,
  actions,
  activeThreadId,
  copy,
  smoothStreaming,
  onScrollIfSticky,
  formatTime,
  processInternal = false,
}) {
  const streamKey = `${trimmedText(activeThreadId)}:${trimmedText(message.id)}`;
  const streamingAssistant = message.role === 'assistant' && message.done === false;
  const [streamedMarkdown] = useState(streamingAssistant);
  const displayText = useSmoothStreamingText(message.text, {
    enabled: streamingAssistant && smoothStreaming,
    streamKey,
  });

  useLayoutEffect(() => {
    if (streamingAssistant && onScrollIfSticky) {
      onScrollIfSticky(false);
    }
  }, [displayText, streamingAssistant, smoothStreaming, onScrollIfSticky]);

  if (isApprovalMessage(message)) return <ChatApprovalMessage message={message} actions={actions} formatTime={formatTime} />;
  if (isReasoningMessage(message)) return <ReasoningTrace message={message} active={message.done === false} />;
  if (message.kind === 'turn_terminal') {
    const error = message.publicError;
    const isFailure = message.terminalOutcome === 'failed' || (message.terminalOutcome !== 'success' && error);
    return (
      <article className={`message assistant no-avatar turn-terminal-message${processInternal ? ' process-internal' : ''}`}>
        <div className="bubble">
          {isFailure ? (
            <TerminalPublicError error={error} processInternal={processInternal} />
          ) : (
            <output role="status" className="turn-terminal-status">{TERMINAL_STATUS_LABELS[message.terminalOutcome] || '\u5df2\u5b8c\u6210'}</output>
          )}
          <div className="assistant-footer"><time>{formatTime(message.time)}</time></div>
        </div>
      </article>
    );
  }

  const isUser = message.role === 'user';
  // 消息体使用 Ant Design X Bubble 原生聊天气泡；Markdown/附件/复制等既有
  // 渲染能力作为 content/footer 插槽继续复用，业务数据与流式语义不变。
  return (
    <article className={`message ${message.role} no-avatar${processInternal ? ' process-internal' : ''}`}>
      <Bubble
        className={`chat-x-bubble chat-x-bubble--${isUser ? 'user' : 'assistant'}`}
        placement={isUser ? 'end' : 'start'}
        variant={isUser ? 'filled' : 'outlined'}
        shape="corner"
        avatar={isUser || processInternal ? undefined : CHAT_ASSISTANT_AVATAR}
        loading={streamingAssistant && !trimmedText(displayText)}
        content={(
          <>
            {isUser ? <UserMessageAttachments attachments={message.attachments} actions={actions} /> : null}
            <MessageContent text={displayText} actions={actions} forceMarkdown={streamedMarkdown} />
          </>
        )}
        footer={(
          <div className={isUser ? 'user-footer' : 'assistant-footer'}>
            {!isUser && streamingAssistant ? (
              <output className="assistant-thinking-status" aria-live="polite">
                {ASSISTANT_THINKING_LABEL}
              </output>
            ) : null}
            <time>{formatTime(message.time)}</time>
            {isUser || processInternal ? null : <AssistantMessageActions copy={copy} text={message.text} />}
          </div>
        )}
        footerPlacement={processInternal ? 'inner-end' : 'outer-start'}
      />
    </article>
  );
});

function TimelineLoadingPlaceholder() {
  return (
    <div className="timeline-placeholder" data-testid="timeline-loading-placeholder" aria-live="polite">
      <span className="timeline-placeholder-line" />
      <span className="timeline-placeholder-line timeline-placeholder-line--short" />
      <p>{SYNC_HISTORY_LABEL}</p>
    </div>
  );
}

export { TimelineLoadingPlaceholder, TimelineMessage, UserMessageAttachments };
