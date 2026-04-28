import { ref, computed, watch } from '../../lib/vue.esm-browser.prod.js';
import { logDebug } from '../services/log.js';

// CronJobModal — create / edit form for cronjob/* RPC.
//
// Required backend fields (see internal/module/cron/service.go validateCreate):
//   - name, prompt, schedule_expr, cwd
//   - provider must be "codex" (v1 whitelist)
//   - config must contain a resolvable codex identity triple
//     (codexHome / codexInstanceKey / codexModelProvider).
//
// The form keeps the codex identity fields as first-class inputs and exposes
// an "extra config (JSON)" textarea for anything else the caller wants to
// merge into config — kept additive so we never silently drop keys the
// backend may rely on later.

function buildEmptyForm(defaults) {
  return {
    name: '',
    prompt: '',
    schedule_expr: '*/5 * * * *',
    schedule_type: 'cron',
    timezone: '',
    provider: 'codex',
    model: '',
    cwd: defaults?.cwd || '',
    codexHome: defaults?.codexHome || '',
    codexInstanceKey: defaults?.codexInstanceKey || '',
    codexModelProvider: defaults?.codexModelProvider || '',
    extraConfig: '',
    skills: '',
    notify_channel: '',
    enabled: true,
    next_run_at: '',
    max_attempts: 0,
  };
}

function jobToForm(job, defaults) {
  const cfg = (job && typeof job.config === 'object' && job.config) ? job.config : {};
  const codexHome = (cfg.codexHome ?? '').toString();
  const codexInstanceKey = (cfg.codexInstanceKey ?? '').toString();
  const codexModelProvider = (cfg.codexModelProvider ?? '').toString();
  const extra = {};
  for (const [k, v] of Object.entries(cfg)) {
    if (k === 'codexHome' || k === 'codexInstanceKey' || k === 'codexModelProvider') continue;
    extra[k] = v;
  }
  return {
    name: (job?.name || '').toString(),
    prompt: (job?.prompt || '').toString(),
    schedule_expr: (job?.schedule_expr || '*/5 * * * *').toString(),
    schedule_type: (job?.schedule_type || 'cron').toString(),
    timezone: (job?.timezone || '').toString(),
    provider: (job?.provider || 'codex').toString(),
    model: (job?.model || '').toString(),
    cwd: (job?.cwd || defaults?.cwd || '').toString(),
    codexHome: codexHome || (defaults?.codexHome || ''),
    codexInstanceKey: codexInstanceKey || (defaults?.codexInstanceKey || ''),
    codexModelProvider: codexModelProvider || (defaults?.codexModelProvider || ''),
    extraConfig: Object.keys(extra).length === 0 ? '' : JSON.stringify(extra, null, 2),
    skills: Array.isArray(job?.skills) ? job.skills.join(', ') : '',
    notify_channel: (job?.notify_channel || '').toString(),
    enabled: job?.enabled !== false,
    next_run_at: (job?.next_run_at || '').toString(),
    max_attempts: Number.isFinite(job?.max_attempts) ? job.max_attempts : 0,
  };
}

