import { normalizeSlashCommandItem } from '../model/slashCommandModel.js';

function requiredText(raw, field, source) {
  if (typeof raw[field] !== 'string' || !raw[field].trim()) {
    throw new TypeError(`${source} ${field} must be a non-empty string`);
  }
  return raw[field].trim();
}

function optionalText(raw, field, source) {
  if (typeof raw[field] !== 'string') {
    throw new TypeError(`${source} ${field} must be a string`);
  }
  return raw[field].trim();
}

function adaptPromptCommand(raw, index) {
  const source = `slash command prompt item ${index}`;
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new TypeError(`${source} must be an object`);
  }
  const id = requiredText(raw, 'id', source);
  const name = requiredText(raw, 'name', source);
  const description = optionalText(raw, 'description', source);
  if (typeof raw.enabled !== 'boolean') {
    throw new TypeError(`${source} enabled must be a boolean`);
  }

  return normalizeSlashCommandItem({
    id: `prompt:${id}`,
    kind: 'prompt',
    name,
    label: name,
    description,
    keywords: [id],
    payload: { promptId: id },
    disabled: !raw.enabled,
    disabledReason: raw.enabled ? '' : 'Prompt is disabled',
  });
}

export function adaptPromptCommands(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new TypeError('slash command prompts response must be an object');
  }
  if (!Array.isArray(response.prompts)) {
    throw new TypeError('slash command prompts response prompts must be an array');
  }
  return response.prompts.map(adaptPromptCommand);
}

export function promptContentFromResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new TypeError('slash command prompt response must be an object');
  }
  const prompt = response.prompt;
  if (!prompt || typeof prompt !== 'object' || Array.isArray(prompt)) {
    throw new TypeError('slash command prompt response prompt must be an object');
  }
  for (const field of ['content', 'prompt_text', 'promptText']) {
    if (prompt[field] === undefined) continue;
    if (typeof prompt[field] !== 'string') {
      throw new TypeError(`slash command prompt response ${field} must be a string`);
    }
    const content = prompt[field].trim();
    if (content) return content;
  }
  throw new TypeError('slash command prompt response content is required');
}
