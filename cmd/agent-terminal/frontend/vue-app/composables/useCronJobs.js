import { reactive } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';

// useCronJobs — extracts the cronjob/* RPC orchestration out of app.js so
// the AppRoot setup() function stays under the 250-line guard limit. The
// returned object is a flat bag of reactive state + handlers consumed
// directly by TasksPage / CronJobModal / CronRunsModal templates.

function errorMessage(err) {
  if (!err) return '';
  if (typeof err === 'string') return err;
  return err.message || JSON.stringify(err);
}

export function useCronJobs() {
  const state = reactive({
    jobs: [],
    loading: false,
    error: '',
    togglingId: '',
    modalShow: false,
    modalMode: 'create',
    modalJob: null,
    modalSubmitting: false,
    modalError: '',
    runsShow: false,
    runsJob: null,
    runs: [],
    runsLoading: false,
    runsError: '',
  });

  async function refresh() {
    state.loading = true;
    state.error = '';
    try {
      const res = await callAPI('cronjob/list', {});
      state.jobs = Array.isArray(res?.jobs) ? res.jobs : [];
    } catch (error) {
      console.warn('refresh cron jobs failed', error);
      state.jobs = [];
      state.error = errorMessage(error) || '加载定时任务失败';
    } finally {
      state.loading = false;
    }
  }

  function openCreate() {
    state.modalMode = 'create';
    state.modalJob = null;
    state.modalError = '';
    state.modalShow = true;
  }

  function openEdit(job) {
    state.modalMode = 'edit';
    state.modalJob = job || null;
    state.modalError = '';
    state.modalShow = true;
  }

  function closeModal() {
    if (state.modalSubmitting) return;
    state.modalShow = false;
    state.modalJob = null;
    state.modalError = '';
  }

  async function submitModal(payload) {
    if (!payload || typeof payload !== 'object') return;
    state.modalSubmitting = true;
    state.modalError = '';
    try {
      if (state.modalMode === 'edit' && state.modalJob?.id) {
        await callAPI('cronjob/update', { id: state.modalJob.id, ...payload });
      } else {
        await callAPI('cronjob/create', payload);
      }
      state.modalShow = false;
      state.modalJob = null;
      await refresh();
    } catch (error) {
      console.warn('submit cron job failed', error);
      state.modalError = errorMessage(error) || '保存失败';
    } finally {
      state.modalSubmitting = false;
    }
  }

  async function remove(job) {
    if (!job?.id) return;
    const label = job.name || job.id;
    if (typeof window !== 'undefined' && typeof window.confirm === 'function') {
      if (!window.confirm(`确定删除定时任务 "${label}" 吗？`)) return;
    }
    try {
      await callAPI('cronjob/delete', { id: job.id });
      await refresh();
    } catch (error) {
      console.warn('delete cron job failed', error);
      state.error = errorMessage(error) || '删除失败';
    }
  }

  async function toggle(job) {
    if (!job?.id) return;
    state.togglingId = job.id;
    try {
      await callAPI('cronjob/setEnabled', { id: job.id, enabled: !job.enabled });
      await refresh();
    } catch (error) {
      console.warn('toggle cron job failed', error);
      state.error = errorMessage(error) || '切换状态失败';
    } finally {
      state.togglingId = '';
    }
  }

  async function loadRuns(job) {
    if (!job?.id) return;
    state.runsLoading = true;
    state.runsError = '';
    try {
      const res = await callAPI('cronjob/listRuns', { job_id: job.id, limit: 50 });
      state.runs = Array.isArray(res?.runs) ? res.runs : [];
    } catch (error) {
      console.warn('list cron runs failed', error);
      state.runs = [];
      state.runsError = errorMessage(error) || '加载运行记录失败';
    } finally {
      state.runsLoading = false;
    }
  }

  function openRuns(job) {
    state.runsJob = job || null;
    state.runsShow = true;
    state.runs = [];
    loadRuns(job);
  }

  function closeRuns() {
    state.runsShow = false;
    state.runsJob = null;
    state.runs = [];
    state.runsError = '';
  }

  function refreshRuns() {
    if (state.runsJob) loadRuns(state.runsJob);
  }

  return {
    state,
    refresh,
    openCreate,
    openEdit,
    closeModal,
    submitModal,
    remove,
    toggle,
    openRuns,
    closeRuns,
    refreshRuns,
  };
}
