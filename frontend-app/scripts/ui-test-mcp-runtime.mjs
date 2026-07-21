import { fileURLToPath } from "node:url";
import {
  createMCPFrameReader,
  encodeMCPFrame,
} from "./ui-test-mcp-framing.mjs";
import { createBrowserSession } from "./ui-test-mcp-browser-session.mjs";
import {
  createToolArgumentValidator,
  createToolDefinitions,
} from "./ui-test-mcp-tools.mjs";
import {
  assertContract,
  assertProductionGate,
  DEFAULT_BASE_URL,
  DEFAULT_PROTOCOL_VERSION,
  ERROR_CODES,
  isPlainObject,
  JSONRPCError,
  makeErrorResponse,
  responseID,
  SERVER_INFO,
  successResponse,
  truthy,
  validateBaseURL,
  validateInitializeParams,
  validateNoParams,
  writeStderr,
} from "./ui-test-mcp-protocol.mjs";

const UI_TEST_CONTRACT_URL = new URL(
  "../src/devtools/uiTestContract.js",
  import.meta.url,
);
const ACCEPTANCE_GLOBAL = "__SUPER_DOLPHIN_UI_TEST_ACCEPTANCE__";

export { validateBaseURL };
export { createToolDefinitions };
export async function loadUITestContract(contractURL = UI_TEST_CONTRACT_URL) {
  return import(contractURL.href);
}
export function browserLaunchOptions(env = process.env) {
  const executablePath =
    env.SUPER_DOLPHIN_UI_TEST_BROWSER_EXECUTABLE_PATH ||
    env.SUPER_DOLPHIN_UI_TEST_BROWSER_EXECUTABLE ||
    "";
  return executablePath ? { executablePath } : {};
}
async function defaultBrowserFactory(env = process.env) {
  const { chromium } = await import("@playwright/test");
  const options = browserLaunchOptions(env);
  return Object.keys(options).length
    ? chromium.launch(options)
    : chromium.launch();
}
async function closeIfPresent(value) {
  if (value && typeof value.close === "function") await value.close();
}

