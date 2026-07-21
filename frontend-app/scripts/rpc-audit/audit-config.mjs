import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { collectFrontendPayloadKeysFromSource as discoverFrontendPayloadKeys } from "./frontend-payload-discovery.mjs";

export const SCRIPT_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..");
export const DEFAULT_FRONTEND_ROOT = resolve(SCRIPT_DIR, "..");
export const DEFAULT_REPO_ROOT = resolve(DEFAULT_FRONTEND_ROOT, "..");

export const RPC_METHODS_PATH = "frontend-app/src/shared/api/backend/backendRpcMethods.js";
export const RPC_FACADE_PATH = "frontend-app/src/shared/api/backendApi.js";
export const FRONTEND_PAYLOAD_BUILDERS_PATH =
  "frontend-app/src/shared/api/backend/backendApiFactoryThread.js";
export const TURN_INTERRUPT_INJECTION_PATH =
  "frontend-app/src/entities/client/model/helpers/a1/clientStoreActiveThreadActions.js";
export const TURN_INTERRUPT_RUNTIME_PATH =
  "frontend-app/src/entities/client/model/threadLifecycleRuntime.js";
export const TURN_INTERRUPT_REGRESSION_PATH =
  "frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js";
export const RPC_FACADE_REEXPORT_SOURCE = "./backend/backendRpcMethods.js";
export const RPC_RESPONSE_VALIDATORS_PATH =
  "frontend-app/src/shared/api/response-validators/registry.js";
export const RPC_RESPONSE_VALIDATORS_RUNTIME_PATH =
  "frontend-app/src/shared/api/response-validators/runtime/sidebar-state.js";
export const RPC_MATRIX_PATH = "frontend-app/src/shared/api/backendApi.contractMatrix.js";
export const SIDEBAR_GO_STATE_PATH = "internal/module/uistate/state.go";
export const BACKEND_API_FACTORY_PATHS = [
  "frontend-app/src/shared/api/backend/backendApiFactoryCore.js",
  "frontend-app/src/shared/api/backend/backendApiFactoryOps.js",
  "frontend-app/src/shared/api/backend/backendApiFactoryThread.js",
];
export const DIRECT_FACADE_RPC_LOCATORS = new Map([
  [
    "UI_LOG",
    {
      facade: "sendFrontendLogBatch",
      implementationPath: "frontend-app/src/shared/api/wails/wailsBridgeRpc.js",
      methodPath: "frontend-app/src/shared/api/wails/wailsBridgeRpc.js",
      method: "ui/log",
    },
  ],
  [
    "OBSERVABILITY_FRONTEND_INGEST",
    {
      facade: "emitFrontendTraceEvent",
      implementationPath: "frontend-app/src/shared/api/wails/wailsBridgeTraceTransport.js",
      methodPath: "frontend-app/src/shared/api/wails/wailsBridgeConstants.js",
      method: "observability/frontend/ingest",
    },
  ],
]);
export const GO_RPC_CONSTANTS_PATH = "internal/contract/rpc_handler.go";
export const GO_HANDLER_ROOTS = ["internal", "cmd"];
export const GO_PAYLOAD_STRUCTS = new Map([
  [
    "thread/start",
    [
      "internal/module/thread/rpc_types.go:startParams",
      "internal/module/thread/rpc_types.go:startParamCompatFields",
    ],
  ],
  [
    "turn/start",
    [
      "internal/module/turn/rpc_types.go:turnStartParams",
      "internal/module/turn/rpc_types.go:legacyTurnStartParams",
    ],
  ],
  [
    "turn/steer",
    [
      "internal/module/turn/rpc_types.go:turnSteerParams",
      "internal/module/turn/rpc_types.go:legacyTurnSteerParams",
    ],
  ],
  [
    "turn/interrupt",
    [
      "internal/module/turn/rpc_types.go:turnInterruptParams",
      "internal/module/turn/rpc_types.go:legacyTurnInterruptParams",
    ],
  ],
]);

