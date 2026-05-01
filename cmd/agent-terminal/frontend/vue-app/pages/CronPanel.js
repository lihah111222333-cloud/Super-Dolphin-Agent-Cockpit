// CronPanel: P1b-UI v1, 作为「任务」页的子 tab 渲染。
// 仅覆盖列表 / 启停 / 删除三件事；表单 / 详情 / cron 表达式预览
// 留给后续 PR（详见 docs/plans/迁移/p21/P1b_CronUI.md）。
import { onMounted, onBeforeUnmount, computed, ref } from '../../lib/vue.esm-browser.prod.js';
import { useCronStore } from '../stores/cron.js';
import { mapCronRpcError } from '../services/cron-api.js';
import { logDebug, logInfo, logWarn } from '../services/log.js';
import { CronJobForm } from '../components/cron/CronJobForm.js';
import { CronJobDetail } from '../components/cron/CronJobDetail.js';

function formatSchedule(job) {
  const expr = (job?.schedule_expr || '').toString();
  const tz = (job?.timezone || '').toString();
  if (!expr) return '-';
  return tz ? `${expr} (${tz})` : expr;
}

function formatRetryBudget(job) {
  const max = Number(job?.max_attempts || 0);
  if (!Number.isFinite(max) || max <= 0) return '不重试';
  const failed = Number(job?.failure_count || 0);
  return `${failed} / ${max}`;
}

function formatLastRun(job) {
  const status = (job?.last_status || '').toString();
  const at = (job?.last_run_at || '').toString();
  if (!status && !at) return '从未运行';
  if (!at) return status;
  return `${status} · ${at}`;
}

