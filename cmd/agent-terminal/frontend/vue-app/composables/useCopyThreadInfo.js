import {
  ref,
  computed,
} from '../../lib/vue.esm-browser.prod.js';
import {
  callAPI,
  copyTextToClipboard,
  resolveThreadIdentity,
} from '../services/api.js';
import {
  buildCwdLogPath,
  formatUTC8HumanReadable,
} from '../utils/thread-copy-utils.js';

/**
 * @param {object} deps
 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<string>} deps.selectedThreadId
 * @param {import('../../lib/vue.esm-browser.prod.js').ComputedRef<any>} deps.activeRuntime
 * @param {import('../../lib/vue.esm-browser.prod.js').ComputedRef<any>} deps.activeThread
 * @param {import('../../lib/vue.esm-browser.prod.js').ComputedRef<string>} deps.activeStatus

 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<boolean>} deps.useClaudeProvider
 * @param {import('../../lib/vue.esm-browser.prod.js').ComputedRef<string>} deps.activeProjectCwd
 * @param {object} deps.threadStore
 */
export function useCopyThreadInfo(deps) {
  const {
    selectedThreadId,
    activeRuntime,
    activeThread,
    activeStatus,

    useClaudeProvider,
    activeProjectCwd,
    threadStore,
  } = deps;

  const copyState = ref('idle');
  let copyStateTimer = 0;

  const copyButtonLabel = computed(() => {
    if (copyState.value === 'done') return '已复制';
    if (copyState.value === 'error') return '复制失败';
    return '复制信息';
  });

  async function copySelectedThreadId() {
    const threadId = (selectedThreadId.value || '').toString();
    if (!threadId) return;
    const runtime = /** @type {any} */ ((activeRuntime.value && typeof activeRuntime.value === 'object')
      ? activeRuntime.value
      : { providerThreadId: '', port: null });
    let resolved = /** @type {any} */ ({ providerThreadId: '', port: null });
    const existingProviderThreadID = (runtime.providerThreadId || '').toString().trim();
    if (!existingProviderThreadID) {
      try {
        resolved = /** @type {any} */ (await resolveThreadIdentity(threadId));
      } catch {
        resolved = { providerThreadId: '', port: null };
      }
    }
    const providerThreadID = existingProviderThreadID
      || (resolved.providerThreadId || '').toString().trim();
    const resolvedPort = Number.isFinite(Number(runtime.port))
      ? Number(runtime.port)
      : (Number.isFinite(Number(resolved.port)) ? Number(resolved.port) : null);
    const agentProvider = (runtime.provider || '').toString().trim() || (useClaudeProvider.value ? 'claude' : 'codex');
    let agentModel = '';
    try {
      const modelPref = await callAPI('ui/preferences/get', { key: `settings.provider.${agentProvider}.model` });
      agentModel = (typeof modelPref === 'string' && modelPref.trim()) ? modelPref.trim() : '';
    } catch {
      // ignore model preference lookup failure
    }
    const runtimeCwd = (runtime.cwd || '').toString().trim();
    const runtimeLogPath = (runtime.logPath || '').toString().trim();
    const currentCwd = runtimeCwd || (activeProjectCwd.value || '').toString().trim();
    const payload = {
      agentId: threadId,
      providerThreadId: providerThreadID,
      uuid: providerThreadID,
      name: activeThread.value ? threadStore.displayName(activeThread.value) : threadId,
      status: activeStatus.value,

      provider: agentProvider,
      model: agentModel || null,
      port: resolvedPort,
      cwd: runtimeCwd || null,
      'log-path': runtimeLogPath || buildCwdLogPath(currentCwd),
      copiedAt: formatUTC8HumanReadable(),
    };

    const text = JSON.stringify(payload, null, 2);
    if (copyStateTimer) {
      window.clearTimeout(copyStateTimer);
      copyStateTimer = 0;
    }
    try {
      const ok = await copyTextToClipboard(text);
      copyState.value = ok ? 'done' : 'error';
    } catch {
      copyState.value = 'error';
    }
    copyStateTimer = window.setTimeout(() => {
      copyState.value = 'idle';
      copyStateTimer = 0;
    }, 1200);
  }

  function cleanup() {
    if (copyStateTimer) {
      window.clearTimeout(copyStateTimer);
      copyStateTimer = 0;
    }
  }

  return {
    copyButtonLabel,
    copySelectedThreadId,
    cleanup,
  };
}
