import { beforeAll, vi } from 'vitest';

const APP_ROUTE_MODULE_LOADERS = [
  () => import('../pages/chat/ChatPage.jsx'),
  () => import('../pages/files/FilesPage.jsx'),
  () => import('../pages/observability/ObservabilityPage.jsx'),
  () => import('../pages/settings/SettingsPage.jsx'),
  () => import('../pages/workflows/WorkflowPage.jsx'),
];

function preloadAppRouteModules() {
  return Promise.all(APP_ROUTE_MODULE_LOADERS.map((loadModule) => loadModule()));
}

vi.setConfig({ testTimeout: 15_000 });
beforeAll(preloadAppRouteModules, 180_000);
