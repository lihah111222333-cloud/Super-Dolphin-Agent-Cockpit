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
import { logWarn } from '../services/log.js';

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

  function pickString(...values) {
    for (const value of values) {
      if (value == null) continue;
      if (typeof value === 'string') {
        const trimmed = value.trim();
        if (trimmed && trimmed !== '.' && trimmed !== '[object Object]') return trimmed;
        continue;
      }
      if (typeof value === 'number' && Number.isFinite(value)) return String(value);
      // skip objects/arrays — callers must extract string fields before passing
    }
    return '';
  }

  async function resolveAgentModel(threadId, agentProvider, runtime, thread, storeRuntime) {
    const directModel = pickString(
      runtime?.model,
      runtime?.modelName,
      runtime?.model_name,
      storeRuntime?.model,
      storeRuntime?.modelName,
      storeRuntime?.model_name,
      thread?.model,
      thread?.effectiveModel,
      thread?.effective?.model,
      thread?.config?.effective?.model,
    );
    if (directModel) return directModel;
    try {
      const config = typeof threadStore?.getThreadConfig === 'function'
        ? await threadStore.getThreadConfig(threadId)
        : await callAPI('thread/config/get', { threadId });
      const effectiveModel = pickString(config?.effective?.model);
      if (effectiveModel) return effectiveModel;
    } catch {
      // ignore thread config lookup failure
    }
    try {
      const modelPref = await callAPI('ui/preferences/get', { key: `settings.provider.${agentProvider}.model` });
      return pickString(modelPref);
    } catch {
      return '';
    }
  }

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
    const resolvedPort = (Number.isFinite(Number(runtime.port)) && Number(runtime.port) > 0)
      ? Number(runtime.port)
      : ((Number.isFinite(Number(resolved.port)) && Number(resolved.port) > 0) ? Number(resolved.port) : null);
    const storeRuntime = /** @type {any} */ ((threadStore?.state?.agentRuntimeById?.[threadId] && typeof threadStore.state.agentRuntimeById[threadId] === 'object')
      ? threadStore.state.agentRuntimeById[threadId]
      : {});
    const thread = /** @type {any} */ ((activeThread.value && typeof activeThread.value === 'object')
      ? activeThread.value
      : {});
    const agentProvider = pickString(runtime.provider, storeRuntime.provider) || (useClaudeProvider.value ? 'claude' : 'codex');
    const agentModel = await resolveAgentModel(threadId, agentProvider, runtime, thread, storeRuntime);
    const currentCwd = pickString(
      runtime.cwd,
      storeRuntime.cwd,
      thread.cwd,
      activeProjectCwd.value,
    );
    const resolvedLogPath = pickString(
      runtime.logPath,
      runtime.log_path,
      storeRuntime.logPath,
      storeRuntime.log_path,
      thread.logPath,
      thread.log_path,
    );
    logWarn('provider.config', 'copy_thread_info.resolved', {
      thread_id: threadId,
      provider: agentProvider,
      model: agentModel || null,
      runtime_provider: runtime?.provider || null,
      runtime_model: runtime?.model || null,
      store_provider: storeRuntime?.provider || null,
      store_model: storeRuntime?.model || null,
      thread_model: thread?.model || null,
      thread_effective_model: thread?.effective?.model || null,
      use_claude_provider: useClaudeProvider.value,
    });
    const payload = {
      agentId: threadId,
      providerThreadId: providerThreadID,
      uuid: providerThreadID,
      name: activeThread.value ? threadStore.displayName(activeThread.value) : threadId,
      status: activeStatus.value,

      provider: agentProvider,
      model: agentModel || null,
      port: resolvedPort,
      cwd: currentCwd || null,
      'log-path': resolvedLogPath || buildCwdLogPath(currentCwd),
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
