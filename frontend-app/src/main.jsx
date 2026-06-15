import { createElement, Profiler, StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

// Base layers load first so page modules can override shared shell primitives.
import './styles.css';
import './AppChrome.css';
import './AppShell.css';

// Route and feature styles stay in navigation order to keep cascade drift reviewable.
import './pages/chat/ChatPage.css';
import './pages/chat/ChatMessages.css';
import './pages/chat/ChatReasoning.css';
import './pages/chat/components/ComposerDock.css';
import './pages/chat/components/RuntimePanel.css';
import './shared/styles/PagePrimitives.css';
import './pages/workflows/WorkflowPage.css';
import './pages/skills/SkillsPage.css';
import './pages/files/FilesPage.css';
import './pages/memory/MemoryPage.css';
import './pages/settings/SettingsPage.css';
import './pages/observability/ObservabilityPage.css';
import './shared/styles/PagePrimitivesLate.css';
import './features/prompts/PromptPageView.css';
import './pages/settings/components/SettingsPageComponents.css';

// Late polish layers intentionally override earlier page modules.
import './shared/styles/ThemePolish.css';
import './shared/styles/PagePrimitivesPolish.css';
import './AppShellWorkbench.css';
import './pages/chat/ChatPageWorkbench.css';
import './pages/workflows/WorkflowEmptyState.css';
import './pages/files/FilesPageWorkbench.css';
import './features/prompts/PromptPagePolish.css';
import './pages/skills/SkillsPageHub.css';
import './features/prompts/Personalization.css';
import './AppShellSidebarPolish.css';
import './pages/workflows/WorkflowPolish.css';
import './pages/chat/components/RuntimePanelPolish.css';
import './pages/skills/DatasourcePage.css';
import './AppShellSidebarThreadActions.css';
import './shared/styles/MarkdownReferences.css';
import App, { APP_PROFILER_ID } from './App.jsx';
import { emitFrontendTraceEvent } from './shared/api/backendApi.js';

const REACT_RENDER_SLOW_MS = 50;

/**
 * @param {string} id
 * @param {string} phase
 * @param {number} actualDuration
 */
function emitSlowRenderTrace(id, phase, actualDuration) {
  const durationMs = Number(actualDuration);
  if (!Number.isFinite(durationMs) || durationMs < REACT_RENDER_SLOW_MS) return;
  emitFrontendTraceEvent({
    phase: 'frontend.render.slow',
    duration_ms: durationMs,
    status: 'ok',
    metadata: {
      component: id,
      react_phase: phase,
    },
  });
}

createRoot(document.getElementById('root')).render(
  createElement(
    StrictMode,
    null,
    createElement(
      Profiler,
      { id: APP_PROFILER_ID, onRender: emitSlowRenderTrace },
      createElement(App),
    ),
  ),
);
