import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { CronJobForm as VueComp } from './CronJobForm.js';
import { ScheduleField } from './ScheduleField.jsx';
import { ProviderIdentityField } from './ProviderIdentityField.jsx';

export function CronJobForm(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const fieldErrors = vm.fieldErrors; // Reactive object
  const form = vm.form; // Reactive object
  const submitting = val(vm.submitting);
  const submitError = val(vm.submitError);
  const isEdit = val(vm.isEdit);
  const submitLabel = val(vm.submitLabel);

  if (!form) return null;

  return (
    <form className="cron-job-form" data-testid="cron-job-form" onSubmit={(e) => { e.preventDefault(); vm.submit(); }}>
      <h3>{isEdit ? '编辑定时任务' : '新建定时任务'}</h3>

      <div className="form-row">
        <label>名称 *</label>
        <input
          type="text"
          value={form.name}
          data-testid="cron-form-name"
          className={fieldErrors.name ? 'error' : ''}
          onChange={(e) => { form.name = e.target.value; }}
        />
        {fieldErrors.name && (
          <div className="form-error" data-testid="cron-form-name-error">
            {fieldErrors.name}
          </div>
        )}
      </div>

      <div className="form-row">
        <label>Prompt *</label>
        <textarea
          value={form.prompt}
          rows={4}
          data-testid="cron-form-prompt"
          className={fieldErrors.prompt ? 'error' : ''}
          onChange={(e) => { form.prompt = e.target.value; }}
        ></textarea>
        {fieldErrors.prompt && <div className="form-error">{fieldErrors.prompt}</div>}
      </div>

      <div className="form-row">
        <label>工作目录 (CWD) *</label>
        <div className="cwd-row">
          <input
            type="text"
            value={form.cwd}
            data-testid="cron-form-cwd"
            className={fieldErrors.cwd ? 'error' : ''}
            onChange={(e) => { form.cwd = e.target.value; }}
          />
          <button type="button" className="btn btn-ghost btn-xs" data-testid="cron-form-cwd-pick" onClick={vm.pickCwd}>
            选择…
          </button>
        </div>
        {fieldErrors.cwd && <div className="form-error">{fieldErrors.cwd}</div>}
      </div>

      <ScheduleField modelValue={form.schedule} onChange={(val) => { form.schedule = val; }} />
      {fieldErrors.schedule_expr && (
        <div className="form-error" data-testid="cron-form-schedule-error">
          {fieldErrors.schedule_expr}
        </div>
      )}

      <ProviderIdentityField modelValue={form.identity} onChange={(val) => { form.identity = val; }} />
      {fieldErrors.codex_identity && (
        <div className="form-error" data-testid="cron-form-identity-error">
          {fieldErrors.codex_identity}
        </div>
      )}
      {fieldErrors.provider && <div className="form-error">{fieldErrors.provider}</div>}

      <div className="form-row">
        <label>Skills（逗号分隔）</label>
        <input
          type="text"
          value={form.skills}
          data-testid="cron-form-skills"
          onChange={(e) => { form.skills = e.target.value; }}
        />
      </div>

      <div className="form-row">
        <label>通知渠道</label>
        <input
          type="text"
          value={form.notify_channel}
          placeholder="留空=失败时不通知"
          data-testid="cron-form-notify"
          onChange={(e) => { form.notify_channel = e.target.value; }}
        />
      </div>

      <div className="form-row">
        <label>重试预算</label>
        <input
          type="number"
          value={form.max_attempts}
          min="0"
          data-testid="cron-form-max-attempts"
          className={fieldErrors.max_attempts ? 'error' : ''}
          onChange={(e) => { form.max_attempts = e.target.value === '' ? '' : Number(e.target.value); }}
        />
        <small>0 = 不重试，失败后等下次 schedule</small>
        {fieldErrors.max_attempts && <div className="form-error">{fieldErrors.max_attempts}</div>}
      </div>

      <div className="form-row">
        <label>
          <input
            type="checkbox"
            checked={!!form.enabled}
            data-testid="cron-form-enabled"
            onChange={(e) => { form.enabled = e.target.checked; }}
          />
          启用
        </label>
      </div>

      {submitError && (
        <div className="form-error" data-testid="cron-form-submit-error">
          {submitError}
        </div>
      )}

      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={submitting} data-testid="cron-form-submit">
          {submitLabel}
        </button>
        <button type="button" className="btn btn-ghost" data-testid="cron-form-cancel" onClick={vm.onCancel}>
          取消
        </button>
      </div>
    </form>
  );
}
