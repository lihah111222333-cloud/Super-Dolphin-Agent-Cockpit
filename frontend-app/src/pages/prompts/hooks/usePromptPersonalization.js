import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  getPersonalizationProfile,
  savePersonalizationProfile,
} from "../services/promptPageService.js";
import {
  runBackgroundAction,
  runUIAction,
} from "../../../shared/ui/runUIAction.js";
import { noticeText } from "../model/promptPageErrorUtils.js";
import {
  emptyPersonalizationProfile,
  validatePersonalizationProfile,
} from "../model/promptPageProfileUtils.js";
import { withTimeout } from "../model/promptPageTextUtils.js";
import { PROMPTS_REQUEST_TIMEOUT_MS } from "../model/promptPageViewSchemas.js";

function savedPersonalizationProfile(result) {
  const profile =
    result.profile && typeof result.profile === "object" ? result.profile : {};
  return { ...emptyPersonalizationProfile, ...profile };
}

export function usePromptPersonalization({
  cwd,
  queryClient,
  saving,
  setters,
}) {
  const profileCwdRef = useRef(cwd);
  const profileSaveGenerationRef = useRef(0);
  const profileDraftRevisionRef = useRef(0);
  useEffect(() => {
    profileCwdRef.current = cwd;
    profileSaveGenerationRef.current += 1;
    profileDraftRevisionRef.current += 1;
  }, [cwd]);
  const {
    data: profileData,
    error: profileError,
    isLoading: profileLoading,
  } = useQuery({
    queryKey: ["personalizationProfile", cwd],
    queryFn: () =>
      runBackgroundAction("prompt.profile.load", () =>
        withTimeout(
          getPersonalizationProfile({ cwd }),
          PROMPTS_REQUEST_TIMEOUT_MS,
          "个人资料加载超时，请检查后端状态。",
        ),
      ),
    enabled: Boolean(cwd),
  });
  const loadedProfile = profileData?.profile
    ? { ...emptyPersonalizationProfile, ...profileData.profile }
    : emptyPersonalizationProfile;
  const [profileDraft, setProfileDraft] = useState({ cwd: "", profile: null });
  const profileForm =
    profileDraft.cwd === cwd && profileDraft.profile
      ? profileDraft.profile
      : loadedProfile;
  const setProfileForm = useCallback(
    (nextProfile) => {
      profileDraftRevisionRef.current += 1;
      setProfileDraft({ cwd, profile: nextProfile });
    },
    [cwd],
  );
  const saveProfile = async () => {
    if (!cwd || saving) return;
    const generation = profileSaveGenerationRef.current + 1;
    const draftRevision = profileDraftRevisionRef.current;
    profileSaveGenerationRef.current = generation;
    const isCurrentRequest = () =>
      profileCwdRef.current === cwd &&
      profileSaveGenerationRef.current === generation;
    const isCurrentDraft = () =>
      isCurrentRequest() && profileDraftRevisionRef.current === draftRevision;
    setters.setSaving(true);
    setters.setNotice("");
    try {
      const result = await savePersonalizationProfile({
        cwd,
        profile: profileForm,
      });
      const savedProfile = savedPersonalizationProfile(result);
      if (!isCurrentRequest()) return;
      queryClient.setQueryData(["personalizationProfile", cwd], {
        profile: savedProfile,
      });
      if (!isCurrentDraft()) return;
      setProfileDraft({ cwd, profile: null });
      setters.setNotice("个人资料已保存");
    } catch (err) {
      if (!isCurrentDraft()) return;
      setters.setNotice(noticeText(err, "个人资料保存失败"));
      throw err;
    } finally {
      if (isCurrentRequest()) setters.setSaving(false);
    }
  };
  const handleImportMemory = () => {
    setters.setWizardDraft({ kind: "recall", rawInput: "", scope: "project" });
    setters.setWizardOpen(true);
  };
  const handleProjectRequired = useCallback(() => {
    setters.setNotice("请先在聊天页选择项目，再使用个性化设置。");
  }, [setters]);
  return {
    profileError,
    profileForm,
    profileLoading,
    profileSaving: saving,
    profileValidationErrors: useMemo(
      () => validatePersonalizationProfile(profileForm),
      [profileForm],
    ),
    handleImportMemory,
    handleProjectRequired,
    handleSaveProfile: () => runUIAction("prompt.profile.save", saveProfile),
    setProfileForm,
  };
}
