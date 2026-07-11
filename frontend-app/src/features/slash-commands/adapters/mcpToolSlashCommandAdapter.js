import { normalizeSlashCommandItem } from '../model/slashCommandModel.js';

function requiredText(raw, field, source) {
  if (typeof raw[field] !== 'string' || !raw[field].trim()) {
    throw new TypeError(`${source} ${field} must be a non-empty string`);
  }
  return raw[field].trim();
}

function text(raw, field, source) {
  if (typeof raw[field] !== 'string') {
    throw new TypeError(`${source} ${field} must be a string`);
  }
  return raw[field].trim();
}

function adaptMCPToolCommand(raw, index) {
  const source = `slash command MCP tool item ${index}`;
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new TypeError(`${source} must be an object`);
  }
  const serverName = requiredText(raw, 'serverName', source);
  const toolName = requiredText(raw, 'toolName', source);
  const displayName = requiredText(raw, 'displayName', source);
  const description = text(raw, 'description', source);
  const disabledReason = text(raw, 'disabledReason', source);
  if (typeof raw.enabled !== 'boolean') {
    throw new TypeError(`${source} enabled must be a boolean`);
  }
  const key = `mcp_tool:${serverName}:${toolName}`;

  return normalizeSlashCommandItem({
    id: key,
    kind: 'mcp_tool',
    name: toolName,
    label: displayName,
    description,
    keywords: [serverName],
    payload: {
      capability: {
        kind: 'mcp_tool',
        key,
        name: toolName,
        label: displayName,
        serverName,
      },
    },
    disabled: !raw.enabled,
    disabledReason,
  });
}

export function adaptMCPToolCommands(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new TypeError('slash command MCP tools response must be an object');
  }
  if (!Array.isArray(response.tools)) {
    throw new TypeError('slash command MCP tools response tools must be an array');
  }
  return response.tools.map(adaptMCPToolCommand);
}
