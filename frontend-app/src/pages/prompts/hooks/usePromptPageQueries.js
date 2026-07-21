import { useCallback, useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  getDashboardPrompts,
  getPreference,
  listPromptAssets,
} from "../services/promptPageService.js";
import { runBackgroundAction } from "../../../shared/ui/runUIAction.js";
import {
  ACTIVE_PROMPT_PREF_KEY,
  PROMPTS_REQUEST_TIMEOUT_MS,
} from "../model/promptPageViewSchemas.js";
import {
  canForceLaunchPrompt,
  isReadonlyFallbackListError,
  normalizeDashboardPromptList,
  normalizePromptList,
} from "../model/promptPageAssetListUtils.js";
import { textValue, withTimeout } from "../model/promptPageTextUtils.js";

export function promptAssetsQueryKey(cwd) {
  return ["dashboard", "project", cwd, "prompts"];
}
export function activePromptQueryKey(cwd) {
  return ["dashboard", "project", cwd, "active-prompt"];
}
export async function fetchPromptAssetsSurface(cwd) {
  try {
    const response = await withTimeout(
      listPromptAssets({ cwd }),
      PROMPTS_REQUEST_TIMEOUT_MS,
      "提示词列表加载超时，请检查提示词目录或后端状态。",
    );
    return { items: normalizePromptList(response), fallbackMode: false };
  } catch (err) {
    if (!isReadonlyFallbackListError(err)) throw err;
    const response = await withTimeout(
      getDashboardPrompts({ cwd }),
      PROMPTS_REQUEST_TIMEOUT_MS,
      "只读提示词列表加载超时，请检查 dashboard/prompts 后端状态。",
    );
    return {
      items: normalizeDashboardPromptList(response),
      fallbackMode: true,
    };
  }
}
export async function fetchActivePromptId(cwd) {
  const value = await withTimeout(
    getPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY }),
    PROMPTS_REQUEST_TIMEOUT_MS,
    "强制提示词状态加载超时，请检查后端状态。",
  );
  return typeof value === "string" ? value.trim() : "";
}
export function promptQueryState(cwd, promptAssetsQuery, activePromptQuery) {
  const items = Array.isArray(promptAssetsQuery.data?.items)
    ? promptAssetsQuery.data.items
    : [];
  const hasPromptSnapshot = Array.isArray(promptAssetsQuery.data?.items);
  const promptSyncError = Boolean(promptAssetsQuery.error);
  const activePromptSyncError = Boolean(activePromptQuery.error);
  const syncErrorMessage = promptSyncError || activePromptSyncError;
  const activePromptId = promptActiveIdForItems(
    textValue(activePromptQuery.data),
    items,
    hasPromptSnapshot,
  );
  return {
    items,
    fallbackMode: Boolean(promptAssetsQuery.data?.fallbackMode),
    activePromptId,
    loading: Boolean(cwd) && promptAssetsQuery.isPending && !hasPromptSnapshot,
    syncError:
      syncErrorMessage && hasPromptSnapshot
        ? "同步失败，显示的是上次成功的数据。"
        : "",
    error:
      promptSyncError && !hasPromptSnapshot ? "加载提示词失败，请重试。" : "",
  };
}
export function promptActiveIdForItems(
  activePromptId,
  items,
  hasPromptSnapshot,
) {
  if (!activePromptId) return "";
  if (!hasPromptSnapshot) return activePromptId;
  return items.some(
    (item) => item.id === activePromptId && canForceLaunchPrompt(item),
  )
    ? activePromptId
    : "";
}
export function usePromptQueries(cwd) {
  const {
    data: promptAssetsData,
    error: promptAssetsError,
    isPending: promptAssetsPending,
    refetch: refetchPromptAssets,
  } = useQuery({
    queryKey: promptAssetsQueryKey(cwd),
    queryFn: () =>
      runBackgroundAction("prompt.assets.load", () =>
        fetchPromptAssetsSurface(cwd),
      ),
    enabled: Boolean(cwd),
  });
  const {
    data: activePromptData,
    error: activePromptError,
    refetch: refetchActivePrompt,
  } = useQuery({
    queryKey: activePromptQueryKey(cwd),
    queryFn: () =>
      runBackgroundAction("prompt.active.load", () => fetchActivePromptId(cwd)),
    enabled: Boolean(cwd),
  });
  const promptAssetsQuery = {
    data: promptAssetsData,
    error: promptAssetsError,
    isPending: promptAssetsPending,
  };
  const activePromptQuery = {
    data: activePromptData,
    error: activePromptError,
  };
  const state = promptQueryState(cwd, promptAssetsQuery, activePromptQuery);
  return { ...state, refetchPromptAssets, refetchActivePrompt };
}
export function usePromptRefreshSurface(
  cwd,
  queryClient,
  refetchPromptAssets,
  refetchActivePrompt,
) {
  return useCallback(
    async (options = {}) => {
      const isCancelled =
        typeof options.isCancelled === "function"
          ? options.isCancelled
          : () => false;
      if (!cwd) return [];
      const [assetResult, activeResult] = await Promise.all([
        refetchPromptAssets(),
        refetchActivePrompt(),
      ]);
      const nextItems = Array.isArray(assetResult.data?.items)
        ? assetResult.data.items
        : [];
      if (isCancelled()) return nextItems;
      const nextActiveId = textValue(activeResult.data);
      if (
        nextActiveId &&
        !nextItems.some(
          (item) => item.id === nextActiveId && canForceLaunchPrompt(item),
        )
      ) {
        queryClient.setQueryData(activePromptQueryKey(cwd), "");
      }
      return nextItems;
    },
    [cwd, queryClient, refetchActivePrompt, refetchPromptAssets],
  );
}
export function usePromptRefreshEffects(
  promptRefreshKey,
  refreshPromptSurface,
) {
  useEffect(() => {
    if (promptRefreshKey <= 0) return undefined;
    let cancelled = false;
    void refreshPromptSurface({ isCancelled: () => cancelled });
    return () => {
      cancelled = true;
    };
  }, [promptRefreshKey, refreshPromptSurface]);
  useEffect(() => {
    const runAutoRefresh = () => {
      if (
        typeof document !== "undefined" &&
        document.visibilityState === "hidden"
      )
        return;
      void refreshPromptSurface();
    };
    const handleVisibilityChange = () => {
      if (
        typeof document === "undefined" ||
        document.visibilityState === "visible"
      )
        runAutoRefresh();
    };
    window.addEventListener("focus", runAutoRefresh);
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      window.removeEventListener("focus", runAutoRefresh);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [refreshPromptSurface]);
}
