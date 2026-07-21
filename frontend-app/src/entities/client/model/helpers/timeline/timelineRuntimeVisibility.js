import { approvalIdentityFromFields } from "../../../../../shared/api/support/approvalRequestId.js";
import {
  firstOptionalPresent,
  normalizeString,
  normalizeTimelineKind,
} from "./timelineRuntimeFields.js";

const MESSAGE_LIFECYCLE_ITEM_TYPES = new Set([
  "message",
  "usermessage",
  "user_message",
  "assistantmessage",
  "assistant_message",
]);
const GENERIC_COMMAND_TITLES = new Set([
  "command",
  "execute command",
  "running command",
  "执行命令",
  "命令",
  "终端命令",
]);

function isInjectedPromptTimelineItem(item) {
  if (item?.role !== "user") return false;
  const text = normalizeString(item?.text).trim();
  if (!text) return false;
  if (/^<recommended_plugins(?:\s[^>]*)?>/i.test(text)) return true;
  return (
    /#\s+AGENTS\.md instructions for .+\n/i.test(text) &&
    /<INSTRUCTIONS>[\s\S]*<\/INSTRUCTIONS>/i.test(text)
  );
}

function isVisibleApprovalTimelineItem(item) {
  const identity = approvalIdentityFromFields(item, "timeline approval");
  const status = normalizeString(item?.status).toLowerCase();
  if (status === "pending") return identity.complete;
  return status === "approved" || status === "rejected";
}

export function isVisibleTimelineItem(item) {
  if (item?.controlOnly || isInjectedPromptTimelineItem(item)) return false;
  if (
    !item?.role &&
    MESSAGE_LIFECYCLE_ITEM_TYPES.has(
      normalizeString(item?.itemType).toLowerCase(),
    )
  )
    return false;
  if (item?.role === "user") return true;
  const kind = normalizeTimelineKind(item);
  if (kind === "approval") return isVisibleApprovalTimelineItem(item);
  if (normalizeString(item?.text)) return true;
  if (kind === "command") {
    const toolBacked = Boolean(
      normalizeString(
        firstOptionalPresent(item?.tool, item?.toolName, item?.tool_name),
      ),
    );
    const title = normalizeString(item?.title).trim();
    const meaningful =
      Boolean(normalizeString(item?.command)) ||
      Boolean(
        normalizeString(
          firstOptionalPresent(item?.text, item?.output, item?.error),
        ),
      ) ||
      Boolean(
        title.startsWith("$ ") &&
        !GENERIC_COMMAND_TITLES.has(title.toLowerCase()),
      );
    return !toolBacked && meaningful;
  }
  return (
    kind === "thinking" ||
    kind === "reasoning" ||
    kind === "tool" ||
    kind === "process" ||
    kind === "plan"
  );
}
