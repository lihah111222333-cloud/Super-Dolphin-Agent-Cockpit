import React from 'react';
import { ChevronDown } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { TimelineMessage } from './TimelineMessage.jsx';
import './TurnProcessGroup.css';

function TurnProcessGroup({
  active = false,
  activeThreadId,
  actions,
  copy = APP_COPY.zh.chat,
  formatTime,
  messages,
  onScrollIfSticky,
  smoothStreaming,
}) {
  const label = active ? copy.processRunning : copy.processComplete;
  const countSuffix = messages.length === 1 ? copy.processStepSingle : copy.processStepPlural;
  const messageKeyOccurrences = new Map();
  return (
    <details className={`turn-process${active ? ' is-active' : ''}`} data-testid="turn-process-group">
      <summary>
        <span className="turn-process-state" aria-hidden="true" />
        <span>{`${label} · ${messages.length} ${countSuffix}`}</span>
        <ChevronDown className="turn-process-chevron" size={16} aria-hidden="true" />
      </summary>
      <div className="turn-process-list">
        {messages.map((message) => {
          const identity = `${message.callId ? `tool-${message.callId}` : 'tool'}:${message.id || 'message'}`;
          const occurrence = messageKeyOccurrences.get(identity) || 0;
          messageKeyOccurrences.set(identity, occurrence + 1);
          return (
            <TimelineMessage
              key={`${identity}:${occurrence}`}
              message={message}
              actions={actions}
              activeThreadId={activeThreadId}
              copy={copy}
              smoothStreaming={smoothStreaming}
              onScrollIfSticky={onScrollIfSticky}
              formatTime={formatTime}
            />
          );
        })}
      </div>
    </details>
  );
}

export { TurnProcessGroup };
