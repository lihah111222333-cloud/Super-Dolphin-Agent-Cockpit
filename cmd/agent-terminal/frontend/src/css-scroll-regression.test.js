// @ts-nocheck
/**
 * CSS 回归测试 — 防止 chat-item / markdown 的 fade-in 动画和滚动锚定被意外恢复。
 *
 * 根因: .chat-item 和 .agent-markdown-root 内元素使用 opacity:0 + agent-fade-in 动画，
 * timeline 数据替换时 Vue 重建 DOM，所有元素从 opacity:0 开始重播淡入 → scrollHeight
 * 瞬间塌缩 → 滚动位置跳到中间或顶部。
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const FRONTEND_ROOT = resolve(import.meta.dirname, '.');

function readCSS(relativePath) {
  return readFileSync(resolve(FRONTEND_ROOT, relativePath), 'utf-8').replace(/\r\n/g, '\n');
}

function cssBlock(css, selector) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = css.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`));
  return match ? match[1].replace(/\/\*[\s\S]*?\*\//g, '') : '';
}

describe('[regression] CSS scroll-jump guards', () => {
  it('.chat-item must NOT have agent-fade-in animation', () => {
    const css = readCSS('styles/diff-chat.css');
    // 匹配 .chat-item { ... } 块
    const chatItemBlock = css.match(/\.chat-item\s*\{[^}]*\}/);
    expect(chatItemBlock).toBeTruthy();
    // 去掉注释后检查，避免注释中提到 agent-fade-in 导致误报
    const block = chatItemBlock[0].replace(/\/\*[\s\S]*?\*\//g, '');
    expect(block).not.toMatch(/animation\s*:/);
    expect(block).not.toMatch(/agent-fade-in/);
    // 不能有 opacity: 0
    expect(block).not.toMatch(/opacity\s*:\s*0/);
  });

  it('.chat-messages-vue must have overflow-anchor: auto', () => {
    const css = readCSS('styles/diff-chat.css');
    // 匹配 .chat-messages-vue { ... } 第一个块（不含 .has-plan-pin 复合选择器）
    const blocks = [...css.matchAll(/\.chat-messages-vue\s*\{([^}]*)\}/g)];
    const mainBlock = blocks.find(m => !m[0].includes('.has-plan-pin'));
    expect(mainBlock).toBeTruthy();
    expect(mainBlock[1]).toMatch(/overflow-anchor\s*:\s*auto/);
  });

  it('.agent-markdown-root elements must NOT start at opacity:0 with animation', () => {
    const css = readCSS('agent-components.css');
    // 找到 .agent-markdown-root 相关的规则块
    const markdownBlocks = [...css.matchAll(/\.agent-markdown-root[^{]*\{([^}]*)\}/g)];
    expect(markdownBlocks.length).toBeGreaterThan(0);
    for (const match of markdownBlocks) {
      const body = match[1];
      // 不允许 opacity: 0 + animation 组合（这会导致流式渲染闪烁）
      const hasOpacityZero = /opacity\s*:\s*0/.test(body);
      const hasAnimation = /animation\s*:.*agent-fade-in/.test(body);
      expect(
        hasOpacityZero && hasAnimation,
        `规则 "${match[0].slice(0, 80)}..." 不应同时包含 opacity:0 + agent-fade-in 动画`,
      ).toBe(false);
    }
  });

  it('composer textarea allows user-controlled vertical resizing', () => {
    const css = readCSS('styles/composer.css');
    const block = cssBlock(css, '.chat-input-row-vue textarea,\n#chatInput');

    expect(block).toMatch(/resize\s*:\s*vertical/);
  });
});
describe('[regression] activity tool detail layout guards', () => {
  it('keeps per-tool counts in a right-aligned numeric column', () => {
    const css = readCSS('styles/activity-widgets.css');
    const detailBlock = cssBlock(css, '.activity-tool-detail');
    const entryBlock = cssBlock(css, '.tool-entry');
    const countBlock = cssBlock(css, '.tool-entry strong');

    expect(detailBlock).toMatch(/display\s*:\s*grid/);
    expect(detailBlock).toMatch(/grid-template-columns\s*:/);
    expect(entryBlock).toMatch(/grid-template-columns\s*:\s*minmax\(0\s*,\s*1fr\)\s+auto/);
    expect(countBlock).toMatch(/text-align\s*:\s*right/);
    expect(countBlock).toMatch(/font-variant-numeric\s*:\s*tabular-nums/);
  });
});
