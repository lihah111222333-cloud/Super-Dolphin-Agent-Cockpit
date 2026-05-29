export function defaultLayoutForMode(mode) {
  return mode === 'cmd' ? 'mix' : 'focus';
}

export function normalizeChatLayout(layout) {
  return layout === 'mix' ? 'mix' : 'focus';
}

export function normalizeCmdLayout(layout) {
  if (layout === 'overview' || layout === 'chat' || layout === 'mix') {
    return layout;
  }
  return 'mix';
}

export function deriveChatAgents({ threads }) {
  return Array.isArray(threads) ? threads : [];
}

export function deriveCmdAgents({ threads }) {
  return Array.isArray(threads) ? threads : [];
}

// Regex for the `[label] rest` prefix the backend prepends to a thread's
// display_name when the router picks a non-default persona. Kept deliberately
// strict: must be at the very start, no nested brackets in the label, exactly
// one space after the closing bracket. Anything that doesn't match treats the
// whole string as a plain name so user-typed content that happens to contain
// brackets doesn't get misinterpreted as a badge.
const AGENT_BADGE_PATTERN = /^\[([^\[\]]+)\]\s(.*)$/;

// parseAgentBadge splits a thread display name into an optional "agent label"
// pill and the plain name. Used by sidebar / chat header so the sidebar can
// render a small blue pill for the label and a regular strong for the rest,
// instead of baking the bracketed prefix straight into the user-visible text.
//
// When no prefix is present, label is empty and name equals the input. Empty /
// nullish input yields empty strings on both fields so callers can unconditionally
// consume {label, name} without null checks.
export function parseAgentBadge(name) {
  const raw = (name == null ? '' : String(name));
  const match = AGENT_BADGE_PATTERN.exec(raw);
  if (!match) return { label: '', name: raw };
  return { label: match[1].trim(), name: match[2] };
}
