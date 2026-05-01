import { ref, computed } from '../../lib/vue.esm-browser.prod.js';
import { listPendingCandidates } from '../services/skills-api.js';

export function usePendingCandidates(cwdFn) {
  const pendingCandidates = ref([]);

  const sidebarBadges = computed(() => {
    const count = pendingCandidates.value.length;
    return count > 0 ? { skills: count } : {};
  });

  async function refreshPendingCandidates() {
    try {
      const cwd = (typeof cwdFn === 'function' ? cwdFn() : '').toString().trim();
      pendingCandidates.value = await listPendingCandidates(cwd);
    } catch (error) {
      console.warn('refresh pending candidates failed', error);
      pendingCandidates.value = [];
    }
  }

  return { pendingCandidates, sidebarBadges, refreshPendingCandidates };
}
