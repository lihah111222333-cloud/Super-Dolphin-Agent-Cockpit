import { describe, expect, it } from 'vitest';
import { renderAssistantMarkdown } from './utils/assistant-markdown.js';
import { isCodexInlineLiteral, preprocessCodexMarkdown } from './utils/assistant-markdown-codex.js';

describe('assistant markdown codex enhancements', () => {
  it('strips hidden cites and protects codex inline literals', () => {
    const result = preprocessCodexMarkdown('路径 @src/utils/foo.ts 使用 $HOME \uE200cite\uE202abc\uE201');
    expect(result).toContain('`@src/utils/foo.ts`');
    expect(result).toContain('`$HOME`');
    expect(result).not.toContain('\uE200cite');
    expect(isCodexInlineLiteral('@src/utils/foo.ts')).toBe(true);
    expect(isCodexInlineLiteral('$HOME')).toBe(true);
  });

  it('renders codex inline literals as plain inline code instead of file refs', () => {
    const html = renderAssistantMarkdown('看看 @src/utils/foo.ts 和 $HOME');
    expect(html).toContain('<code class="chat-md-inline-code">@src/utils/foo.ts</code>');
    expect(html).toContain('<code class="chat-md-inline-code">$HOME</code>');
    expect(html).not.toContain('data-file-path="@src/utils/foo.ts"');
  });

  it('decorates callouts and gfm-style tables for chat timeline rendering', () => {
    const html = renderAssistantMarkdown('> [!WARNING]\n> 注意风险\n\n| name | value |\n| :-- | --: |\n| foo | 1 |');
    expect(html).toContain('chat-md-callout chat-md-callout-warning');
    expect(html).toContain('chat-md-callout-title');
    expect(html).toContain('chat-md-table-wrap');
    expect(html).toContain('class="chat-md-table"');
    expect(html).toContain('data-align="right"');
  });

  it('renders github-style task lists with codex task styling', () => {
    const html = renderAssistantMarkdown('- [x] done\n- [ ] todo');
    expect(html).toContain('chat-md-task-item');
    expect(html).toContain('chat-md-task-box is-checked');
    expect(html).toContain('chat-md-task-content">done');
    expect(html).toContain('chat-md-task-content">todo');
  });

  it('renders inline and block math with katex output', () => {
    const html = renderAssistantMarkdown('公式 $x^2 + 1$\n\n$$\\frac{1}{2}$$');
    expect(html).toContain('class="katex"');
    expect(html).toContain('katex-block');
    expect(html).not.toContain('$x^2 + 1$');
  });

  it('merges adjacent ordered and unordered lists into nested list structure', () => {
    const html = renderAssistantMarkdown(['1. Parent:', '- child a', '- child b', '2. Next'].join('\n'));
    expect(html).toContain('<li>Parent:\n<ul>');
    expect(html).toContain('<li>child a</li>');
    expect(html).toContain('<li>Next</li>');
  });

  it('preserves visible backslashes for escaped markdown punctuation', () => {
    const bs = String.fromCharCode(92);
    const html = renderAssistantMarkdown(`Use ${bs}${bs}*literal asterisk${bs}${bs}* and custom:${bs}[name${bs}]`);
    expect(html).toContain('&#92;*literal asterisk&#92;*');
    expect(html).toContain('custom:&#92;[name&#92;]');
  });

  it('renders codex file citations as clickable file refs', () => {
    const html = renderAssistantMarkdown(':codex-file-citation[]{path="internal/app/server.go" line_range_start="12" line_range_end="18"}');
    expect(html).toContain('chat-md-file-citation');
    expect(html).toContain('data-file-path="internal/app/server.go"');
    expect(html).toContain('data-file-line="12"');
    expect(html).toContain('server.go (lines 12-18)');
  });

  it('renders markdown file links as clickable file-link anchors', () => {
    const html = renderAssistantMarkdown('[server.go](internal/app/server.go#L12-L18)');
    expect(html).toContain('chat-md-file-link');
    expect(html).toContain('data-file-path="internal/app/server.go"');
    expect(html).toContain('data-file-line="12"');
    expect(html).toContain('>server.go</a>');
  });

  it('renders skill and conversation links as codex chips', () => {
    const html = renderAssistantMarkdown('[DeploySkill](app://deploy-skill) [thread-active](agent://thread-active) [SkillDoc](docs/skills/deploy/SKILL.md)');
    expect(html).toContain('chat-md-skill-chip');
    expect(html).toContain('data-citation-kind="skill"');
    expect(html).toContain('data-skill-id="deploy-skill"');
    expect(html).toContain('chat-md-conversation-chip');
    expect(html).toContain('data-conversation-id="thread-active"');
    expect(html).toContain('data-skill-path="docs/skills/deploy/SKILL.md"');
  });

  it('renders skill file chips with a derived skill name instead of the raw path', () => {
    const html = renderAssistantMarkdown('[docs/skills/DeploySkill/SKILL.md](docs/skills/DeploySkill/SKILL.md)');
    expect(html).toContain('data-skill-path="docs/skills/DeploySkill/SKILL.md"');
    expect(html).toContain('>DeploySkill</a>');
    expect(html).not.toContain('>docs/skills/DeploySkill/SKILL.md</a>');
  });

  it('preserves an existing friendly skill file chip label', () => {
    const html = renderAssistantMarkdown('[发布助手](docs/skills/DeploySkill/SKILL.md)');
    expect(html).toContain('>发布助手</a>');
    expect(html).not.toContain('>DeploySkill</a>');
  });

  it('renders markdown images as preview cards with click metadata', () => {
    const html = renderAssistantMarkdown('![Preview](https://example.com/shot.png "Open preview")');
    expect(html).toContain('chat-md-image-card');
    expect(html).toContain('data-citation-kind="image"');
    expect(html).toContain('data-image-src="https://example.com/shot.png"');
    expect(html).toContain('chat-md-image-card__img');
  });

  it('renders repo-local markdown images as file-backed preview cards', () => {
    const html = renderAssistantMarkdown('![Diff image](./artifacts/diff.png)');
    expect(html).toContain('chat-md-image-card');
    expect(html).toContain('chat-md-file-link');
    expect(html).toContain('data-file-path="./artifacts/diff.png"');
  });

  it('renders code fences with codex-style code chrome', () => {
    const html = renderAssistantMarkdown('```ts\nconst value = 1;\n```');
    expect(html).toContain('chat-md-code-block');
    expect(html).toContain('chat-md-code-head');
    expect(html).toContain('chat-md-code-lang">ts<');
  });

  it('renders terminal, task, automation, and code-comment directives as actionable codex cards', () => {
    const html = renderAssistantMarkdown([
      ':codex-terminal-citation[Terminal output]{terminal_chunk_id="chunk-1" line_range_start="3" line_range_end="5"}',
      ':task-stub[Review the patch]{title="Review task"}',
      ':automation-update[Workflow rerun completed]{name="Nightly lint" prompt="Run lint on main" mode="suggested-update" rrule="RRULE:FREQ=DAILY" status="ACTIVE"}',
      ':code-comment[Please rename this]{title="Naming" path="src/main.go" line_range_start="9" line_range_end="11" priority="high"}',
    ].join(' '));
    expect(html).toContain('chat-md-terminal-citation');
    expect(html).toContain('chat-md-citation chat-md-task-stub');
    expect(html).toContain('chat-md-citation chat-md-automation-update');
    expect(html).toContain('chat-md-citation chat-md-code-comment');
    expect(html).toContain('data-task-prompt="Review the patch"');
    expect(html).toContain('data-automation-name="Nightly lint"');
    expect(html).toContain('data-automation-prompt="Run lint on main"');
    expect(html).toContain('data-comment-title="Naming"');
    expect(html).toContain('data-file-path="src/main.go"');
  });
});
