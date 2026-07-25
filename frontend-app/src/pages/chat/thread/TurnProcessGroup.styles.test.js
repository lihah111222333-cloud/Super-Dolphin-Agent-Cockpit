import { readFileSync } from 'node:fs';
import path from 'node:path';
import { cwd } from 'node:process';
import postcss from 'postcss';
import { describe, expect, it } from 'vitest';

const stylesheet = readFileSync(path.join(cwd(), 'src/pages/chat/thread/TurnProcessGroup.css'), 'utf8');
const root = postcss.parse(stylesheet);
const chatStylesheet = readFileSync(path.join(cwd(), 'src/pages/chat/ChatMessages.css'), 'utf8');
const chatRoot = postcss.parse(chatStylesheet);

function declarationsFor(selector, stylesheetRoot = root) {
  const declarations = {};
  stylesheetRoot.walkRules(selector, (rule) => {
    if (rule.selector !== selector) return;
    rule.walkDecls((declaration) => {
      declarations[declaration.prop] = declaration.value;
    });
  });
  return declarations;
}

describe('TurnProcessGroup styles', () => {
  it('keeps user bubbles content-sized and anchored to the right', () => {
    const userBubble = declarationsFor('.message.user .chat-x-bubble', chatRoot);

    expect(userBubble.width).toBe('fit-content');
    expect(userBubble['max-width']).toBe('min(720px, 78%)');
    expect(userBubble['margin-left']).toBe('auto');
  });

  it('keeps expanded process messages inside a scrollable window', () => {
    const list = declarationsFor('.turn-process-list');

    expect(list['max-height']).toBe('min(480px, 52vh)');
    expect(list['overflow-y']).toBe('auto');
    expect(list['overscroll-behavior']).toBe('contain');
    expect(list['scrollbar-gutter']).toBe('stable');
  });
});
