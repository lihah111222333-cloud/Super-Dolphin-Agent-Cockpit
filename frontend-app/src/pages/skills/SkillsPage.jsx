import React from "react";

import { APP_COPY } from "../../shared/i18n/appI18n.js";
import { SkillsPageController } from "./library/app/SkillsPageController.jsx";
import {
  PluginsSquareView,
  SkillsLibraryTab,
} from "./library/overview/SkillsPageLibrary.jsx";
import "./SkillsPage.css";

function SkillsPage({
  copy = APP_COPY.zh.skills,
  projectPath,
  refreshKey = 0,
  resolveLaunchPreferences,
}) {
  return (
    <SkillsPageController
      copy={copy}
      projectPath={projectPath}
      refreshKey={refreshKey}
      renderPlugins={() => (
        <PluginsSquareView copy={copy} projectPath={projectPath} />
      )}
      renderLibrary={() => (
        <SkillsLibraryTab
          copy={copy}
          projectPath={projectPath}
          refreshKey={refreshKey}
          resolveLaunchPreferences={resolveLaunchPreferences}
        />
      )}
    />
  );
}

export { SkillsPage };
