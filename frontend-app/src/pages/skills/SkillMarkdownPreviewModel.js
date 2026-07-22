function textFromValue(value) {
  if (value === null || value === undefined) return '';
  return value.toString();
}

function trimmedText(value) {
  return textFromValue(value).trim();
}

function optionalArray(value) {
  return Array.isArray(value) ? value : [];
}

function normalizeSkillPreviewPathKey(path) {
  return trimmedText(path).replace(/\\/g, '/').replace(/\/+/g, '/').toLowerCase();
}

export function skillPreviewDir(path) {
  const clean = trimmedText(path).replace(/\\/g, '/').replace(/\/+$/g, '');
  const index = clean.lastIndexOf('/');
  return index > 0 ? clean.slice(0, index) : '';
}

export function stripLinkHash(path) {
  return trimmedText(path).replace(/[#?].*$/, '');
}

export function skillCitationFromLink(target, label = '') {
  const rawTarget = trimmedText(target);
  if (!rawTarget) return null;
  const appMatch = /^app:\/\/([^/?#]+)$/i.exec(rawTarget);
  if (appMatch) return { kind: 'skill', skillId: appMatch[1], skillName: label, path: '', raw: label || rawTarget };
  const conversationMatch = /^agent:\/\/([^/?#]+)$/i.exec(rawTarget);
  if (conversationMatch) return { kind: 'conversation', conversationId: conversationMatch[1], raw: label || rawTarget };
  const cleanPath = stripLinkHash(rawTarget);
  if (/(^|[\\/])SKILL\.md$/i.test(cleanPath)) return { kind: 'skill', skillId: '', skillName: label, path: cleanPath, raw: label || rawTarget };
  return null;
}

export function resolveSkillPreviewFile(path, files, activeSkillPath) {
  const target = trimmedText(path);
  if (!target) return null;
  const candidates = new Set([normalizeSkillPreviewPathKey(target), normalizeSkillPreviewPathKey(target.replace(/^\.\//, ''))]);
  if (!target.startsWith('/')) {
    const activeDir = skillPreviewDir(activeSkillPath);
    if (activeDir) candidates.add(normalizeSkillPreviewPathKey(`${activeDir}/${target.replace(/^\.\//, '')}`));
  }
  return optionalArray(files).find((file) => candidates.has(normalizeSkillPreviewPathKey(file?.path))) || null;
}
