import { createAppTestContext } from "./appTestContext.jsx";
import React from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AppErrorBoundary } from "../app/AppErrorBoundary.jsx";
import { rightPanelWidthSchema } from "../app/shell/model/shellLayoutSchema.js";
import {
  rightPanelDefaultWidth,
  rightPanelMaxWidth,
  threadRailTargetWidth,
} from "../pages/chat/model/chatWorkbenchLayoutModel.js";
import {
  clientStore,
  resetClientStoreForTests,
} from "../entities/client/model/clientStoreTestApi.js";
import {
  frontendHealthSnapshot,
  resetFrontendHealthForTest,
} from "../shared/diagnostics/frontendHealthStore.js";
import { normalizeMemorySnapshot as normalizeMemorySnapshotForFacade } from "../adapters/memoryAdapter.js";
import mermaid from "mermaid";
import App from "../App.jsx";

const backend = vi.hoisted(() => {
  const mockNames = `
      readConfig getWindowBootstrap openNewWindow getProjects setActiveProject addProject removeProject
      callBackend checkAppUpdate installLatestAppUpdate
    getSidebarState getThreadState getThreadMessages getBuildInfo getVideoApiKey getDashboardPage getObservabilityStatus
    getObservabilityTrace getObservabilityThreadRecent listObservabilityRecent listObservabilitySlow listModelProviders
    listObservabilityErrors listSharedFiles listPromptAssets getDashboardPrompts getPrompt writePrompt
    readLspPromptHint writeLspPromptHint readBuiltinTools writeBuiltinTool listDashboardLogs
    getPersonalizationProfile savePersonalizationProfile listPromptSections writePromptSection deletePromptSection
    deletePrompt draftPromptIntent commitPromptIntent discardPromptIntent dryRunPromptIntent getMemorySnapshot
    getMemoryEntry upsertMemoryEntry deleteMemoryEntry setMemoryAutoDreamIntent mergeMemoryEntries
    ignoreMemorySimilarity consolidateMemorySimilarities startConsolidateMemorySimilarities getMemoryConsolidationStatus
    listDags getDagDetail getDagRuns getDagRun startDag terminateDagRun deleteDag applyDagOps listWorkflowTemplates getWorkflowTemplate renderWorkflowTemplateDraft deleteSkill
    listCronJobs getCronJob createCronJob updateCronJob deleteCronJob runCronJobOnce setCronJobEnabled listCronJobRuns
    readSkill listSkillFiles createSkill writeSkill importSkillDirectories suggestSkillSummary selectProjectDir selectProjectDirs
    createSkillTool listSkillTools getSkillTool updateSkillTool deleteSkillTool
    listMCPServers listToolbridgeTools startSQLiteMCPServer stopSQLiteMCPServer startPlaywrightMCPServer stopPlaywrightMCPServer
    listSkillResolutions previewSkillResolution applySkillResolution readSharedFile deleteSharedFile getPreference
    forkThread startThread startTurn interruptTurn forceCompleteTurn compactThread recoverThread respondApproval resolveThreadIdentity archiveThread unarchiveThread
    deleteThread getThreadConfig setThreadConfig renameThread setPreference setVideoApiKey selectFiles saveClipboardImage saveTextFile
    locateCodeFile openCodeFile openPath saveCodeFile beginTextClipboardWrite copyTextToClipboard emitFrontendTraceEvent
  `
    .trim()
    .split(/\s+/);
  return {
    ...Object.fromEntries(mockNames.map((name) => [name, vi.fn()])),
    onFilesDropped: vi.fn(() => () => {}),
    onRuntimeReconnect: vi.fn(() => () => {}),
    onBridgeEvent: vi.fn((callback) => {
      backend.__bridgeCallback = callback;
      return () => {
        backend.__bridgeCallback = null;
      };
    }),
  };
});

vi.mock("../shared/api/backendApi.js", () => ({
  ...backend,
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

vi.mock("mermaid", () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn((_id, source) =>
      Promise.resolve({
        svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
      }),
    ),
  },
}));

function resetMermaidMocks() {
  mermaid.initialize.mockReset();
  mermaid.render.mockReset();
  mermaid.render.mockImplementation((_id, source) =>
    Promise.resolve({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    }),
  );
}

