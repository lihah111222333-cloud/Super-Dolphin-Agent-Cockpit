const OPTIONAL_EMPTY_TEXT = String();

function failContract(label, expected) {
  throw new Error(`${label} must be ${expected}`);
}

export function requireArrayField(value, label) {
  if (!Array.isArray(value)) failContract(label, 'an array');
  return value;
}

export function requireObjectField(value, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) failContract(label, 'an object');
  return value;
}

export function requireStringField(value, label) {
  if (typeof value !== 'string' || !value.trim()) failContract(label, 'a non-empty string');
  return value.trim();
}

export function optionalTextField(value) {
  return String(value ?? OPTIONAL_EMPTY_TEXT);
}

export function normalizeOptionalTextField(value) {
  return optionalTextField(value).trim();
}

export function firstOptionalPresent(...values) {
  return values.find((value) => value !== undefined && value !== null && value !== false && value !== OPTIONAL_EMPTY_TEXT);
}

function daysBeforeYear(year) {
  let days = 0;
  for (let current = 1970; current < year; current += 1) {
    days += current % 4 === 0 && (current % 100 !== 0 || current % 400 === 0) ? 366 : 365;
  }
  return days;
}

function daysBeforeMonth(year, month) {
  const monthDays = [31, year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return monthDays.slice(0, month - 1).reduce((sum, days) => sum + days, 0);
}

function daysInMonth(year, month) {
  const monthDays = [31, year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return monthDays[month - 1] || 0;
}

function pad2(value) {
  return String(value).padStart(2, '0');
}

function pad3(value) {
  return String(value).padStart(3, '0');
}

function yearFromEpochDays(epochDays) {
  let year = 1970;
  let remaining = epochDays;
  while (remaining >= (year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 366 : 365)) {
    remaining -= year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 366 : 365;
    year += 1;
  }
  return { year, dayOfYear: remaining };
}

function monthFromDayOfYear(year, dayOfYear) {
  const monthDays = [31, year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  let month = 1;
  let remaining = dayOfYear;
  for (const days of monthDays) {
    if (remaining < days) return { month, day: remaining + 1 };
    remaining -= days;
    month += 1;
  }
  failContract('epoch milliseconds', 'a valid timestamp');
}

export function systemClockMillis(label) {
  const value = globalThis.Date.now();
  if (!Number.isFinite(value) || value < 0) failContract(label, 'a non-negative timestamp');
  return value;
}

export function currentIsoTimestamp(label) {
  const epochMillis = Math.floor(systemClockMillis(label));
  const milliseconds = epochMillis % 1000;
  const epochSeconds = Math.floor(epochMillis / 1000);
  const second = epochSeconds % 60;
  const epochMinutes = Math.floor(epochSeconds / 60);
  const minute = epochMinutes % 60;
  const epochHours = Math.floor(epochMinutes / 60);
  const hour = epochHours % 24;
  const { year, dayOfYear } = yearFromEpochDays(Math.floor(epochHours / 24));
  const { month, day } = monthFromDayOfYear(year, dayOfYear);
  return `${year}-${pad2(month)}-${pad2(day)}T${pad2(hour)}:${pad2(minute)}:${pad2(second)}.${pad3(milliseconds)}Z`;
}

export function utcPartsFromEpochMillis(value, label) {
  if (!Number.isFinite(value) || value < 0) failContract(label, 'a non-negative timestamp');
  const epochSeconds = Math.floor(value / 1000);
  const second = epochSeconds % 60;
  const epochMinutes = Math.floor(epochSeconds / 60);
  const minute = epochMinutes % 60;
  const epochHours = Math.floor(epochMinutes / 60);
  const hour = epochHours % 24;
  const { year, dayOfYear } = yearFromEpochDays(Math.floor(epochHours / 24));
  const { month, day } = monthFromDayOfYear(year, dayOfYear);
  return { year, month, day, hour, minute, second };
}

export function parseRequiredTimestamp(value, label) {
  if (typeof value === 'number') {
    if (Number.isFinite(value) && value > 0) return value;
    failContract(label, 'a positive timestamp');
  }
  const text = requireStringField(value, label);
  const numeric = Number(text);
  if (Number.isFinite(numeric) && numeric > 0) return numeric;
  const match = text.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,3}))?Z$/);
  if (!match) failContract(label, 'an ISO-8601 UTC timestamp');
  const [, yearText, monthText, dayText, hourText, minuteText, secondText, milliText = '0'] = match;
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const hour = Number(hourText);
  const minute = Number(minuteText);
  const second = Number(secondText);
  const millisecond = Number(milliText.padEnd(3, '0'));
  if (month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) || hour > 23 || minute > 59 || second > 59) {
    failContract(label, 'an ISO-8601 UTC timestamp');
  }
  return (((daysBeforeYear(year) + daysBeforeMonth(year, month) + day - 1) * 24 + hour) * 60 + minute) * 60 * 1000 + second * 1000 + millisecond;
}

export function parseOptionalTimestamp(value, label) {
  if (value === null || value === undefined || value === false || value === OPTIONAL_EMPTY_TEXT) return 0;
  return parseRequiredTimestamp(value, label);
}

export function parseRequiredJsonObject(value, label) {
  const text = requireStringField(value, label);
  try {
    return requireObjectField(JSON.parse(text), label);
  } catch (error) {
    throw new Error(`${label} must be valid JSON object: ${error.message}`, { cause: error });
  }
}
