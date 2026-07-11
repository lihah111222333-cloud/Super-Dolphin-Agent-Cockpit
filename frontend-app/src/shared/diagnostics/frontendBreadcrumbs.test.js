import { describe, expect, it } from 'vitest';
import { createFrontendBreadcrumbBuffer } from './frontendBreadcrumbs.js';
import breadcrumbsSource from './frontendBreadcrumbs.js?raw';

describe('frontend breadcrumb buffer', () => {
  it('routes breadcrumb fields through the shared safe log sanitizer', () => {
    expect(breadcrumbsSource).toContain("from './safeLogFields.js'");
    expect(breadcrumbsSource).toContain('safeLogFields(');
  });

  it('keeps a bounded ring in stable chronological order', () => {
    const timestamps = [
      '2026-07-11T13:00:00.000Z',
      '2026-07-11T13:00:01.000Z',
      '2026-07-11T13:00:02.000Z',
    ];
    const breadcrumbs = createFrontendBreadcrumbBuffer({
      capacity: 2,
      now: () => timestamps.shift(),
    });

    breadcrumbs.record({ actionCode: 'app.open', routeId: 'chat', phase: 'start' });
    breadcrumbs.record({ actionCode: 'thread.select', routeId: 'chat', phase: 'accepted' });
    breadcrumbs.record({ actionCode: 'settings.open', routeId: 'settings', phase: 'complete' });

    expect(breadcrumbs.snapshot()).toEqual([
      {
        actionCode: 'thread.select',
        routeId: 'chat',
        phase: 'accepted',
        timestamp: '2026-07-11T13:00:01.000Z',
      },
      {
        actionCode: 'settings.open',
        routeId: 'settings',
        phase: 'complete',
        timestamp: '2026-07-11T13:00:02.000Z',
      },
    ]);
  });

  it('fails fast for invalid capacity or clock dependencies', () => {
    expect(() => createFrontendBreadcrumbBuffer({ capacity: 0 })).toThrow(
      'frontend breadcrumb capacity must be a positive integer',
    );
    expect(() => createFrontendBreadcrumbBuffer({ capacity: 2, now: null })).toThrow(
      'frontend breadcrumb now must be a function',
    );
  });

  it('rejects fields outside the stable breadcrumb contract', () => {
    const breadcrumbs = createFrontendBreadcrumbBuffer({ capacity: 2 });

    expect(() => breadcrumbs.record({
      actionCode: 'chat.send',
      routeId: 'chat',
      phase: 'start',
      prompt: 'private prompt body',
    })).toThrow('frontend breadcrumb must not include prompt');
    expect(breadcrumbs.snapshot()).toEqual([]);
  });
});
