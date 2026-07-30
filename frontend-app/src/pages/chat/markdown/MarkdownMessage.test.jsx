import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ImageLightbox } from './ImageLightbox.jsx';
import { CodePreviewMarkdown, MarkdownImagePreview, MessageContent } from './MarkdownMessage.jsx';

describe('MarkdownMessage', () => {
  it('renders markdown blocks and routes file citation actions', () => {
    const onFileRef = vi.fn();

    render(
      <MessageContent
        text={[
          '# Plan',
          '',
          '- first item',
          '',
          ':codex-file-citation[]{path="src/main.go" line_range_start="9" line_range_end="11"}',
        ].join('\n')}
        actions={{ onFileRef }}
      />
    );

    expect(screen.getByRole('heading', { name: 'Plan' })).toBeInTheDocument();
    expect(screen.getByRole('listitem')).toHaveTextContent('first item');

    fireEvent.click(screen.getByRole('button', { name: /src\/main\.go/ }));

    expect(onFileRef).toHaveBeenCalledWith(expect.objectContaining({
      column: 0,
      line: 9,
      path: 'src/main.go',
    }));
  });

  it('keeps every streamed chunk inside one semantic Markdown container', () => {
    const chunks = [
      '# Streaming title',
      '\n\n- unordered item',
      '\n    - nested item',
      '\n1. ordered item',
      '\n\n**bold** [link](https://example.test) `inline`',
      '\n\n> quoted text',
      '\n\n```js\nconst answer = 42',
      '\n```',
    ];
    let text = '';
    const { container, rerender } = render(<MessageContent text={text} forceMarkdown />);
    const markdownContainer = container.querySelector('.message-markdown');

    chunks.forEach((chunk, index) => {
      text += chunk;
      rerender(<MessageContent text={text} forceMarkdown />);

      expect(container.querySelector('.message-markdown')).toBe(markdownContainer);
      expect(container.querySelector('[data-output-kind]')).toBeNull();
      expect(screen.getByRole('heading', { name: 'Streaming title' })).toBeInTheDocument();
      if (index >= 1) expect(screen.getByText('unordered item')).toBeInTheDocument();
      if (index >= 2) expect(screen.getByText('nested item').closest('li')).toBeInTheDocument();
      if (index >= 3) expect(screen.getByText('ordered item').closest('ol')).toBeInTheDocument();
      if (index >= 4) {
        expect(container.querySelector('strong')).toHaveTextContent('bold');
        expect(screen.getByRole('link', { name: 'link' })).toHaveAttribute('href', 'https://example.test/');
        expect(container.querySelector('p code')).toHaveTextContent('inline');
      }
      if (index >= 5) expect(container.querySelector('blockquote')).toHaveTextContent('quoted text');
      if (index >= 6) expect(container.querySelector('pre code')).toHaveTextContent('const answer = 42');
    });
  });

  it('parses a plain streamed chunk through the shared GFM renderer', () => {
    render(<MessageContent text="See https://example.test while streaming" forceMarkdown />);

    expect(screen.getByRole('link', { name: 'https://example.test' })).toHaveAttribute('href', 'https://example.test/');
  });

  it('routes local markdown file links to direct open actions', () => {
    const onOpenPath = vi.fn();

    render(
      <MessageContent
        text="[chat](frontend-app/src/pages/chat/) [main](file:///C:/repo/app/src/main.go#L12)"
        actions={{ onOpenPath }}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /chat/ }));
    fireEvent.click(screen.getByRole('button', { name: /main/ }));

    expect(onOpenPath).toHaveBeenNthCalledWith(1, expect.objectContaining({
      column: 0,
      line: 1,
      path: 'frontend-app/src/pages/chat/',
      raw: 'chat',
    }));
    expect(onOpenPath).toHaveBeenNthCalledWith(2, expect.objectContaining({
      column: 0,
      line: 12,
      path: 'C:/repo/app/src/main.go',
      raw: 'main',
    }));
  });

  it('keeps malformed directive citations visible without opening missing files', () => {
    const onFileRef = vi.fn();

    render(
      <MessageContent
        text={'Broken citation: :codex-file-citation[]{line_range_start="7"}'}
        actions={{ onFileRef }}
      />,
    );

    expect(screen.getByText(/Broken citation:/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /line 7/ })).not.toBeInTheDocument();
    expect(onFileRef).not.toHaveBeenCalled();
  });

  it('renders invalid fenced JSON snippets as visible parse errors', () => {
    const { container } = render(
      <MessageContent text={'```json\n{"ok": true,\n```'} />,
    );

    const output = container.querySelector('[data-output-kind="json-error"]');
    expect(output).not.toBeNull();
    expect(output).toHaveTextContent('Invalid JSON:');
    expect(output).toHaveTextContent('{"ok": true,');
  });

  it('renders code preview markdown without the message wrapper', () => {
    const { container } = render(
      <div className="message-markdown">
        <CodePreviewMarkdown content={'## Preview\n\n`ok`'} />
      </div>
    );

    expect(screen.getByRole('heading', { name: 'Preview' })).toBeInTheDocument();
    expect(container.querySelector('.message-markdown code')).toHaveTextContent('ok');
    expect(container.querySelector('.message-markdown .message-markdown')).toBeNull();
  });

  it('does not treat snake_case identifiers or file paths as emphasis', () => {
    const { container } = render(
      <MessageContent
        text="工具包括 shared_file 和 video，入口在 internal/module/thread/start_session.go，正常 _强调_ 仍应渲染。"
      />
    );

    expect(screen.getByText(/shared_file/)).toBeInTheDocument();
    expect(screen.getByText(/internal\/module\/thread\/start_session\.go/)).toBeInTheDocument();
    expect(container.querySelectorAll('.message-markdown em')).toHaveLength(1);
    expect(container.querySelector('.message-markdown em')).toHaveTextContent('强调');
    expect(container.querySelector('.message-markdown strong')).toBeNull();
  });

  it('renders nested markdown lists using markdown indentation', () => {
    const { container } = render(
      <MessageContent text={'- parent\n    - child'} />
    );

    const topLevelItems = container.querySelectorAll('.message-markdown > ul > li');
    expect(topLevelItems).toHaveLength(1);
    expect(topLevelItems[0]).toHaveTextContent('parent');
    expect(topLevelItems[0].querySelector('ul > li')).toHaveTextContent('child');
  });

  it('renders indented assistant code after prose as a code block', () => {
    const { container } = render(
      <MessageContent text={'缩进代码：\n    pnpm install\n    pnpm test'} />
    );

    expect(screen.getByText('缩进代码：')).toBeInTheDocument();
    const codeBlock = container.querySelector('.message-markdown pre code');
    expect(codeBlock).toHaveTextContent('pnpm install');
    expect(codeBlock).toHaveTextContent('pnpm test');
  });

  it('normalizes malformed inline code fences before markdown rendering', () => {
    const { container } = render(
      <MessageContent
        text={[
          '下面是当前仓库结构： ```textSuper-Dolphin/',
          '├── cmd/#可执行入口',
          '├── frontend-app/#当前前端',
          '└── README.md',
        ].join('\n')}
      />
    );

    expect(screen.getByText('下面是当前仓库结构：')).toBeInTheDocument();
    const codeBlock = container.querySelector('.message-markdown pre code');
    expect(codeBlock).toHaveTextContent('Super-Dolphin/');
    expect(codeBlock).toHaveTextContent('frontend-app/#当前前端');
    expect(codeBlock).not.toHaveTextContent('```');
  });

  it('renders unfenced terminal transcripts as code blocks', () => {
    const { container } = render(
      <MessageContent
        text={[
          '执行结果：',
          '$ npm test',
          '> super-dolphin-frontend-app@0.1.0 test',
          '> vitest run',
          'PASS src/App.test.jsx',
        ].join('\n')}
      />
    );

    expect(screen.getByText('执行结果：')).toBeInTheDocument();
    const codeBlock = container.querySelector('.message-markdown pre code');
    expect(codeBlock).toHaveTextContent('$ npm test');
    expect(codeBlock).toHaveTextContent('> vitest run');
    expect(container.querySelector('.message-markdown blockquote')).toBeNull();
  });

  it('does not open local file links without scoped actions', () => {
    render(<CodePreviewMarkdown content="[secret](file:///C:/outside/secret.txt)" />);

    expect(screen.queryByRole('link', { name: 'secret' })).toBeNull();
    expect(screen.getByText('secret')).toBeInTheDocument();
  });

  it('blocks unsafe markdown image URLs before preview rendering', () => {
    render(
      <MessageContent
        text={[
          '![js](javascript:alert(1))',
          '![html](data:text/html,%3Cscript%3Ealert(1)%3C/script%3E)',
          '![svg](data:image/svg+xml,%3Csvg%20onload=alert(1)%3E%3C/svg%3E)',
        ].join('\n\n')}
      />,
    );

    expect(screen.queryByRole('img', { name: 'js' })).not.toBeInTheDocument();
    expect(screen.queryByRole('img', { name: 'html' })).not.toBeInTheDocument();
    expect(screen.queryByRole('img', { name: 'svg' })).not.toBeInTheDocument();
  });

  it('opens and closes local image previews in a lightbox', async () => {
    render(<MarkdownImagePreview src="data:image/png;base64,AA==" label="sample.png" />);

    fireEvent.click(screen.getByRole('button', { name: /sample\.png/ }));

    const dialog = screen.getByRole('dialog', { name: /sample\.png/ });
    expect(dialog).toBeInTheDocument();
    await waitFor(() => expect(dialog).toContainElement(document.activeElement));

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(screen.queryByRole('dialog', { name: /sample\.png/ })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /sample\.png/ }));

    expect(screen.getByRole('dialog', { name: /sample\.png/ })).toBeInTheDocument();
    const overlay = document.body.querySelector('.image-lightbox');
    expect(overlay).not.toBeNull();
    fireEvent.mouseDown(overlay);
    fireEvent.mouseUp(overlay);

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: /sample\.png/ })).not.toBeInTheDocument();
    });
  });

  it('lets the shared lightbox handle Escape through React Aria', async () => {
    const onClose = vi.fn();

    render(
      <ImageLightbox label="sample.png" onClose={onClose}>
        <img src="data:image/png;base64,AA==" alt="sample.png" />
      </ImageLightbox>,
    );

    const dialog = screen.getByRole('dialog', { name: /sample\.png/ });
    await waitFor(() => expect(dialog).toContainElement(document.activeElement));

    fireEvent.keyDown(dialog, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('opens bare generated image path previews from markdown paragraphs', () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_lightbox.png';

    render(<MessageContent text={`图片已生成：${imagePath}`} />);

    fireEvent.click(screen.getByRole('button', { name: '放大图片 ig_lightbox.png' }));

    expect(screen.getByRole('dialog', { name: '图片预览：ig_lightbox.png' })).toBeInTheDocument();
  });

  it('shows fallback for bare generated image path load errors', () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_missing.png';
    render(<MessageContent text={`图片文件路径：\`${imagePath}\``} />);

    fireEvent.error(screen.getByRole('img', { name: 'ig_missing.png' }));

    expect(screen.getByRole('note')).toHaveTextContent('图片无法加载');
    expect(screen.getByRole('note')).toHaveTextContent('ig_missing.png');
  });
});
