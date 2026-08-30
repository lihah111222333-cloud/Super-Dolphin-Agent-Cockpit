// @ts-check

import { createBackendApi } from './backend/backendApiFactoryThread.js';
import { registerBridgeLogStore, reportFrontendReadiness, sendFrontendLogBatch, emitFrontendTraceEvent } from './wailsBridge.js';

export { RPC_METHODS } from './backend/backendRpcMethods.js';
export { createBackendApi };

const backendApi = createBackendApi();

const {
  callBackend, readConfig,
  readBuiltinTools, writeBuiltinTool, checkAppUpdate, downloadAppUpdate,
  installAppUpdate, installLatestAppUpdate, getWindowBootstrap, getSidebarState,
  openNewWindow, getThreadState, getProjects, setActiveProject,
  addProject, removeProject, getPreference, getAllPreferences,
  setPreference, listModelProviders, saveModelProviders, applyModelProvider,
  getDashboardPage, getVideoApiKey, setVideoApiKey, listDashboardLogs,
  getObservabilityTrace, getObservabilityThreadRecent, listObservabilityRecent, listObservabilitySlow,
  listObservabilityErrors, getObservabilityStatus,
  listSharedFiles, readSharedFile, writeWorkflowMaterial,
  listDags, getDagDetail,
  getDagRuns, getDagRun, startDag, createAndStartDag,
  dispatchDagNode, terminateDagRun, terminateDag, deleteDag,
  applyDagOps, listWorkflowTemplates, getWorkflowTemplate, renderWorkflowTemplateDraft,
  saveWorkflowTemplate, rollbackWorkflowTemplate, listCronJobs, getCronJob,
  createCronJob, updateCronJob, deleteCronJob, runCronJobOnce,
  setCronJobEnabled, listCronJobRuns, locateCodeFile, openCodeFile,
  openPath, saveCodeFile, createDatasourceDocument, importDatasourceLocalFile, listDatasourceDocuments,
  getDatasourceDocument, listDatasourceChunks, updateDatasourceDocument, deleteDatasourceDocument,
  listMCPServers, listToolbridgeTools, startSQLiteMCPServer, stopSQLiteMCPServer, startPlaywrightMCPServer,
  stopPlaywrightMCPServer, setMCPToolLifecycle, listMCPToolLifecycle, exportMCPToolLifecycle,
  listThreadsPage, listLoadedThreadsPage, getThreadMessages, getPromptHistory, resolveThreadIdentity,
  archiveThread, unarchiveThread, deleteThread, getThreadConfig,
  setThreadConfig, forkThread, startThread, startTurn, interruptTurn,
  forceCompleteTurn, respondApproval, compactThread, recoverThread,
  renameThread, getBuildInfo, onAgentEvent, onBridgeEvent,
  onFilesDropped, onRuntimeReconnect, readDroppedTextFiles, saveClipboardImage,
  saveTextFile, openSharedFile, previewSharedFile, beginTextClipboardWrite,
  copyTextToClipboard, selectFiles, selectDatasourceImportFile, selectProjectDir,
  selectProjectDirs,
} = backendApi;

export {
  callBackend, readConfig,
  readBuiltinTools, writeBuiltinTool, checkAppUpdate, downloadAppUpdate,
  installAppUpdate, installLatestAppUpdate, getWindowBootstrap, getSidebarState,
  openNewWindow, getThreadState, getProjects, setActiveProject,
  addProject, removeProject, getPreference, getAllPreferences,
  setPreference, listModelProviders, saveModelProviders, applyModelProvider,
  getDashboardPage, getVideoApiKey, setVideoApiKey, listDashboardLogs,
  getObservabilityTrace, getObservabilityThreadRecent, listObservabilityRecent, listObservabilitySlow,
  listObservabilityErrors, getObservabilityStatus,
  listSharedFiles, readSharedFile, writeWorkflowMaterial,
  listDags, getDagDetail,
  getDagRuns, getDagRun, startDag, createAndStartDag,
  dispatchDagNode, terminateDagRun, terminateDag, deleteDag,
  applyDagOps, listWorkflowTemplates, getWorkflowTemplate, renderWorkflowTemplateDraft,
  saveWorkflowTemplate, rollbackWorkflowTemplate, listCronJobs, getCronJob,
  createCronJob, updateCronJob, deleteCronJob, runCronJobOnce,
  setCronJobEnabled, listCronJobRuns, locateCodeFile, openCodeFile,
  openPath, saveCodeFile, createDatasourceDocument, importDatasourceLocalFile, listDatasourceDocuments,
  getDatasourceDocument, listDatasourceChunks, updateDatasourceDocument, deleteDatasourceDocument,
  listMCPServers, listToolbridgeTools, startSQLiteMCPServer, stopSQLiteMCPServer, startPlaywrightMCPServer,
  stopPlaywrightMCPServer, setMCPToolLifecycle, listMCPToolLifecycle, exportMCPToolLifecycle,
  listThreadsPage, listLoadedThreadsPage, getThreadMessages, getPromptHistory, resolveThreadIdentity,
  archiveThread, unarchiveThread, deleteThread, getThreadConfig,
  setThreadConfig, forkThread, startThread, startTurn, interruptTurn,
  forceCompleteTurn, respondApproval, compactThread, recoverThread,
  renameThread, getBuildInfo, onAgentEvent, onBridgeEvent,
  onFilesDropped, onRuntimeReconnect, readDroppedTextFiles, saveClipboardImage,
  saveTextFile, openSharedFile, previewSharedFile, beginTextClipboardWrite,
  copyTextToClipboard, selectFiles, selectDatasourceImportFile, selectProjectDir,
  selectProjectDirs, registerBridgeLogStore, reportFrontendReadiness, sendFrontendLogBatch, emitFrontendTraceEvent,
};