export const CronJobModal = {
  name: 'CronJobModal',
  props: {
    show: { type: Boolean, default: false },
    mode: { type: String, default: 'create' },
    job: { type: Object, default: null },
    defaults: { type: Object, default: () => ({}) },
    submitting: { type: Boolean, default: false },
    errorText: { type: String, default: '' },
  },
  emits: ['close', 'submit'],
  setup(props, { emit }) {
    const form = ref(buildEmptyForm(props.defaults));
    const localError = ref('');

    watch(
      () => [props.show, props.job, props.mode],
      () => {
        localError.value = '';
        if (!props.show) return;
        if (props.mode === 'edit' && props.job) {
          form.value = jobToForm(props.job, props.defaults);
        } else {
          form.value = buildEmptyForm(props.defaults);
        }
      },
      { immediate: true },
    );

    const title = computed(() => (props.mode === 'edit' ? '编辑定时任务' : '新建定时任务'));

    function close() {
      logDebug('ui', 'cronModal.close', {});
      emit('close');
    }

    function submit() {
      localError.value = '';
      const f = form.value;
      const name = (f.name || '').trim();
      const prompt = (f.prompt || '').trim();
      const scheduleExpr = (f.schedule_expr || '').trim();
      const cwd = (f.cwd || '').trim();
      const codexHome = (f.codexHome || '').trim();
      if (!name) { localError.value = '名称必填'; return; }
      if (!prompt) { localError.value = '提示词必填'; return; }
      if (!scheduleExpr) { localError.value = 'cron 表达式必填'; return; }
      if (!cwd) { localError.value = 'CWD 必填'; return; }
      if (!codexHome) { localError.value = 'codexHome 必填（codex 身份三元组）'; return; }

      let extraConfig = {};
      const extraRaw = (f.extraConfig || '').trim();
      if (extraRaw) {
        try {
          const parsed = JSON.parse(extraRaw);
          if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
            throw new Error('extra config 必须是对象');
          }
          extraConfig = parsed;
        } catch (err) {
          localError.value = '附加 config JSON 解析失败：' + (err?.message || String(err));
          return;
        }
      }

      const config = {
        ...extraConfig,
        codexHome,
      };
      const codexInstanceKey = (f.codexInstanceKey || '').trim();
      const codexModelProvider = (f.codexModelProvider || '').trim();
      if (codexInstanceKey) config.codexInstanceKey = codexInstanceKey;
      if (codexModelProvider) config.codexModelProvider = codexModelProvider;

      const skills = (f.skills || '')
        .split(',')
        .map((s) => s.trim())
        .filter((s) => s !== '');

      const payload = {
        name,
        prompt,
        schedule_expr: scheduleExpr,
        schedule_type: (f.schedule_type || 'cron').trim() || 'cron',
        timezone: (f.timezone || '').trim(),
        provider: (f.provider || 'codex').trim() || 'codex',
        model: (f.model || '').trim(),
        cwd,
        config,
        skills,
        notify_channel: (f.notify_channel || '').trim(),
        enabled: f.enabled !== false,
        max_attempts: Math.max(0, Number(f.max_attempts) || 0),
      };
      const nextRunAt = (f.next_run_at || '').trim();
      if (nextRunAt) payload.next_run_at = nextRunAt;

      logDebug('ui', 'cronModal.submit', { mode: props.mode, name });
      emit('submit', payload);
    }

    return { form, localError, title, close, submit };
  },
  template: `
    <div v-if="show" class="modal-overlay" @click.self="close">
      <div class="modal-box cron-modal-box" data-testid="cron-job-modal">
        <div class="modal-title">{{ title }}</div>
        <div class="cron-form">
          <div class="cron-form-row">
            <label>名称 *</label>
            <input class="modal-input" v-model="form.name" placeholder="每日报表" data-testid="cron-input-name" />
          </div>
          <div class="cron-form-row">
            <label>Cron 表达式 *</label>
            <input class="modal-input" v-model="form.schedule_expr" placeholder="*/5 * * * *" data-testid="cron-input-schedule" />
          </div>
          <div class="cron-form-row cron-form-row-2col">
            <div>
              <label>时区</label>
              <input class="modal-input" v-model="form.timezone" placeholder="Asia/Shanghai" />
            </div>
            <div>
              <label>下次运行 (RFC3339, 可选)</label>
              <input class="modal-input" v-model="form.next_run_at" placeholder="2026-04-28T10:00:00Z" />
            </div>
          </div>
          <div class="cron-form-row">
            <label>Prompt *</label>
            <textarea class="modal-input cron-textarea" v-model="form.prompt" rows="4" placeholder="任务每次触发时发送给 agent 的提示词" data-testid="cron-input-prompt"></textarea>
          </div>
          <div class="cron-form-row cron-form-row-2col">
            <div>
              <label>CWD *</label>
              <input class="modal-input" v-model="form.cwd" placeholder="/Users/you/projects/foo" data-testid="cron-input-cwd" />
            </div>
            <div>
              <label>Provider</label>
              <input class="modal-input" v-model="form.provider" placeholder="codex" disabled />
            </div>
          </div>
          <div class="cron-form-row cron-form-row-2col">
            <div>
              <label>Model</label>
              <input class="modal-input" v-model="form.model" placeholder="gpt-5.5" />
            </div>
            <div>
              <label>Notify Channel</label>
              <input class="modal-input" v-model="form.notify_channel" placeholder="bus / 空" />
            </div>
          </div>
          <div class="cron-form-row">
            <label>codexHome *</label>
            <input class="modal-input" v-model="form.codexHome" placeholder="/Users/you/.codex" data-testid="cron-input-codex-home" />
          </div>
          <div class="cron-form-row cron-form-row-2col">
            <div>
              <label>codexInstanceKey</label>
              <input class="modal-input" v-model="form.codexInstanceKey" placeholder="glm" />
            </div>
            <div>
              <label>codexModelProvider</label>
              <input class="modal-input" v-model="form.codexModelProvider" placeholder="codex" />
            </div>
          </div>
          <div class="cron-form-row">
            <label>附加 config (JSON, 可选)</label>
            <textarea class="modal-input cron-textarea" v-model="form.extraConfig" rows="3" placeholder='{"foo": "bar"}'></textarea>
          </div>
          <div class="cron-form-row cron-form-row-2col">
            <div>
              <label>Skills (逗号分隔)</label>
              <input class="modal-input" v-model="form.skills" placeholder="lint, test" />
            </div>
            <div>
              <label>最大重试 (max_attempts)</label>
              <input class="modal-input" type="number" min="0" v-model.number="form.max_attempts" />
            </div>
          </div>
          <div class="cron-form-row">
            <label class="cron-form-checkbox">
              <input type="checkbox" v-model="form.enabled" />
              <span>启用</span>
            </label>
          </div>
        </div>
        <div v-if="localError || errorText" class="cron-form-error" data-testid="cron-form-error">
          {{ localError || errorText }}
        </div>
        <div class="modal-btns">
          <button class="btn btn-ghost" :disabled="submitting" @click="close" data-testid="cron-modal-cancel">取消</button>
          <button class="btn btn-primary" :disabled="submitting" @click="submit" data-testid="cron-modal-submit">
            {{ submitting ? '保存中…' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  `,
};
