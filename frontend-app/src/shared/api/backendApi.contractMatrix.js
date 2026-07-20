// @ts-check

import { RPC_METHODS } from './backend/backendRpcMethods.js';

export const RPC_CONTRACT_LEVELS = Object.freeze({
  P0: 'P0',
  P1: 'P1',
  P2: 'P2',
});

const TESTS = Object.freeze({
  API: 'src/shared/api/backendApi.test.js',
  SURFACE: 'src/shared/api/backendApi.surface.test.js',
  MATRIX: 'src/shared/api/backendApi.contractMatrix.test.js',
  CONSUMER: 'src/pages/backendApiConsumer.surface.test.js',
  APP: 'src/App.test.jsx',
  SETTINGS: 'src/pages/settings/SettingsPage.test.jsx',
  WORKFLOWS: 'src/pages/workflows/WorkflowPage.test.jsx',
  FILES_PAGE_SERVICE: 'src/pages/files/services/filesPageService.test.js',
  PROMPTS: 'src/features/prompts/PromptPageView.test.jsx',
  PROMPT_PAGE_SERVICE: 'src/pages/prompts/services/promptPageService.test.js',
  MEMORY: 'src/pages/memory/MemoryPage.test.jsx',
  MEMORY_PAGE_SERVICE: 'src/pages/memory/services/memoryPageService.test.js',
  SKILLS: 'src/pages/skills/SkillsPage.test.jsx',
  OBSERVABILITY: 'src/pages/observability/ObservabilityPage.test.jsx',
  OBSERVABILITY_PAGE_SERVICE: 'src/pages/observability/services/observabilityPageService.test.js',
  PROMPT_HISTORY_CONTROLLER: 'src/features/prompt-history/model/promptHistoryController.test.js',
  PROMPT_HISTORY_HOOK: 'src/features/prompt-history/hooks/usePromptHistory.test.jsx',
  COMPOSER: 'src/pages/chat/composer/ComposerDock.test.jsx',
  WAILS_BRIDGE: 'src/shared/api/wailsBridge.test.js',
});
const EMPTY_CONTRACT_FIELD = '';

/**
 * @typedef {{ path: string, symbol: string, visibility?: 'module-private' }} ResponsePolicyLocator
 * @typedef {
 *   | { kind: 'ignored-result', consumer: ResponsePolicyLocator, outcome?: { kind: 'published-callback', target: readonly string[] }, regressionTest: ResponsePolicyLocator }
 *   | { kind: 'result-handled', consumer: ResponsePolicyLocator, handler: ResponsePolicyLocator, regressionTest: ResponsePolicyLocator }
 *   | { kind: 'consumer-validated', consumer: ResponsePolicyLocator, shape: ResponsePolicyLocator, regressionTest: ResponsePolicyLocator }
 *   | { kind: 'unused', productionScanRoots: readonly ['frontend-app/src'], excludedGlobs: readonly string[] }
 * } ResponsePolicy
 */

/** @param {ResponsePolicy | null} responsePolicy */
function freezeResponsePolicy(responsePolicy) {
  if (responsePolicy == null) return null;
  if (responsePolicy.kind === 'unused') {
    return Object.freeze({
      ...responsePolicy,
      productionScanRoots: Object.freeze(responsePolicy.productionScanRoots),
      excludedGlobs: Object.freeze(responsePolicy.excludedGlobs),
    });
  }
  return Object.freeze({
    ...responsePolicy,
    consumer: Object.freeze(responsePolicy.consumer),
    regressionTest: Object.freeze(responsePolicy.regressionTest),
    ...(responsePolicy.kind === 'ignored-result' && responsePolicy.outcome
      ? { outcome: Object.freeze({ ...responsePolicy.outcome, target: Object.freeze(responsePolicy.outcome.target) }) }
      : {}),
    ...(responsePolicy.kind === 'result-handled'
      ? { handler: Object.freeze(responsePolicy.handler) }
      : {}),
    ...(responsePolicy.kind === 'consumer-validated'
      ? { shape: Object.freeze(responsePolicy.shape) }
      : {}),
  });
}

/**
 * @param {readonly [keyof typeof RPC_METHODS, string, string, 'P0' | 'P1' | 'P2', string, readonly string[], (readonly string[])?, boolean?, { responseValidator?: string, responsePolicy?: ResponsePolicy }?]} contractParts
 */
function contract(...contractParts) {
  const [key, method, facade, level, backendOwner, tests, notes = [], rawLiteralRpc = false, options = {}] = contractParts;
  const responseValidator = options.responseValidator ?? EMPTY_CONTRACT_FIELD;
  const responsePolicy = freezeResponsePolicy(options.responsePolicy ?? null);
  return Object.freeze({
    key,
    method,
    facade,
    level,
    backendOwner,
    tests: Object.freeze(tests),
    rawLiteralRpc,
    responseValidator,
    responsePolicy,
    notes: Object.freeze(notes),
  });
}

import { createSystemContracts } from './backendApi.contractRegistrySystem.js';
import { createContentContracts } from './backendApi.contractRegistryContent.js';
import { createOperationsContracts } from './backendApi.contractRegistryOperations.js';

export const RPC_CONTRACT_REGISTRY = Object.freeze({
  ...createSystemContracts({ contract, tests: TESTS, methods: RPC_METHODS }),
  ...createContentContracts({ contract, tests: TESTS, methods: RPC_METHODS }),
  ...createOperationsContracts({ contract, tests: TESTS, methods: RPC_METHODS }),
});

export const RPC_CONTRACT_MATRIX = Object.freeze(Object.values(RPC_CONTRACT_REGISTRY));
