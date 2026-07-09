import { describe, expect, it } from 'vitest';
import {
  cronExprFromSchedule,
  scheduleLabelFromCron,
  scheduleStateFromCron,
} from './workflowScheduleModel.js';

describe('workflowScheduleModel', () => {
  it('parses daily cron with the backend timezone prefix', () => {
    expect(scheduleStateFromCron('CRON_TZ=Asia/Shanghai 5 9 * * *')).toMatchObject({
      preset: 'daily',
      time: '09:05',
      warning: '',
    });
    expect(scheduleLabelFromCron('CRON_TZ=Asia/Shanghai 5 9 * * *')).toBe('每天 09:05');
  });

  it('parses weekday cron with the backend timezone prefix', () => {
    expect(scheduleStateFromCron('CRON_TZ=Asia/Shanghai 0 5 * * 1-5')).toMatchObject({
      preset: 'weekdays',
      time: '05:00',
      warning: '',
    });
  });

  it('keeps complex but valid cron outside the supported preset range', () => {
    expect(scheduleStateFromCron('CRON_TZ=Asia/Shanghai */15 9 * * 1-5')).toMatchObject({
      warning: '已有计划超出简化设置范围，请重新选择运行频率和时间。',
    });
  });

  it('rejects malformed or six-field cron expressions with the existing warning copy', () => {
    expect(scheduleStateFromCron('CRON_TZ=Asia/Shanghai 0 5 * * * *')).toMatchObject({
      warning: '已有计划格式无法识别，请重新选择运行频率和时间。',
    });
    expect(scheduleStateFromCron('CRON_TZ=Asia/Shanghai nope 5 * * *')).toMatchObject({
      warning: '已有计划格式无法识别，请重新选择运行频率和时间。',
    });
  });

  it('continues generating the backend cron format without cron-parser formatting', () => {
    expect(cronExprFromSchedule('weekly', '07:30', '3', '1')).toEqual({
      cronExpr: 'CRON_TZ=Asia/Shanghai 30 7 * * 3',
      error: '',
    });
  });
});
