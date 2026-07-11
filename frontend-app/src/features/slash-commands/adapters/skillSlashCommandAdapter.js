import { normalizeSlashCommandItem } from '../model/slashCommandModel.js';

function requiredText(raw, field, source) {
  if (typeof raw[field] !== 'string' || !raw[field].trim()) {
    throw new TypeError(`${source} ${field} must be a non-empty string`);
  }
  return raw[field].trim();
}

function optionalText(raw, field, source) {
  if (raw[field] === undefined) return '';
  if (typeof raw[field] !== 'string') {
    throw new TypeError(`${source} ${field} must be a string`);
  }
  return raw[field].trim();
}

function stringListValue(value, index, field, source) {
  if (typeof value !== 'string') {
    throw new TypeError(`${source} ${field}[${index}] must be a string`);
  }
  return value;
}

function stringList(raw, field, source) {
  if (!Array.isArray(raw[field])) {
    throw new TypeError(`${source} ${field} must be an array`);
  }
  return raw[field].map((value, index) => stringListValue(value, index, field, source));
}

function adaptSkillCommand(raw, index) {
  const source = `slash command skill item ${index}`;
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new TypeError(`${source} must be an object`);
  }
  const name = requiredText(raw, 'name', source);
  const displayName = optionalText(raw, 'display_name', source);
  const dir = requiredText(raw, 'dir', source);
  requiredText(raw, 'skill_file', source);
  const scope = requiredText(raw, 'scope', source);
  if (scope !== 'project' && scope !== 'personal') {
    throw new TypeError(`${source} scope is unsupported: ${scope}`);
  }
  const personalType = optionalText(raw, 'personal_type', source);
  const description = optionalText(raw, 'description', source);
  const summary = optionalText(raw, 'summary', source);
  const label = displayName || name;
  const key = `skill:${scope}:${personalType}:${name}:${dir}`;
  const keywords = [
    ...stringList(raw, 'trigger_words', source),
    ...stringList(raw, 'force_words', source),
  ];
  const ref = { name, scope, personalType, path: dir };
  const capability = {
    kind: 'skill',
    key,
    name,
    label,
    ref,
  };

  return normalizeSlashCommandItem({
    id: key,
    kind: 'skill',
    name,
    label,
    description: description || summary,
    keywords,
    payload: { capability },
    disabled: false,
    disabledReason: '',
  });
}

export function adaptSkillCommands(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new TypeError('slash command skills response must be an object');
  }
  if (!Array.isArray(response.skills)) {
    throw new TypeError('slash command skills response skills must be an array');
  }
  return response.skills.map(adaptSkillCommand);
}
