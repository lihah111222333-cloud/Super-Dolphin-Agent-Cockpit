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

export function validateSkillNameText(value) {
  const text = (value || '').toString().trim();
  if (!text) return '请先填写技能名称';
  const chars = Array.from(text);
  if (!/[\p{L}\p{N}]/u.test(chars[0])) {
    return '技能名称必须以中文、英文或数字开头。';
  }
  if (chars.length > 64) {
    return '技能名称不能超过 64 个字符。';
  }
  if (!/^[\p{L}\p{N}_-]+$/u.test(text)) {
    return '技能名称不能包含非法字符，请使用中文、英文、数字、- 或 _；带空格的展示文本请填写显示名称。';
  }
  return '';
}

export function isInternalSkillReferenceWord(word, skillName = '') {
  const text = (word || '').toString().trim();
  if (/^\[skill:[^\]]+\]$/i.test(text)) return true;
  if (text.startsWith('@')) return true;
  return false;
}

function isInternalSkillMarkerSummary(value) {
  return /^<\/?[A-Z][A-Z0-9_-]*>$/.test((value || '').toString().trim());
}

function skillNameSlug(value) {
  const text = (value || '').toString().trim().toLowerCase();
  let slug = '';
  let lastDash = false;
  for (const char of Array.from(text)) {
    if (/[\p{L}\p{N}]/u.test(char)) {
      slug += char;
      lastDash = false;
    } else if (!lastDash) {
      slug += '-';
      lastDash = true;
    }
  }
  return slug.replace(/^-+|-+$/g, '') || 'skill';
}

function isSafeLegacyDisplayName(value) {
  const text = (value || '').toString().trim();
  if (!text || !text.includes(' ') || Array.from(text).length > 120) return false;
  const chars = Array.from(text);
  if (!/[\p{L}\p{N}]/u.test(chars[0])) return false;
  return chars.every((char) => /[\p{L}\p{N} _-]/u.test(char));
}

function normalizeParsedSkillIdentity(rawName, rawDisplayName, fallbackName = '') {
  const originalName = cleanScalar(rawName);
  const displayName = cleanScalar(rawDisplayName);
  if (originalName && !displayName && validateSkillNameText(originalName) && isSafeLegacyDisplayName(originalName)) {
    return { name: skillNameSlug(originalName), displayName: originalName };
  }
  return { name: originalName || fallbackName, displayName };
}

export function parseSkillMarkdown(content, fallbackName = '') {
  const { attrs, body } = parseFrontmatter(content);
  const { name, displayName } = normalizeParsedSkillIdentity(
    attrs.name,
    attrs.display_name ?? attrs.displayname ?? attrs.title ?? '',
    fallbackName,
  );
  const description = cleanScalar(attrs.description);
  const rawSummary = cleanScalar(attrs.summary ?? attrs.digest ?? '');
  const summary = isInternalSkillMarkerSummary(rawSummary) ? '' : rawSummary;
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
    displayName,
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

function compactTextLength(value) {
  return Array.from((value || '').toString().replace(/\s+/g, '')).length;
}

function containsAny(text, terms) {
  return terms.some((term) => text.includes(term));
}

export function skillDescriptionQualityIssue(description) {
  const text = (description || '').toString().trim();
  if (!text) return 'missing';
  const normalized = text.toLowerCase();
  if (looksLikeWorkflowDescription(text)) return 'workflow';
  if (looksTooGenericDescription(normalized)) return 'generic';
  if (compactTextLength(text) < 12) return 'too_short';
  if (compactTextLength(text) > 120) return 'too_long';
  if (!containsAny(text, ['当你需要', '当你遇到', '当你正在', '当你准备', '需要', '遇到', '正在', '准备'])) {
    return 'missing_scenario';
  }
  return '';
}

function looksTooGenericDescription(text) {
  return containsAny(text, [
    '帮你处理各种问题',
    '帮助处理各种问题',
    '处理各种问题',
    '处理很多事情',
    '处理很多事',
    '做很多事情',
    '做很多事',
    '通用助手',
    '提高效率',
    '什么都可以',
    '各种东西',
  ]);
}

function looksLikeWorkflowDescription(text) {
  if (/先.+(然后|再|最后)/.test(text)) return true;
  if (/读取.+分析.+输出/.test(text)) return true;
  return containsAny(text, ['实现步骤', '执行步骤', '工作流程']);
}

export function skillDescriptionQualitySaveMessage(description) {
  switch (skillDescriptionQualityIssue(description)) {
    case 'missing':
      return '已保存。建议填写简介，更好使用技能。';
    case 'too_short':
      return '已保存。简介偏短，建议写清楚“什么时候使用”。';
    case 'generic':
      return '已保存。简介比较宽泛，建议补充具体场景。';
    case 'workflow':
    case 'missing_scenario':
      return '已保存。建议把简介写成一句话：什么时候该用这个技能。';
    case 'too_long':
      return '已保存。简介偏长，建议压缩成一句清楚的技能简介。';
    default:
      return '已保存';
  }
}

export function buildSkillMarkdown(form) {
  const name = (form.name || '').trim();
  const displayName = (form.displayName || '').trim();
  const description = ((form.description || '').trim() || (form.summary || '').trim());
  const triggerWords = normalizeWordList(`${form.triggerWordsText || ''},${form.forceWordsText || ''},${form.internalScenarioWordsText || ''}`);
  const body = (form.body || '').toString().trim();
  const lines = ['---', `name: ${quoteYAML(name)}`];
  if (displayName) lines.push(`display_name: ${quoteYAML(displayName)}`);
  if (description) lines.push(`description: ${quoteYAML(description)}`);
  if (triggerWords.length > 0) {
    lines.push(`trigger_words: [${triggerWords.map(quoteYAML).join(', ')}]`);
  }
  lines.push('---', '', body || '## 说明\n\n请补充技能规则。');
  return lines.join('\n');
}
