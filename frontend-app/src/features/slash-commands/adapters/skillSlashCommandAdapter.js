import skillInfoFieldRegistry from './skillInfoFieldRegistry.json';
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

function requiredString(raw, field, source) {
  if (typeof raw[field] !== 'string') {
    throw new TypeError(`${source} ${field} must be a string`);
  }
  return raw[field].trim();
}

function optionalStringList(raw, field, source) {
  if (raw[field] === undefined) return [];
  if (!Array.isArray(raw[field])) {
    throw new TypeError(`${source} ${field} must be an array`);
  }
  return raw[field].map((value, index) => {
    if (typeof value !== 'string') {
      throw new TypeError(`${source} ${field}[${index}] must be a string`);
    }
    return value;
  });
}

function scope(raw, field, source) {
  const value = requiredText(raw, field, source);
  if (value !== 'project' && value !== 'personal') {
    throw new TypeError(`${source} scope is unsupported: ${value}`);
  }
  return value;
}

function optionalTrust(raw, field, source) {
  if (raw[field] === undefined) return '';
  const value = requiredText(raw, field, source);
  if (!['user', 'project', 'signed'].includes(value)) {
    throw new TypeError(`${source} trust is unsupported: ${value}`);
  }
  return value;
}

function optionalBoolean(raw, field, source) {
  if (raw[field] === undefined) return false;
  if (typeof raw[field] !== 'boolean') {
    throw new TypeError(`${source} ${field} must be a boolean`);
  }
  return raw[field];
}

function optionalStringListMap(raw, field, source) {
  if (raw[field] === undefined) return {};
  const value = raw[field];
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${source} ${field} must be an object`);
  }
  for (const [key, entries] of Object.entries(value)) {
    if (!key.trim()) {
      throw new TypeError(`${source} ${field} keys must be non-empty strings`);
    }
    optionalStringList({ [field]: entries }, field, source);
  }
  return value;
}

const skillInfoValidators = {
  requiredText,
  optionalText,
  requiredString,
  optionalStringList,
  scope,
  optionalTrust,
  optionalBoolean,
  optionalStringListMap,
};

function validateSkillInfo(raw, source) {
  const registeredFields = new Set(skillInfoFieldRegistry.map(({ field }) => field));
  const unknownFields = Object.keys(raw)
    .filter((field) => !registeredFields.has(field))
    .sort((left, right) => left.localeCompare(right));
  if (unknownFields.length) {
    throw new TypeError(`${source} has unknown fields: ${unknownFields.join(', ')}`);
  }

  const validated = {};
  for (const { field, validator } of skillInfoFieldRegistry) {
    const validate = skillInfoValidators[validator];
    if (!validate) {
      throw new TypeError(`SkillInfo field registry validator is unsupported: ${validator}`);
    }
    validated[field] = validate(raw, field, source);
  }
  if (validated.scope === 'project' && validated.personal_type) {
    throw new TypeError(`${source} project scope must not set personal_type`);
  }
  if (
    validated.scope === 'personal'
    && !['user', 'agent', 'imported'].includes(validated.personal_type)
  ) {
    throw new TypeError(`${source} personal scope requires a supported personal_type`);
  }
  return validated;
}

function adaptSkillCommand(raw, index) {
  const source = `slash command skill item ${index}`;
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new TypeError(`${source} must be an object`);
  }
  const validated = validateSkillInfo(raw, source);
  const name = validated.name;
  const displayName = validated.display_name;
  const dir = validated.dir;
  const scopeValue = validated.scope;
  const personalType = validated.personal_type;
  const description = validated.description;
  const summary = validated.summary;
  const label = displayName || name;
  const key = `skill:${scopeValue}:${personalType}:${name}:${dir}`;
  const keywords = [
    ...validated.trigger_words,
    ...validated.force_words,
  ];
  const ref = { name, scope: scopeValue, personalType, path: dir };
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
