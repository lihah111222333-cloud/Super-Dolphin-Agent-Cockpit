import { logDebug } from '../services/log.js';

// CronRunsModal — read-only viewer for cronjob/listRuns output. The run DTO
// shape is defined in internal/module/cron/contract.go (Run struct).
export const CronRunsModal = {
  name: 'CronRunsModal',
  props: {
    show: { type: Boolean, default: false },
    job: { type: Object, default: null },
    runs: { type: Array, default: () => [] },
    loading: { type: Boolean, default: false },
    errorText: { type: String, default: '' },
  },
  emits: ['close', 'refresh'],
  setup(_props, { emit }) {
    function close() {
      logDebug('ui', 'cronRunsModal.close', {});
      emit('close');
    }
    function refresh() {
      logDebug('ui', 'cronRunsModal.refresh', {});
      emit('refresh');
    }
    return { close, refresh };
  },
  template: `
    <div v-if="show" class="modal-overlay" @click.self="close">
      <div class="modal-box cron-runs-box" data-testid="cron-runs-modal">
        <div class="modal-title">运行记录 — {{ job?.name || job?.id || '' }}</div>
        <div class="cron-runs-toolbar">
          <button class="btn btn-ghost btn-xs" :disabled="loading" @click="refresh" data-testid="cron-runs-refresh">
            {{ loading ? '加载中…' : '刷新' }}
          </button>
        </div>
        <div v-if="errorText" class="cron-form-error">{{ errorText }}</div>
        <div v-if="!loading && runs.length === 0" class="cron-runs-empty" data-testid="cron-runs-empty">
          暂无运行记录
        </div>
        <div v-else class="cron-runs-list" data-testid="cron-runs-list">
          <article
            v-for="(r, idx) in runs"
            :key="r.id || idx"
            class="data-card-vue"
            :data-testid="'cron-run-card-' + idx"
          >
            <div class="data-row-vue"><strong>状态</strong><span>{{ r.status || '-' }}</span></div>
            <div class="data-row-vue"><strong>计划时间</strong><span>{{ r.scheduled_at || '-' }}</span></div>
            <div class="data-row-vue"><strong>提交时间</strong><span>{{ r.submitted_at || '-' }}</span></div>
            <div class="data-row-vue"><strong>Turn</strong><span>{{ r.turn_id || '-' }}</span></div>
            <div v-if="r.error" class="data-row-vue"><strong>错误</strong><span>{{ r.error }}</span></div>
          </article>
        </div>
        <div class="modal-btns">
          <button class="btn btn-ghost" @click="close">关闭</button>
        </div>
      </div>
    </div>
  `,
};
