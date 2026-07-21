import { z } from "zod";
import {
  cleanScalar,
  textValue,
  withTimeout,
  wordListFromText,
} from "../../shared/pageShared.js";
import { skillsPageService } from "../services/skillsPageService.js";
import { normalizeResolutionResponse } from "./resolution/SkillsPageResolutionLabels.js";
import {
  SKILLS_DASHBOARD_TIMEOUT_MS,
  textFromValue,
  trimmedText,
} from "./dashboard/skillsDashboardModel.js";

const { listSkillResolutions } = skillsPageService;

function normalizeSettingsCwd(value) {
  const cwd = trimmedText(value);
  if (!cwd || cwd === "." || cwd === "未选择项目") {
    throw new Error("settings: cwd is required");
  }
  return cwd;
}

const skillResolutionItemsSchema = z.array(z.unknown());
const skillResolutionResponseSchema = z.union([
  skillResolutionItemsSchema,
  z.object({ items: skillResolutionItemsSchema }).passthrough(),
  z.object({ conflicts: skillResolutionItemsSchema }).passthrough(),
]);
async function fetchSkillResolutionsDashboard(cwd) {
  const response = await withTimeout(
    listSkillResolutions({ cwd }),
    SKILLS_DASHBOARD_TIMEOUT_MS,
    "技能冲突检查超时，请检查技能目录或后端状态。",
  );
  return normalizeResolutionResponse(response);
}
function scopeLabel(scope) {
  return scope === "personal" ? "私人使用" : "项目共享";
}
function normalizeSummarySuggestion(value) {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return textValue(value.description);
  }
  return textValue(value);
}
function parseWordsValue(value) {
  if (Array.isArray(value)) return wordListFromText(value);
  const raw = trimmedText(value);
  if (!raw) return [];
  return wordListFromText(
    raw.startsWith("[") && raw.endsWith("]") ? raw.slice(1, -1) : raw,
  );
}
function parseSkillMarkdown(content, fallbackName = "") {
  const text = textFromValue(content).replace(/\r\n/g, "\n");
  if (!text.startsWith("---\n")) {
    return {
      name: fallbackName,
      displayName: "",
      description: "",
      triggerWords: [],
      body: text,
    };
  }
  const rest = text.slice(4);
  const end = rest.indexOf("\n---");
  if (end < 0)
    return {
      name: fallbackName,
      displayName: "",
      description: "",
      triggerWords: [],
      body: text,
    };
  const attrs = {};
  for (const line of rest.slice(0, end).split("\n")) {
    const idx = line.indexOf(":");
    if (idx <= 0) continue;
    attrs[line.slice(0, idx).trim().toLowerCase().replace(/-/g, "_")] = line
      .slice(idx + 1)
      .trim();
  }
  return {
    name: cleanScalar(attrs.name) || fallbackName,
    displayName: cleanScalar(
      attrs.display_name || attrs.displayname || attrs.title,
    ),
    description: cleanScalar(
      attrs.description || attrs.summary || attrs.digest,
    ),
    triggerWords: wordListFromText([
      ...parseWordsValue(
        attrs.trigger_words ||
          attrs.triggerwords ||
          attrs.keywords ||
          attrs.tags,
      ),
      ...parseWordsValue(attrs.force_words || attrs.forcewords),
    ]),
    body: rest
      .slice(end + 4)
      .replace(/^\n/, "")
      .trim(),
  };
}
function quoteYAML(value) {
  return `"${textFromValue(value).replace(/"/g, '\\"')}"`;
}
function skillNameFromDisplayName(value) {
  const text = trimmedText(value);
  let slug = "";
  let lastDash = false;
  for (const char of Array.from(text)) {
    if (/[\p{L}\p{N}_-]/u.test(char)) {
      slug += char;
      lastDash = false;
    } else if (!lastDash) {
      slug += "-";
      lastDash = true;
    }
  }
  return slug.replace(/^-+|-+$/g, "");
}
function buildSkillMarkdown(form) {
  const name = trimmedText(form.name);
  const displayName = trimmedText(form.displayName);
  const description = trimmedText(form.description);
  const words = wordListFromText(form.keywords);
  const body = trimmedText(form.body);
  const lines = ["---", `name: ${quoteYAML(name)}`];
  if (displayName) lines.push(`display_name: ${quoteYAML(displayName)}`);
  if (description) lines.push(`description: ${quoteYAML(description)}`);
  if (words.length > 0)
    lines.push(`trigger_words: [${words.map(quoteYAML).join(", ")}]`);
  lines.push("---", "", body ? body : "## 说明\n\n请补充技能规则。");
  return lines.join("\n");
}

export {
  normalizeSettingsCwd,
  skillResolutionItemsSchema,
  skillResolutionResponseSchema,
  fetchSkillResolutionsDashboard,
  scopeLabel,
  normalizeSummarySuggestion,
  parseWordsValue,
  parseSkillMarkdown,
  quoteYAML,
  skillNameFromDisplayName,
  buildSkillMarkdown,
};
