import { stripLinkHash } from "./SkillMarkdownPreviewModel.js";
import {
  lowerTrimmedText,
  requiredText,
  trimmedText,
} from "../dashboard/skillsDashboardModel.js";

function normalizeSkillCitationPath(path) {
  return stripLinkHash(path)
    .replace(/\\/g, "/")
    .replace(/\/+/g, "/")
    .replace(/\/+$/g, "")
    .toLowerCase();
}
function compactSkillLookupText(value) {
  return Array.from(lowerTrimmedText(value))
    .filter((char) => /[\p{L}\p{N}]/u.test(char))
    .join("");
}
function skillFileForItem(skill) {
  const explicit = trimmedText(skill?.skillFile);
  if (explicit) return explicit;
  const dir = trimmedText(skill?.dir).replace(/[\\/]+$/g, "");
  return dir ? `${dir}/SKILL.md` : "";
}
function skillMatchesCitationPath(skill, path) {
  const citationPath = normalizeSkillCitationPath(path);
  if (!citationPath) return false;
  const skillFile = normalizeSkillCitationPath(skillFileForItem(skill));
  const skillDir = normalizeSkillCitationPath(skill?.dir);
  return (
    citationPath === skillFile ||
    (skillDir && citationPath === `${skillDir}/skill.md`)
  );
}
function skillMatchesCitationName(skill, citation) {
  const needles = [citation.skillName, citation.raw, citation.skillId]
    .map(compactSkillLookupText)
    .filter(Boolean);
  if (needles.length === 0) return false;
  const haystack = [skill?.name, skill?.title, skill?.displayName]
    .map(compactSkillLookupText)
    .filter(Boolean);
  return needles.some((needle) => haystack.includes(needle));
}
function findSkillForCitation(skills, citation) {
  const items = Array.isArray(skills) ? skills : [];
  if (citation.path) {
    const byPath = items.find((skill) =>
      skillMatchesCitationPath(skill, citation.path),
    );
    if (byPath) return byPath;
  }
  return (
    items.find((skill) => skillMatchesCitationName(skill, citation)) || null
  );
}
function emptySkillForm() {
  return {
    name: "",
    displayName: "",
    description: "",
    keywords: "",
    body: "",
    scope: "project",
    personalType: "",
  };
}
function normalizeSkillFileList(response) {
  if (
    !response ||
    typeof response !== "object" ||
    !Array.isArray(response.files)
  ) {
    throw new Error("skills/local/files response.files must be an array");
  }
  const files = [];
  for (const file of response.files) {
    const normalized = {
      name: requiredText(file?.name, "name", "skills/local/files item"),
      path: requiredText(file?.path, "path", "skills/local/files item"),
      isMain: Boolean(file?.is_main),
    };
    files.push(normalized);
  }
  return files;
}
function isMainSkillFile(path) {
  return /(^|[\\/])SKILL\.md$/i.test(trimmedText(path));
}

export {
  normalizeSkillCitationPath,
  compactSkillLookupText,
  skillFileForItem,
  skillMatchesCitationPath,
  skillMatchesCitationName,
  findSkillForCitation,
  emptySkillForm,
  normalizeSkillFileList,
  isMainSkillFile,
};
