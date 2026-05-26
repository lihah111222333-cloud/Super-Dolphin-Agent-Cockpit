export const DagScheduleModal = {
  name: 'DagScheduleModal',
  props: {
    open: { type: Boolean, default: false },
    title: { type: String, default: '' },
    actionLabel: { type: String, default: '创建定时任务' },
    preset: { type: String, default: 'daily' },
    time: { type: String, default: '08:00' },
    weekday: { type: String, default: '1' },
    monthDay: { type: String, default: '1' },
    previewText: { type: String, default: '' },
    inputError: { type: String, default: '' },
    scheduleErrorText: { type: String, default: '' },
    saving: { type: Boolean, default: false },
  },
  emits: ['update-preset', 'update-time', 'update-weekday', 'update-month-day', 'cancel', 'confirm'],
  setup(_props, { emit }) {
    const weekdays = [
      { value: '1', label: '周一' },
      { value: '2', label: '周二' },
      { value: '3', label: '周三' },
      { value: '4', label: '周四' },
      { value: '5', label: '周五' },
      { value: '6', label: '周六' },
      { value: '7', label: '周日' },
    ];
    const monthDays = Array.from({ length: 31 }, (_item, index) => (index + 1).toString());
    function updatePreset(event) {
      emit('update-preset', event?.target?.value || '');
    }
    function updateTime(event) {
      emit('update-time', event?.target?.value || '');
    }
    function updateWeekday(event) {
      emit('update-weekday', event?.target?.value || '');
    }
    function updateMonthDay(event) {
      emit('update-month-day', event?.target?.value || '');
    }
    return { monthDays, updateMonthDay, updatePreset, updateTime, updateWeekday, weekdays };
  },
  template: `
    <div v-if="open" class="modal-overlay dag-delete-overlay" data-testid="dag-schedule-overlay" @click.self="$emit('cancel')">
      <div class="modal-box dag-delete-modal" role="dialog" aria-modal="true" data-testid="dag-schedule-modal">
        <div class="dag-delete-modal-head">
          <div>
            <div class="dag-delete-modal-title">{{ actionLabel }}</div>
            <div class="dag-delete-modal-tip">{{ title }}</div>
          </div>
          <button
            type="button"
            class="btn btn-ghost"
            data-testid="dag-schedule-close"
            :disabled="saving"
            @click="$emit('cancel')"
          >关闭</button>
        </div>
        <div class="dag-schedule-fields">
          <label class="dag-schedule-field">
            <span>运行频率</span>
            <select
              :value="preset"
              data-testid="dag-schedule-preset"
              :disabled="saving"
              @change="updatePreset"
            >
              <option value="daily">每天</option>
              <option value="weekdays">工作日</option>
              <option value="weekly">每周</option>
              <option value="monthly">每月</option>
            </select>
          </label>
          <label v-if="preset === 'weekly'" class="dag-schedule-field">
            <span>星期几</span>
            <select
              :value="weekday"
              data-testid="dag-schedule-weekday"
              :disabled="saving"
              @change="updateWeekday"
            >
              <option v-for="item in weekdays" :key="item.value" :value="item.value">{{ item.label }}</option>
            </select>
          </label>
          <label v-if="preset === 'monthly'" class="dag-schedule-field">
            <span>每月几号</span>
            <select
              :value="monthDay"
              data-testid="dag-schedule-month-day"
              :disabled="saving"
              @change="updateMonthDay"
            >
              <option v-for="day in monthDays" :key="day" :value="day">{{ day }} 日</option>
            </select>
          </label>
          <label class="dag-schedule-field">
            <span>运行时间</span>
            <input
              :value="time"
              type="time"
              autocomplete="off"
              data-testid="dag-schedule-time-input"
              :disabled="saving"
              @input="updateTime"
            />
          </label>
        </div>
        <div v-if="previewText" class="dag-schedule-preview" data-testid="dag-schedule-preview">{{ previewText }}</div>
        <div v-if="inputError" class="dag-console-error-inline" data-testid="dag-schedule-input-error">{{ inputError }}</div>
        <div v-if="scheduleErrorText" class="dag-console-error-inline" data-testid="dag-schedule-modal-error">{{ scheduleErrorText }}</div>
        <div class="dag-delete-modal-actions">
          <button
            type="button"
            class="btn btn-ghost"
            data-testid="dag-schedule-cancel"
            :disabled="saving"
            @click="$emit('cancel')"
          >取消</button>
          <button
            type="button"
            class="btn btn-primary"
            data-testid="dag-schedule-confirm"
            :disabled="saving"
            @click="$emit('confirm')"
          >{{ saving ? '保存中' : actionLabel }}</button>
        </div>
      </div>
    </div>
  `,
};
