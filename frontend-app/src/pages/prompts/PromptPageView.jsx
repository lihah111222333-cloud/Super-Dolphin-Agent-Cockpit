import { useQueryClient } from "@tanstack/react-query";
import React, { useCallback, useMemo, useState } from "react";
import { APP_COPY } from "../../shared/i18n/appI18n.js";
import "./PromptPageView.css";
import { emptyPromptForm } from "./model/promptPageAssetFormUtils.js";
import { promptCounts } from "./model/promptPageAssetListUtils.js";
import { optionalPromptCwd } from "./model/promptPageTextUtils.js";
import {
  usePromptQueries,
  usePromptRefreshEffects,
  usePromptRefreshSurface,
} from "./hooks/usePromptPageQueries.js";
import {
  usePromptDraftActions,
  usePromptEditorActions,
} from "./hooks/usePromptPageActions.js";
import { usePromptPersonalization } from "./hooks/usePromptPersonalization.js";
import { PromptPageLayout } from "./PromptPageLayout.jsx";
function usePromptPageState(cwd) {
  const [noticeState, setNoticeState] = useState({ cwd, notice: "" });
  if (noticeState.cwd !== cwd) {
    setNoticeState({ cwd, notice: "" });
  }
  const setNotice = useCallback((value) => {
    setNoticeState((current) => ({
      ...current,
      notice: typeof value === "function" ? value(current.notice) : value,
    }));
  }, []);
  const [actioning, setActioning] = useState("");
  const [editorOpen, setEditorOpen] = useState(false);
  const [savingState, setSavingState] = useState({ cwd, value: false });
  if (savingState.cwd !== cwd) setSavingState({ cwd, value: false });
  const saving = savingState.cwd === cwd ? savingState.value : false;
  const setSaving = useCallback(
    (value) => {
      setSavingState((current) =>
        current.cwd === cwd ? { cwd, value } : current,
      );
    },
    [cwd],
  );
  const [form, setForm] = useState(emptyPromptForm);
  const [wizardOpen, setWizardOpen] = useState(false);
  const [wizardDraft, setWizardDraft] = useState(null);
  return {
    actioning,
    form,
    modals: { editorOpen, form, wizardDraft, wizardOpen },
    notice: noticeState.cwd === cwd ? noticeState.notice : "",
    saving,
    setters: {
      setActioning,
      setEditorOpen,
      setForm,
      setNotice,
      setSaving,
      setWizardDraft,
      setWizardOpen,
    },
  };
}
export function PromptPageView(props) {
  return <PromptPageViewContent {...props} />;
}

function PromptPageViewContent({
  copy = APP_COPY.zh.prompts,
  projectPath,
  refreshKey = 0,
  resolveLaunchPreferences,
}) {
  const cwd = optionalPromptCwd(projectPath);
  const isProjectPending = !cwd;
  const queryClient = useQueryClient();
  const pageState = usePromptPageState(cwd);
  const { actioning, form, modals, notice, saving, setters } = pageState;
  const queryState = usePromptQueries(cwd);
  const { items, fallbackMode, activePromptId, loading, syncError, error } =
    queryState;
  const refreshPromptSurface = usePromptRefreshSurface(
    cwd,
    queryClient,
    queryState.refetchPromptAssets,
    queryState.refetchActivePrompt,
  );
  usePromptRefreshEffects(Number(refreshKey || 0), refreshPromptSurface);
  const counts = useMemo(() => promptCounts(items), [items]);
  const visibleItems = items;
  const editorActions = usePromptEditorActions({
    cwd,
    fallbackMode,
    actioning,
    form,
    queryClient,
    refreshPromptSurface,
    setters,
  });
  const draftActions = usePromptDraftActions({
    cwd,
    actioning,
    refreshPromptSurface,
    setters,
  });
  const personalization = usePromptPersonalization({
    cwd,
    queryClient,
    saving,
    setters,
  });
  return (
    <PromptPageLayout
      {...promptPageLayoutProps({
        activePromptId,
        actioning,
        copy,
        counts,
        cwd,
        draftActions,
        editorActions,
        error,
        fallbackMode,
        isProjectPending,
        loading,
        modals,
        notice,
        ...personalization,
        projectPath,
        resolveLaunchPreferences,
        saving,
        setters,
        syncError,
        visibleItems,
      })}
    />
  );
}

function promptPageLayoutProps(input) {
  const {
    activePromptId,
    actioning,
    copy,
    counts,
    cwd,
    draftActions,
    editorActions,
    error,
    fallbackMode,
    isProjectPending,
    loading,
    modals,
    notice,
    profileError,
    profileForm,
    profileLoading,
    profileSaving,
    profileValidationErrors,
    handleImportMemory,
    handleProjectRequired,
    handleSaveProfile,
    setProfileForm,
    projectPath,
    resolveLaunchPreferences,
    saving,
    setters,
    syncError,
    visibleItems,
  } = input;
  return {
    activePromptId,
    actioning,
    counts,
    copy,
    cwd,
    draftActions,
    editorActions,
    error,
    fallbackMode,
    isProjectPending,
    loading,
    modals,
    notice,
    personalization: {
      error: profileError,
      loading: profileLoading,
      onImportMemory: handleImportMemory,
      onProfileChange: setProfileForm,
      onProjectRequired: handleProjectRequired,
      onSaveProfile: handleSaveProfile,
      profile: profileForm,
      saving: profileSaving,
      validationErrors: profileValidationErrors,
    },
    projectPath,
    resolveLaunchPreferences,
    saving,
    setters,
    showBlockingError: Boolean(error),
    syncError,
    visibleItems,
  };
}
