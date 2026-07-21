import React, { useState } from "react";

import { DataSourceView } from "../../datasource/DataSourceView.jsx";

export function SkillsPageController({
  copy,
  projectPath,
  refreshKey,
  renderLibrary,
  renderPlugins,
}) {
  const [subTab, setSubTab] = useState("plugins");

  return (
    <div className="skills-tabbed-container">
      <div className="skills-subtabs-header">
        <button
          type="button"
          className={subTab === "plugins" ? "active" : ""}
          onClick={() => setSubTab("plugins")}
        >
          {copy.tabs.plugins}
        </button>
        <button
          type="button"
          className={subTab === "library" ? "active" : ""}
          onClick={() => setSubTab("library")}
        >
          {copy.tabs.library}
        </button>
        <button
          type="button"
          className={subTab === "datasource" ? "active" : ""}
          onClick={() => setSubTab("datasource")}
        >
          {copy.tabs.datasource}
        </button>
      </div>
      <div className="skills-tab-content">
        {subTab === "plugins" ? (
          renderPlugins()
        ) : subTab === "datasource" ? (
          <DataSourceView copy={copy} />
        ) : (
          renderLibrary({ projectPath, refreshKey })
        )}
      </div>
    </div>
  );
}
