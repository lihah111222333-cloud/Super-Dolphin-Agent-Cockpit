import React, { createRef } from 'react';
import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { ConversationTimeline } from './Conversation.jsx';

function renderTimeline(copy) {
  render(
    <ConversationTimeline
      activeCurrentTurn={null}
      activeThreadId=""
      copy={copy}
      introMode={false}
      messages={[]}
      pendingReasoning={null}
      timelineContentBlocked
      timelineRef={createRef()}
    />,
  );
}

it.each([
  ['中文', APP_COPY.zh.chat, '聊天记录'],
  ['English', APP_COPY.en.chat, 'Chat history'],
])('names the focusable timeline region in %s without a live announcement', (_locale, copy, name) => {
  renderTimeline(copy);

  const timeline = screen.getByRole('region', { name });
  expect(timeline).toHaveClass('timeline');
  expect(timeline).toHaveAttribute('tabindex', '0');
  expect(timeline).not.toHaveAttribute('aria-live');
});
