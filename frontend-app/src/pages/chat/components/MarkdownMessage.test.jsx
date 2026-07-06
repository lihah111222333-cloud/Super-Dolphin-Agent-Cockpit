import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
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

  it('opens and closes local image previews in a lightbox', () => {
    render(<MarkdownImagePreview src="data:image/png;base64,AA==" label="sample.png" />);

    fireEvent.click(screen.getByRole('button', { name: /sample\.png/ }));

    expect(screen.getByRole('dialog', { name: /sample\.png/ })).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(screen.queryByRole('dialog', { name: /sample\.png/ })).not.toBeInTheDocument();
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
