import { computed, ref } from '../../lib/vue.esm-browser.prod.js';

function textValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined) continue;
    const text = value.toString().trim();
    if (text) return text;
  }
  return '-';
}

function normalizedValue(...values) {
  const text = textValue(...values);
  return text === '-' ? '' : text.toLowerCase();
}

export function isScheduledTrigger(trigger) {
  return ['scheduled', 'schedule', 'cron'].includes((trigger || '').toString().trim().toLowerCase());
}

export function cronExprFromDagItem(item) {
  const trigger = item?.trigger || item?.trigger_config || item?.triggerConfig;
  const schedule = textValue(
    trigger?.schedule,
    trigger?.cron,
    trigger?.expression,
    item?.schedule,
    item?.cron,
    item?.cron_expr,
    item?.cronExpr,
  );
  return schedule === '-' ? '' : schedule;
}

export function scheduleEnabledFromDagItem(item) {
  const trigger = item?.trigger || item?.trigger_config || item?.triggerConfig;
  const nextRunAt = textValue(
    trigger?.next_run_at,
    trigger?.nextRunAt,
    item?.next_run_at,
    item?.nextRunAt,
  );
  return nextRunAt !== '-';
}

function cronExprOf(items) {
  for (const item of items) {
    const value = cronExprFromDagItem(item);
    if (value) return value;
  }
  return '';
}

function scheduleEnabledOf(items) {
  return items.some((item) => scheduleEnabledFromDagItem(item));
}

const DEFAULT_SCHEDULE = { preset: 'daily', time: '08:00', weekday: '1', monthDay: '1' };
const SCHEDULE_PRESET_LABELS = {
  daily: '每天',
  weekdays: '工作日',
  weekly: '每周',
  monthly: '每月',
};
const WEEKDAY_LABELS = {
  1: '周一',
  2: '周二',
  3: '周三',
  4: '周四',
  5: '周五',
  6: '周六',
  7: '周日',
};

function twoDigits(value) {
  return value.toString().padStart(2, '0');
}

function parseScheduleTime(value) {
  const text = (value || '').toString().trim();
  const match = /^(\d{1,2}):(\d{2})$/.exec(text);
  if (!match) return null;
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > 23 || minute < 0 || minute > 59) {
    return null;
  }
  return { hour, minute, label: `${twoDigits(hour)}:${twoDigits(minute)}` };
}

function scheduleStateFromCron(cronExpr) {
  const text = (cronExpr || '').toString().trim();
  if (!text) return { ...DEFAULT_SCHEDULE, warning: '' };
  const parts = text.split(/\s+/);
  if (parts.length !== 5) return { ...DEFAULT_SCHEDULE, warning: '已有计划格式无法识别，请重新选择运行频率和时间。' };
  const [minuteText, hourText, dayOfMonth, month, dayOfWeek] = parts;
  const hour = Number(hourText);
  const minute = Number(minuteText);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > 23 || minute < 0 || minute > 59) {
    return { ...DEFAULT_SCHEDULE, warning: '已有计划格式无法识别，请重新选择运行频率和时间。' };
  }
  const time = `${twoDigits(hour)}:${twoDigits(minute)}`;
  if (dayOfMonth === '*' && month === '*' && dayOfWeek === '1-5') return { ...DEFAULT_SCHEDULE, preset: 'weekdays', time };
  if (dayOfMonth === '*' && month === '*' && Object.prototype.hasOwnProperty.call(WEEKDAY_LABELS, dayOfWeek)) {
    return { ...DEFAULT_SCHEDULE, preset: 'weekly', weekday: dayOfWeek, time };
  }
  const monthDay = Number(dayOfMonth);
  if (Number.isInteger(monthDay) && monthDay >= 1 && monthDay <= 31 && month === '*' && dayOfWeek === '*') {
    return { ...DEFAULT_SCHEDULE, preset: 'monthly', monthDay: monthDay.toString(), time };
  }
  if (dayOfMonth === '*' && month === '*' && dayOfWeek === '*') return { ...DEFAULT_SCHEDULE, preset: 'daily', time };
  return { ...DEFAULT_SCHEDULE, warning: '已有计划超出简化设置范围，请重新选择运行频率和时间。' };
}

