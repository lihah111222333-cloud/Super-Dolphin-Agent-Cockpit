import { useCallback, useState } from "react";
import { runUIAction } from "../../../../shared/ui/runUIAction.js";
import {
  confirmResolutionName,
  runResolutionPipeline,
} from "./SkillsPageResolutionRequestModel.js";
import { confirmResolutionPreview } from "./SkillsPageResolutionApplyModel.js";

export function useSkillResolution(options) {
  const {
    projectPath,
    refreshSkillSurface,
    resetKey,
    resolutionConflicts,
    setError,
    setNotice,
  } = options;
  const [resolutionState, setResolutionState] = useState({
    resetKey,
    preview: null,
    namePrompt: null,
    nameInput: "",
  });
  const [actioning, setActioning] = useState("");
  const stateShouldReset =
    resolutionState.resetKey !== resetKey ||
    (resolutionConflicts.length === 0 &&
      (resolutionState.preview ||
        resolutionState.namePrompt ||
        resolutionState.nameInput));
  if (stateShouldReset) {
    setResolutionState({
      resetKey,
      preview: null,
      namePrompt: null,
      nameInput: "",
    });
  }
  const preview = stateShouldReset ? null : resolutionState.preview;
  const namePrompt = stateShouldReset ? null : resolutionState.namePrompt;
  const nameInput = stateShouldReset ? "" : resolutionState.nameInput;
  const setPreview = useCallback((value) => {
    setResolutionState((current) => ({
      ...current,
      preview: typeof value === "function" ? value(current.preview) : value,
    }));
  }, []);
  const setNamePrompt = useCallback((value) => {
    setResolutionState((current) => ({
      ...current,
      namePrompt:
        typeof value === "function" ? value(current.namePrompt) : value,
    }));
  }, []);
  const setNameInput = useCallback((value) => {
    setResolutionState((current) => ({
      ...current,
      nameInput: typeof value === "function" ? value(current.nameInput) : value,
    }));
  }, []);
  const reset = useCallback(
    () =>
      setResolutionState((current) => ({
        ...current,
        preview: null,
        namePrompt: null,
        nameInput: "",
      })),
    [],
  );
  const runAction = useCallback(
    (conflict, actionOrEntry, entry = null, newName = "") =>
      runUIAction(
        "skill.resolution.preview",
        () =>
          runResolutionPipeline({
            actionOrEntry,
            actioning,
            conflict,
            entry,
            newName,
            projectPath,
            refreshSkillSurface,
            setActioning,
            setError,
            setNameInput,
            setNamePrompt,
            setNotice,
            setPreview,
          }),
        { retryable: true },
      ),
    [
      actioning,
      projectPath,
      refreshSkillSurface,
      setError,
      setNameInput,
      setNamePrompt,
      setNotice,
      setPreview,
    ],
  );
  const confirmName = useCallback(
    () =>
      confirmResolutionName({
        nameInput,
        namePrompt,
        runAction,
        setError,
        setNameInput,
        setNamePrompt,
      }),
    [nameInput, namePrompt, runAction, setError, setNameInput, setNamePrompt],
  );
  const confirmPreview = useCallback(
    () =>
      runUIAction("skill.resolution.apply", () =>
        confirmResolutionPreview({
          preview,
          refreshSkillSurface,
          setActioning,
          setError,
          setNameInput,
          setNamePrompt,
          setNotice,
          setPreview,
        }),
      ),
    [
      preview,
      refreshSkillSurface,
      setError,
      setNameInput,
      setNamePrompt,
      setNotice,
      setPreview,
    ],
  );
  return {
    actioning,
    confirmName,
    confirmPreview,
    nameInput,
    namePrompt,
    preview,
    reset,
    runAction,
    setNameInput,
    setNamePrompt,
    setPreview,
  };
}
