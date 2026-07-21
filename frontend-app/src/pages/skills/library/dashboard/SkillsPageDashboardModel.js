import { useCallback, useEffect, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { dashboardQueryKey } from "../../../shared/pageShared.js";
import { runBackgroundAction } from "../../../../shared/ui/runUIAction.js";
import { fetchSkillsDashboard } from "./skillsDashboardModel.js";
import { fetchSkillResolutionsDashboard } from "../SkillsPageMarkdownModel.js";

function useSkillsDashboard(projectCwd, refreshKey) {
  const queryClient = useQueryClient();
  const skillRefreshKey = Number(refreshKey || 0);
  const {
    data: skillsData,
    error: skillsError,
    isPending: skillsPending,
    refetch: refetchSkills,
  } = useQuery({
    queryKey: dashboardQueryKey(
      projectCwd,
      "skills",
      `revision:${skillRefreshKey}`,
    ),
    queryFn: () =>
      runBackgroundAction("skill.dashboard.load", async () => {
        const data = await fetchSkillsDashboard(projectCwd);
        queryClient.setQueryData(dashboardQueryKey(projectCwd, "skills"), data);
        return data;
      }),
    enabled: Boolean(projectCwd),
    initialData: () =>
      queryClient.getQueryData(dashboardQueryKey(projectCwd, "skills")),
    initialDataUpdatedAt: 0,
    placeholderData: (previousData) => previousData,
  });
  const {
    data: resolutionsData,
    error: resolutionsError,
    isPending: resolutionsPending,
    refetch: refetchResolutions,
  } = useQuery({
    queryKey: dashboardQueryKey(
      projectCwd,
      "skill-resolutions",
      `revision:${skillRefreshKey}`,
    ),
    queryFn: () =>
      runBackgroundAction("skill.resolutions.load", async () => {
        const data = await fetchSkillResolutionsDashboard(projectCwd);
        queryClient.setQueryData(
          dashboardQueryKey(projectCwd, "skill-resolutions"),
          data,
        );
        return data;
      }),
    enabled: Boolean(projectCwd),
    initialData: () =>
      queryClient.getQueryData(
        dashboardQueryKey(projectCwd, "skill-resolutions"),
      ),
    initialDataUpdatedAt: 0,
    placeholderData: (previousData) => previousData,
  });
  const skillsQuery = {
    data: skillsData,
    error: skillsError,
    isPending: skillsPending,
  };
  const resolutionsQuery = {
    data: resolutionsData,
    error: resolutionsError,
    isPending: resolutionsPending,
  };
  const items = useMemo(
    () => (Array.isArray(skillsData) ? skillsData : []),
    [skillsData],
  );
  const resolutionConflicts = useMemo(
    () => (Array.isArray(resolutionsData) ? resolutionsData : []),
    [resolutionsData],
  );
  const refreshSkillSurface = useCallback(async () => {
    if (!projectCwd) return;
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: dashboardQueryKey(projectCwd, "skills"),
      }),
      queryClient.invalidateQueries({
        queryKey: dashboardQueryKey(projectCwd, "skill-resolutions"),
      }),
    ]);
  }, [projectCwd, queryClient]);
  const retrySkillSurface = useCallback(async () => {
    if (!projectCwd) return;
    await Promise.all([refetchSkills(), refetchResolutions()]);
  }, [projectCwd, refetchResolutions, refetchSkills]);
  useSkillSurfaceRefresh(projectCwd, refreshSkillSurface);
  return skillsDashboardState({
    items,
    projectCwd,
    resolutionConflicts,
    resolutionsQuery,
    retrySkillSurface,
    refreshSkillSurface,
    skillsQuery,
  });
}
function skillsDashboardState(options) {
  const {
    items,
    projectCwd,
    resolutionConflicts,
    resolutionsQuery,
    retrySkillSurface,
    refreshSkillSurface,
    skillsQuery,
  } = options;
  const hasSnapshot = Array.isArray(skillsQuery.data);
  const hasResolutionSnapshot = Array.isArray(resolutionsQuery.data);
  const resolutionSyncErrorText = resolutionsQuery.error
    ? "读取技能冲突失败，请重试。"
    : "";
  const syncErrorText = skillsSyncErrorText(skillsQuery, resolutionsQuery);
  return {
    items,
    isInitialLoading:
      Boolean(projectCwd) &&
      skillsQuery.isPending &&
      !hasSnapshot &&
      !syncErrorText,
    isResolutionPending:
      Boolean(projectCwd) &&
      resolutionsQuery.isPending &&
      !hasResolutionSnapshot &&
      !resolutionSyncErrorText,
    refreshSkillSurface,
    resolutionConflicts,
    resolutionSyncErrorText,
    retrySkillSurface,
    showBlockingSyncError: Boolean(syncErrorText && !hasSnapshot),
    showCachedSyncError: Boolean(syncErrorText && hasSnapshot),
    syncErrorText,
  };
}
function skillsSyncErrorText(skillsQuery, resolutionsQuery) {
  if (skillsQuery.error) return "读取技能失败，请重试。";
  if (resolutionsQuery.error) return "读取技能冲突失败，请重试。";
  return "";
}
function useSkillSurfaceRefresh(projectCwd, refreshSkillSurface) {
  useEffect(
    () => skillSurfaceFocusHandler(projectCwd, refreshSkillSurface),
    [projectCwd, refreshSkillSurface],
  );
}
function skillSurfaceFocusHandler(projectCwd, refreshSkillSurface) {
  if (!projectCwd) return undefined;
  const refreshWhenVisible = () => {
    if (
      typeof document !== "undefined" &&
      document.visibilityState === "hidden"
    )
      return;
    void refreshSkillSurface();
  };
  const handleVisibilityChange = () => {
    if (
      typeof document === "undefined" ||
      document.visibilityState === "visible"
    )
      refreshWhenVisible();
  };
  window.addEventListener("focus", refreshWhenVisible);
  document.addEventListener("visibilitychange", handleVisibilityChange);
  return () => {
    window.removeEventListener("focus", refreshWhenVisible);
    document.removeEventListener("visibilitychange", handleVisibilityChange);
  };
}
function useSkillsFilters(items, query, scopeFilter) {
  const counts = useMemo(() => skillCounts(items), [items]);
  const filteredItems = useMemo(
    () => filterSkills(items, query, scopeFilter),
    [items, query, scopeFilter],
  );
  const scopeOptions = useMemo(
    () => [
      ["personal", "私人使用 " + counts.personal],
      ["project", "项目共享 " + counts.project],
      ["all", "全部 " + counts.all],
    ],
    [counts],
  );
  const countText = skillCountText({
    counts,
    filteredCount: filteredItems.length,
    query,
    scopeFilter,
  });
  return { counts, countText, filteredItems, scopeOptions };
}
function skillCountText({ counts, filteredCount, query, scopeFilter }) {
  if (counts.all === 0) return "";
  if (scopeFilter === "all" && !query.trim()) return `共 ${counts.all} 个技能`;
  if (filteredCount === 0) return `当前没有匹配技能，共 ${counts.all} 个`;
  return `显示 ${filteredCount} 个，共 ${counts.all} 个技能`;
}
function skillCounts(items) {
  return items.reduce(
    (acc, item) => {
      acc.all += 1;
      if (item.scope === "personal") acc.personal += 1;
      else acc.project += 1;
      return acc;
    },
    { all: 0, personal: 0, project: 0 },
  );
}
function filterSkills(items, query, scopeFilter) {
  const keyword = query.trim().toLowerCase();
  return items.filter((item) => skillMatchesFilter(item, keyword, scopeFilter));
}
function skillMatchesFilter(item, keyword, scopeFilter) {
  if (scopeFilter !== "all" && item.scope !== scopeFilter) return false;
  if (!keyword) return true;
  return [
    item.name,
    item.title,
    item.description,
    item.summary,
    item.dir,
    ...item.tags,
  ]
    .join(" ")
    .toLowerCase()
    .includes(keyword);
}

export { useSkillsDashboard, useSkillsFilters };