function scheduleLabel(schedule) {
  const parsed = parseScheduleTime(schedule?.time);
  if (!parsed) return '';
  switch (schedule?.preset) {
    case 'daily':
      return `每天 ${parsed.label}`;
    case 'weekdays':
      return `工作日 ${parsed.label}`;
    case 'weekly':
      return `${WEEKDAY_LABELS[schedule.weekday] ? `每${WEEKDAY_LABELS[schedule.weekday]}` : '每周'} ${parsed.label}`;
    case 'monthly':
      return `每月 ${schedule.monthDay || DEFAULT_SCHEDULE.monthDay} 日 ${parsed.label}`;
    default:
      return '';
  }
}

export function scheduleLabelFromCron(cronExpr) {
  if (!(cronExpr || '').toString().trim()) return '';
  const state = scheduleStateFromCron(cronExpr);
  if (state.warning) return '';
  return scheduleLabel(state);
}

export function scheduleLabelFromDagItem(item) {
  return scheduleLabelFromCron(cronExprFromDagItem(item));
}

export function scheduledTaskStatusLabel(status, trigger, item = null) {
  if (!isScheduledTrigger(trigger)) return '';
  const normalizedStatus = normalizedValue(status);
  if (normalizedStatus === 'running') return '运行中';
  if (item && cronExprFromDagItem(item)) {
    return scheduleEnabledFromDagItem(item) ? '已启用' : '已暂停';
  }
  if (['ready', 'queued', 'starting'].includes(normalizedStatus)) return '已启用';
  if (['draft', 'pending', 'idle'].includes(normalizedStatus)) return '未启用';
  return '';
}

function cronExprFromSchedule(preset, time, weekday, monthDay) {
  const parsed = parseScheduleTime(time);
  if (!parsed) return { cronExpr: '', error: '请选择运行时间' };
  const minute = parsed.minute.toString();
  const hour = parsed.hour.toString();
  switch (preset) {
    case 'daily':
      return { cronExpr: `${minute} ${hour} * * *`, error: '' };
    case 'weekdays':
      return { cronExpr: `${minute} ${hour} * * 1-5`, error: '' };
    case 'weekly':
      if (!Object.prototype.hasOwnProperty.call(WEEKDAY_LABELS, weekday)) return { cronExpr: '', error: '请选择星期几' };
      return { cronExpr: `${minute} ${hour} * * ${weekday}`, error: '' };
    case 'monthly':
      if (!/^\d+$/.test((monthDay || '').toString())) return { cronExpr: '', error: '请选择每月几号' };
      {
        const day = Number(monthDay);
        if (!Number.isInteger(day) || day < 1 || day > 31) return { cronExpr: '', error: '请选择每月几号' };
        return { cronExpr: `${minute} ${hour} ${day} * *`, error: '' };
      }
    default:
      return { cronExpr: '', error: '请选择运行频率' };
  }
}

