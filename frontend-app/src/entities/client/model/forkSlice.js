import { createForkActionSet } from './helpers/a2Slice/forkSliceActions.js';

/*
 * fork slice 从当前对话创建继承会话。
 * 它只把 timeline 摘要和选中的 shared files 带进新 thread，不伪造回复。
 */

export function createForkSlice(runtime, deps) {
  return createForkActionSet(runtime, deps);
}
