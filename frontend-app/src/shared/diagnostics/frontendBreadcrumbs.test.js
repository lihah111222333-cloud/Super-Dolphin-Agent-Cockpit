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

    breadcrumbs.record({ actionCode: 'app.bootstrap', routeId: 'app', phase: 'start' });
    breadcrumbs.record({ actionCode: 'app.navigation', routeId: 'chat', phase: 'complete' });
    breadcrumbs.record({ actionCode: 'approval.submit', routeId: 'settings', phase: 'success' });

    expect(breadcrumbs.snapshot()).toEqual([
      {
        actionCode: 'app.navigation',
        routeId: 'chat',
        phase: 'complete',
        timestamp: '2026-07-11T13:00:01.000Z',
      },
      {
        actionCode: 'approval.submit',
        routeId: 'settings',
        phase: 'success',
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

  it.each([
    [
      'actionCode',
      { actionCode: 'chat.send', routeId: 'app', phase: 'start' },
      'frontend breadcrumb actionCode is not allowed',
    ],
    [
      'routeId',
      { actionCode: 'app.bootstrap', routeId: '/Users/alice/private', phase: 'start' },
      'frontend breadcrumb routeId is not allowed',
    ],
    [
      'phase',
      { actionCode: 'app.bootstrap', routeId: 'app', phase: 'accepted' },
      'frontend breadcrumb phase is not allowed',
    ],
  ])('rejects %s values outside the low-cardinality breadcrumb contract', (_field, input, message) => {
    const breadcrumbs = createFrontendBreadcrumbBuffer({ capacity: 2 });

    expect(() => breadcrumbs.record(input)).toThrow(message);
    expect(breadcrumbs.snapshot()).toEqual([]);
  });

  it('accepts app plus the eight current page route identifiers without importing client state', () => {
    const breadcrumbs = createFrontendBreadcrumbBuffer({ capacity: 9 });
    const routeIds = [
      'app',
      'chat',
      'prompts',
      'workflows',
      'skills',
      'memory',
      'observability',
      'files',
      'settings',
    ];

    for (const routeId of routeIds) {
      breadcrumbs.record({ actionCode: 'app.navigation', routeId, phase: 'complete' });
    }

    expect(breadcrumbs.snapshot().map((entry) => entry.routeId)).toEqual(routeIds);
  });
});