export function useDagScheduleAction({
  props,
  detailState,
  selectedRow,
  selectedDagKey,
  selectedDagItems,
  dagDetail,
  emit,
  startableStatuses,
  dagStatusOf,
  dagTriggerOf,
  hasActiveRun,
}) {
  const scheduleConfirmOpen = ref(false);
  const schedulePreset = ref(DEFAULT_SCHEDULE.preset);
  const scheduleTime = ref(DEFAULT_SCHEDULE.time);
  const scheduleWeekday = ref(DEFAULT_SCHEDULE.weekday);
  const scheduleMonthDay = ref(DEFAULT_SCHEDULE.monthDay);
  const scheduleInputError = ref('');
  const scheduleNeedsChoice = ref(false);
  const scheduleTrigger = computed(() => dagTriggerOf(selectedDagItems.value));
  const scheduleCronExpr = computed(() => cronExprOf(selectedDagItems.value));
  const scheduleEnabled = computed(() => scheduleEnabledOf(selectedDagItems.value));
  const scheduleCanEditTrigger = computed(() => ['manual', 'scheduled', 'schedule', 'cron'].includes(scheduleTrigger.value));
  const scheduleActionLabel = computed(() => (['scheduled', 'schedule', 'cron'].includes(scheduleTrigger.value) ? '修改计划' : '创建定时任务'));
  const scheduleToggleVisible = computed(() => isScheduledTrigger(scheduleTrigger.value) && Boolean(scheduleCronExpr.value));
  const scheduleToggleLabel = computed(() => (scheduleEnabled.value ? '暂停自动运行' : '启用自动运行'));
  const scheduleDisabledReason = computed(() => {
    if (!selectedRow.value || !selectedDagKey.value) return '未选择任务流程';
    if (props.loading || detailState.loading) return '任务流程详情加载中';
    if (detailState.error) return '任务流程详情不可用，不能设置定时';
    if (detailState.runsError) return '运行历史不可用，不能设置定时';
    if (detailState.scheduling) return '保存中';
    if (hasActiveRun(selectedDagItems.value, detailState, selectedDagKey.value)) return '已有运行正在进行，不能设置定时';
    const status = dagStatusOf(selectedDagItems.value);
    if (!startableStatuses.has(status)) return '当前流程状态不可设置定时';
    if (!scheduleCanEditTrigger.value) return '当前触发方式不可设置定时';
    return '';
  });
  const scheduleToggleDisabledReason = computed(() => {
    if (!scheduleToggleVisible.value) return '当前任务没有运行计划';
    return scheduleDisabledReason.value;
  });
  const scheduleActionVisible = computed(() => (
    startableStatuses.has(dagStatusOf(selectedDagItems.value))
      && scheduleCanEditTrigger.value
  ));
  const scheduleErrorText = computed(() => (detailState.scheduleError ? '设置定时任务失败，请稍后重试。' : ''));
  const schedulePreviewText = computed(() => {
    const label = scheduleLabel({
      preset: schedulePreset.value,
      time: scheduleTime.value,
      weekday: scheduleWeekday.value,
      monthDay: scheduleMonthDay.value,
    });
    return label ? `${label} 自动运行` : '';
  });

  function openScheduleModal() {
    scheduleInputError.value = '';
    if (scheduleDisabledReason.value) return;
    const next = scheduleStateFromCron(cronExprOf(selectedDagItems.value));
    schedulePreset.value = next.preset;
    scheduleTime.value = next.time;
    scheduleWeekday.value = next.weekday;
    scheduleMonthDay.value = next.monthDay;
    scheduleNeedsChoice.value = Boolean(next.warning);
    scheduleInputError.value = next.warning || '';
    scheduleConfirmOpen.value = true;
  }

  function cancelScheduleDAG() {
    if (detailState.scheduling) return;
    scheduleConfirmOpen.value = false;
    scheduleInputError.value = '';
    scheduleNeedsChoice.value = false;
  }

  async function confirmScheduleSelectedDag() {
    scheduleInputError.value = '';
    if (scheduleDisabledReason.value) return;
    if (scheduleNeedsChoice.value) {
      scheduleInputError.value = '请先选择运行频率或运行时间';
      return;
    }
    const { cronExpr, error } = cronExprFromSchedule(
      schedulePreset.value,
      scheduleTime.value,
      scheduleWeekday.value,
      scheduleMonthDay.value,
    );
    if (error) {
      scheduleInputError.value = error;
      return;
    }
    const result = await dagDetail.setSchedule({ cronExpr });
    if (result?.ok && !detailState.scheduleError) {
      scheduleConfirmOpen.value = false;
      emit('refresh-dags');
    }
  }

  async function toggleScheduleEnabled() {
    if (scheduleToggleDisabledReason.value) return;
    const result = await dagDetail.setScheduleEnabled({ enabled: !scheduleEnabled.value });
    if (result?.ok && !detailState.scheduleError) emit('refresh-dags');
  }

  function updateSchedulePreset(value) {
    schedulePreset.value = value;
    scheduleNeedsChoice.value = false;
  }

  function updateScheduleTime(value) {
    scheduleTime.value = value;
    scheduleNeedsChoice.value = false;
  }

  function updateScheduleWeekday(value) {
    scheduleWeekday.value = value;
    scheduleNeedsChoice.value = false;
  }

  function updateScheduleMonthDay(value) {
    scheduleMonthDay.value = value;
    scheduleNeedsChoice.value = false;
  }

  return {
    cancelScheduleDAG,
    confirmScheduleSelectedDag,
    openScheduleModal,
    scheduleActionVisible,
    scheduleConfirmOpen,
    scheduleDisabledReason,
    scheduleEnabled,
    scheduleErrorText,
    scheduleInputError,
    scheduleActionLabel,
    scheduleMonthDay,
    schedulePreset,
    schedulePreviewText,
    scheduleToggleDisabledReason,
    scheduleToggleLabel,
    scheduleToggleVisible,
    scheduleTime,
	    scheduleWeekday,
	    toggleScheduleEnabled,
	    updateScheduleMonthDay,
    updateSchedulePreset,
    updateScheduleTime,
    updateScheduleWeekday,
  };
}
