import parser from 'cron-parser';
import { firstText, objectValue, textValue } from '../../shared/pageShared.js';

const DEFAULT_DAG_SCHEDULE = Object.freeze({ preset: 'daily', time: '08:00', weekday: '1', monthDay: '1' });
const MAX_SCHEDULE_HOUR = 23;
const MAX_SCHEDULE_MINUTE = 59;
const DAYS_IN_MONTH = 31;
const DAG_SCHEDULE_TIMEZONE = 'Asia/Shanghai';
const DAG_SCHEDULE_CRON_TZ_PREFIX = `CRON_TZ=${DAG_SCHEDULE_TIMEZONE}`;

const DAG_SCHEDULE_FORMAT_WARNING = '已有计划格式无法识别，请重新选择运行频率和时间。';

const DAG_SCHEDULE_RANGE_WARNING = '已有计划超出简化设置范围，请重新选择运行频率和时间。';

const DAG_WEEKDAY_OPTIONS = Object.freeze([
  { value: '1', label: '周一' },
  { value: '2', label: '周二' },
  { value: '3', label: '周三' },
  { value: '4', label: '周四' },
  { value: '5', label: '周五' },
  { value: '6', label: '周六' },
  { value: '7', label: '周日' },
]);

const DAG_WEEKDAY_LABELS = Object.freeze(Object.fromEntries(DAG_WEEKDAY_OPTIONS.map((item) => [item.value, item.label])));

function twoDigits(value) {
  return value.toString().padStart(2, '0');
}

function parseScheduleTime(value) {
  const text = textValue(value);
  const match = /^(\d{1,2}):(\d{2})$/.exec(text);
  if (!match) return null;
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > MAX_SCHEDULE_HOUR || minute < 0 || minute > MAX_SCHEDULE_MINUTE) {
    return null;
  }
  return { hour, minute, label: `${twoDigits(hour)}:${twoDigits(minute)}` };
}

function dagScheduleState(warning = '', patch = {}) {
  return { ...DEFAULT_DAG_SCHEDULE, ...patch, warning };
}

function isDagWeekday(value) {
  return Object.prototype.hasOwnProperty.call(DAG_WEEKDAY_LABELS, value);
}

function isMonthDayText(value) {
  const day = Number(value);
  return Number.isInteger(day) && day >= 1 && day <= DAYS_IN_MONTH;
}

function cronSchedulePartsWithTimezone(cronExpr) {
  const text = textValue(cronExpr);
  if (!text) return { cronText: '', timezone: DAG_SCHEDULE_TIMEZONE };
  const parts = text.split(/\s+/);
  const first = textValue(parts.at(0));
  if (first.startsWith('CRON_TZ=')) {
    return {
      cronText: parts.slice(1).join(' '),
      timezone: firstText(first.slice('CRON_TZ='.length), DAG_SCHEDULE_TIMEZONE),
    };
  }
  return { cronText: text, timezone: DAG_SCHEDULE_TIMEZONE };
}

function cronTextIsValid(cronText, timezone) {
  try {
    parser.parseExpression(cronText, { tz: timezone });
    return true;
  } catch {
    return false;
  }
}

function parseCronScheduleParts(cronExpr) {
  const { cronText: text, timezone } = cronSchedulePartsWithTimezone(cronExpr);
  if (!text) return { empty: true };
  const parts = text.split(/\s+/);
  if (parts.length !== 5) return { error: DAG_SCHEDULE_FORMAT_WARNING };
  if (!cronTextIsValid(text, timezone)) return { error: DAG_SCHEDULE_FORMAT_WARNING };
  const [minuteText, hourText, dayOfMonth, month, dayOfWeek] = parts;
  const hour = Number(hourText);
  const minute = Number(minuteText);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > MAX_SCHEDULE_HOUR || minute < 0 || minute > MAX_SCHEDULE_MINUTE) {
    return { error: DAG_SCHEDULE_RANGE_WARNING };
  }
  return { minute, hour, dayOfMonth, month, dayOfWeek, time: `${twoDigits(hour)}:${twoDigits(minute)}`, timezone };
}

function cronFieldMatches(expected, actual) {
  return typeof expected === 'function' ? expected(actual) : actual === expected;
}

function cronScheduleRuleMatches(rule, parsed) {
  return (
    cronFieldMatches(rule.dayOfMonth, parsed.dayOfMonth)
    && cronFieldMatches(rule.month, parsed.month)
    && cronFieldMatches(rule.dayOfWeek, parsed.dayOfWeek)
  );
}

