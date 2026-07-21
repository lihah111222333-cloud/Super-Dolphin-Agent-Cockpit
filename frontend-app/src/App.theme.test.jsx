import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import App from './App.jsx';
import { getStoredTheme, syncThemeDOM } from './app/appShellModel.js';

let createRootMock = null;
let syncThemeDOMMock = null;

vi.mock('react-dom/client', async (importOriginal) => {
  const original = await importOriginal();
  return {
    ...original,
    createRoot: (...args) => (createRootMock ? createRootMock(...args) : original.createRoot(...args)),
  };
});

vi.mock('./app/appShellModel.js', async (importOriginal) => {
  const original = await importOriginal();
  return {
    ...original,
    syncThemeDOM: (...args) => (syncThemeDOMMock ? syncThemeDOMMock(...args) : original.syncThemeDOM(...args)),
  };
});

const backend = vi.hoisted(() => {
  const names = `
    readConfig getWindowBootstrap openNewWindow getProjects setActiveProject addProject removeProject
    callBackend checkAppUpdate installLatestAppUpdate getSidebarState getThreadState getThreadMessages
    getBuildInfo getVideoApiKey getDashboardPage getObservabilityStatus getObservabilityTrace
    getObservabilityThreadRecent listObservabilityRecent listObservabilitySlow listObservabilityErrors
    listSharedFiles listPromptAssets getDashboardPrompts getPrompt writePrompt readLspPromptHint
    writeLspPromptHint readBuiltinTools writeBuiltinTool listDashboardLogs getPersonalizationProfile
    savePersonalizationProfile listPromptSections writePromptSection deletePromptSection deletePrompt
    draftPromptIntent commitPromptIntent discardPromptIntent dryRunPromptIntent getMemorySnapshot
    getMemoryEntry upsertMemoryEntry deleteMemoryEntry setMemoryAutoDreamIntent mergeMemoryEntries
    ignoreMemorySimilarity consolidateMemorySimilarities startConsolidateMemorySimilarities
    getMemoryConsolidationStatus listDags getDagDetail getDagRuns getDagRun startDag terminateDagRun
    deleteDag applyDagOps listWorkflowTemplates getWorkflowTemplate renderWorkflowTemplateDraft deleteSkill
    listCronJobs getCronJob createCronJob updateCronJob deleteCronJob runCronJobOnce setCronJobEnabled
    listCronJobRuns readSkill listSkillFiles createSkill writeSkill importSkillDirectories
    suggestSkillSummary selectProjectDir selectProjectDirs createSkillTool listSkillTools getSkillTool
    updateSkillTool deleteSkillTool listMCPServers listToolbridgeTools startSQLiteMCPServer
    stopSQLiteMCPServer startPlaywrightMCPServer stopPlaywrightMCPServer listSkillResolutions
    previewSkillResolution applySkillResolution readSharedFile deleteSharedFile getPreference forkThread
    startThread startTurn interruptTurn forceCompleteTurn compactThread recoverThread respondApproval
    resolveThreadIdentity archiveThread unarchiveThread deleteThread getThreadConfig setThreadConfig
    renameThread setPreference setVideoApiKey selectFiles saveClipboardImage saveTextFile locateCodeFile
    openCodeFile openPath saveCodeFile beginTextClipboardWrite copyTextToClipboard emitFrontendTraceEvent
  `.trim().split(/\s+/);
  return {
    ...Object.fromEntries(names.map((name) => [name, vi.fn()])),
    onFilesDropped: vi.fn(() => () => {}),
    onRuntimeReconnect: vi.fn(() => () => {}),
    onBridgeEvent: vi.fn(() => () => {}),
  };
});

