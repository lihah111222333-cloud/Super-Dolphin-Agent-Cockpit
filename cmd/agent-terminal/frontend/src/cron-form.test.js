// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
  registerLogBridgeSink: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  selectProjectDir: vi.fn().mockResolvedValue('/picked/path'),
}));

vi.mock('./services/cron-api.js', () => ({
  mapCronRpcError: vi.fn((err) => ({
    code: 0,
    kind: (err && err.kindOverride) || 'unknown',
    message: (err && err.message) || String(err || ''),
  })),
}));

vi.mock('./stores/cron.js', () => ({
  useCronStore: () => ({
    createJob: vi.fn(),
    updateJob: vi.fn(),
  }),
}));

import { previewNextRuns, browserTimezone } from './components/cron/ScheduleField.js';
import { validateForm, CronJobForm } from './components/cron/CronJobForm.js';

function fullValidForm(overrides = {}) {
  return {
    name: 'demo',
    prompt: 'hello',
    cwd: '/repo',
    schedule: { schedule_expr: '0 9 * * *', timezone: 'Asia/Seoul' },
    identity: {
      provider: 'codex',
      model: '',
      config: {
        codexHome: '/Users/demo/.codex-providers/glm',
        codexInstanceKey: 'glm',
        codexModelProvider: 'glm-compat',
      },
    },
    skills: '',
    notify_channel: '',
    enabled: true,
    max_attempts: 0,
    ...overrides,
  };
}

describe('ScheduleField cron-parser preview', () => {
  it('returns 5 future dates for a valid cron expression', () => {
    const out = previewNextRuns('0 9 * * *', 'Asia/Seoul', 5);
    expect(out.error).toBeUndefined();
    expect(out.dates).toHaveLength(5);
    for (const d of out.dates) {
      expect(d).toBeInstanceOf(Date);
    }
    // each successive date must strictly increase
    for (let i = 1; i < out.dates.length; i += 1) {
      expect(out.dates[i].getTime()).toBeGreaterThan(out.dates[i - 1].getTime());
    }
  });

  it('reports parse errors for invalid expressions', () => {
    expect(previewNextRuns('not a cron', 'UTC').error).toBeTruthy();
    expect(previewNextRuns('', 'UTC').error).toBeTruthy();
  });

  it('browserTimezone returns a non-empty string', () => {
    expect(typeof browserTimezone()).toBe('string');
    expect(browserTimezone().length).toBeGreaterThan(0);
  });
});

describe('CronJobForm validateForm', () => {
  it('passes a fully valid form', () => {
    expect(validateForm(fullValidForm())).toEqual({});
  });

  it('flags missing required text fields', () => {
    const errs = validateForm(fullValidForm({ name: '', prompt: '', cwd: '' }));
    expect(errs.name).toBeTruthy();
    expect(errs.prompt).toBeTruthy();
    expect(errs.cwd).toBeTruthy();
  });

  it('flags invalid cron expression', () => {
    const errs = validateForm(fullValidForm({
      schedule: { schedule_expr: 'garbage', timezone: 'UTC' },
    }));
    expect(errs.schedule_expr).toBeTruthy();
  });

  it('requires all three codex identity fields when provider=codex', () => {
    const errs = validateForm(fullValidForm({
      identity: {
        provider: 'codex',
        model: '',
        config: { codexHome: '/x', codexInstanceKey: '', codexModelProvider: 'p' },
      },
    }));
    expect(errs.codex_identity).toBeTruthy();
  });

  it('rejects negative max_attempts', () => {
    const errs = validateForm(fullValidForm({ max_attempts: -1 }));
    expect(errs.max_attempts).toBeTruthy();
  });
});

describe('CronJobForm contract', () => {
  it('exports the expected component shape', () => {
    expect(CronJobForm.name).toBe('CronJobForm');
    expect(CronJobForm.template).toContain('cron-job-form');
    expect(CronJobForm.template).toContain('cron-form-name');
    expect(CronJobForm.template).toContain('cron-form-cwd');
    expect(CronJobForm.template).toContain('cron-form-submit');
    expect(CronJobForm.template).toContain('cron-form-cancel');
    expect(CronJobForm.components).toHaveProperty('ScheduleField');
    expect(CronJobForm.components).toHaveProperty('ProviderIdentityField');
  });
});
