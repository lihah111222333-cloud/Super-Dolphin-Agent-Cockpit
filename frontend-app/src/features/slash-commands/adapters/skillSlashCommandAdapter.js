import { normalizeSlashCommandItem } from '../model/slashCommandModel.js';
import { validateSkillInfo } from './skillInfoWireContract.js';

function adaptSkillCommand(raw, index) {
  const source = `slash command skill item ${index}`;
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
