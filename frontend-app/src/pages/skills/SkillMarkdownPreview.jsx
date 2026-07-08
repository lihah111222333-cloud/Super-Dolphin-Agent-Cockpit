import { defaultUrlTransform } from 'react-markdown';
import { skillCitationFromLink } from './SkillMarkdownPreviewModel.js';

function textFromValue(value) {
  if (value === null || value === undefined) return '';
  return value.toString();
}

function trimmedText(value) {
  return textFromValue(value).trim();
}

function skillPreviewUrlProtocol(value) {
  if (/^[A-Za-z]:[\\/]/.test(value)) return '';
  const text = trimmedText(value);
  const colon = text.indexOf(':');
  if (colon <= 0) return '';
  const beforeSpecial = text.search(/[/?#]/);
  if (beforeSpecial >= 0 && beforeSpecial < colon) return '';
  const protocol = text.slice(0, colon).toLowerCase();
  return /^[a-z][a-z0-9+.-]*$/.test(protocol) ? `${protocol}:` : '';
}

function safeSkillPreviewButtonTarget(target, label = '') {
  const value = trimmedText(target);
  if (!value) return '';
  if (skillCitationFromLink(value, label)) return value;
  if (/^[A-Za-z]:[\\/]/.test(value)) return value;
  if (skillPreviewUrlProtocol(value)) return '';
  return defaultUrlTransform(value) ? value : '';
}

function safeSkillPreviewExternalHref(target) {
  const value = trimmedText(target);
  if (!value) return '';
  const transformed = defaultUrlTransform(value);
  if (!transformed) return '';
  const protocol = skillPreviewUrlProtocol(transformed);
  return protocol === 'http:' || protocol === 'https:' || protocol === 'mailto:' ? transformed : '';
}

function skillPreviewLinkClass(target) {
  const meta = skillCitationFromLink(target);
  if (!meta) return 'skills-preview-link';
  const suffix = meta.kind === 'conversation' ? ' chat-md-conversation-chip' : ' chat-md-skill-chip';
  return `skills-preview-link chat-md-citation${suffix}`;
}

function SkillMarkdownInline({ text, onOpenPath, keyPrefix }) {
  const source = textFromValue(text);
  const parts = [];
  const linkPattern = /\[([^\]]+)\]\(([^)]+)\)/g;
  let lastIndex = 0;
  let match;
  while ((match = linkPattern.exec(source)) !== null) {
    const [raw, label, target] = match;
    const imageStart = match.index > 0 && source[match.index - 1] === '!';
    if (imageStart) {
      if (match.index - 1 > lastIndex) parts.push(source.slice(lastIndex, match.index - 1));
      parts.push(`!${raw}`);
      lastIndex = match.index + raw.length;
      continue;
    }
    if (match.index > lastIndex) parts.push(source.slice(lastIndex, match.index));
    const cleanTarget = safeSkillPreviewButtonTarget(target, label);
    const externalHref = cleanTarget ? '' : safeSkillPreviewExternalHref(target);
    parts.push(cleanTarget && typeof onOpenPath === 'function'
      ? <button className={skillPreviewLinkClass(cleanTarget)} key={`${keyPrefix}-link-${match.index}`} type="button" onClick={() => onOpenPath(cleanTarget, label)}>{label}</button>
      : externalHref
        ? <a className="skills-preview-link" href={externalHref} key={`${keyPrefix}-link-${match.index}`} rel="noreferrer" target="_blank">{label}</a>
        : raw);
    lastIndex = match.index + raw.length;
  }
  if (lastIndex < source.length) parts.push(source.slice(lastIndex));
  return <>{parts.length > 0 ? parts : source}</>;
}

function renderSkillMarkdownList(block, onOpenPath) {
  return <ul key={block.key}>{block.items.map((item) => renderSkillMarkdownListItem(item, onOpenPath))}</ul>;
}

function renderSkillMarkdownListItem(item, onOpenPath) {
  return <li key={item.key}><SkillMarkdownInline text={item.text} onOpenPath={onOpenPath} keyPrefix={item.key} /></li>;
}

export function SkillMarkdownPreview({ content, onOpenPath }) {
  const text = trimmedText(content);
  if (!text) return <p>暂无内容，点击“编辑正文”开始编写。</p>;
  const blocks = [];
  let paragraph = [];
  let paragraphStartLine = 0;
  let list = [];
  let listStartLine = 0;
  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    const paragraphText = paragraph.join(' ');
    blocks.push({ type: 'p', key: `p-${paragraphStartLine}-${paragraphText}`, text: paragraphText });
    paragraph = [];
    paragraphStartLine = 0;
  };
  const flushList = () => {
    if (list.length === 0) return;
    blocks.push({ type: 'ul', key: `list-${listStartLine}-${list.map((item) => item.text).join('|')}`, items: list });
    list = [];
    listStartLine = 0;
  };
  for (const [lineIndex, line] of text.split('\n').entries()) {
    const lineNumber = lineIndex + 1;
    const trimmed = line.trim();
    if (!trimmed) { flushParagraph(); flushList(); continue; }
    const heading = /^(#{1,6})\s+(.+)$/.exec(trimmed);
    if (heading) {
      flushParagraph();
      flushList();
      blocks.push({ type: 'heading', key: `heading-${lineNumber}-${heading[2]}`, level: Math.min(heading[1].length, 3), text: heading[2] });
      continue;
    }
    const bullet = /^[-*]\s+(.+)$/.exec(trimmed);
    if (bullet) {
      flushParagraph();
      if (list.length === 0) listStartLine = lineNumber;
      list.push({ key: `item-${lineNumber}-${bullet[1]}`, text: bullet[1] });
      continue;
    }
    if (paragraph.length === 0) paragraphStartLine = lineNumber;
    paragraph.push(trimmed);
  }
  flushParagraph();
  flushList();
  return (
    <>
      {blocks.map((block) => {
        if (block.type === 'heading') return renderSkillMarkdownHeading(block, onOpenPath);
        if (block.type === 'ul') return renderSkillMarkdownList(block, onOpenPath);
        return <p key={block.key}><SkillMarkdownInline text={block.text} onOpenPath={onOpenPath} keyPrefix={block.key} /></p>;
      })}
    </>
  );
}

function renderSkillMarkdownHeading(block, onOpenPath) {
  const Tag = block.level <= 1 ? 'h3' : 'h4';
  return <Tag key={block.key}><SkillMarkdownInline text={block.text} onOpenPath={onOpenPath} keyPrefix={block.key} /></Tag>;
}
