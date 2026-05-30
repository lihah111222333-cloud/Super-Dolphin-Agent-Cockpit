// ScheduleField: cron 表达式 + 时区 + 未来 5 次本地预览。
// 之所以前端自己解析：host RPC (rpc.go:17-19) 当前不会从 schedule_expr
// 算 next_run_at；省略时退化为 now+1min。表单层必须显式传 next_run_at
// 才能让 cron 语义生效（phase 2b 后端补解析后可拿掉）。
import { computed } from '../../../lib/vue.esm-browser.prod.js';
import cronParser from 'cron-parser';

export const COMMON_TIMEZONES = Object.freeze([
  'UTC',
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Asia/Singapore',
  'Europe/London',
  'Europe/Berlin',
  'America/Los_Angeles',
  'America/New_York',
]);

export function browserTimezone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

// previewNextRuns returns up to `count` upcoming Date objects, or
// { error: string } when expr is invalid.
export function previewNextRuns(expr, tz, count = 5) {
  if (!expr || typeof expr !== 'string') {
    return { error: 'cron 表达式不能为空' };
  }
  try {
    const it = cronParser.parseExpression(expr, { tz: tz || 'UTC' });
    const dates = [];
    for (let i = 0; i < count; i += 1) {
      dates.push(it.next().toDate());
    }
    return { dates };
  } catch (err) {
    return { error: (err && err.message) || String(err) };
  }
}

export const ScheduleField = {
  name: 'ScheduleField',
  props: {
    modelValue: { type: Object, default: () => ({ schedule_expr: '', timezone: '' }) },
  },
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    function update(patch) {
      emit('update:modelValue', { ...props.modelValue, ...patch });
    }

    const preview = computed(() =>
      previewNextRuns(props.modelValue.schedule_expr, props.modelValue.timezone, 5),
    );

    const errorMessage = computed(() => preview.value.error || '');

    const formattedDates = computed(() => {
      if (!preview.value.dates) return [];
      const tz = props.modelValue.timezone || 'UTC';
      return preview.value.dates.map((d) => {
        try {
          return new Intl.DateTimeFormat('zh-CN', {
            timeZone: tz,
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit',
          }).format(d);
        } catch {
          return d.toISOString();
        }
      });
    });

    return {
      update,
      preview,
      errorMessage,
      formattedDates,
      timezones: COMMON_TIMEZONES,
    };
  },
  template: `
    <div class="schedule-field" data-testid="schedule-field">
      <div class="form-row">
        <label>Cron 表达式 *</label>
        <input
          type="text"
          :value="modelValue.schedule_expr"
          placeholder="例如 0 9 * * *"
          data-testid="schedule-expr-input"
          @input="update({ schedule_expr: $event.target.value })"
        />
      </div>
      <div class="form-row">
        <label>时区</label>
        <input
          type="text"
          list="schedule-tz-options"
          :value="modelValue.timezone"
          :placeholder="'例如 ' + (timezones[0] || 'UTC')"
          data-testid="schedule-tz-input"
          @input="update({ timezone: $event.target.value })"
        />
        <datalist id="schedule-tz-options">
          <option v-for="tz in timezones" :key="tz" :value="tz" />
        </datalist>
      </div>
      <div v-if="errorMessage" class="form-error" data-testid="schedule-error">
        {{ errorMessage }}
      </div>
      <div v-else class="schedule-preview" data-testid="schedule-preview">
        <div class="schedule-preview-title">未来 5 次触发：</div>
        <ul>
          <li v-for="(d, i) in formattedDates" :key="i">{{ d }}</li>
        </ul>
      </div>
    </div>
  `,
};
