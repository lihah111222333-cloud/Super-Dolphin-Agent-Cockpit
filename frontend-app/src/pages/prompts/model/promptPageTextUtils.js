import {
  firstPresentText,
  parseStrictJsonValue,
  rawTextValue,
} from "../../shared/pageShared.js";

export { firstPresentText, rawTextValue };

export function textValue(value) {
  return value === null || value === undefined ? "" : value.toString().trim();
}
export function textValues(values) {
  if (!Array.isArray(values)) return [];
  const result = [];
  for (const value of values) {
    const text = textValue(value);
    if (text) result.push(text);
  }
  return result;
}
export function optionalPromptCwd(value) {
  const cwd = textValue(value);
  return cwd && cwd !== "." && cwd !== "未选择项目" ? cwd : "";
}
export function firstText(...values) {
  for (const value of values) {
    const text = textValue(value);
    if (text) return text;
  }
  return "";
}
export function serializeJsonForEditor(value) {
  if (value === undefined || value === null || value === "") return "";
  if (typeof value === "string") return value;
  const serialized = JSON.stringify(value, null, 2);
  if (typeof serialized !== "string") {
    throw new TypeError("提示词匹配条件必须是可序列化的 JSON 值");
  }
  return serialized;
}
export function parseJsonObjectForEditor(value, label) {
  const text = textValue(value);
  if (!text) return { value: null, error: "" };
  try {
    const parsed = parseStrictJsonValue(text, label);
    if (parsed === null) return { value: null, error: "" };
    if (typeof parsed !== "object" || Array.isArray(parsed)) {
      return { value: undefined, error: `${label}必须是 JSON 对象` };
    }
    return { value: parsed, error: "" };
  } catch (err) {
    return {
      value: undefined,
      error: `${label}不是合法 JSON：${errorMessage(err)}`,
    };
  }
}
export function wordListFromText(value) {
  const result = [];
  const seen = new Set();
  for (const word of textValue(value).split(/[，,;；\n]/)) {
    const text = textValue(word);
    const key = text.toLowerCase();
    if (!text || seen.has(key)) continue;
    seen.add(key);
    result.push(text);
  }
  return result;
}
export function errorMessage(error) {
  if (!error) return "";
  return error?.message || String(error);
}
export async function withTimeout(promise, timeoutMs, message) {
  let timeoutID;
  const timeout = new Promise((_, reject) => {
    timeoutID = globalThis.setTimeout(
      () => reject(new Error(message)),
      timeoutMs,
    );
  });
  try {
    return await Promise.race([promise, timeout]);
  } finally {
    if (timeoutID) globalThis.clearTimeout(timeoutID);
  }
}
