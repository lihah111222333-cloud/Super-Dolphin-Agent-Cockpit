import { createProjectActionSet } from './helpers/a2Slice/projectSliceActions.js';

/*
 * project slice 管当前窗口选中的项目。
 * 切换项目会保存草稿、刷新聊天列表；失败要回到原来的项目。
 */

export function createProjectSlice(runtime, deps) {
  return createProjectActionSet(runtime, deps);
}
