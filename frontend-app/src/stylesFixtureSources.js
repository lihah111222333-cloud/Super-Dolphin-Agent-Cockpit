import { readFileSync } from "node:fs";
import path from "node:path";
import { cwd } from "node:process";
import postcss from "postcss";

export const LAYER_TOKENS_FILE = "src/shared/styles/LayerTokens.css";
export const cssFiles = [
  LAYER_TOKENS_FILE,
  "src/styles.css",
  "src/AppChrome.css",
  "src/AppShell.css",
  "src/pages/chat/ChatPage.css",
  "src/pages/chat/ChatMessages.css",
  "src/pages/chat/ChatReasoning.css",
  "src/pages/chat/composer/ComposerDock.css",
  "src/pages/chat/runtime/RuntimePanel.css",
  "src/shared/styles/PagePrimitives.css",
  "src/pages/workflows/WorkflowPage.css",
  "src/pages/skills/SkillsPage.css",
  "src/pages/files/FilesPage.css",
  "src/pages/memory/MemoryPage.css",
  "src/pages/settings/SettingsPage.css",
  "src/pages/observability/ObservabilityPage.css",
  "src/shared/styles/PagePrimitivesLate.css",
  "src/pages/prompts/PromptPageView.css",
  "src/pages/settings/components/SettingsPageComponents.css",
  "src/shared/styles/ThemePolish.css",
  "src/shared/styles/PagePrimitivesPolish.css",
  "src/AppShellWorkbench.css",
  "src/pages/chat/ChatPageWorkbench.css",
  "src/pages/chat/components/ProjectSelector.css",
  "src/pages/workflows/WorkflowEmptyState.css",
  "src/pages/files/FilesPageWorkbench.css",
  "src/features/prompts/PromptPagePolish.css",
  "src/pages/skills/SkillsPageHub.css",
  "src/features/prompts/Personalization.css",
  "src/AppShellSidebarPolish.css",
  "src/pages/workflows/WorkflowPolish.css",
  "src/pages/chat/components/RuntimePanelPolish.css",
  "src/pages/skills/DatasourcePage.css",
  "src/AppShellSidebarThreadActions.css",
  "src/shared/styles/MarkdownReferences.css",
];
export const mainSource = readFileSync(
  path.join(cwd(), "src/main.jsx"),
  "utf8",
);
export const appSource = readFileSync(path.join(cwd(), "src/App.jsx"), "utf8");
export const suiyuanAppWindowSource = readFileSync(
  path.join(cwd(), "src/app/shell/SuiyuanAppWindow.jsx"),
  "utf8",
);
export const indexSource = readFileSync(path.join(cwd(), "index.html"), "utf8");
export const mainCssImports = [
  ...mainSource.matchAll(/^import '\.\/([^']+\.css)';$/gm),
].map((match) => `src/${match[1]}`);
export const cssSources = new Map(
  cssFiles.map((file) => [file, readFileSync(path.join(cwd(), file), "utf8")]),
);
export const css = [...cssSources.values()].join("\n");
export const root = postcss.parse(css);
export const EXPECTED_Z_INDEX_TOKENS = new Set([
  "--z-local-behind",
  "--z-local-raised",
  "--z-local-handle",
  "--z-local-sticky",
  "--z-shell-control",
  "--z-overlay-popover",
  "--z-overlay-dialog",
  "--z-overlay-lightbox",
  "--z-overlay-critical",
]);
export const EXPECTED_Z_INDEX_FILES = [
  "src/AppChrome.css",
  "src/AppShell.css",
  "src/AppShellSidebarThreadActions.css",
  "src/AppShellWorkbench.css",
  "src/pages/chat/ChatMessages.css",
  "src/pages/chat/ChatPage.css",
  "src/pages/chat/ChatPageWorkbench.css",
  "src/pages/chat/components/ProjectSelector.css",
  "src/pages/chat/composer/ComposerDock.css",
  "src/pages/chat/runtime/RuntimePanel.css",
  "src/pages/memory/MemoryPage.css",
  "src/pages/skills/SkillsPage.css",
];
export const FORBIDDEN_HOST_STACKING_PROPERTIES = new Set([
  "transform",
  "opacity",
  "filter",
  "perspective",
  "contain",
  "isolation",
]);
export const OVERLAY_THEME_SELECTOR_MIGRATIONS = [
  [
    '.sa-window[data-theme="light"] .runtime-stat-tooltip',
    '#overlay-root[data-theme="light"] .runtime-stat-tooltip',
  ],
  [
    '.sa-window[data-theme="light"] .warning-log-popover',
    '#overlay-root[data-theme="light"] .warning-log-popover',
  ],
  [
    '.sa-window[data-theme="light"] .warning-log-popover code',
    '#overlay-root[data-theme="light"] .warning-log-popover code',
  ],
  [
    '.sa-window[data-theme="light"] .skills-editor-modal button',
    '#overlay-root[data-theme="light"] .skills-editor-modal button',
  ],
  [
    '.sa-window[data-theme="light"] .skills-editor-modal button:hover:not(:disabled)',
    '#overlay-root[data-theme="light"] .skills-editor-modal button:hover:not(:disabled)',
  ],
  [
    '.sa-window[data-theme="light"] .skills-editor-modal button.ghost',
    '#overlay-root[data-theme="light"] .skills-editor-modal button.ghost',
  ],
];
