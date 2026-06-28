import { skillNameKey } from './skill-match-utils.js';

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
