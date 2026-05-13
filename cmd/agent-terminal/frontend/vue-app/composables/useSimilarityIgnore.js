import { ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';

// pairKey 生成 v-for 列表稳定 key 与 ignoringGroup 状态键。
// 用 path 组合而非数组 index，防止 refresh 重排后 ignoring 状态错位到别的行。
export function pairKey(group) {
  if (!group) return '';
  return `${group.targetA || ''}:${group.pathA || ''}|${group.targetB || ''}:${group.pathB || ''}`;
}

/**
 * 相似度对"忽略"按钮的可复用状态/处理器。
 *
 * 调用方提供：
 *   - currentCwd: { value } 当前 cwd 的 ref
 *   - setNotice(level, message): 通知条 setter
 *   - emit(name): Vue setup() 的 emit
 *
 * ignoringGroup.value 存当前正在忽略的 pair stable key（null 表示空闲）。
 */
export function useSimilarityIgnore({ currentCwd, setNotice, emit }) {
  const ignoringGroup = ref(null);
  async function ignoreGroup(group) {
    if (!group || ignoringGroup.value !== null) return;
    const key = pairKey(group);
    ignoringGroup.value = key;
    try {
      await callAPI('ui/memory/similarity/ignore', {
        cwd: currentCwd.value,
        targetA: group.targetA, pathA: group.pathA,
        targetB: group.targetB, pathB: group.pathB,
      });
      setNotice('info', `已忽略「${group.nameA}」与「${group.nameB}」`);
      emit('refresh');
    } catch (error) {
      setNotice('error', `忽略失败：${(error && error.message) || String(error || '')}`);
    } finally {
      ignoringGroup.value = null;
    }
  }
  return { ignoringGroup, ignoreGroup };
}
