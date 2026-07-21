import { vi } from "vitest";

const backendMethods = [
  "readConfig", "getWindowBootstrap", "openNewWindow", "getProjects", "setActiveProject", "addProject", "removeProject",
  "getSidebarState", "getThreadState", "getThreadMessages", "getPreference", "forkThread", "startThread", "startTurn",
  "interruptTurn", "forceCompleteTurn", "respondApproval", "compactThread", "recoverThread", "resolveThreadIdentity",
  "archiveThread", "unarchiveThread", "deleteThread", "getThreadConfig", "setThreadConfig", "renameThread", "setPreference",
  "selectProjectDir", "selectFiles", "saveClipboardImage", "beginTextClipboardWrite", "copyTextToClipboard", "emitFrontendTraceEvent",
  "listSharedFiles", "readSharedFile",
];

function clearBridgeSubscription(runtime, callback) {
  if (runtime.bridgeCallback !== callback) return;
  runtime.bridgeCallback = null;
  runtime.bridgeOptions = null;
}

export async function createClientStoreBackendMock({ importOriginal, runtime }) {
  const actual = await importOriginal();
  const backend = Object.fromEntries(backendMethods.map((method) => [method, vi.fn()]));
  runtime.backend = backend;
  return {
    ...backend,
    registerBridgeLogStore: actual.registerBridgeLogStore,
    sendFrontendLogBatch: vi.fn(),
    onBridgeEvent: vi.fn((callback, options = {}) => {
      runtime.bridgeCallback = callback;
      runtime.bridgeOptions = options;
      return () => clearBridgeSubscription(runtime, callback);
    }),
    onRuntimeReconnect: vi.fn((callback) => {
      runtime.runtimeReconnectCallback = callback;
      return () => {
        runtime.runtimeReconnectCallback = null;
      };
    }),
  };
}
