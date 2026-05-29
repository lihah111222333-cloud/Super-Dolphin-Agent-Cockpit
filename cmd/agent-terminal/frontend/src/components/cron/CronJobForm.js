// CronJobForm: 创建 + 编辑同组件。提交前做本地强制校验：
//   1) 必填字段：name / prompt / cwd / schedule_expr
//   2) provider=codex 时 codexHome / codexInstanceKey /
//      codexModelProvider 三字段都必填
//   3) cron 表达式必须能被 cron-parser 成功 parse；首条预览作为
//      next_run_at（RFC3339）显式传给后端，避开 rpc.go 的 now+1min
//      fallback。
// 提交失败时，后端 sentinel 通过 mapCronRpcError → kind 映射回字段红框。
import { reactive, ref, computed, watch } from '../../../lib/vue.esm-browser.prod.js';
import { selectProjectDir } from '../../services/api.js';
import { mapCronRpcError } from '../../services/cron-api.js';
import { logDebug, logInfo, logWarn } from '../../services/log.js';
import { useCronStore } from '../../stores/cron.js';
import { ScheduleField, browserTimezone, previewNextRuns } from './ScheduleField.js';
import { ProviderIdentityField } from './ProviderIdentityField.js';

function emptyForm() {
  return {
    name: '',
    prompt: '',
    cwd: '',
    schedule: { schedule_expr: '', timezone: browserTimezone() },
    identity: {
      provider: 'codex',
      model: '',
      config: { codexHome: '', codexInstanceKey: '', codexModelProvider: '' },
    },
    skills: '',
    notify_channel: '',
    enabled: true,
    max_attempts: 0,
  };
}

function jobToForm(job) {
  if (!job || typeof job !== 'object') return emptyForm();
  const cfg = (job.config && typeof job.config === 'object') ? job.config : {};
  return {
    name: job.name || '',
    prompt: job.prompt || '',
    cwd: job.cwd || '',
    schedule: {
      schedule_expr: job.schedule_expr || '',
      timezone: job.timezone || browserTimezone(),
    },
    identity: {
      provider: job.provider || 'codex',
      model: job.model || '',
      config: {
        codexHome: cfg.codexHome || '',
        codexInstanceKey: cfg.codexInstanceKey || '',
        codexModelProvider: cfg.codexModelProvider || '',
      },
    },
    skills: Array.isArray(job.skills) ? job.skills.join(', ') : '',
    notify_channel: job.notify_channel || '',
    enabled: job.enabled !== false,
    max_attempts: Number.isFinite(job.max_attempts) ? job.max_attempts : 0,
  };
}

// validateForm returns a map of fieldName -> errorMsg; empty map = valid.
export function validateForm(form) {
  const errors = {};
  if (!form.name || !form.name.trim()) errors.name = '名称必填';
  if (!form.prompt || !form.prompt.trim()) errors.prompt = 'Prompt 必填';
  if (!form.cwd || !form.cwd.trim()) errors.cwd = '工作目录必填';
  if (!form.schedule.schedule_expr || !form.schedule.schedule_expr.trim()) {
    errors.schedule_expr = 'Cron 表达式必填';
  } else {
    const preview = previewNextRuns(form.schedule.schedule_expr, form.schedule.timezone, 1);
    if (preview.error) errors.schedule_expr = preview.error;
  }
  if (form.identity.provider === 'codex') {
    const cfg = form.identity.config || {};
    if (!cfg.codexHome || !cfg.codexHome.trim()) {
      errors.codex_identity = 'codexHome / codexInstanceKey / codexModelProvider 三项都必填';
    } else if (!cfg.codexInstanceKey || !cfg.codexInstanceKey.trim()) {
      errors.codex_identity = 'codexHome / codexInstanceKey / codexModelProvider 三项都必填';
    } else if (!cfg.codexModelProvider || !cfg.codexModelProvider.trim()) {
      errors.codex_identity = 'codexHome / codexInstanceKey / codexModelProvider 三项都必填';
    }
  }
  if (!Number.isFinite(form.max_attempts) || form.max_attempts < 0) {
    errors.max_attempts = '重试预算必须 >= 0';
  }
  return errors;
}

// Maps a backend sentinel kind to the form field name(s) that should
// be flagged. Mirrors the contract documented in P1b_CronUI.md.
function backendKindToField(kind) {
  switch (kind) {
    case 'cwd_required': return 'cwd';
    case 'name_required': return 'name';
    case 'prompt_required': return 'prompt';
    case 'schedule_required': return 'schedule_expr';
    case 'invalid_max_attempts': return 'max_attempts';
    case 'invalid_config': return 'codex_identity';
    case 'provider_unsupported': return 'provider';
    default: return null;
  }
}

