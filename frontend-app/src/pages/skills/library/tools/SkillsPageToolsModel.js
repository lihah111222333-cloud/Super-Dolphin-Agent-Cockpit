import { cleanScalar } from "../../../shared/pageShared.js";

function skillToolsQueryKey(cwd) {
  return ["skillTools", cwd];
}
function normalizeSkillTool(raw, index = 0) {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    throw new Error(`skill tool ${index} must be an object`);
  }
  const id = Number(raw.id);
  if (!Number.isInteger(id) || id <= 0) {
    throw new Error(`skill tool ${index} is missing id`);
  }
  const methodName = cleanScalar(raw.methodName ?? raw.method_name ?? raw.name);
  if (!methodName) {
    throw new Error(`skill tool ${index} is missing methodName`);
  }
  return {
    id,
    methodName,
    name: cleanScalar(raw.name) || methodName,
    description: cleanScalar(raw.description),
    command: cleanScalar(raw.command),
    args: Array.isArray(raw.args)
      ? raw.args.flatMap((arg) => {
          const value = cleanScalar(arg);
          return value ? [value] : [];
        })
      : [],
    enabled: raw.enabled !== false,
  };
}
function normalizeSkillToolsResponse(response) {
  if (!response || typeof response !== "object" || Array.isArray(response)) {
    throw new Error("skills/tools/list response must be an object");
  }
  if (!Array.isArray(response.tools)) {
    throw new Error("skills/tools/list response.tools must be an array");
  }
  return response.tools.map(normalizeSkillTool);
}

export { skillToolsQueryKey, normalizeSkillTool, normalizeSkillToolsResponse };
