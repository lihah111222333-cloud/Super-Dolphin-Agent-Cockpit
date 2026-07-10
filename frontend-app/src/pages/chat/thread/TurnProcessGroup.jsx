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
  return (
    <details className={`turn-process${active ? ' is-active' : ''}`} data-testid="turn-process-group">
      <summary>
        <span className="turn-process-state" aria-hidden="true" />
        <span>{`${label} · ${messages.length} ${countSuffix}`}</span>
        <ChevronDown className="turn-process-chevron" size={16} aria-hidden="true" />
      </summary>
      <div className="turn-process-list">
        {messages.map((message) => (
          <TimelineMessage
            key={message.callId ? `tool-${message.callId}` : message.id}
            message={message}
            actions={actions}
            activeThreadId={activeThreadId}
            copy={copy}
            smoothStreaming={smoothStreaming}
            onScrollIfSticky={onScrollIfSticky}
            formatTime={formatTime}
          />
        ))}
      </div>
    </details>
  );
}

export { TurnProcessGroup };
