// @ts-nocheck

export const CmdOverviewPanel = {
  name: 'CmdOverviewPanel',
  props: {
    stats: { type: Object, default: () => ({ total: 0, running: 0, thinking: 0, editing: 0, error: 0 }) },
    recentThreads: { type: Array, default: () => [] },
    selectedThreadId: { type: String, default: '' },
    getDisplayName: { type: Function, default: (thread) => thread?.name || '' },
  },
  emits: ['select-thread'],
  setup(_, { emit }) {
    return { emit };
  },
  template: `
    <section class="agent-overview-panel">
      <div class="overview-metrics">
        <div class="metric"><strong>{{ stats.total }}</strong><span>子Agent</span></div>
        <div class="metric"><strong>{{ stats.running }}</strong><span>执行中</span></div>
        <div class="metric"><strong>{{ stats.thinking }}</strong><span>思考/回复</span></div>
        <div class="metric"><strong>{{ stats.editing }}</strong><span>改文件</span></div>
        <div class="metric"><strong>{{ stats.error }}</strong><span>异常</span></div>
      </div>
      <div class="overview-recent" v-if="recentThreads.length > 0">
        <span class="recent-label">最近活跃:</span>
        <button
          v-for="thread in recentThreads"
          :key="thread.id"
          class="recent-chip"
          :class="{active: thread.id === selectedThreadId}"
          @click="emit('select-thread', thread.id)"
        >
          {{ getDisplayName(thread) }}
        </button>
      </div>
    </section>
  `,
};
