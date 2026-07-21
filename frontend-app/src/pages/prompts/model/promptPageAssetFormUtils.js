import {
  rawTextValue,
  serializeJsonForEditor,
  textValue,
} from "./promptPageTextUtils.js";

export function promptFormFromItem(item) {
  return {
    id: textValue(item.id),
    name: textValue(item.name),
    description: textValue(item.description),
    whenToUse: textValue(item.whenToUse),
    content: rawTextValue(item.content),
    originalContent: rawTextValue(item.content),
    agentType: item.agentType,
    tagsText: (Array.isArray(item.tags) ? item.tags : []).join(", "),
    scope: item.scope === "global" ? "global" : "project",
    enabled: item.enabled !== false,
    priority: Number.isFinite(Number(item.priority))
      ? Number(item.priority)
      : 0,
    hasPriority: item.priority !== undefined,
    matchWhenText: serializeJsonForEditor(item.matchWhen),
    hasMatchWhen: item.matchWhen !== undefined,
  };
}

export function emptyPromptForm() {
  return {
    id: "",
    name: "",
    description: "",
    whenToUse: "",
    content: "",
    originalContent: "",
    agentType: "main",
    tagsText: "",
    scope: "project",
    enabled: true,
    priority: 0,
    hasPriority: true,
    matchWhenText: "",
    hasMatchWhen: false,
  };
}