function buildSubmitPayload(form) {
  const tz = (form.schedule.timezone || '').trim();
  const preview = previewNextRuns(form.schedule.schedule_expr, tz, 1);
  const nextRunAt = (preview.dates && preview.dates[0]) ? preview.dates[0].toISOString() : '';
  const skills = (form.skills || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
  return {
    name: form.name.trim(),
    prompt: form.prompt,
    schedule_expr: form.schedule.schedule_expr.trim(),
    timezone: tz,
    provider: form.identity.provider,
    model: (form.identity.model || '').trim(),
    cwd: form.cwd.trim(),
    config: form.identity.provider === 'codex' ? form.identity.config : {},
    skills,
    notify_channel: (form.notify_channel || '').trim(),
    enabled: !!form.enabled,
    max_attempts: Number(form.max_attempts) || 0,
    next_run_at: nextRunAt,
  };
}

export const CronJobForm = {
  name: 'CronJobForm',
  components: { ScheduleField, ProviderIdentityField },
  props: {
    editingJob: { type: Object, default: null },
  },
  emits: ['cancel', 'saved'],
  setup(props, { emit }) {
    const store = useCronStore();
    const form = reactive(jobToForm(props.editingJob));
    const fieldErrors = reactive({});
    const submitting = ref(false);
    const submitError = ref('');

    watch(() => props.editingJob, (next) => {
      Object.assign(form, jobToForm(next));
      for (const k of Object.keys(fieldErrors)) delete fieldErrors[k];
      submitError.value = '';
    });

    const isEdit = computed(() => !!(props.editingJob && props.editingJob.id));
    const submitLabel = computed(() => {
      if (submitting.value) return '提交中...';
      return isEdit.value ? '保存' : '创建';
    });

    async function pickCwd() {
      try {
        const seed = form.cwd || '';
        const picked = await selectProjectDir(seed);
        if (picked) form.cwd = picked;
      } catch (err) {
        logWarn('cron-form', 'cwd.pick.failed', { message: (err && err.message) || String(err) });
      }
    }

    function clearErrors() {
      for (const k of Object.keys(fieldErrors)) delete fieldErrors[k];
      submitError.value = '';
    }

    async function submit() {
      clearErrors();
      const errors = validateForm(form);
      if (Object.keys(errors).length > 0) {
        Object.assign(fieldErrors, errors);
        logDebug('cron-form', 'submit.local_invalid', { fields: Object.keys(errors) });
        return;
      }
      const payload = buildSubmitPayload(form);
      submitting.value = true;
      try {
        if (isEdit.value) {
          await store.updateJob(props.editingJob.id, payload);
          logInfo('cron-form', 'submit.updated', { id: props.editingJob.id });
        } else {
          await store.createJob(payload);
          logInfo('cron-form', 'submit.created', { name: payload.name });
        }
        emit('saved');
      } catch (err) {
        const mapped = mapCronRpcError(err);
        const field = backendKindToField(mapped.kind);
        if (field) {
          fieldErrors[field] = mapped.message;
        } else {
          submitError.value = mapped.message;
        }
        logWarn('cron-form', 'submit.failed', { kind: mapped.kind, field });
      } finally {
        submitting.value = false;
      }
    }

    return {
      form,
      fieldErrors,
      submitting,
      submitError,
      isEdit,
      submitLabel,
      pickCwd,
      submit,
      onCancel: () => emit('cancel'),
    };
  },
  template: `
    <form class="cron-job-form" data-testid="cron-job-form" @submit.prevent="submit">
      <h3>{{ isEdit ? '编辑定时任务' : '新建定时任务' }}</h3>

      <div class="form-row">
        <label>名称 *</label>
        <input
          type="text"
          v-model="form.name"
          data-testid="cron-form-name"
          :class="{ error: fieldErrors.name }"
        />
        <div v-if="fieldErrors.name" class="form-error" data-testid="cron-form-name-error">
          {{ fieldErrors.name }}
        </div>
      </div>

      <div class="form-row">
        <label>Prompt *</label>
        <textarea
          v-model="form.prompt"
          rows="4"
          data-testid="cron-form-prompt"
          :class="{ error: fieldErrors.prompt }"
        ></textarea>
        <div v-if="fieldErrors.prompt" class="form-error">{{ fieldErrors.prompt }}</div>
      </div>

      <div class="form-row">
        <label>工作目录 (CWD) *</label>
        <div class="cwd-row">
          <input
            type="text"
            v-model="form.cwd"
            data-testid="cron-form-cwd"
            :class="{ error: fieldErrors.cwd }"
          />
          <button type="button" class="btn btn-ghost btn-xs" data-testid="cron-form-cwd-pick" @click="pickCwd">选择…</button>
        </div>
        <div v-if="fieldErrors.cwd" class="form-error">{{ fieldErrors.cwd }}</div>
      </div>

      <ScheduleField v-model="form.schedule" />
      <div v-if="fieldErrors.schedule_expr" class="form-error" data-testid="cron-form-schedule-error">
        {{ fieldErrors.schedule_expr }}
      </div>

      <ProviderIdentityField v-model="form.identity" />
      <div v-if="fieldErrors.codex_identity" class="form-error" data-testid="cron-form-identity-error">
        {{ fieldErrors.codex_identity }}
      </div>
      <div v-if="fieldErrors.provider" class="form-error">{{ fieldErrors.provider }}</div>

      <div class="form-row">
        <label>Skills（逗号分隔）</label>
        <input type="text" v-model="form.skills" data-testid="cron-form-skills" />
      </div>

      <div class="form-row">
        <label>通知渠道</label>
        <input
          type="text"
          v-model="form.notify_channel"
          placeholder="留空=失败时不通知"
          data-testid="cron-form-notify"
        />
      </div>

      <div class="form-row">
        <label>重试预算</label>
        <input
          type="number"
          v-model.number="form.max_attempts"
          min="0"
          data-testid="cron-form-max-attempts"
          :class="{ error: fieldErrors.max_attempts }"
        />
        <small>0 = 不重试，失败后等下次 schedule</small>
        <div v-if="fieldErrors.max_attempts" class="form-error">{{ fieldErrors.max_attempts }}</div>
      </div>

      <div class="form-row">
        <label>
          <input type="checkbox" v-model="form.enabled" data-testid="cron-form-enabled" />
          启用
        </label>
      </div>

      <div v-if="submitError" class="form-error" data-testid="cron-form-submit-error">
        {{ submitError }}
      </div>

      <div class="form-actions">
        <button type="submit" class="btn btn-primary" :disabled="submitting" data-testid="cron-form-submit">
          {{ submitLabel }}
        </button>
        <button type="button" class="btn btn-ghost" data-testid="cron-form-cancel" @click="onCancel">取消</button>
      </div>
    </form>
  `,
};
