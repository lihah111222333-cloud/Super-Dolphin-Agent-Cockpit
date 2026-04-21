// useRouterClassify \u2014 debounced preview caller for the router/classify RPC.
// Mirrors the backend: pure read, no side effects, never throws; on store /
// network failure we just clear the preview.
//
// Typical usage:
//   const rc = useRouterClassify();
//   watch(() => composer.state.text, (t) => rc.classify(t));
//   watch(() => selectedAgentKey.value, (v) => { if (v) rc.clear(); });
//
// The caller decides WHEN to call (and when to clear); the composable only
// owns the debounce + in-flight staleness guard + reactive preview state.
import { ref, shallowRef } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';

const MIN_INPUT_LEN = 3;
const DEFAULT_DEBOUNCE_MS = 350;

export function useRouterClassify() {
  const preview = shallowRef(null); // { matched, agentKey, promptKey, title, reason } | null
  const loading = ref(false);
  let inflightToken = 0;
  let debounceTimer = null;

  async function classifyNow(userInput) {
    const text = (userInput || '').toString();
    if (text.trim().length < MIN_INPUT_LEN) {
      preview.value = null;
      return;
    }
    const token = ++inflightToken;
    loading.value = true;
    try {
      const res = await callAPI('router/classify', { user_input: text });
      if (token !== inflightToken) return;
      if (res && res.matched) {
        preview.value = {
          agentKey: (res.agent_key || res.agentKey || '').toString().trim(),
          promptKey: (res.prompt_key || res.promptKey || '').toString().trim(),
          title: (res.title || '').toString().trim(),
          reason: (res.reason || '').toString().trim(),
        };
      } else {
        preview.value = null;
      }
    } catch (err) {
      if (token === inflightToken) preview.value = null;
      logWarn('ui', 'router.classify.error', { error: err?.message || String(err) });
    } finally {
      if (token === inflightToken) loading.value = false;
    }
  }

  function classify(userInput, delayMs = DEFAULT_DEBOUNCE_MS) {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      debounceTimer = null;
      classifyNow(userInput);
    }, Math.max(0, delayMs));
  }

  function clear() {
    if (debounceTimer) {
      clearTimeout(debounceTimer);
      debounceTimer = null;
    }
    inflightToken++;
    preview.value = null;
  }

  return { preview, loading, classify, classifyNow, clear };
}
