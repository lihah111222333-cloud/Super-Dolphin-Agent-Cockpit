import { memo, useLayoutEffect } from 'react';
import { File } from 'lucide-react';
import { textValue } from '../../shared/pageShared.js';
import { useSmoothStreamingText } from '../hooks/useSmoothStreamingText.js';
import { ChatApprovalMessage } from './ChatApprovalMessage.jsx';
import { AssistantMessageActions, ReasoningTrace } from './ChatReasoningTrace.jsx';
import { MarkdownImagePreview, MessageContent } from './MarkdownMessage.jsx';
import { isApprovalMessage } from './chatApprovalModel.js';
import { isReasoningMessage } from './chatReasoningModel.js';
import { basenameFromPath } from './markdownMessageModel.js';
import { resolveAttachmentImageSrc } from './timelineMessageModel.js';

const IMAGE_ATTACHMENT_LABEL = '\u56fe\u7247\u9644\u4ef6';
const SYNC_HISTORY_LABEL = '\u6b63\u5728\u540c\u6b65\u4f1a\u8bdd\u5386\u53f2';

function UserMessageAttachments({ attachments }) {
  if (!Array.isArray(attachments) || attachments.length === 0) return null;
  const images = [];
  const files = [];
  for (const att of attachments) {
    if (!att) continue;
    const kind = textValue(att.kind).toLowerCase();
    if (kind === 'image') {
      const src = resolveAttachmentImageSrc(att);
      if (src) {
        images.push({ ...att, _resolvedSrc: src });
      } else {
        files.push(att);
      }
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
              key={att.path || att.previewUrl || idx}
              src={att._resolvedSrc}
              label={att.name || basenameFromPath(att.path || att.previewUrl || '') || IMAGE_ATTACHMENT_LABEL}
            />
          ))}
        </div>
      ) : null}
      {files.length > 0 ? (
        <div className="user-attachment-file-list">
          {files.map((att, idx) => (
            <span key={att.path || att.name || idx} className="user-attachment-file-pill">
              <File size={12} />
              <span>{att.name || basenameFromPath(att.path || '') || att.path}</span>
            </span>
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
  smoothStreaming,
  onScrollIfSticky,
  formatTime,
}) {
  const streamKey = `${activeThreadId || ''}:${message.id || ''}`;
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
        {isUser ? <UserMessageAttachments attachments={message.attachments} /> : null}
        <MessageContent text={displayText} actions={actions} />
        {!isUser && message.role === 'assistant' ? (
          <div className="assistant-footer">
            <time>{formatTime(message.time)}</time>
            <AssistantMessageActions text={message.text} />
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
