export function normalizeSkillPreviewMatches(rawMatches) {
  if (!Array.isArray(rawMatches)) return [];
  const deduped = /** @type {any[]} */ ([]);
  const seenNames = /** @type {Set<string>} */ (new Set());
  rawMatches.forEach((raw) => {
    const name = (raw?.name || raw?.skill || '').toString().trim();
    if (!name) return;
    const nameKey = skillNameKey(name);
    if (seenNames.has(nameKey)) return;
    seenNames.add(nameKey);
    const matchedByRaw = (raw?.matched_by || raw?.matchedBy || '').toString().trim().toLowerCase();
    const sourceTermsRaw = Array.isArray(raw?.matched_terms)
      ? raw.matched_terms
      : (Array.isArray(raw?.matchedTerms) ? raw.matchedTerms : []);
    const terms = /** @type {string[]} */ ([]);
    const seenTerms = /** @type {Set<string>} */ (new Set());
    sourceTermsRaw.forEach((rawTerm) => {
      const term = (rawTerm || '').toString().trim();
      if (!term) return;
      const termKey = term.toLowerCase();
      if (seenTerms.has(termKey)) return;
      seenTerms.add(termKey);
      terms.push(term);
    });
    const hasAtForceTerm = terms.some((term) => term.startsWith('@'));
    const matchedBy = (matchedByRaw === 'force' || (matchedByRaw === 'explicit' && hasAtForceTerm))
      ? 'force'
      : normalizeComposerSkillMatchType(matchedByRaw);
    deduped.push({
      name,
      matchedBy,
      matchedTerms: terms,
    });
  });
  return deduped;
}

export function skillNameKey(rawName) {
  return (rawName || '').toString().trim().toLowerCase();
}

export function collectForceMatchedSkillNames(matches) {
  if (!Array.isArray(matches) || matches.length === 0) {
    return [];
  }
  const next = /** @type {string[]} */ ([]);
  const seen = new Set();
  matches.forEach((match) => {
    const type = normalizeComposerSkillMatchType(match?.matchedBy);
    if (type !== 'force') return;
    const name = (match?.name || '').toString().trim();
    const key = skillNameKey(name);
    if (!key || seen.has(key)) return;
    seen.add(key);
    next.push(name);
  });
  return next;
}

export function mergeSkillNameLists(...lists) {
  const next = [];
  const seen = new Set();
  lists.forEach((list) => {
    if (!Array.isArray(list)) return;
    list.forEach((rawName) => {
      const name = (rawName || '').toString().trim();
      const key = skillNameKey(name);
      if (!key || seen.has(key)) return;
      seen.add(key);
      next.push(name);
    });
  });
  return next;
}

export function normalizeComposerSkillMatchType(rawType) {
  const type = (rawType || '').toString().trim().toLowerCase();
  if (type === 'force') return 'force';
  if (type === 'explicit') return 'explicit';
  return 'trigger';
}

export function composerSkillMatchClass(match) {
  return normalizeComposerSkillMatchType(match?.matchedBy);
}

export function composerSkillMatchReason(match) {
  const type = normalizeComposerSkillMatchType(match?.matchedBy);
  const label = type === 'force' ? '强制词' : (type === 'explicit' ? '显式提及' : '触发词');
  const terms = Array.isArray(match?.matchedTerms)
    ? match.matchedTerms.map((term) => (term || '').toString().trim()).filter(Boolean)
    : [];
  if (terms.length === 0) return label;
  return `${label}: ${terms.join(' / ')}`;
}

export function buildSkillPreviewSignature(matches) {
  if (!Array.isArray(matches) || matches.length === 0) return '';
  return matches
    .map((item) => {
      const name = (item?.name || '').toString().trim().toLowerCase();
      const type = (item?.matchedBy || '').toString().trim().toLowerCase();
      const terms = Array.isArray(item?.matchedTerms)
        ? item.matchedTerms.map((term) => (term || '').toString().trim().toLowerCase()).filter(Boolean).join('|')
        : '';
      return `${name}:${type}:${terms}`;
    })
    .join(';');
}
