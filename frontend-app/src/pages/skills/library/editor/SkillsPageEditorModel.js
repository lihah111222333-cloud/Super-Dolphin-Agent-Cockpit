import { useCallback, useMemo, useState } from "react";
import { skillsPageService } from "../../services/skillsPageService.js";
import { emptySkillForm } from "./SkillsPageCitationModel.js";
import { runUIAction } from "../../../../shared/ui/runUIAction.js";
import {
  confirmDeleteSkill,
  openCreateSkillEditor,
  openEditSkill,
  openSkillCitation,
  openSkillFile,
  saveSkillEditor,
  suggestSkillSummaryForEditor,
} from "./SkillsPageEditorActions.js";
import {
  applyImportSummaryDraft,
  confirmImportScope,
  dismissImportSummaryDraft,
  openImportSummaryDraft,
} from "./SkillsPageImportActions.js";

export function useSkillEditor(options) {
  const {
    projectPath,
    refreshSkillSurface,
    resolveLaunchPreferences,
    setError,
    setNotice,
    skills,
  } = options;
  const [state, setState] = useState(defaultSkillEditorState);
  const setPatch = useCallback(
    (patch) =>
      setState((current) => ({
        ...current,
        ...patch,
      })),
    [],
  );
  const setForm = useCallback(
    (updater) =>
      setState((current) => ({
        ...current,
        editorForm: updater(current.editorForm),
      })),
    [],
  );
  const actions = useMemo(
    () =>
      skillEditorActions({
        facade: skillsPageService,
        projectPath,
        refreshSkillSurface,
        resolveLaunchPreferences,
        setError,
        setForm,
        setNotice,
        setPatch,
        skills,
        state,
      }),
    [
      projectPath,
      refreshSkillSurface,
      resolveLaunchPreferences,
      setError,
      setForm,
      setNotice,
      setPatch,
      skills,
      state,
    ],
  );
  return { ...state, ...actions, setForm };
}

function defaultSkillEditorState() {
  return {
    activeSkillPath: "",
    deleteTarget: null,
    deleting: false,
    editorForm: emptySkillForm(),
    editorOpen: false,
    importScopeOpen: false,
    importSummaryDrafts: [],
    importing: false,
    saving: false,
    skillFiles: [],
    summarySuggestion: "",
    summarySuggesting: false,
  };
}

function skillEditorActions(ctx) {
  return {
    applySummary: () => {
      ctx.setForm((form) => ({
        ...form,
        description: ctx.state.summarySuggestion,
      }));
      ctx.setPatch({ summarySuggestion: "" });
    },
    closeDelete: () => ctx.setPatch({ deleteTarget: null }),
    closeEditor: () => ctx.setPatch({ editorOpen: false }),
    closeImportScope: () => ctx.setPatch({ importScopeOpen: false }),
    clearImportSummaryDrafts: () => ctx.setPatch({ importSummaryDrafts: [] }),
    confirmDeleteSkill: () =>
      runUIAction("skill.delete", () => confirmDeleteSkill(ctx)),
    confirmImportScope: (scope) =>
      runUIAction("skill.import", () => confirmImportScope(ctx, scope)),
    applyImportSummaryDraft: (draft) => applyImportSummaryDraft(ctx, draft),
    dismissImportSummaryDraft: (draft) => dismissImportSummaryDraft(ctx, draft),
    openImportSummaryDraft: (draft) =>
      runUIAction(
        "skill.import-summary.open",
        () => openImportSummaryDraft(ctx, draft),
        { retryable: true },
      ),
    onDeleteSkill: (skill) => ctx.setPatch({ deleteTarget: skill }),
    openCreateEditor: () => openCreateSkillEditor(ctx),
    openEditSkill: (skill) =>
      runUIAction("skill.open", () => openEditSkill(ctx, skill), {
        retryable: true,
      }),
    openImportScope: () => ctx.setPatch({ importScopeOpen: true }),
    openSkillFile: (file) =>
      runUIAction("skill.file.open", () => openSkillFile(ctx, file), {
        retryable: true,
      }),
    openSkillCitation: (target, label) => openSkillCitation(ctx, target, label),
    saveEditor: () => runUIAction("skill.save", () => saveSkillEditor(ctx)),
    suggestSummary: () =>
      runUIAction(
        "skill.summary.suggest",
        () => suggestSkillSummaryForEditor(ctx),
        { retryable: true },
      ),
  };
}
