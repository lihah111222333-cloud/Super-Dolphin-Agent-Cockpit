import {
  callAPI as callWailsAPI,
  getBuildInfo as getWailsBuildInfo,
  onBridgeEvent as subscribeBridgeEvent,
  onAgentEvent as subscribeAgentEvent,
  onFilesDropped as subscribeFilesDropped,
  readDroppedTextFiles as readDroppedTextFilesViaBridge,
  registerBridgeLogStore,
  saveTextFile as saveTextFileViaBridge,
  selectFiles as selectFilesViaBridge,
  selectProjectDir as selectProjectDirViaBridge,
  sendFrontendLogBatch,
} from './wailsBridge';

export const RPC_METHODS = Object.freeze({
  CONFIG_READ: 'config/read',

  UI_WINDOW_BOOTSTRAP_GET: 'ui/windowBootstrap/get',
  UI_STATE_GET: 'ui/state/get',
  UI_SIDEBAR_GET: 'ui/sidebar/get',
  UI_LOG: 'ui/log',

  UI_PROJECTS_GET: 'ui/projects/get',
  UI_PROJECTS_SET_ACTIVE: 'ui/projects/setActive',
  UI_PROJECTS_ADD: 'ui/projects/add',
  UI_PROJECTS_REMOVE: 'ui/projects/remove',

  UI_PREFERENCES_GET: 'ui/preferences/get',
  UI_PREFERENCES_GET_ALL: 'ui/preferences/getAll',
  UI_PREFERENCES_SET: 'ui/preferences/set',

  UI_DASHBOARD_GET: 'ui/dashboard/get',
  UI_MEMORY_GET: 'ui/memory/get',

  DASHBOARD_DAG_START: 'dashboard/dagStart',
  DASHBOARD_DAG_TERMINATE: 'dashboard/dagTerminate',

  THREAD_START: 'thread/start',
  THREAD_MESSAGES: 'thread/messages',
  THREAD_RESOLVE: 'thread/resolve',
  THREAD_COMPACT_START: 'thread/compact/start',
  THREAD_RECOVER: 'thread/recover',
  THREAD_NAME_SET: 'thread/name/set',

  TURN_START: 'turn/start',
  TURN_INTERRUPT: 'turn/interrupt',
});

const objectPrototype = Object.prototype;

