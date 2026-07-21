import React from "react";
import { createUseSkillsPageModel } from "../app/SkillsPageModel.js";
import { useSkillEditor } from "../editor/SkillsPageEditorModel.js";
import { useSkillResolution } from "../resolution/SkillsPageResolutionModel.js";
import { SkillsPageView } from "./SkillsPageOverviewView.jsx";
import { PluginsSquareView } from "../tools/SkillsPageToolingViews.jsx";

const useSkillsPageModel = createUseSkillsPageModel({
  useSkillEditor,
  useSkillResolution,
});

export function SkillsLibraryTab({
  copy,
  projectPath,
  refreshKey,
  resolveLaunchPreferences,
}) {
  const model = useSkillsPageModel({
    projectPath,
    refreshKey,
    resolveLaunchPreferences,
  });
  return <SkillsPageView copy={copy} model={model} />;
}

export { PluginsSquareView };
