import { memo, useLayoutEffect } from 'react';
import { File } from 'lucide-react';
import { useSmoothStreamingText } from '../hooks/useSmoothStreamingText.js';
import { ChatApprovalMessage } from './ChatApprovalMessage.jsx';
import { AssistantMessageActions, ReasoningTrace } from './ChatReasoningTrace.jsx';
import { MarkdownImagePreview, MessageContent } from '../markdown/MarkdownMessage.jsx';
import { isApprovalMessage } from './chatApprovalModel.js';
import { isReasoningMessage } from './chatReasoningModel.js';
import { basenameFromPath, firstText, trimmedText } from '../markdown/markdownMessageModel.js';
import { resolveAttachmentImageSrc } from './timelineMessageModel.js';

const IMAGE_ATTACHMENT_LABEL = '\u56fe\u7247\u9644\u4ef6';
const SYNC_HISTORY_LABEL = '\u6b63\u5728\u540c\u6b65\u4f1a\u8bdd\u5386\u53f2';
const ASSISTANT_THINKING_LABEL = '\u601d\u8003\u4e2d';

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

const TimelineMessage = memo(function TimelineMessage({
  message,
  actions,
  activeThreadId,
  copy,
  smoothStreaming,
  onScrollIfSticky,
  formatTime,
}) {
  const streamKey = `${trimmedText(activeThreadId)}:${trimmedText(message.id)}`;
  const streamingAssistant = message.role === 'assistant' && message.done === false;
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

  const isUser = message.role === 'user';
  return (
    <article className={`message ${message.role} no-avatar`}>
      <div className="bubble">
        {isUser ? (
          <header>
            <time>{formatTime(message.time)}</time>
          </header>
        ) : null}
        {isUser ? <UserMessageAttachments attachments={message.attachments} actions={actions} /> : null}
        <MessageContent text={displayText} actions={actions} />
        {streamingAssistant ? (
          <output className="assistant-thinking-status" aria-live="polite">
            {ASSISTANT_THINKING_LABEL}
          </output>
        ) : null}
        {!isUser && message.role === 'assistant' ? (
          <div className="assistant-footer">
            <time>{formatTime(message.time)}</time>
            <AssistantMessageActions copy={copy} text={message.text} />
          </div>
        ) : null}
      </div>
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
