// @ts-check

import { createBackendApi } from './backend/backendApiFactoryThread.js';
import { registerBridgeLogStore, sendFrontendLogBatch, emitFrontendTraceEvent } from './wailsBridge.js';

export { RPC_METHODS } from './backend/backendRpcMethods.js';

export { createBackendApi };


/**
 * @param {unknown} unused
 * @param {readonly string[]} keys
 */
function takePayloadFields(unused, keys) {
  return { unused, keys };
}

/**
 * @param {unknown} unused
 */
function threadStartPayload(unused) {
  return takePayloadFields(unused, [
    'agent_id',
    'agent_key',
    'agent_memory_scope',
    'agent_type',
    'agentId',
    'agentMemoryScope',
    'agentType',
    'approval_policy',
    'approvalPolicy',
    'base_instructions',
    'baseInstructions',
    'codexModelProvider',
    'codex_model_provider',
    'config',
    'cwd',
    'defer_spawn',
    'deferSpawn',
    'developer_instructions',
    'developerInstructions',
    'effort',
    'instructions',
    'language',
    'launch_intent_id',
    'launchIntentId',
    'manual_skill_selection',
    'manualSkillSelection',
    'memory_scope',
    'memoryScope',
    'model',
    'model_provider',
    'modelProvider',
    'name',
    'optimistic_user_message',
    'optimisticUserMessage',
    'parent_agent_id',
    'parentAgentId',
    'parentId',
    'parentID',
    'personality',
    'prompt',
    'prompt_key',
    'promptKey',
    'provider',
    'sandbox',
    'selected_skill_refs',
    'selected_skills',
    'selectedSkillRefs',
    'selectedSkills',
    'skip_initial_runtime_sync',
    'skipInitialRuntimeSync',
    'summary',
    'tool_surface_mode',
    'toolSurfaceMode',
  ]);
}

/**
 * @param {unknown} unused
 */
function turnStartPayload(unused) {
  return takePayloadFields(unused, [
    'additional_working_directories',
    'additionalWorkingDirectories',
    'approval_policy',
    'approvalPolicy',
    'attachments',
    'cwd',
    'effort',
    'enabled_tools',
    'enabledTools',
    'files',
    'git_root',
    'gitRoot',
    'images',
    'input',
    'is_worktree',
    'isWorktree',
    'language',
    'manual_skill_selection',
    'manualSkillSelection',
    'mcp_snapshot',
    'mcpSnapshot',
    'model',
    'output_schema',
    'outputSchema',
    'prompt',
    'provider',
    'selected_skill_refs',
    'selected_skills',
    'selectedSkillRefs',
    'selectedSkills',
    'session_flags',
    'sessionFlags',
    'thread_id',
    'threadId',
    'threadID',
  ]);
}

void threadStartPayload;
void turnStartPayload;

const backendApi = createBackendApi();