export const SERVICE_FACADE_LOCATORS = new Map([
  [
    "OBSERVABILITY_TRACE_GET",
    "frontend-app/src/pages/observability/services/observabilityPageService.js",
  ],
  [
    "OBSERVABILITY_RECENT_LIST",
    "frontend-app/src/pages/observability/services/observabilityPageService.js",
  ],
  ["UI_MEMORY_ENTRY_GET", "frontend-app/src/pages/memory/services/memoryPageService.js"],
  ["UI_MEMORY_ENTRY_UPSERT", "frontend-app/src/pages/memory/services/memoryPageService.js"],
  ["UI_MEMORY_ENTRY_DELETE", "frontend-app/src/pages/memory/services/memoryPageService.js"],
  [
    "UI_MEMORY_AUTO_DREAM_SET_INTENT",
    "frontend-app/src/pages/memory/services/memoryPageService.js",
  ],
  ["UI_MEMORY_ENTRY_MERGE", "frontend-app/src/pages/memory/services/memoryPageService.js"],
  ["UI_MEMORY_SIMILARITY_IGNORE", "frontend-app/src/pages/memory/services/memoryPageService.js"],
  [
    "UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START",
    "frontend-app/src/pages/memory/services/memoryPageService.js",
  ],
  [
    "UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS",
    "frontend-app/src/pages/memory/services/memoryPageService.js",
  ],
  ["UI_SHARED_FILE_GET", "frontend-app/src/pages/files/services/filesPageService.js"],
  ["PROMPT_ASSETS_LIST", "frontend-app/src/pages/prompts/services/promptPageService.js"],
  ["DASHBOARD_PROMPTS", "frontend-app/src/pages/prompts/services/promptPageService.js"],
  ["PROMPTS_GET", "frontend-app/src/pages/prompts/services/promptPageService.js"],
  ["PROMPTS_WRITE", "frontend-app/src/pages/prompts/services/promptPageService.js"],
  ["PROMPTS_DELETE", "frontend-app/src/pages/prompts/services/promptPageService.js"],
  ["PROMPT_INTENTS_DRAFT", "frontend-app/src/pages/prompts/services/promptPageService.js"],
  ["PROMPT_INTENTS_COMMIT", "frontend-app/src/pages/prompts/services/promptPageService.js"],
  ["PROMPT_INTENTS_DISCARD", "frontend-app/src/pages/prompts/services/promptPageService.js"],
  ["PROMPT_INTENTS_DRY_RUN", "frontend-app/src/pages/prompts/services/promptPageService.js"],
  ["PERSONALIZATION_PROFILE_GET", "frontend-app/src/pages/prompts/services/promptPageService.js"],
  ["PERSONALIZATION_PROFILE_SAVE", "frontend-app/src/pages/prompts/services/promptPageService.js"],
]);

export const FRONTEND_PAYLOAD_METHOD_EXEMPTIONS = new Map([
  ["turn/steer", "turn/steer is provider-facing and has no React facade builder"],
]);

export const FRONTEND_FACADE_ONLY_PAYLOAD_KEYS = new Map([
  [
    "thread/start",
    [
      "agentKey",
      "codexModelProvider",
      "codex_model_provider",
      "deferSpawn",
      "optimisticUserMessage",
      "optimistic_user_message",
      "promptKey",
      "skipInitialRuntimeSync",
      "skip_initial_runtime_sync",
    ],
  ],
  ["turn/start", ["attachments"]],
  ["turn/interrupt", ["cwd"]],
]);

export const GO_HANDLER_CALLS = [
  "StrictHandler",
  "LoggedStrictHandler",
  "ThreadHandler",
  "CapabilityThreadHandler",
];

export function collectFrontendPayloadKeysFromSource(
  source,
  methodValues = new Map(),
  requiredMethods = null,
) {
  return discoverFrontendPayloadKeys(source, {
    sourcePath: FRONTEND_PAYLOAD_BUILDERS_PATH,
    methodValues,
    requiredMethods,
  });
}
