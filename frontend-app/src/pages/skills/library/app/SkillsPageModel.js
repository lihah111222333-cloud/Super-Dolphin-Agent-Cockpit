import { useCallback, useState } from "react";
import { optionalSettingsCwd } from "../../../shared/pageShared.js";
import {
  useSkillsDashboard,
  useSkillsFilters,
} from "../dashboard/SkillsPageDashboardModel.js";

export function createUseSkillsPageModel({
  useSkillEditor,
  useSkillResolution,
}) {
  return function useSkillsPageModel({
    projectPath,
    refreshKey,
    resolveLaunchPreferences,
  }) {
    const projectCwd = optionalSettingsCwd(projectPath);
    const [query, setQuery] = useState("");
    const [scopeFilter, setScopeFilter] = useState("all");
    const [status, setStatus] = useState({ projectCwd, error: "", notice: "" });
    if (status.projectCwd !== projectCwd)
      setStatus({ projectCwd, error: "", notice: "" });
    const setError = useCallback(
      (value) =>
        setStatus((current) => ({
          ...current,
          error: typeof value === "function" ? value(current.error) : value,
        })),
      [],
    );
    const setNotice = useCallback(
      (value) =>
        setStatus((current) => ({
          ...current,
          notice: typeof value === "function" ? value(current.notice) : value,
        })),
      [],
    );
    const dashboard = useSkillsDashboard(projectCwd, refreshKey);
    const filters = useSkillsFilters(dashboard.items, query, scopeFilter);
    const editor = useSkillEditor({
      projectPath,
      refreshSkillSurface: dashboard.refreshSkillSurface,
      resolveLaunchPreferences,
      setError,
      setNotice,
      skills: dashboard.items,
    });
    const resolution = useSkillResolution({
      projectPath,
      refreshSkillSurface: dashboard.refreshSkillSurface,
      resetKey: projectCwd,
      resolutionConflicts: dashboard.resolutionConflicts,
      setError,
      setNotice,
    });
    const error = status.projectCwd === projectCwd ? status.error : "";
    const notice = status.projectCwd === projectCwd ? status.notice : "";
    return {
      dashboard,
      editor,
      error,
      filters,
      isProjectPending: !projectCwd,
      notice,
      query,
      resolution,
      scopeFilter,
      setQuery,
      setScopeFilter,
    };
  };
}
