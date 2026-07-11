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

function automationContent(raw, source) {
  let configPrompt = '';
  let configCommand = '';
  if (raw.config !== undefined) {
    if (!raw.config || typeof raw.config !== 'object' || Array.isArray(raw.config)) {
      throw new TypeError(`${source} config must be an object`);
    }
    configPrompt = optionalText(raw.config, 'prompt', `${source} config`);
    configCommand = optionalText(raw.config, 'command', `${source} config`);
  }
  const candidates = [
    optionalText(raw, 'prompt', source),
    optionalText(raw, 'command_template', source),
    optionalText(raw, 'commandTemplate', source),
    configPrompt,
    configCommand,
  ];
  for (const candidate of candidates) {
    if (candidate) return candidate;
  }
  return '';
}

function adaptAutomationCommand(raw, index) {
  const source = `slash command automation item ${index}`;
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new TypeError(`${source} must be an object`);
  }
  const id = requiredText(raw, 'id', source);
  const title = requiredText(raw, 'title', source);
  const description = optionalText(raw, 'description', source);
  const content = automationContent(raw, source);

  return normalizeSlashCommandItem({
    id: `automation:${id}`,
    kind: 'automation',
    name: title,
    label: title,
    description,
    keywords: [id],
    payload: { title, content },
    disabled: !content,
    disabledReason: content ? '' : 'Automation has no executable content',
  });
}

export function adaptAutomationCommands(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new TypeError('slash command automations response must be an object');
  }
  if (!Array.isArray(response.dags)) {
    throw new TypeError('slash command automations response dags must be an array');
  }
  return response.dags.map(adaptAutomationCommand);
}
