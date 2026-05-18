import { skillNameKey } from './skill-match-utils.js';

export function skillScopeFromTrust(trust) {
  return trust === 'project' ? 'project' : 'personal';
}

export function skillScopeFromSkill(skill) {
  const scope = (skill?.scope || '').toString().trim().toLowerCase();
  if (scope === 'project' || scope === 'personal') return scope;
  return skillScopeFromTrust((skill?.trust || '').toString().trim().toLowerCase());
}

export function skillPersonalType(skill) {
  if (skillScopeFromSkill(skill) !== 'personal') return '';
  return (skill?.personal_type || skill?.personalType || '').toString().trim().toLowerCase() || 'user';
}

export function skillRefFor(skill, source = '') {
  const directName = skill && typeof skill === 'object' ? skill.name : skill;
  const name = (directName || '').toString().trim();
  const nameKey = skillNameKey(name);
  if (!nameKey) return { key: '', name: '', scope: '', personalType: '', path: '' };
  const scope = skillScopeFromSkill(skill);
  const personalType = skillPersonalType(skill);
  const path = (skill?.dir || skill?.skill_file || skill?.path || '').toString().trim();
  const key = [scope || 'unknown', personalType, nameKey, path.toLowerCase()].join(':');
  const ref = { key, name, scope, personalType, path };
  const resolvedSource = (source || skill?.source || '').toString().trim().toLowerCase();
  if (resolvedSource) ref.source = resolvedSource;
  return ref;
}

export function mergeSkillRefs(...lists) {
  const next = [];
  const seen = new Set();
  lists.forEach((list) => {
    if (!Array.isArray(list)) return;
    list.forEach((rawRef) => {
      const refItem = skillRefFor(rawRef);
      if (!refItem.key || seen.has(refItem.key)) return;
      seen.add(refItem.key);
      next.push(refItem);
    });
  });
  return next;
}

function skillRefHasExactIdentity(ref) {
  return Boolean(
    (ref?.key || '').toString().trim()
    || (ref?.scope || '').toString().trim()
    || (ref?.personalType || ref?.personal_type || '').toString().trim()
    || (ref?.path || '').toString().trim(),
  );
}

export function dropSkillNamesCoveredByRefs(rawNames, refs) {
  const names = Array.isArray(rawNames)
    ? rawNames.map((item) => (item || '').toString().trim()).filter(Boolean)
    : [];
  if (names.length === 0 || !Array.isArray(refs) || refs.length === 0) return names;
  const covered = new Set();
  refs.forEach((ref) => {
    if (!skillRefHasExactIdentity(ref)) return;
    const nameKey = skillNameKey(ref?.name);
    if (nameKey) covered.add(nameKey);
  });
  if (covered.size === 0) return names;
  return names.filter((name) => !covered.has(skillNameKey(name)));
}

export function normalizeSkillCatalog(rawSkills) {
  if (!Array.isArray(rawSkills)) return [];
  const next = [];
  const seen = new Set();
  rawSkills.forEach((rawSkill) => {
    const name = (rawSkill?.name || '').toString().trim();
    const scope = (rawSkill?.scope || '').toString().trim().toLowerCase();
    const personalType = (rawSkill?.personal_type || rawSkill?.personalType || '').toString().trim().toLowerCase();
    const ref = skillRefFor({
      name,
      scope,
      personal_type: personalType,
      dir: rawSkill?.dir,
      skill_file: rawSkill?.skill_file,
      path: rawSkill?.path,
      trust: rawSkill?.trust,
    });
    if (!ref.key || seen.has(ref.key)) return;
    seen.add(ref.key);
    next.push({
      name,
      key: ref.key,
      ref,
      summary: (rawSkill?.summary || '').toString().trim(),
      description: (rawSkill?.description || '').toString().trim(),
      trust: (rawSkill?.trust || '').toString().trim().toLowerCase(),
      scope,
      personal_type: personalType,
      dir: (rawSkill?.dir || '').toString().trim(),
    });
  });
  return next;
}

export function catalogRefForName(catalog, rawName, source = '') {
  const nameKey = skillNameKey(rawName);
  if (!nameKey) return { key: '', name: '', scope: '', personalType: '', path: '' };
  const matches = Array.isArray(catalog)
    ? catalog.filter((skill) => skillNameKey(skill?.name) === nameKey)
    : [];
  if (matches.length !== 1) return { key: '', name: '', scope: '', personalType: '', path: '' };
  return skillRefFor(matches[0], source);
}