const support = createAppTestContext({
  backend,
  App,
  resetClientStoreForTests,
});
const {
  dispatchPointer,
  deferred,
  formatParsedTimestampForTest,
  promptPreferenceValue,
  mockPromptPreferences,
  decodedSvgDataUrl,
  waitForBackendThreadHeading,
  appPreferenceDefaults,
  mockShortcutPreferenceLoad,
  openPluginsAndSkillsPage,
  getSidebarNavButton,
  getBackendThreadText,
  getThreadCardByName,
  clickThreadCardByName,
  queryThreadCardByName,
  findThreadCardByName,
  defaultSkillFixtures,
  resetConnectedShellTestState,
  installAppOverlayHost,
  createShellLayoutStorage,
  mockBootstrapBackendDefaults,
  mockDashboardPageDefaults,
  mockObservabilityDefaults,
  mockPromptDefaults,
  canonicalPromptRPCItem,
  mockPromptWizardEntryPrompt,
  mockMemoryDefaults,
  mockWorkflowDefaults,
  mockCronDefaults,
  mockSkillDefaults,
  mockSharedFileDefaults,
  mockSettingsAndThreadDefaults,
  mockTraceDashboardQueryResult,
  openTraceDashboardForTraceId,
  expectTraceDashboardRpcCalls,
  expectTraceDashboardRows,
  expectTraceDashboardDetails,
  showAllTraceDashboardEvents,
  mockRecentSystemLogsResult,
  openRecentSystemLogs,
  expectRecentSystemLogsTable,
  expectRecentSystemLogsRpcCall,
  copyTraceFromRecentLogs,
  toggleInlineTraceFromRecentLogs,
  mockPromptAssetWorkflow,
  openPromptAssetsPage,
  openPromptWizardFromPendingCard,
  editAndDeleteReviewerPrompt,
  handlePendingPromptDraft,
  createGeneratedPromptIntent,
  createSimilaritySnapshots,
  openMemoryCenterWithSimilarity,
  runConsolidationUntilSimilaritiesClear,
  expectSimilarityWarningCleared,
  createSharedFileState,
  mockSharedFileWorkflow,
  openSharedFilesPage,
  refreshSharedFilesFromBridge,
  refreshSharedFilesFromFocus,
  previewFinalSharedFile,
  exportAndDeleteWorkSharedFile,
  continueChatFromFinalSharedFile,
  mockWorkflowDagLifecycle,
  openWorkflowDashboard,
  runAndStopWorkflowDag,
  createWorkflowSchedule,
  editWorkflowStep,
  deleteWorkflowDag,
  designWorkflowWithAi,
} = support;

export function installAppTestHooks() {
  beforeEach(resetMermaidMocks);
  beforeEach(installAppOverlayHost);
  beforeEach(resetConnectedShellTestState);
  beforeEach(mockBootstrapBackendDefaults);
  beforeEach(mockDashboardPageDefaults);
  beforeEach(mockObservabilityDefaults);
  beforeEach(mockPromptDefaults);
  beforeEach(mockMemoryDefaults);
  beforeEach(mockWorkflowDefaults);
  beforeEach(mockCronDefaults);
  beforeEach(mockSkillDefaults);
  beforeEach(mockSharedFileDefaults);
  beforeEach(mockSettingsAndThreadDefaults);
  beforeEach(resetFrontendHealthForTest);
  afterEach(() => {
    cleanup();
    document.querySelectorAll("#overlay-root").forEach((node) => node.remove());
    vi.useRealTimers();
  });
}

export const testEnv = {
  React,
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
  afterEach,
  beforeEach,
  expect,
  it,
  vi,
  AppErrorBoundary,
  rightPanelWidthSchema,
  rightPanelDefaultWidth,
  rightPanelMaxWidth,
  threadRailTargetWidth,
  resetClientStoreForTests,
  useClientStore: clientStore,
  frontendHealthSnapshot,
  resetFrontendHealthForTest,
  normalizeMemorySnapshotForFacade,
  mermaid,
  App,
  support,
  backend,
  dispatchPointer,
  deferred,
  formatParsedTimestampForTest,
  promptPreferenceValue,
  mockPromptPreferences,
  decodedSvgDataUrl,
  waitForBackendThreadHeading,
  appPreferenceDefaults,
  mockShortcutPreferenceLoad,
  openPluginsAndSkillsPage,
  getSidebarNavButton,
  getBackendThreadText,
  getThreadCardByName,
  clickThreadCardByName,
  queryThreadCardByName,
  findThreadCardByName,
  defaultSkillFixtures,
  resetConnectedShellTestState,
  installAppOverlayHost,
  createShellLayoutStorage,
  mockBootstrapBackendDefaults,
  mockDashboardPageDefaults,
  mockObservabilityDefaults,
  mockPromptDefaults,
  canonicalPromptRPCItem,
  mockPromptWizardEntryPrompt,
  mockMemoryDefaults,
  mockWorkflowDefaults,
  mockCronDefaults,
  mockSkillDefaults,
  mockSharedFileDefaults,
  mockSettingsAndThreadDefaults,
  mockTraceDashboardQueryResult,
  openTraceDashboardForTraceId,
  expectTraceDashboardRpcCalls,
  expectTraceDashboardRows,
  expectTraceDashboardDetails,
  showAllTraceDashboardEvents,
  mockRecentSystemLogsResult,
  openRecentSystemLogs,
  expectRecentSystemLogsTable,
  expectRecentSystemLogsRpcCall,
  copyTraceFromRecentLogs,
  toggleInlineTraceFromRecentLogs,
  mockPromptAssetWorkflow,
  openPromptAssetsPage,
  openPromptWizardFromPendingCard,
  editAndDeleteReviewerPrompt,
  handlePendingPromptDraft,
  createGeneratedPromptIntent,
  createSimilaritySnapshots,
  openMemoryCenterWithSimilarity,
  runConsolidationUntilSimilaritiesClear,
  expectSimilarityWarningCleared,
  createSharedFileState,
  mockSharedFileWorkflow,
  openSharedFilesPage,
  refreshSharedFilesFromBridge,
  refreshSharedFilesFromFocus,
  previewFinalSharedFile,
  exportAndDeleteWorkSharedFile,
  continueChatFromFinalSharedFile,
  mockWorkflowDagLifecycle,
  openWorkflowDashboard,
  runAndStopWorkflowDag,
  createWorkflowSchedule,
  editWorkflowStep,
  deleteWorkflowDag,
  designWorkflowWithAi,
};
