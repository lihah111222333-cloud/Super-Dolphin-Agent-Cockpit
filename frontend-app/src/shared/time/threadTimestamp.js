const MIN_THREAD_EPOCH_MILLIS = 1_000_000_000_000;
const MAX_THREAD_EPOCH_MILLIS = 253_402_300_799_999;

function isLeapYear(year) {
  return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
}

function daysInMonth(year, month) {
  if (month === 2) return isLeapYear(year) ? 29 : 28;
  return [4, 6, 9, 11].includes(month) ? 30 : 31;
}

function civilFromDays(days) {
  const shifted = days + 719468;
  const era = Math.floor(shifted / 146097);
  const dayOfEra = shifted - era * 146097;
  const yearOfEra = Math.floor((dayOfEra - Math.floor(dayOfEra / 1460) + Math.floor(dayOfEra / 36524) - Math.floor(dayOfEra / 146096)) / 365);
  const yearDay = dayOfEra - (365 * yearOfEra + Math.floor(yearOfEra / 4) - Math.floor(yearOfEra / 100));
  const monthPrime = Math.floor((5 * yearDay + 2) / 153);
  const day = yearDay - Math.floor((153 * monthPrime + 2) / 5) + 1;
  const month = monthPrime + (monthPrime < 10 ? 3 : -9);
  const year = yearOfEra + era * 400 + (month <= 2 ? 1 : 0);
  return { year, month, day };
}

function pad(value, width) {
  return String(value).padStart(width, '0');
}

function isoTimestampFromEpochMillis(value) {
  const totalSeconds = Math.floor(value / 1000);
  const dayNumber = Math.floor(totalSeconds / 86400);
  const secondsOfDay = totalSeconds - (dayNumber * 86400);
  const date = civilFromDays(dayNumber);
  const hour = Math.floor(secondsOfDay / 3600);
  const minute = Math.floor((secondsOfDay % 3600) / 60);
  const second = secondsOfDay % 60;
  const milliseconds = value % 1000;
  return `${pad(date.year, 4)}-${pad(date.month, 2)}-${pad(date.day, 2)}T${pad(hour, 2)}:${pad(minute, 2)}:${pad(second, 2)}.${pad(milliseconds, 3)}Z`;
}

function validIsoTimestamp(text) {
  const match = text.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/);
  if (!match) return false;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const hour = Number(match[4]);
  const minute = Number(match[5]);
  const second = Number(match[6]);
  if (year < 1970 || month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month)) return false;
  if (hour > 23 || minute > 59 || second > 59) return false;
  if (match[7] !== 'Z') {
    const offsetHour = Number(match[7].slice(1, 3));
    const offsetMinute = Number(match[7].slice(4, 6));
    if (offsetHour > 23 || offsetMinute > 59) return false;
  }
  return true;
}

export function normalizeThreadTimestamp(value, label = 'thread updatedAt') {
  if (value === undefined || value === null || value === '' || value === 0) return '';
  const text = String(value).trim();
  const numeric = Number(text);
  if (Number.isFinite(numeric)) {
    if (!Number.isInteger(numeric) || numeric < MIN_THREAD_EPOCH_MILLIS || numeric > MAX_THREAD_EPOCH_MILLIS) {
      throw new Error(`${label} 必须是毫秒时间戳：${text}`);
    }
    return isoTimestampFromEpochMillis(numeric);
  }
  if (!validIsoTimestamp(text)) throw new Error(`${label} 时间戳无效：${text}`);
  return text;
}
