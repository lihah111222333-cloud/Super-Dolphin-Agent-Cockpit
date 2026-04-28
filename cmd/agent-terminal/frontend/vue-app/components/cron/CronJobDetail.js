// CronJobDetail: 任务详情 + 历史 runs。store 已经反应式持有 runs，
// 详情页只需在 mount 时拉一次 + 依赖 wails 事件桥（cron/job/runStateChanged）
// 增量更新；失败回退到「再次刷新」按钮。
import { onMounted, computed } from '../../../lib/vue.esm-browser.prod.js';
import { useCronStore } from '../../stores/cron.js';
import { logDebug, logWarn } from '../../services/log.js';

// runStatusColor maps the 7 run states to a color/icon family used
// for the status pill. observe_lost gets its own warning color +
// affordance per the P1b plan.
export function runStatusColor(status) {
  switch ((status || '').toString()) {
    case 'pending':      return { tone: 'gray',   label: '待启动' };
    case 'submitting':   return { tone: 'blue',   label: '提交中' };
    case 'submitted':    return { tone: 'blue',   label: '已提交' };
    case 'running':      return { tone: 'purple', label: '运行中' };
    case 'finished':     return { tone: 'green',  label: '已完成' };
    case 'failed':       return { tone: 'red',    label: '失败' };
    case 'observe_lost': return { tone: 'orange', label: '观察链丢失' };
    default:             return { tone: 'gray',   label: status || '未知' };
  }
}

export const CronJobDetail = {
  name: 'CronJobDetail',
  props: {
    jobId: { type: String, required: true },
  },
  emits: ['back', 'edit'],
  setup(props, { emit }) {
    const store = useCronStore();

    const job = computed(() => {
      return store.state.jobs.find((j) => j && j.id === props.jobId) || null;
    });
    const runs = computed(() => {
      const list = store.state.runsByJob[props.jobId];
      return Array.isArray(list) ? list : [];
    });
    const loadingRuns = computed(() => !!store.state.loading.runs[props.jobId]);
    const runsError = computed(() => store.state.error.runs[props.jobId] || '');

    async function refreshRuns() {
      try {
        await store.loadRuns(props.jobId);
      } catch (err) {
        logWarn('cron-detail', 'runs.refresh.failed', {
          job_id: props.jobId,
          message: (err && err.message) || String(err),
        });
      }
    }

    onMounted(() => {
      logDebug('cron-detail', 'mount', { job_id: props.jobId });
      refreshRuns();
    });

    function onEdit() {
      const j = job.value;
      if (j) emit('edit', j);
    }

    return {
      job,
      runs,
      loadingRuns,
      runsError,
      refreshRuns,
      onBack: () => emit('back'),
      onEdit,
      runStatusColor,
    };
  },
  template: `
    <div class="cron-job-detail" data-testid="cron-job-detail">
      <div class="cron-detail-toolbar">
        <button class="btn btn-ghost btn-xs" data-testid="cron-detail-back" @click="onBack">← 返回</button>
        <button class="btn btn-ghost btn-xs" data-testid="cron-detail-edit" :disabled="!job" @click="onEdit">编辑</button>
        <button
          class="btn btn-ghost btn-xs"
          data-testid="cron-detail-refresh-runs"
          :disabled="loadingRuns"
          @click="refreshRuns"
        >{{ loadingRuns ? '加载中…' : '刷新历史' }}</button>
      </div>

      <div v-if="!job" class="empty-state" data-testid="cron-detail-not-found">
        <h3>找不到任务</h3>
        <p>该任务可能已被删除，请返回列表。</p>
      </div>

      <template v-else>
        <section class="cron-detail-summary">
          <h3 :data-testid="'cron-detail-name'">{{ job.name || '(未命名)' }}</h3>
          <div class="data-row-vue"><strong>ID</strong><span>{{ job.id }}</span></div>
          <div class="data-row-vue">
            <strong>Schedule</strong>
            <span>{{ job.schedule_expr || '-' }}<span v-if="job.timezone"> ({{ job.timezone }})</span></span>
          </div>
          <div class="data-row-vue"><strong>启用</strong><span>{{ job.enabled ? '是' : '否' }}</span></div>
          <div class="data-row-vue"><strong>下一次</strong><span>{{ job.next_run_at || '-' }}</span></div>
          <div class="data-row-vue"><strong>上次运行</strong><span>{{ job.last_run_at || '从未运行' }}</span></div>
          <div class="data-row-vue"><strong>上次状态</strong><span>{{ job.last_status || '-' }}</span></div>
          <div class="data-row-vue"><strong>Provider</strong><span>{{ job.provider || '-' }}</span></div>
          <div class="data-row-vue"><strong>Model</strong><span>{{ job.model || '-' }}</span></div>
          <div class="data-row-vue"><strong>CWD</strong><span :title="job.cwd">{{ job.cwd || '-' }}</span></div>
          <div class="data-row-vue"><strong>Active Turn</strong><span>{{ job.active_turn_id || '-' }}</span></div>
          <div class="data-row-vue"><strong>失败 / 预算</strong><span>{{ job.failure_count || 0 }} / {{ job.max_attempts || 0 }}</span></div>
          <div v-if="job.last_error" class="data-row-vue cron-detail-error">
            <strong>上次错误</strong><span>{{ job.last_error }}</span>
          </div>
        </section>

        <section class="cron-detail-runs">
          <h4>历史 runs</h4>
          <div v-if="runsError" class="alert alert-error" data-testid="cron-detail-runs-error">
            {{ runsError }}
          </div>
          <div v-if="!loadingRuns && runs.length === 0" class="empty-state" data-testid="cron-detail-runs-empty">
            <p>还没有历史 run</p>
          </div>
          <ul v-else class="cron-runs-list" data-testid="cron-detail-runs-list">
            <li v-for="(run, idx) in runs" :key="run.id || idx" class="cron-run-row" :data-testid="'cron-run-' + idx">
              <span :class="['cron-status-pill', 'tone-' + runStatusColor(run.status).tone]">
                {{ runStatusColor(run.status).label }}
              </span>
              <span class="cron-run-time">{{ run.scheduled_at || '-' }}</span>
              <span v-if="run.turn_id" class="cron-run-turn" :title="run.turn_id">turn={{ run.turn_id.slice(0, 8) }}</span>
              <span v-if="run.error" class="cron-run-error" :title="run.error">{{ run.error }}</span>
              <span v-if="run.status === 'observe_lost'" class="cron-run-warn" data-testid="cron-run-observe-lost-hint">
                观察链丢失，需人工核对 turn 真实结局
              </span>
            </li>
          </ul>
        </section>
      </template>
    </div>
  `,
};