const {
  callBackend, readConfig, readLspPromptHint, writeLspPromptHint,
  readBuiltinTools, writeBuiltinTool, checkAppUpdate, downloadAppUpdate,
  installAppUpdate, installLatestAppUpdate, getWindowBootstrap, getSidebarState,
  openNewWindow, getThreadState, getProjects, setActiveProject,
  addProject, removeProject, getPreference, getAllPreferences,
  setPreference, listModelProviders, saveModelProviders, applyModelProvider,
  getDashboardPage, getVideoApiKey, setVideoApiKey, listDashboardLogs,
  getObservabilityTrace, getObservabilityThreadRecent, listObservabilityRecent, listObservabilitySlow,
  listObservabilityErrors, getObservabilityStatus, getMemorySnapshot, getMemoryEntry,
  upsertMemoryEntry, deleteMemoryEntry, setMemoryAutoDreamIntent, mergeMemoryEntries,
  ignoreMemorySimilarity, consolidateMemorySimilarities, startConsolidateMemorySimilarities, getMemoryConsolidationStatus,
  listSharedFiles, readSharedFile, deleteSharedFile, writeWorkflowMaterial,
  listPromptAssets, getDashboardPrompts, getPrompt, writePrompt,
  deletePrompt, draftPromptIntent, commitPromptIntent, discardPromptIntent,
  dryRunPromptIntent, getPersonalizationProfile, savePersonalizationProfile, listPromptSections,
  writePromptSection, deletePromptSection, listDags, getDagDetail,
  getDagRuns, getDagRun, startDag, createAndStartDag,
  dispatchDagNode, terminateDagRun, terminateDag, deleteDag,
  applyDagOps, listWorkflowTemplates, getWorkflowTemplate, renderWorkflowTemplateDraft,
  saveWorkflowTemplate, rollbackWorkflowTemplate, listCronJobs, getCronJob,
  createCronJob, updateCronJob, deleteCronJob, runCronJobOnce,
  setCronJobEnabled, listCronJobRuns, locateCodeFile, openCodeFile,
  openPath, saveCodeFile, readSkill, listSkillFiles,
  writeSkill, createSkill, importSkillDirectories, suggestSkillSummary,
  listSkillResolutions, previewSkillResolution, applySkillResolution, deleteSkill,
  createSkillTool, listSkillTools, getSkillTool, updateSkillTool,
  deleteSkillTool, createDatasourceDocument, importDatasourceLocalFile, listDatasourceDocuments,
  getDatasourceDocument, listDatasourceChunks, updateDatasourceDocument, deleteDatasourceDocument,
  listMCPServers, startSQLiteMCPServer, stopSQLiteMCPServer, startPlaywrightMCPServer,
  stopPlaywrightMCPServer, setMCPToolLifecycle, listMCPToolLifecycle, exportMCPToolLifecycle,
  listThreadsPage, listLoadedThreadsPage, getThreadMessages, resolveThreadIdentity,
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
  callBackend, readConfig, readLspPromptHint, writeLspPromptHint,
  readBuiltinTools, writeBuiltinTool, checkAppUpdate, downloadAppUpdate,
  installAppUpdate, installLatestAppUpdate, getWindowBootstrap, getSidebarState,
  openNewWindow, getThreadState, getProjects, setActiveProject,
  addProject, removeProject, getPreference, getAllPreferences,
  setPreference, listModelProviders, saveModelProviders, applyModelProvider,
  getDashboardPage, getVideoApiKey, setVideoApiKey, listDashboardLogs,
  getObservabilityTrace, getObservabilityThreadRecent, listObservabilityRecent, listObservabilitySlow,
  listObservabilityErrors, getObservabilityStatus, getMemorySnapshot, getMemoryEntry,
  upsertMemoryEntry, deleteMemoryEntry, setMemoryAutoDreamIntent, mergeMemoryEntries,
  ignoreMemorySimilarity, consolidateMemorySimilarities, startConsolidateMemorySimilarities, getMemoryConsolidationStatus,
  listSharedFiles, readSharedFile, deleteSharedFile, writeWorkflowMaterial,
  listPromptAssets, getDashboardPrompts, getPrompt, writePrompt,
  deletePrompt, draftPromptIntent, commitPromptIntent, discardPromptIntent,
  dryRunPromptIntent, getPersonalizationProfile, savePersonalizationProfile, listPromptSections,
  writePromptSection, deletePromptSection, listDags, getDagDetail,
  getDagRuns, getDagRun, startDag, createAndStartDag,
  dispatchDagNode, terminateDagRun, terminateDag, deleteDag,
  applyDagOps, listWorkflowTemplates, getWorkflowTemplate, renderWorkflowTemplateDraft,
  saveWorkflowTemplate, rollbackWorkflowTemplate, listCronJobs, getCronJob,
  createCronJob, updateCronJob, deleteCronJob, runCronJobOnce,
  setCronJobEnabled, listCronJobRuns, locateCodeFile, openCodeFile,
  openPath, saveCodeFile, readSkill, listSkillFiles,
  writeSkill, createSkill, importSkillDirectories, suggestSkillSummary,
  listSkillResolutions, previewSkillResolution, applySkillResolution, deleteSkill,
  createSkillTool, listSkillTools, getSkillTool, updateSkillTool,
  deleteSkillTool, createDatasourceDocument, importDatasourceLocalFile, listDatasourceDocuments,
  getDatasourceDocument, listDatasourceChunks, updateDatasourceDocument, deleteDatasourceDocument,
  listMCPServers, startSQLiteMCPServer, stopSQLiteMCPServer, startPlaywrightMCPServer,
  stopPlaywrightMCPServer, setMCPToolLifecycle, listMCPToolLifecycle, exportMCPToolLifecycle,
  listThreadsPage, listLoadedThreadsPage, getThreadMessages, resolveThreadIdentity,
  archiveThread, unarchiveThread, deleteThread, getThreadConfig,
  setThreadConfig, forkThread, startThread, startTurn, interruptTurn,
  forceCompleteTurn, respondApproval, compactThread, recoverThread,
  renameThread, getBuildInfo, onAgentEvent, onBridgeEvent,
  onFilesDropped, onRuntimeReconnect, readDroppedTextFiles, saveClipboardImage,
  saveTextFile, openSharedFile, previewSharedFile, beginTextClipboardWrite,
  copyTextToClipboard, selectFiles, selectDatasourceImportFile, selectProjectDir,
  selectProjectDirs, registerBridgeLogStore, sendFrontendLogBatch, emitFrontendTraceEvent,
};
