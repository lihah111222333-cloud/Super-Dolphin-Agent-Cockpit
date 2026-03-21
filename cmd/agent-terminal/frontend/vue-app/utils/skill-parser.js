// @ts-nocheck

export function normalizeWordList(text) {
  const raw = (text || '').toString().trim();
  if (!raw) return [];
  const normalized = raw
    .replace(/[，、；;\n]/g, ',')
    .split(',')
    .map((item) => cleanScalar(item))
    .filter(Boolean);
  const dedup = [];
  const seen = new Set();
  for (const word of normalized) {
    const key = word.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    dedup.push(word);
  }
  return dedup;
}

export function listToText(words) {
  if (!Array.isArray(words) || words.length === 0) return '';
  return words.join(', ');
}

export function inferSkillNameFromPath(path) {
  const normalized = (path || '').toString().trim().replace(/[\\/]+$/g, '');
  if (!normalized) return '';
  const parts = normalized.split(/[\\/]/).filter(Boolean);
  if (parts.length === 0) return '';
  return parts[parts.length - 1].trim();
}

export function summarizeItems(items, limit = 3) {
  if (!Array.isArray(items) || items.length === 0) return '';
  const visible = items.slice(0, limit);
  const remaining = items.length - visible.length;
  if (remaining <= 0) return visible.join(', ');
  return `${visible.join(', ')} 等 ${items.length} 项`;
}

export function normalizePathKey(path) {
  return (path || '').toString().trim().replace(/\\/g, '/').toLowerCase();
}

export function fileNameFromPath(path) {
  const normalized = (path || '').toString().trim().replace(/[\\/]+$/g, '');
  if (!normalized) return '';
  const parts = normalized.split(/[\\/]/).filter(Boolean);
  if (parts.length === 0) return '';
  return parts[parts.length - 1];
}

export function skillDirFromFilePath(path) {
  const normalized = (path || '').toString().trim().replace(/[\\/]+$/g, '');
  if (!normalized) return '';
  return normalized.replace(/[\\/][^\\/]+$/, '');
}

export function isSkillMainFilePath(path) {
  return /(^|[\\/])SKILL\.md$/i.test((path || '').toString().trim());
}

export function parseFrontmatter(content) {
  const text = (content || '').replace(/\r\n/g, '\n');
  if (!text.startsWith('---\n')) {
    return { attrs: {}, body: text };
  }
  const rest = text.slice(4);
  const end = rest.indexOf('\n---');
  if (end < 0) {
    return { attrs: {}, body: text };
  }
  const header = rest.slice(0, end);
  const body = rest.slice(end + 4).replace(/^\n/, '');
  const lines = header.split('\n');
  const attrs = {};
  for (let i = 0; i < lines.length; i += 1) {
    const line = (lines[i] || '').trim();
    if (!line || line.startsWith('#')) continue;
    const idx = line.indexOf(':');
    if (idx <= 0) continue;
    const key = line.slice(0, idx).trim().toLowerCase().replace(/-/g, '_');
    const value = line.slice(idx + 1).trim();
    if (value) {
      attrs[key] = value;
      continue;
    }
    const list = [];
    let consumed = 0;
    for (let j = i + 1; j < lines.length; j += 1) {
      const listLine = (lines[j] || '').trim();
      if (!listLine) {
        consumed += 1;
        continue;
      }
      if (!listLine.startsWith('-')) break;
      consumed += 1;
      list.push(listLine.slice(1).trim());
    }
    if (list.length > 0) {
      attrs[key] = list;
      i += consumed;
    }
  }
  return { attrs, body };
}

export function parseWordsValue(value) {
  const source = Array.isArray(value)
    ? value.join(',')
    : (value || '').toString().trim();
  if (!source) return [];
  const bracketText = source.startsWith('[') && source.endsWith(']')
    ? source.slice(1, -1)
    : source;
  let words = normalizeWordList(bracketText);
  if (words.length === 1 && /\s+[@#]/.test(words[0])) {
    words = normalizeWordList(words[0].replace(/\s+(?=[@#])/g, ','));
  }
  return words;
}

export function cleanScalar(value) {
  return (value || '').toString().trim().replace(/^['"]|['"]$/g, '').trim();
}

export function parseSkillMarkdown(content, fallbackName = '') {
  const { attrs, body } = parseFrontmatter(content);
  const name = cleanScalar(attrs.name) || fallbackName;
  const description = cleanScalar(attrs.description);
  const summary = cleanScalar(attrs.summary ?? attrs.digest ?? '');
  const aliasWords = parseWordsValue(
    attrs.aliases ?? attrs.alias ?? attrs.tags ?? attrs.tag ?? attrs.keywords ?? attrs.keyword ?? '',
  );
  const triggerWords = normalizeWordList([
    ...parseWordsValue(
      attrs.trigger_words ?? attrs.triggerwords ?? attrs.trigger_words_list ?? attrs.triggers ?? '',
    ),
    ...aliasWords,
  ].join(','));
  const forceWords = normalizeWordList([
    ...parseWordsValue(
      attrs.force_words ?? attrs.forcewords ?? attrs.mandatory_words ?? attrs.must_words ?? '',
    ),
    ...aliasWords,
  ].join(','));
  return {
    name,
    description,
    summary,
    triggerWords,
    forceWords,
    body: body || '',
  };
}

export function quoteYAML(value) {
  return `"${(value || '').replace(/"/g, '\\"')}"`;
}

export function buildSkillMarkdown(form) {
  const name = (form.name || '').trim();
  const description = (form.description || '').trim();
  const summary = (form.summary || '').trim();
  const triggerWords = normalizeWordList(form.triggerWordsText);
  const forceWords = normalizeWordList(form.forceWordsText);
  const body = (form.body || '').toString().trim();
  const lines = ['---', `name: ${quoteYAML(name)}`];
  if (description) lines.push(`description: ${quoteYAML(description)}`);
  if (summary) lines.push(`summary: ${quoteYAML(summary)}`);
  if (triggerWords.length > 0) {
    lines.push(`trigger_words: [${triggerWords.map(quoteYAML).join(', ')}]`);
  }
  if (forceWords.length > 0) {
    lines.push(`force_words: [${forceWords.map(quoteYAML).join(', ')}]`);
  }
  lines.push('---', '', body || '## 说明\n\n请补充技能规则。');
  return lines.join('\n');
}