const DAG_CRON_SCHEDULE_RULES = Object.freeze([
  { preset: 'weekdays', dayOfMonth: '*', month: '*', dayOfWeek: '1-5' },
  { preset: 'weekly', dayOfMonth: '*', month: '*', dayOfWeek: isDagWeekday, patch: (parsed) => ({ weekday: parsed.dayOfWeek }) },
  { preset: 'monthly', dayOfMonth: isMonthDayText, month: '*', dayOfWeek: '*', patch: (parsed) => ({ monthDay: Number(parsed.dayOfMonth).toString() }) },
  { preset: 'daily', dayOfMonth: '*', month: '*', dayOfWeek: '*' },
]);

function scheduleStateForCronRule(rule, parsed) {
  return dagScheduleState('', { preset: rule.preset, time: parsed.time, ...objectValue(rule.patch?.(parsed)) });
}

function scheduleStateFromCron(cronExpr) {
  const parsed = parseCronScheduleParts(cronExpr);
  if (parsed.empty) return dagScheduleState();
  if (parsed.error) return dagScheduleState(parsed.error);
  const rule = DAG_CRON_SCHEDULE_RULES.find((item) => cronScheduleRuleMatches(item, parsed));
  return rule ? scheduleStateForCronRule(rule, parsed) : dagScheduleState(DAG_SCHEDULE_RANGE_WARNING);
}

function scheduleLabelFromState(schedule) {
  const parsed = parseScheduleTime(schedule?.time);
  if (!parsed) return '';
  if (schedule?.preset === 'daily') return `每天 ${parsed.label}`;
  if (schedule?.preset === 'weekdays') return `工作日 ${parsed.label}`;
  if (schedule?.preset === 'weekly') return `${DAG_WEEKDAY_LABELS[schedule.weekday] ? `每${DAG_WEEKDAY_LABELS[schedule.weekday]}` : '每周'} ${parsed.label}`;
  if (schedule?.preset === 'monthly') return `每月 ${schedule.monthDay || DEFAULT_DAG_SCHEDULE.monthDay} 日 ${parsed.label}`;
  return '';
}

function scheduleLabelFromCron(cronExpr) {
  if (!textValue(cronExpr)) return '';
  const state = scheduleStateFromCron(cronExpr);
  if (state.warning) return '';
  return scheduleLabelFromState(state);
}

function cronExprFromSchedule(preset, time, weekday, monthDay) {
  const parsed = parseScheduleTime(time);
  if (!parsed) return { cronExpr: '', error: '请选择运行时间' };
  const minute = parsed.minute.toString();
  const hour = parsed.hour.toString();
  if (preset === 'daily') return { cronExpr: `${DAG_SCHEDULE_CRON_TZ_PREFIX} ${minute} ${hour} * * *`, error: '' };
  if (preset === 'weekdays') return { cronExpr: `${DAG_SCHEDULE_CRON_TZ_PREFIX} ${minute} ${hour} * * 1-5`, error: '' };
  if (preset === 'weekly') {
    if (!Object.prototype.hasOwnProperty.call(DAG_WEEKDAY_LABELS, weekday)) return { cronExpr: '', error: '请选择星期几' };
    return { cronExpr: `${DAG_SCHEDULE_CRON_TZ_PREFIX} ${minute} ${hour} * * ${weekday}`, error: '' };
  }
  if (preset === 'monthly') {
    const day = Number(monthDay);
    if (!Number.isInteger(day) || day < 1 || day > DAYS_IN_MONTH) return { cronExpr: '', error: '请选择每月几号' };
    return { cronExpr: `${DAG_SCHEDULE_CRON_TZ_PREFIX} ${minute} ${hour} ${day} * *`, error: '' };
  }
  return { cronExpr: '', error: '请选择运行频率' };
}

export {
  cronExprFromSchedule,
  DAG_SCHEDULE_FORMAT_WARNING,
  DAG_SCHEDULE_RANGE_WARNING,
  DAG_WEEKDAY_OPTIONS,
  DEFAULT_DAG_SCHEDULE,
  isDagWeekday,
  isMonthDayText,
  parseScheduleTime,
  scheduleLabelFromCron,
  scheduleLabelFromState,
  scheduleStateFromCron,
};
