import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { ScheduleField as VueComp, COMMON_TIMEZONES } from './ScheduleField.js';

export function ScheduleField(props) {
  const emit = (event, ...args) => {
    if (event === 'update:modelValue') {
      props.onChange?.(...args);
    }
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const modelValue = props.modelValue || { schedule_expr: '', timezone: '' };
  const errorMessage = val(vm.errorMessage);
  const formattedDates = val(vm.formattedDates) || [];

  return (
    <div className="schedule-field" data-testid="schedule-field">
      <div className="form-row">
        <label>Cron 表达式 *</label>
        <input
          type="text"
          value={modelValue.schedule_expr}
          placeholder="例如 0 9 * * *"
          data-testid="schedule-expr-input"
          onChange={(e) => vm.update({ schedule_expr: e.target.value })}
        />
      </div>
      <div className="form-row">
        <label>时区</label>
        <input
          type="text"
          list="schedule-tz-options"
          value={modelValue.timezone}
          placeholder={`例如 ${COMMON_TIMEZONES[0] || 'UTC'}`}
          data-testid="schedule-tz-input"
          onChange={(e) => vm.update({ timezone: e.target.value })}
        />
        <datalist id="schedule-tz-options">
          {COMMON_TIMEZONES.map((tz) => (
            <option key={tz} value={tz} />
          ))}
        </datalist>
      </div>
      {errorMessage ? (
        <div className="form-error" data-testid="schedule-error">
          {errorMessage}
        </div>
      ) : (
        <div className="schedule-preview" data-testid="schedule-preview">
          <div className="schedule-preview-title">未来 5 次触发：</div>
          <ul>
            {formattedDates.map((d, i) => (
              <li key={i}>{d}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