function assertPlainObject(method, params) {
  const value = params == null ? {} : params;
  if (typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${method} params must be an object`);
  }
  return value;
}

function normalizeString(value) {
  return (value || '').toString().trim();
}

function normalizeProvider(params) {
  return normalizeString(params.provider || params.agentKey || params.agent_key);
}

function requireCwd(method, params) {
  const payload = assertPlainObject(method, params);
  const cwd = normalizeString(payload.cwd);
  if (!cwd || cwd === '.') {
    throw new Error(`${method}: cwd is required`);
  }
  return { ...payload, cwd };
}

function requireThreadId(method, params) {
  const payload = assertPlainObject(method, params);
  const threadId = normalizeString(payload.threadId || payload.thread_id);
  if (!threadId) {
    throw new Error(`${method}: threadId is required`);
  }
  return { ...payload, threadId };
}

function requireKey(method, params, key) {
  const payload = assertPlainObject(method, params);
  const value = normalizeString(payload[key]);
  if (!value) {
    throw new Error(`${method}: ${key} is required`);
  }
  return { ...payload, [key]: value };
}

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

function basename(path) {
  const value = normalizeString(path);
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function normalizeAttachmentPath(item) {
  if (typeof item === 'string') return normalizeString(item);
  if (item && typeof item === 'object') return normalizeString(item.path || item.url);
  return '';
}

function normalizeAttachmentInputItem(item) {
  if (item && typeof item === 'object' && normalizeString(item.kind) === 'image') {
    const path = normalizeString(item.path);
    const previewUrl = normalizeString(item.previewUrl || item.url);
    if (path) {
      const payload = { type: 'localImage', path };
      if (previewUrl.toLowerCase().startsWith('data:image/')) payload.url = previewUrl;
      return payload;
    }
    if (previewUrl) return { type: 'image', url: previewUrl };
    return null;
  }

  const path = normalizeAttachmentPath(item);
  if (!path) return null;
  return { type: 'mention', name: basename(path), path };
}

function normalizeTurnInput(input, attachments = []) {
  const extraItems = Array.isArray(attachments)
    ? attachments.map(normalizeAttachmentInputItem).filter(Boolean)
    : [];

  if (Array.isArray(input)) {
    if (input.length === 0 && extraItems.length === 0) {
      throw new Error(`${RPC_METHODS.TURN_START}: input is required`);
    }
    return { input: [...input, ...extraItems] };
  }

  const text = normalizeString(input);
  if (!text && extraItems.length === 0) {
    throw new Error(`${RPC_METHODS.TURN_START}: input is required`);
  }
  if (extraItems.length > 0) {
    return {
      input: [
        ...(text ? [{ type: 'text', text }] : []),
        ...extraItems,
      ],
    };
  }
  return { prompt: text };
}

function dashboardDagStartPayload(params) {
  const payload = requireKey(RPC_METHODS.DASHBOARD_DAG_START, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_START, params), 'dagKey');
  return cleanObject({
    dagKey: payload.dagKey,
    triggerSource: normalizeString(payload.triggerSource),
    idempotencyKey: normalizeString(payload.idempotencyKey),
  });
}

function dashboardDagTerminatePayload(params) {
  const payload = requireKey(RPC_METHODS.DASHBOARD_DAG_TERMINATE, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_TERMINATE, params), 'dagKey');
  return cleanObject({
    dagKey: payload.dagKey,
    runKey: normalizeString(payload.runKey),
    reason: normalizeString(payload.reason),
  });
}

function hasOwn(value, key) {
  return objectPrototype.hasOwnProperty.call(value, key);
}

export function createBackendApi(deps = {}) {
  const callAPI = deps.callAPI || callWailsAPI;
  const native = {
    getBuildInfo: deps.getBuildInfo || getWailsBuildInfo,
    onAgentEvent: deps.onAgentEvent || subscribeAgentEvent,
    onBridgeEvent: deps.onBridgeEvent || subscribeBridgeEvent,
    onFilesDropped: deps.onFilesDropped || subscribeFilesDropped,
    readDroppedTextFiles: deps.readDroppedTextFiles || readDroppedTextFilesViaBridge,
    saveTextFile: deps.saveTextFile || saveTextFileViaBridge,
    selectFiles: deps.selectFiles || selectFilesViaBridge,
    selectProjectDir: deps.selectProjectDir || selectProjectDirViaBridge,
  };

  const callBackend = (method, params = {}) => {
    const rpcMethod = normalizeString(method);
    if (!rpcMethod) throw new Error('backend RPC method is required');
    return callAPI(rpcMethod, assertPlainObject(rpcMethod, params));
  };

  return {
    callBackend,

    readConfig: () => callBackend(RPC_METHODS.CONFIG_READ, {}),
    getWindowBootstrap: () => callBackend(RPC_METHODS.UI_WINDOW_BOOTSTRAP_GET, {}),

    getSidebarState: (params) => callBackend(RPC_METHODS.UI_SIDEBAR_GET, requireCwd(RPC_METHODS.UI_SIDEBAR_GET, params)),
    getThreadState: (params) => callBackend(
      RPC_METHODS.UI_STATE_GET,
      requireThreadId(RPC_METHODS.UI_STATE_GET, requireCwd(RPC_METHODS.UI_STATE_GET, params)),
    ),

    getProjects: (params) => callBackend(RPC_METHODS.UI_PROJECTS_GET, requireCwd(RPC_METHODS.UI_PROJECTS_GET, params)),
    setActiveProject: (params) => callBackend(
      RPC_METHODS.UI_PROJECTS_SET_ACTIVE,
      requireKey(RPC_METHODS.UI_PROJECTS_SET_ACTIVE, requireCwd(RPC_METHODS.UI_PROJECTS_SET_ACTIVE, params), 'path'),
    ),
    addProject: (params) => callBackend(
      RPC_METHODS.UI_PROJECTS_ADD,
      requireKey(RPC_METHODS.UI_PROJECTS_ADD, requireCwd(RPC_METHODS.UI_PROJECTS_ADD, params), 'path'),
    ),
    removeProject: (params) => callBackend(
      RPC_METHODS.UI_PROJECTS_REMOVE,
      requireKey(RPC_METHODS.UI_PROJECTS_REMOVE, requireCwd(RPC_METHODS.UI_PROJECTS_REMOVE, params), 'path'),
    ),

    getPreference: (params) => callBackend(RPC_METHODS.UI_PREFERENCES_GET, assertPlainObject(RPC_METHODS.UI_PREFERENCES_GET, params)),
    getAllPreferences: (params = {}) => callBackend(RPC_METHODS.UI_PREFERENCES_GET_ALL, assertPlainObject(RPC_METHODS.UI_PREFERENCES_GET_ALL, params)),
    setPreference: (params) => {
      const payload = assertPlainObject(RPC_METHODS.UI_PREFERENCES_SET, params);
      if (!normalizeString(payload.key)) throw new Error(`${RPC_METHODS.UI_PREFERENCES_SET}: key is required`);
      if (!hasOwn(payload, 'value')) throw new Error(`${RPC_METHODS.UI_PREFERENCES_SET}: value is required`);
      return callBackend(RPC_METHODS.UI_PREFERENCES_SET, payload);
    },

    getDashboardPage: (params) => callBackend(
      RPC_METHODS.UI_DASHBOARD_GET,
      requireKey(RPC_METHODS.UI_DASHBOARD_GET, requireCwd(RPC_METHODS.UI_DASHBOARD_GET, params), 'page'),
    ),
    getMemorySnapshot: (params) => callBackend(RPC_METHODS.UI_MEMORY_GET, requireCwd(RPC_METHODS.UI_MEMORY_GET, params)),
    startDag: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_START, dashboardDagStartPayload(params)),
    terminateDag: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_TERMINATE, dashboardDagTerminatePayload(params)),
    runDashboardCommand: () => {
      throw new Error('dashboard command execution backend RPC is not registered');
    },

    getThreadMessages: (params) => callBackend(
      RPC_METHODS.THREAD_MESSAGES,
      requireThreadId(RPC_METHODS.THREAD_MESSAGES, assertPlainObject(RPC_METHODS.THREAD_MESSAGES, params)),
    ),
    resolveThreadIdentity: (params) => callBackend(
      RPC_METHODS.THREAD_RESOLVE,
      requireThreadId(RPC_METHODS.THREAD_RESOLVE, assertPlainObject(RPC_METHODS.THREAD_RESOLVE, params)),
    ),
    startThread: (params) => {
      const payload = requireCwd(RPC_METHODS.THREAD_START, params);
      const provider = normalizeProvider(payload);
      if (!provider) {
        throw new Error(`${RPC_METHODS.THREAD_START}: provider is required`);
      }
      const rest = { ...payload };
      const deferSpawn = rest.deferSpawn;
      delete rest.agentKey;
      delete rest.agent_key;
      delete rest.deferSpawn;
      return callBackend(RPC_METHODS.THREAD_START, {
        ...rest,
        provider,
        defer_spawn: deferSpawn !== false,
      });
    },
    startTurn: (params) => {
      const payload = requireThreadId(RPC_METHODS.TURN_START, requireCwd(RPC_METHODS.TURN_START, params));
      const { input, attachments, ...rest } = payload;
      return callBackend(RPC_METHODS.TURN_START, {
        ...rest,
        ...normalizeTurnInput(input, attachments),
      });
    },
    interruptTurn: (params) => callBackend(
      RPC_METHODS.TURN_INTERRUPT,
      requireThreadId(RPC_METHODS.TURN_INTERRUPT, requireCwd(RPC_METHODS.TURN_INTERRUPT, params)),
    ),
    compactThread: (params) => callBackend(
      RPC_METHODS.THREAD_COMPACT_START,
      requireThreadId(RPC_METHODS.THREAD_COMPACT_START, requireCwd(RPC_METHODS.THREAD_COMPACT_START, params)),
    ),
    recoverThread: (params) => callBackend(
      RPC_METHODS.THREAD_RECOVER,
      requireThreadId(RPC_METHODS.THREAD_RECOVER, requireCwd(RPC_METHODS.THREAD_RECOVER, params)),
    ),
    renameThread: (params) => callBackend(
      RPC_METHODS.THREAD_NAME_SET,
      requireKey(RPC_METHODS.THREAD_NAME_SET, requireThreadId(RPC_METHODS.THREAD_NAME_SET, requireCwd(RPC_METHODS.THREAD_NAME_SET, params)), 'name'),
    ),

    getBuildInfo: native.getBuildInfo,
    onAgentEvent: native.onAgentEvent,
    onBridgeEvent: native.onBridgeEvent,
    onFilesDropped: native.onFilesDropped,
    readDroppedTextFiles: native.readDroppedTextFiles,
    saveTextFile: native.saveTextFile,
    selectFiles: native.selectFiles,
    selectProjectDir: native.selectProjectDir,
  };
}

const backendApi = createBackendApi();

export const callBackend = backendApi.callBackend;
export const readConfig = backendApi.readConfig;
export const getWindowBootstrap = backendApi.getWindowBootstrap;
export const getSidebarState = backendApi.getSidebarState;
export const getThreadState = backendApi.getThreadState;
export const getProjects = backendApi.getProjects;
export const setActiveProject = backendApi.setActiveProject;
export const addProject = backendApi.addProject;
export const removeProject = backendApi.removeProject;
export const getPreference = backendApi.getPreference;
export const getAllPreferences = backendApi.getAllPreferences;
export const setPreference = backendApi.setPreference;
export const getDashboardPage = backendApi.getDashboardPage;
export const getMemorySnapshot = backendApi.getMemorySnapshot;
export const startDag = backendApi.startDag;
export const terminateDag = backendApi.terminateDag;
export const runDashboardCommand = backendApi.runDashboardCommand;
export const getThreadMessages = backendApi.getThreadMessages;
export const resolveThreadIdentity = backendApi.resolveThreadIdentity;
export const startThread = backendApi.startThread;
export const startTurn = backendApi.startTurn;
export const interruptTurn = backendApi.interruptTurn;
export const compactThread = backendApi.compactThread;
export const recoverThread = backendApi.recoverThread;
export const renameThread = backendApi.renameThread;
export const getBuildInfo = backendApi.getBuildInfo;
export const onAgentEvent = backendApi.onAgentEvent;
export const onBridgeEvent = backendApi.onBridgeEvent;
export const onFilesDropped = backendApi.onFilesDropped;
export const readDroppedTextFiles = backendApi.readDroppedTextFiles;
export const saveTextFile = backendApi.saveTextFile;
export const selectFiles = backendApi.selectFiles;
export const selectProjectDir = backendApi.selectProjectDir;
export { registerBridgeLogStore, sendFrontendLogBatch };
