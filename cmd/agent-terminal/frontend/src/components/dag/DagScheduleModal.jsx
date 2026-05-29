import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { DagScheduleModal as VueComp } from './DagScheduleModal.js';

export function DagScheduleModal(props) {
  const emit = (event, ...args) => {
    // Map Vue emits (e.g. 'update-preset' -> 'onUpdatePreset')
    const parts = event.split('-');
    const camelCased = parts.map((part, i) => i === 0 ? part : part[0].toUpperCase() + part.slice(1)).join('');
    const propName = 'on' + camelCased[0].toUpperCase() + camelCased.slice(1);
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const weekdays = val(vm.weekdays) || [];
  const monthDays = val(vm.monthDays) || [];

  if (!props.open) return null;

  return (
    <div className="modal-overlay dag-delete-overlay" data-testid="dag-schedule-overlay" onClick={(e) => { if (e.target === e.currentTarget) props.onCancel?.(); }}>
      <div className="modal-box dag-delete-modal" role="dialog" aria-modal="true" data-testid="dag-schedule-modal">
        <div className="dag-delete-modal-head">
          <div>
            <div className="dag-delete-modal-title">{props.actionLabel}</div>
            <div className="dag-delete-modal-tip">{props.title}</div>
          </div>
          <button
            type="button"
            className="btn btn-ghost"
            data-testid="dag-schedule-close"
            disabled={props.saving}
            onClick={() => props.onCancel?.()}
          >
            关闭
          </button>
        </div>
        <div className="dag-schedule-fields">
          <label className="dag-schedule-field">
            <span>运行频率</span>
            <select
              value={props.preset}
              data-testid="dag-schedule-preset"
              disabled={props.saving}
              onChange={vm.updatePreset}
            >
              <option value="daily">每天</option>
              <option value="weekdays">工作日</option>
              <option value="weekly">每周</option>
              <option value="monthly">每月</option>
            </select>
          </label>
          {props.preset === 'weekly' && (
            <label className="dag-schedule-field">
              <span>星期几</span>
              <select
                value={props.weekday}
                data-testid="dag-schedule-weekday"
                disabled={props.saving}
                onChange={vm.updateWeekday}
              >
                {weekdays.map((item) => (
                  <option key={item.value} value={item.value}>{item.label}</option>
                ))}
              </select>
            </label>
          )}
          {props.preset === 'monthly' && (
            <label className="dag-schedule-field">
              <span>每月几号</span>
              <select
                value={props.monthDay}
                data-testid="dag-schedule-month-day"
                disabled={props.saving}
                onChange={vm.updateMonthDay}
              >
                {monthDays.map((day) => (
                  <option key={day} value={day}>{day} 日</option>
                ))}
              </select>
            </label>
          )}
          <label className="dag-schedule-field">
            <span>运行时间</span>
            <input
              value={props.time}
              type="time"
              autoComplete="off"
              data-testid="dag-schedule-time-input"
              disabled={props.saving}
              onInput={vm.updateTime}
            />
          </label>
        </div>
        {props.previewText && <div className="dag-schedule-preview" data-testid="dag-schedule-preview">{props.previewText}</div>}
        {props.inputError && <div className="dag-console-error-inline" data-testid="dag-schedule-input-error">{props.inputError}</div>}
        {props.scheduleErrorText && <div className="dag-console-error-inline" data-testid="dag-schedule-modal-error">{props.scheduleErrorText}</div>}
        <div className="dag-delete-modal-actions">
          <button
            type="button"
            className="btn btn-ghost"
            data-testid="dag-schedule-cancel"
            disabled={props.saving}
            onClick={() => props.onCancel?.()}
          >
            取消
          </button>
          <button
            type="button"
            className="btn btn-primary"
            data-testid="dag-schedule-confirm"
            disabled={props.saving}
            onClick={() => props.onConfirm?.()}
          >
            {props.saving ? '保存中' : props.actionLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