export function createUITestMCPServer(options = {}) {
  const contract = assertContract(options.contract);
  const env = options.env || process.env;
  assertProductionGate(env);
  const baseURL = validateBaseURL(
    env.SUPER_DOLPHIN_UI_TEST_BASE_URL || options.baseURL || DEFAULT_BASE_URL,
  );
  const stdout = options.stdout || process.stdout;
  const stderr = options.stderr || process.stderr;
  const config = {
    allowSubmit: truthy(env.SUPER_DOLPHIN_UI_TEST_ALLOW_SUBMIT),
    acceptanceOwnsUI: truthy(env.SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_OWNS_UI),
    acceptanceToken: env.SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_TOKEN || "",
  };
  const state = {
    initialized: false,
    shutdown: false,
    stopped: false,
    browser: null,
    page: null,
    pageReady: null,
    cleanupStarted: false,
    exclusive: Promise.resolve(),
    waitAbortController: new AbortController(),
  };
  const session = createBrowserSession({
    contract,
    baseURL,
    browserFactory:
      options.browserFactory || (() => defaultBrowserFactory(env)),
    config,
    state,
    acceptanceGlobal: ACCEPTANCE_GLOBAL,
    closeIfPresent,
  });
  const validateToolCall = createToolArgumentValidator(contract);
  const toolDefinitions = createToolDefinitions(contract);
  const runExclusive = async (task) => {
    const run = state.exclusive.then(task, task);
    state.exclusive = run.catch(() => {});
    return run;
  };
  const requireActive = (method) => {
    if (!state.initialized)
      throw new JSONRPCError(
        ERROR_CODES.LIFECYCLE_ERROR,
        `${method} requires initialize first`,
      );
    if (state.shutdown && method !== "exit")
      throw new JSONRPCError(
        ERROR_CODES.LIFECYCLE_ERROR,
        `${method} cannot run after shutdown`,
      );
  };
  async function cleanup() {
    if (state.cleanupStarted) return;
    state.cleanupStarted = true;
    state.waitAbortController.abort();
    await closeIfPresent(state.page);
    await closeIfPresent(state.browser);
    state.page = null;
    state.browser = null;
    state.pageReady = null;
  }
  async function stop(reason) {
    await cleanup();
    state.stopped = true;
    if (typeof options.onStop === "function") options.onStop(reason);
  }
  async function handleMessage(message) {
    if (!isPlainObject(message))
      return makeErrorResponse(
        ERROR_CODES.INVALID_REQUEST,
        "JSON-RPC request must be an object",
        null,
      );
    const id = responseID(message);
    if (message.jsonrpc !== "2.0")
      return makeErrorResponse(
        ERROR_CODES.INVALID_REQUEST,
        'JSON-RPC request must include jsonrpc: "2.0"',
        id,
      );
    if (typeof message.method !== "string")
      return makeErrorResponse(
        ERROR_CODES.INVALID_REQUEST,
        "JSON-RPC request method must be a string",
        id,
      );
    const notification = !Object.hasOwn(message, "id");
    try {
      if (message.method === "initialize") {
        validateInitializeParams(message.params, contract);
        state.initialized = true;
        state.shutdown = false;
        return successResponse(
          message.id,
          {
            protocolVersion:
              message.params?.protocolVersion || DEFAULT_PROTOCOL_VERSION,
            capabilities: { tools: {} },
            serverInfo: SERVER_INFO,
          },
          notification,
        );
      }
      if (message.method === "notifications/initialized") {
        validateNoParams(
          message.params,
          "notifications/initialized params",
          contract,
        );
        return null;
      }
      if (message.method === "ping") {
        requireActive("ping");
        validateNoParams(message.params, "ping params", contract);
        return successResponse(message.id, {}, notification);
      }
      if (message.method === "shutdown") {
        requireActive("shutdown");
        validateNoParams(message.params, "shutdown params", contract);
        state.shutdown = true;
        await cleanup();
        return successResponse(message.id, {}, notification);
      }
      if (message.method === "exit") {
        validateNoParams(message.params, "exit params", contract);
        await stop("exit");
        return successResponse(message.id, {}, notification);
      }
      if (message.method === "tools/list") {
        requireActive("tools/list");
        validateNoParams(message.params, "tools/list params", contract);
        return successResponse(
          message.id,
          { tools: toolDefinitions },
          notification,
        );
      }
      if (message.method === "tools/call") {
        requireActive("tools/call");
        const { name, args } = validateToolCall(message.params);
        return successResponse(
          message.id,
          await runExclusive(() => session.runTool(name, args)),
          notification,
        );
      }
      return notification
        ? null
        : makeErrorResponse(
            ERROR_CODES.METHOD_NOT_FOUND,
            `unknown JSON-RPC method: ${message.method}`,
            id,
          );
    } catch (error) {
      if (notification) return null;
      return error instanceof JSONRPCError
        ? makeErrorResponse(error.code, error.message, id, error.data)
        : makeErrorResponse(
            ERROR_CODES.INTERNAL_ERROR,
            error.message || "unexpected MCP server exception",
            id,
          );
    }
  }
  const reader = createMCPFrameReader({
    limits: contract.UI_TEST_LIMITS,
    onMessage: async (message, mode) => {
      const response = await handleMessage(message);
      if (response) stdout.write(encodeMCPFrame(response, mode));
    },
    onError: async (error, mode) =>
      stdout.write(
        encodeMCPFrame(
          makeErrorResponse(ERROR_CODES.PARSE_ERROR, error.message, null),
          mode,
        ),
      ),
  });
  return {
    baseURL,
    config,
    handleMessage,
    processChunk: async (chunk) => {
      if (!state.stopped) await reader.push(chunk);
    },
    endInput: async () => {
      await reader.end();
      await stop("eof");
    },
    cleanup,
    stop,
    handleSignal: async (signal) => {
      writeStderr(stderr, `received ${signal}; stopping UI test MCP server`);
      await stop(signal);
    },
    isStopped: () => state.stopped,
  };
}

export async function runStdioMCPServer(options = {}) {
  const contract = options.contract || (await loadUITestContract());
  const stdin = options.stdin || process.stdin;
  const proc = options.processRef || process;
  let scheduled = false;
  const server = createUITestMCPServer({
    ...options,
    contract,
    onStop: () => {
      if (scheduled) return;
      scheduled = true;
      globalThis.setTimeout(() => {
        if (typeof proc.exit === "function") proc.exit(0);
        else proc.exitCode = 0;
      }, 0);
    },
  });
  stdin.on("data", (chunk) => {
    void server.processChunk(chunk).catch((error) => {
      writeStderr(
        options.stderr || process.stderr,
        `failed to process MCP frame: ${error.message}`,
      );
      void server.stop("frame-processing-error");
    });
  });
  stdin.on("end", () => {
    void server.endInput();
  });
  if (typeof stdin.resume === "function") stdin.resume();
  proc.once("SIGINT", () => {
    void server.handleSignal("SIGINT");
  });
  proc.once("SIGTERM", () => {
    void server.handleSignal("SIGTERM");
  });
  return server;
}
if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1])
  runStdioMCPServer().catch((error) => {
    writeStderr(process.stderr, error.message);
    process.exitCode = 1;
  });