vi.mock('./shared/api/backendApi.js', () => ({
  ...backend,
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

function installOverlayHost() {
  document.querySelectorAll('#overlay-root').forEach((node) => node.remove());
  const host = document.createElement('div');
  host.id = 'overlay-root';
  document.body.append(host);
  return host;
}

describe('App theme cold start and switching behavior', () => {
  let overlayHost;

  beforeEach(() => {
    overlayHost = installOverlayHost();
    window.localStorage.clear();
    document.documentElement.removeAttribute('data-theme');
    document.body.removeAttribute('data-theme');
  });

  afterEach(() => {
    cleanup();
    window.dispatchEvent(new Event('pagehide'));
    overlayHost?.remove();
    document.querySelectorAll('#overlay-root').forEach((node) => node.remove());
    window.localStorage.clear();
    document.documentElement.removeAttribute('data-theme');
    document.body.removeAttribute('data-theme');
    createRootMock = null;
    syncThemeDOMMock = null;
    vi.useRealTimers();
  });

  it('performs cold start with no stored value (defaults to light)', () => {
    const theme = getStoredTheme();
    expect(theme).toBe('light');
    syncThemeDOM(theme);
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
    expect(document.body).toHaveAttribute('data-theme', 'light');
  });

  it('performs cold start with pre-stored dark theme', () => {
    window.localStorage.setItem('super-dolphin-theme', 'dark');
    const theme = getStoredTheme();
    expect(theme).toBe('dark');
    syncThemeDOM(theme);
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
    expect(document.body).toHaveAttribute('data-theme', 'dark');
  });

  it('executes main.jsx with no stored theme, calling syncThemeDOM before createRoot', async () => {
    const rootDiv = document.createElement('div');
    rootDiv.id = 'root';
    document.body.appendChild(rootDiv);
    const renderMock = vi.fn();
    createRootMock = vi.fn().mockReturnValue({ render: renderMock });
    syncThemeDOMMock = vi.fn();

    await import('./main.jsx?t=no-stored-theme');

    expect(syncThemeDOMMock).toHaveBeenCalledWith('light');
    expect(createRootMock).toHaveBeenCalled();
    expect(syncThemeDOMMock.mock.invocationCallOrder[0]).toBeLessThan(createRootMock.mock.invocationCallOrder[0]);
    rootDiv.remove();
  });

  it('executes main.jsx with dark stored theme, calling syncThemeDOM before createRoot', async () => {
    window.localStorage.setItem('super-dolphin-theme', 'dark');
    const rootDiv = document.createElement('div');
    rootDiv.id = 'root';
    document.body.appendChild(rootDiv);
    const renderMock = vi.fn();
    createRootMock = vi.fn().mockReturnValue({ render: renderMock });
    syncThemeDOMMock = vi.fn();

    await import('./main.jsx?t=dark-stored-theme');

    expect(syncThemeDOMMock).toHaveBeenCalledWith('dark');
    expect(createRootMock).toHaveBeenCalled();
    expect(syncThemeDOMMock.mock.invocationCallOrder[0]).toBeLessThan(createRootMock.mock.invocationCallOrder[0]);
    rootDiv.remove();
  });

  it('toggles the local color theme without calling backend preferences', async () => {
    render(<App skipBootstrap />);
    const shell = await screen.findByTestId('frontend-app');
    const preferenceCallsBeforeToggle = backend.setPreference.mock.calls.length;

    expect(shell).toHaveAttribute('data-theme', 'light');
    expect(overlayHost).toHaveAttribute('data-theme', 'light');
    fireEvent.click(screen.getByRole('button', { name: '切换到黑夜模式' }));
    expect(shell).toHaveAttribute('data-theme', 'dark');
    expect(overlayHost).toHaveAttribute('data-theme', 'dark');
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
    expect(document.body).toHaveAttribute('data-theme', 'dark');
    expect(window.localStorage.getItem('super-dolphin-theme')).toBe('dark');

    overlayHost.setAttribute('data-theme', 'tampered');
    fireEvent.click(screen.getByRole('button', { name: '切换到白天模式' }));
    expect(shell).toHaveAttribute('data-theme', 'light');
    expect(overlayHost).toHaveAttribute('data-theme', 'light');
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
    expect(document.body).toHaveAttribute('data-theme', 'light');
    expect(window.localStorage.getItem('super-dolphin-theme')).toBe('light');
    expect(backend.setPreference.mock.calls.length).toBe(preferenceCallsBeforeToggle);
  });
});
