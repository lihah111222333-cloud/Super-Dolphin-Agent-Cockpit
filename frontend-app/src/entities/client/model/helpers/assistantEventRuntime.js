import { attachAssistantDeltaRuntime } from './assistantEventRuntime/delta.js';
import { attachAssistantTurnLifecycle } from './assistantEventRuntime/turnLifecycle.js';

// 唯一公开入口：装配 delta、completion 与 turn terminal 的同一 runtime 表面。
export function attachAssistantEventRuntime(runtime, deps) {
  runtime.clearAssistantDeltaFlushTimer = () => {
    if (!runtime.assistantDeltaFlushTimer) return;
    clearTimeout(runtime.assistantDeltaFlushTimer);
    runtime.assistantDeltaFlushTimer = null;
  };
  attachAssistantDeltaRuntime(runtime, deps);
  attachAssistantTurnLifecycle(runtime, deps);
}