export const CronPanel = {
  name: 'CronPanel',
  components: { CronJobForm, CronJobDetail },
  setup() {
    const store = useCronStore();

    const jobs = computed(() => store.state.jobs);
    const loading = computed(() => store.state.loading.list);
    const errorMessage = computed(() => store.state.error.list);

    // view = 'list' | 'form' | 'detail'; editingJob = null 表示创建。
    const view = ref('list');
    const editingJob = ref(null);
    const viewingJobId = ref('');

    function openCreate() {
      editingJob.value = null;
      view.value = 'form';
    }
    function openEdit(job) {
      editingJob.value = job;
      view.value = 'form';
    }
    function openDetail(job) {
      viewingJobId.value = job?.id || '';
      view.value = 'detail';
    }
    function closeForm() {
      view.value = 'list';
      editingJob.value = null;
    }
    function backToList() {
      view.value = 'list';
      viewingJobId.value = '';
      editingJob.value = null;
    }
    function editFromDetail(job) {
      editingJob.value = job;
      view.value = 'form';
    }
    async function onSaved() {
      closeForm();
      try { await store.loadJobs(); } catch (err) {
        logWarn('cron-panel', 'reload.failed', { message: (err && err.message) || String(err) });
      }
    }

    async function refresh() {
      try {
        await store.loadJobs();
      } catch (err) {
        logWarn('cron-panel', 'refresh.failed', { message: (err && err.message) || String(err) });
      }
    }

    async function onToggleEnabled(job, nextEnabled) {
      logDebug('cron-panel', 'toggle.click', { id: job.id, next: nextEnabled });
      try {
        await store.setJobEnabled(job.id, nextEnabled);
      } catch (err) {
        const mapped = mapCronRpcError(err);
        logWarn('cron-panel', 'toggle.failed', { id: job.id, kind: mapped.kind });
        if (typeof window !== 'undefined' && typeof window.alert === 'function') {
          window.alert(`启停失败：${mapped.message}`);
        }
      }
    }

    async function onRunOnce(job) {
      logInfo('cron-panel', 'runOnce.click', { id: job.id });
      try {
        await store.runOnce(job.id);
      } catch (err) {
        const mapped = mapCronRpcError(err);
        logWarn('cron-panel', 'runOnce.failed', { id: job.id, kind: mapped.kind });
        if (typeof window !== 'undefined' && typeof window.alert === 'function') {
          window.alert(`立即触发失败：${mapped.message}`);
        }
      }
    }

    const confirmDeleteJob = ref(null);
    const deletingJobId = ref('');
    const deleteError = ref('');

    function onDelete(job) {
      confirmDeleteJob.value = job;
      deleteError.value = '';
    }

    function cancelDeleteJob() {
      confirmDeleteJob.value = null;
    }

    async function confirmDelete() {
      const job = confirmDeleteJob.value;
      if (!job) return;
      confirmDeleteJob.value = null;
      deletingJobId.value = job.id;
      logInfo('cron-panel', 'delete.click', { id: job.id });
      try {
        await store.deleteJob(job.id);
      } catch (err) {
        const mapped = mapCronRpcError(err);
        logWarn('cron-panel', 'delete.failed', { id: job.id, kind: mapped.kind });
        deleteError.value = `删除失败：${mapped.message}`;
      } finally {
        deletingJobId.value = '';
      }
    }

    onMounted(() => {
      store.attachBridge();
      refresh();
    });

    onBeforeUnmount(() => {
      store.detachBridge();
    });

    return {
      jobs,
      loading,
      errorMessage,
      refresh,
      onToggleEnabled,
      onRunOnce,
      onDelete,
      confirmDeleteJob,
      deletingJobId,
      deleteError,
      cancelDeleteJob,
      confirmDelete,
      formatSchedule,
      formatRetryBudget,
      formatLastRun,
      view,
      editingJob,
      viewingJobId,
      openCreate,
      openEdit,
      openDetail,
      closeForm,
      backToList,
      editFromDetail,
      onSaved,
    };
  },
  template: `
    <div class="cron-panel" data-testid="cron-panel">
      <CronJobForm
        v-if="view === 'form'"
        :editing-job="editingJob"
        @cancel="closeForm"
        @saved="onSaved"
      />
      <CronJobDetail
        v-else-if="view === 'detail'"
        :job-id="viewingJobId"
        @back="backToList"
        @edit="editFromDetail"
      />
      <template v-else>
      <div class="cron-panel-toolbar">
        <button
          class="btn btn-primary btn-xs"
          data-testid="cron-new-button"
          @click="openCreate"
        >新建定时任务</button>
        <button
          class="btn btn-ghost btn-xs"
          data-testid="cron-refresh-button"
          :disabled="loading"
          @click="refresh"
        >{{ loading ? '加载中…' : '刷新' }}</button>
      </div>

      <div v-if="errorMessage" class="alert alert-error" data-testid="cron-error">
        {{ errorMessage }}
      </div>

      <div
        v-if="!loading && jobs.length === 0"
        class="empty-state"
        data-testid="cron-empty-state"
      >
        <div class="es-icon">⏱</div>
        <h3>暂无定时任务</h3>
        <p>使用后端 cronjob/create 创建任务后会显示在此处。</p>
      </div>

      <div v-else-if="jobs.length > 0" class="data-list-vue" data-testid="cron-list">
        <article
          v-for="(job, idx) in jobs"
          :key="job.id || ('cron-' + idx)"
          class="data-card-vue"
          :data-testid="'cron-card-' + idx"
        >
          <div class="data-row-vue">
            <strong>启用</strong>
            <span>
              <input
                type="checkbox"
                :checked="!!job.enabled"
                :data-testid="'cron-toggle-' + idx"
                @change="onToggleEnabled(job, $event.target.checked)"
              />
            </span>
          </div>
          <div class="data-row-vue">
            <strong>名称</strong>
            <span :data-testid="'cron-name-' + idx">{{ job.name || '(未命名)' }}</span>
          </div>
          <div class="data-row-vue">
            <strong>Schedule</strong>
            <span>{{ formatSchedule(job) }}</span>
          </div>
          <div class="data-row-vue">
            <strong>Provider</strong>
            <span>{{ job.provider || '-' }}</span>
          </div>
          <div class="data-row-vue">
            <strong>CWD</strong>
            <span :title="job.cwd">{{ job.cwd || '-' }}</span>
          </div>
          <div class="data-row-vue">
            <strong>上次状态</strong>
            <span>{{ formatLastRun(job) }}</span>
          </div>
          <div class="data-row-vue">
            <strong>失败 / 预算</strong>
            <span>{{ formatRetryBudget(job) }}</span>
          </div>
          <div class="data-actions-vue">
            <button
              class="btn btn-ghost btn-xs"
              :data-testid="'cron-view-' + idx"
              @click="openDetail(job)"
            >查看</button>
            <button
              class="btn btn-ghost btn-xs"
              :data-testid="'cron-runonce-' + idx"
              @click="onRunOnce(job)"
            >立即触发</button>
            <button
              class="btn btn-ghost btn-xs"
              :data-testid="'cron-edit-' + idx"
              @click="openEdit(job)"
            >编辑</button>
            <button
              class="btn btn-ghost btn-xs"
              :data-testid="'cron-delete-' + idx"
              @click="onDelete(job)"
            >删除</button>
          </div>
        </article>
      </div>
      </template>
      <div v-if="confirmDeleteJob" class="modal-overlay" data-testid="cron-delete-overlay" @click.self="cancelDeleteJob">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="cron-delete-modal">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">删除定时任务</div>
              <div class="memory-modal-tip">{{ confirmDeleteJob.name || confirmDeleteJob.id }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="cron-delete-close" :disabled="Boolean(deletingJobId)" @click="cancelDeleteJob">关闭</button>
          </div>
          <div class="memory-form-helper">确认删除定时任务 “{{ confirmDeleteJob.name || confirmDeleteJob.id }}”？该操作不可撤销。</div>
          <div v-if="deleteError" class="memory-form-helper" style="color:var(--color-danger,#f87171)">{{ deleteError }}</div>
          <div class="memory-editor-actions">
            <button class="btn btn-ghost" data-testid="cron-delete-cancel" :disabled="Boolean(deletingJobId)" @click="cancelDeleteJob">取消</button>
            <button class="btn btn-danger" data-testid="cron-delete-confirm" :disabled="Boolean(deletingJobId)" @click="confirmDelete">{{ deletingJobId === confirmDeleteJob.id ? '删除中...' : '确认删除' }}</button>
          </div>
        </div>
      </div>
    </div>
  `,
};
