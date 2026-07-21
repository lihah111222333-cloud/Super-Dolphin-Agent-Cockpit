import {
  PROMPT_DRAFT_NOT_READY_MESSAGE,
  PROMPT_DRAFT_REVIEW_MESSAGE,
} from "./promptPageViewSchemas.js";
import { firstPresentText, textValue } from "./promptPageTextUtils.js";

export function noticeText(error, prefix) {
  const message = firstPresentText(error?.message, error);
  const friendly = promptFriendlyErrorText(message);
  if (friendly) return friendly;
  return `${prefix}，请重试。`;
}
export function promptFriendlyErrorText(message) {
  const lower = textValue(message).toLowerCase();
  if (lower.includes("prompt intent draft is not ready to save")) {
    return PROMPT_DRAFT_NOT_READY_MESSAGE;
  }
  if (lower.includes("prompt intent draft requires risk confirmation")) {
    return PROMPT_DRAFT_REVIEW_MESSAGE;
  }
  return "";
}
