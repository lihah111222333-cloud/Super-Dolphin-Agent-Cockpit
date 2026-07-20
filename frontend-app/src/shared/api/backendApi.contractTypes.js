// @ts-check

import { RPC_METHODS } from './backend/backendRpcMethods.js';

/** @typedef {typeof RPC_METHODS} Methods */
/**
 * @typedef {{
 *   API: string,
 *   SURFACE: string,
 *   MATRIX: string,
 *   CONSUMER: string,
 *   APP: string,
 *   SETTINGS: string,
 *   WORKFLOWS: string,
 *   FILES_PAGE_SERVICE: string,
 *   PROMPTS: string,
 *   PROMPT_PAGE_SERVICE: string,
 *   MEMORY: string,
 *   MEMORY_PAGE_SERVICE: string,
 *   SKILLS: string,
 *   OBSERVABILITY: string,
 *   OBSERVABILITY_PAGE_SERVICE: string,
 *   PROMPT_HISTORY_CONTROLLER: string,
 *   PROMPT_HISTORY_HOOK: string,
 *   COMPOSER: string,
 *   WAILS_BRIDGE: string,
 * }} Tests
 */
/** @typedef {{ path: string, symbol: string, visibility?: 'module-private' }} ResponsePolicyLocator */
/** @typedef {{ responseValidator?: string, responsePolicy?: ResponsePolicy }} ContractOptions */
/**
 * @typedef {{
 *   key: keyof Methods,
 *   method: string,
 *   facade: string,
 *   level: 'P0' | 'P1' | 'P2',
 *   backendOwner: string,
 *   tests: readonly string[],
 *   rawLiteralRpc: boolean,
 *   responseValidator: string,
 *   responsePolicy: ResponsePolicy | null,
 *   notes: readonly string[],
 * }} ContractEntry
 */
/**
 * @typedef {
 *   | {
 *     kind: 'ignored-result',
 *     consumer: ResponsePolicyLocator,
 *     outcome?: { kind: 'published-callback', target: readonly string[] },
 *     regressionTest: ResponsePolicyLocator,
 *   }
 *   | {
 *     kind: 'result-handled',
 *     consumer: ResponsePolicyLocator,
 *     handler: ResponsePolicyLocator,
 *     regressionTest: ResponsePolicyLocator,
 *   }
 *   | {
 *     kind: 'consumer-validated',
 *     consumer: ResponsePolicyLocator,
 *     shape: ResponsePolicyLocator,
 *     regressionTest: ResponsePolicyLocator,
 *   }
 *   | {
 *     kind: 'unused',
 *     productionScanRoots: readonly ['frontend-app/src'],
 *     excludedGlobs: readonly string[],
 *   }
 * } ResponsePolicy
 */
/**
 * @typedef {(
 *   ...contractParts: readonly [
 *     keyof Methods,
 *     string,
 *     string,
 *     'P0' | 'P1' | 'P2',
 *     string,
 *     readonly string[],
 *     (readonly string[])?,
 *     boolean?,
 *     ContractOptions?,
 *   ]
 * ) => Readonly<ContractEntry>} ContractFactory
 */

export {};
