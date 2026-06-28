// @ts-nocheck
// thread-stale-prompt.js
//
// Self-clean the cwd-scoped `settings.activePromptKey` UI preference when the
// backend's thread/start or turn/start router reports the caller-supplied
// prompt_key is stale (template deleted / disabled).
//
// Why this lives in its own module:
//   stores/thread-actions-helpers.js sits near its size-guard ceiling. Adding
//   the stale-detection branch directly would force a baseline bump. Splitting
//   it here keeps both files comfortably under their per-file budget AND keeps
//   the stale logic discoverable by name when troubleshooting "my activated
//   prompt didn't take effect" bug reports.
//
// Contract:
//   - Input: thread/start or turn/start RPC response object + cwd that was sent
//     on payload.
//   - When `res.prompt_key_stale === true` or `res.promptKeyStale === true`,
//     write `settings.activePromptKey = ''` for that cwd and stamp a
//     one-shot notice into `ctx.state.promptStaleNotice` for UI consumption.
//   - Best-effort: any failure to persist the cleared pref is logged but
//     never thrown, since the thread itself spawned fine. Surfacing this as
//     a launch error would be more confusing than the stale pin we just
//     cleaned up.

// Preference key used by SystemPromptPage to record which prompt_template the
// user wants the next blank conversation to use. Duplicated here as a local
// constant (instead of importing from pages/SystemPromptPage.js) to keep the
// store layer free of page imports.
export const PROMPT_ACTIVE_PREF_KEY = 'settings.activePromptKey';

// User-facing message rendered as a one-shot toast after we self-clean the
// stale activePromptKey. Wording mirrors the task spec; tests assert on a
// substring so future polish on either side remains safe.
export const PROMPT_STALE_NOTICE = '已激活的提示词不存在或已禁用，已自动取消激活';

function readStaleSignal(res) {
  if (!res || typeof res !== 'object') return false;
  return res.prompt_key_stale === true || res.promptKeyStale === true;
}

async function persistClearedActivePromptPref(ctx, scope) {
  try {
    await ctx.callAPI('ui/preferences/set', {
      key: PROMPT_ACTIVE_PREF_KEY,
      value: '',
      cwd: scope,
    });
  } catch (error) {
    if (typeof ctx.logWarn === 'function') {
      ctx.logWarn('thread', 'start.prompt_key_stale.persist_failed', { cwd: scope, error });
    }
  }
}

export async function maybeHandleStalePromptKey(ctx, res, cwd) {
  if (!readStaleSignal(res)) return;
  const scope = (cwd || '').toString().trim();
  await persistClearedActivePromptPref(ctx, scope);
  if (ctx && ctx.state) ctx.state.promptStaleNotice = PROMPT_STALE_NOTICE;
  if (typeof ctx?.logWarn === 'function') {
    ctx.logWarn('thread', 'start.prompt_key_stale.detected', {
      cwd: scope,
      prompt_key: (res.prompt_key || res.promptKey || '').toString(),
    });
  }
}
