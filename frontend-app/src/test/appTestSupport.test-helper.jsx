import { cleanup } from '@testing-library/react';
import { vi } from 'vitest';
import { resetFrontendHealthForTest } from '../shared/diagnostics/frontendHealthStore.js';
import { createAppTestCore } from './appTestSupportCore.test-helper.jsx';
import { createAppTestObservabilityFeatures } from './appTestSupportObservabilityFeatures.test-helper.jsx';
import { createAppTestRoutes } from './appTestSupportRoutes.test-helper.jsx';

export function createAppTestSupport(context) {
  const core = createAppTestCore(context);
  const observabilityFeatures = createAppTestObservabilityFeatures({
    ...context,
    formatParsedTimestampForTest: core.formatParsedTimestampForTest,
    canonicalPromptRPCItem: core.canonicalPromptRPCItem,
    waitForBackendThreadHeading: core.waitForBackendThreadHeading,
  });
  const routes = createAppTestRoutes({
    ...context,
    waitForBackendThreadHeading: core.waitForBackendThreadHeading,
  });

  function cleanupAppTest() {
    cleanup();
    document.querySelectorAll('#overlay-root').forEach((node) => node.remove());
    vi.useRealTimers();
  }

  return {
    ...core,
    ...observabilityFeatures,
    ...routes,
    resetFrontendHealthForTest,
    cleanupAppTest,
  };
}
