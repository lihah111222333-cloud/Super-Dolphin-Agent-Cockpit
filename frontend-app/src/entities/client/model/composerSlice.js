import { createComposerActionSet } from './helpers/a2Slice/composerSliceActions.js';

/*
 * composer slice 管输入区：草稿、附件、模型选择和发送。
 * timeline 合并、线程快照不在这里做。
 */

export function createComposerSlice(runtime, deps) {
  return createComposerActionSet(runtime, deps);
}
