import { logDebug } from '../services/log.js';

// TasksPage — three sub-tabs:
//   cron    定时任务 (cronjob/* RPC)
//   acks    任务工单
//   traces  执行追踪
//
// 定时任务 tab is the primary view: it lists cronstore.Job projections and
// exposes create / edit / delete / toggle / runs actions. The acks / traces
// tabs keep their original ui/dashboard/get-backed listing.
export const TasksPage = {
    name: 'TasksPage',
    props: {
        tasksSubTab: { type: String, default: 'cron' },
        // Generic ack/trace listing
        items: { type: Array, default: () => [] },
        fields: { type: Array, default: () => [] },
        // Cron jobs state (managed by app.js)
        cronJobs: { type: Array, default: () => [] },
        cronLoading: { type: Boolean, default: false },
        cronError: { type: String, default: '' },
        cronTogglingId: { type: String, default: '' },
    },
    emits: [
        'update:tasksSubTab',
        'cron-refresh',
        'cron-create',
        'cron-edit',
        'cron-delete',
        'cron-toggle',
        'cron-view-runs',
    ],
    setup(_props, { emit }) {
        void _props;
        function setSubTab(tab) {
            logDebug('page', 'tasks.subTab.changed', { tab });
            emit('update:tasksSubTab', tab);
        }
        function shortId(id) {
            const s = (id || '').toString();
            return s.length > 8 ? s.slice(0, 8) + '…' : s;
        }
        return {
            setSubTab,
            shortId,
        };
    },
    template: `
    <section id="page-tasks" class="page active" data-testid="tasks-page">
      <div class="panel-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>任务管理</h2></div>
      </div>
      <div class="sub-tabs" data-testid="tasks-sub-tabs">
        <button class="sub-tab" data-testid="tasks-subtab-cron" :class="{ active: tasksSubTab === 'cron' }" @click="setSubTab('cron')">定时任务</button>
        <button class="sub-tab" data-testid="tasks-subtab-acks" :class="{ active: tasksSubTab === 'acks' }" @click="setSubTab('acks')">任务工单</button>
        <button class="sub-tab" data-testid="tasks-subtab-traces" :class="{ active: tasksSubTab === 'traces' }" @click="setSubTab('traces')">执行追踪</button>
      </div>
      <div class="panel-body" data-testid="tasks-panel-body">

        <!-- 定时任务 -->
        <template v-if="tasksSubTab === 'cron'">
          <div class="cron-toolbar" data-testid="cron-toolbar">
            <button class="btn btn-primary btn-xs" @click="$emit('cron-create')" data-testid="cron-create-button">新建定时任务</button>
            <button class="btn btn-ghost btn-xs" :disabled="cronLoading" @click="$emit('cron-refresh')" data-testid="cron-refresh-button">
              {{ cronLoading ? '加载中…' : '刷新' }}
            </button>
          </div>
          <div v-if="cronError" class="cron-form-error" data-testid="cron-list-error">{{ cronError }}</div>
          <div v-if="!cronLoading && cronJobs.length === 0" class="empty-state" data-testid="cron-empty-state">
            <div class="es-icon">T</div>
            <h3>暂无定时任务</h3>
          </div>
          <div v-else class="data-list-vue" data-testid="cron-jobs-list">
            <article
              v-for="(job, idx) in cronJobs"
              :key="job.id || idx"
              class="data-card-vue cron-job-card"
              :data-testid="'cron-job-card-' + idx"
            >
              <div class="cron-job-head">
                <div class="cron-job-title">
                  <span class="cron-job-name">{{ job.name || '(未命名)' }}</span>
                  <span class="cron-job-id">{{ shortId(job.id) }}</span>
                </div>
                <span class="cron-pill" :class="{ 'cron-pill-on': job.enabled, 'cron-pill-off': !job.enabled }">
                  {{ job.enabled ? '已启用' : '已禁用' }}
                </span>
              </div>
              <div class="data-row-vue"><strong>Cron</strong><span>{{ job.schedule_expr || '-' }}</span></div>
              <div class="data-row-vue"><strong>下次运行</strong><span>{{ job.next_run_at || '-' }}</span></div>
              <div class="data-row-vue"><strong>上次运行</strong><span>{{ job.last_run_at || '-' }}</span></div>
              <div class="data-row-vue"><strong>上次状态</strong><span>{{ job.last_status || '-' }}</span></div>
              <div class="data-row-vue"><strong>失败次数</strong><span>{{ job.failure_count ?? 0 }} / {{ job.max_attempts ?? 0 }}</span></div>
              <div class="data-row-vue"><strong>CWD</strong><span>{{ job.cwd || '-' }}</span></div>
              <div v-if="job.last_error" class="data-row-vue"><strong>上次错误</strong><span>{{ job.last_error }}</span></div>
              <div class="data-actions-vue cron-actions">
                <button
                  class="btn btn-ghost btn-xs"
                  :disabled="cronTogglingId === job.id"
                  @click="$emit('cron-toggle', job)"
                  :data-testid="'cron-toggle-' + idx"
                >
                  {{ job.enabled ? '禁用' : '启用' }}
                </button>
                <button class="btn btn-ghost btn-xs" @click="$emit('cron-view-runs', job)" :data-testid="'cron-runs-' + idx">运行记录</button>
                <button class="btn btn-ghost btn-xs" @click="$emit('cron-edit', job)" :data-testid="'cron-edit-' + idx">编辑</button>
                <button class="btn btn-ghost btn-xs cron-action-danger" @click="$emit('cron-delete', job)" :data-testid="'cron-delete-' + idx">删除</button>
              </div>
            </article>
          </div>
        </template>

        <!-- 任务工单 / 执行追踪 -->
        <template v-else>
          <div v-if="items.length === 0" class="empty-state" data-testid="tasks-empty-state">
            <div class="es-icon">T</div>
            <h3>暂无任务</h3>
          </div>
          <div v-else class="data-list-vue" data-testid="tasks-list">
            <article
              v-for="(item, idx) in items"
              :key="item.ack_key || item.trace_id || idx"
              class="data-card-vue"
              :data-testid="'tasks-card-' + idx"
            >
              <div v-for="field in fields" :key="field.key" class="data-row-vue">
                <strong>{{ field.label }}</strong>
                <span>{{ item[field.key] ?? '-' }}</span>
              </div>
            </article>
          </div>
        </template>
      </div>
    </section>
  `,
};
