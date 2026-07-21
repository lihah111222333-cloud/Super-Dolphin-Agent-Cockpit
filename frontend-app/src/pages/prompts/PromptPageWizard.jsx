import React, { useCallback, useReducer } from "react";
import {
  commitPromptIntent,
  draftPromptIntent,
  dryRunPromptIntent,
} from "./services/promptPageService.js";
import { runUIAction } from "../../shared/ui/runUIAction.js";
import {
  PROMPT_DRAFT_NOT_READY_MESSAGE,
  PROMPT_DRAFT_REVIEW_MESSAGE,
} from "./model/promptPageViewSchemas.js";
import {
  normalizeDraft,
  promptDraftNeedsRevision,
} from "./model/promptPageDraftUtils.js";
import { noticeText } from "./model/promptPageErrorUtils.js";
import { firstPresentText, textValue } from "./model/promptPageTextUtils.js";
import { PromptAriaModal } from "./PromptPageViewPanels.jsx";
import { PromptDraftRiskConfirmation } from "./PromptPageWizardUi.jsx";
import {
  promptDraftHasReviewIssues,
  promptDryRunSummary,
} from "./model/promptPageWizardState.js";

async function buildPromptDraft({
  cwd,
  kind,
  rawInput,
  scope,
  resolveLaunchPreferences,
}) {
  const launchPreferences =
    typeof resolveLaunchPreferences === "function"
      ? await resolveLaunchPreferences(cwd)
      : null;
  return draftPromptIntent({
    cwd,
    kind,
    rawInput,
    sourceType: "user_input",
    scope,
    provider: firstPresentText(
      launchPreferences?.modelProvider,
      launchPreferences?.provider,
    ),
    model: textValue(launchPreferences?.model),
    codexModelProvider: firstPresentText(
      launchPreferences?.codexModelProvider,
      launchPreferences?.config?.codexModelProvider,
    ),
  });
}
import {
  PromptDraftReview,
  PromptKindTabs,
  PromptScopeChoice,
  PromptWizardNotice,
} from "./PromptPageWizardUi.jsx";

async function runPromptDraftDryRun(options) {
  const { cwd, draft, question, setDryRunResult, setNotice, setWorking } =
    options;
  const cleanQuestion = textValue(question);
  if (!cleanQuestion) {
    setNotice("请先填写试问问题");
    return;
  }
  if (!draft?.draftKey) {
    setNotice("请先生成草稿后再验证");
    return;
  }
  setWorking("dry-run");
  setNotice("");
  try {
    const result = await dryRunPromptIntent({
      cwd,
      draftKey: draft.draftKey,
      kind: draft.kind,
      card: draft.card,
      question: cleanQuestion,
    });
    setDryRunResult(result);
  } catch (err) {
    setNotice(noticeText(err, "验证失败"));
    throw err;
  } finally {
    setWorking("");
  }
}
async function runPromptDraftGeneration(params) {
  const {
    cwd,
    kind,
    rawInput,
    scope,
    resolveLaunchPreferences,
    setDraft,
    setNotice,
    setWorking,
  } = params;
  setWorking("draft");
  setNotice("");
  try {
    const response = await buildPromptDraft({
      cwd,
      kind,
      rawInput,
      scope,
      resolveLaunchPreferences,
    });
    setDraft(normalizeDraft(response, kind));
  } catch (err) {
    setNotice(noticeText(err, "生成失败"));
    throw err;
  } finally {
    setWorking("");
  }
}
async function runPromptDraftCommit(options) {
  const {
    confirmGlobal,
    confirmRisk,
    cwd,
    draft,
    onSaved,
    setNotice,
    setWorking,
  } = options;
  setWorking("commit");
  setNotice("");
  try {
    await commitPromptIntent({
      cwd,
      draftKey: draft.draftKey,
      scope: draft.scope,
      confirmGlobal,
      confirmRisk,
    });
    await onSaved();
  } catch (err) {
    setNotice(noticeText(err, "保存失败"));
    throw err;
  } finally {
    setWorking("");
  }
}
function promptWizardInitialState(initialDraft) {
  const hasDraft =
    Boolean(initialDraft?.draftKey) ||
    Boolean(initialDraft?.id) ||
    Boolean(initialDraft?.card);
  return {
    draft: hasDraft ? initialDraft : null,
    dryRunQuestion: "",
    dryRunResult: null,
    kind: initialDraft?.kind || "expert",
    notice: "",
    rawInput: textValue(initialDraft?.rawInput),
    reviewConfirmed: false,
    scope: initialDraft?.scope || "project",
    working: "",
  };
}
function promptWizardReducer(state, action) {
  switch (action.type) {
    case "draft/generated":
      return {
        ...state,
        draft: action.draft,
        dryRunQuestion: "",
        dryRunResult: null,
        reviewConfirmed: false,
      };
    case "dry-run/result":
      return { ...state, dryRunResult: action.result };
    case "field/set":
      return { ...state, [action.key]: action.value };
    case "notice/set":
      return { ...state, notice: action.notice };
    case "review/confirmed":
      return { ...state, reviewConfirmed: action.confirmed };
    case "working/set":
      return { ...state, working: action.working };
    default:
      return state;
  }
}
export function PromptIntentWizardModal(props) {
  return <PromptIntentWizardModalContent {...props} />;
}

function PromptIntentWizardModalContent({
  cwd,
  initialDraft,
  resolveLaunchPreferences,
  onClose,
  onSaved,
}) {
  const [state, dispatch] = useReducer(
    promptWizardReducer,
    initialDraft,
    promptWizardInitialState,
  );
  const {
    draft,
    dryRunQuestion,
    dryRunResult,
    kind,
    notice,
    rawInput,
    reviewConfirmed,
    scope,
    working,
  } = state;
  const setDraft = useCallback((nextDraft) => {
    dispatch({ type: "draft/generated", draft: nextDraft });
  }, []);
  const setDryRunResult = useCallback((result) => {
    dispatch({ type: "dry-run/result", result });
  }, []);
  const setNotice = useCallback((nextNotice) => {
    dispatch({ type: "notice/set", notice: nextNotice });
  }, []);
  const setWorking = useCallback((nextWorking) => {
    dispatch({ type: "working/set", working: nextWorking });
  }, []);
  const setReviewConfirmed = useCallback((confirmed) => {
    dispatch({ type: "review/confirmed", confirmed });
  }, []);
  const setWizardField = useCallback((key, value) => {
    dispatch({ type: "field/set", key, value });
  }, []);
  const runDraft = () => {
    const text = textValue(rawInput);
    if (!text) {
      setNotice("请先写下希望 AI 记住或使用的内容");
      return undefined;
    }
    return runUIAction("prompt.draft.generate", () =>
      runPromptDraftGeneration({
        cwd,
        kind,
        rawInput: text,
        scope,
        resolveLaunchPreferences,
        setDraft,
        setNotice,
        setWorking,
      }),
    );
  };
  const commitDraft = () => {
    if (!draft?.draftKey) return undefined;
    if (promptDraftNeedsRevision(draft)) {
      setNotice(PROMPT_DRAFT_NOT_READY_MESSAGE);
      return undefined;
    }
    if (draftHasReviewIssues && !reviewConfirmed) {
      setNotice(PROMPT_DRAFT_REVIEW_MESSAGE);
      return undefined;
    }
    return runUIAction("prompt.draft.commit", () =>
      runPromptDraftCommit({
        confirmGlobal: draft.scope === "global" ? true : undefined,
        confirmRisk: draftHasReviewIssues && reviewConfirmed ? true : undefined,
        cwd,
        draft,
        onSaved,
        setNotice,
        setWorking,
      }),
    );
  };
  const draftNeedsRevision = promptDraftNeedsRevision(draft);
  const draftHasReviewIssues = promptDraftHasReviewIssues(draft);
  const canCommitDraft =
    Boolean(draft?.draftKey) &&
    !draftNeedsRevision &&
    (!draftHasReviewIssues || reviewConfirmed);
  const runDryRun = () =>
    runUIAction("prompt.draft.dry-run", () =>
      runPromptDraftDryRun({
        cwd,
        draft,
        question: dryRunQuestion,
        setDryRunResult,
        setNotice,
        setWorking,
      }),
    );
  return (
    <PromptIntentWizardBody
      canCommitDraft={canCommitDraft}
      commitDraft={commitDraft}
      cwd={cwd}
      draft={draft}
      draftHasReviewIssues={draftHasReviewIssues}
      draftNeedsRevision={draftNeedsRevision}
      dryRunQuestion={dryRunQuestion}
      dryRunResult={dryRunResult}
      kind={kind}
      notice={notice}
      onClose={onClose}
      rawInput={rawInput}
      reviewConfirmed={reviewConfirmed}
      scope={scope}
      runDraft={runDraft}
      runDryRun={runDryRun}
      setReviewConfirmed={setReviewConfirmed}
      setWizardField={setWizardField}
      working={working}
    />
  );
}

function PromptIntentWizardBody(props) {
  const {
    canCommitDraft,
    commitDraft,
    cwd,
    draft,
    draftHasReviewIssues,
    draftNeedsRevision,
    dryRunQuestion,
    dryRunResult,
    kind,
    notice,
    onClose,
    rawInput,
    reviewConfirmed,
    scope,
    runDraft,
    runDryRun,
    setReviewConfirmed,
    setWizardField,
    working,
  } = props;
  return (
    <PromptAriaModal
      ariaLabel="添加给 AI 的内容"
      className="modal-box prompt-wizard-modal"
      overlayClassName="modal-overlay prompt-modal-overlay"
      closeDisabled={working === "commit"}
      closeOnOverlayClick
      onClose={onClose}
    >
      {" "}
      <header>
        {" "}
        <div>
          {" "}
          <h2>添加给 AI 的内容</h2> <p>{cwd || "未知"}</p>{" "}
        </div>
      </header>{" "}
      <PromptKindTabs
        autoFocus
        kind={kind}
        onChange={(value) => setWizardField("kind", value)}
      />{" "}
      <PromptScopeChoice
        scope={scope}
        onChange={(value) => setWizardField("scope", value)}
      />{" "}
      <label className="prompt-wizard-input">
        {" "}
        写下希望 AI 记住或使用的内容
        <textarea
          value={rawInput}
          onChange={(event) => setWizardField("rawInput", event.target.value)}
          aria-label="写下希望 AI 记住或使用的内容"
        />{" "}
      </label>{" "}
      <button type="button" onClick={runDraft} disabled={working === "draft"}>
        {working === "draft" ? "生成中..." : "帮我生成"}
      </button>
      {working === "draft" ? (
        <output className="prompt-notice" aria-live="polite">
          正在整理内容，可能需要一点时间。
        </output>
      ) : null}{" "}
      <PromptDraftReview draft={draft} />{" "}
      {draft ? (
        <details className="prompt-dry-run-panel">
          {" "}
          <summary>试问验证</summary>{" "}
          <div className="prompt-dry-run-body">
            {" "}
            <label>
              试问问题{" "}
              <textarea
                value={dryRunQuestion}
                onChange={(event) =>
                  setWizardField("dryRunQuestion", event.target.value)
                }
                aria-label="试问问题"
              />{" "}
            </label>
            <button
              type="button"
              disabled={working === "dry-run"}
              onClick={runDryRun}
            >
              {working === "dry-run" ? "验证中..." : "验证"}
            </button>{" "}
            {dryRunResult ? (
              <div className="prompt-notice">
                {promptDryRunSummary(dryRunResult, draft)}
              </div>
            ) : null}{" "}
          </div>{" "}
        </details>
      ) : null}{" "}
      <PromptWizardNotice
        draftNeedsRevision={draftNeedsRevision}
        notice={notice}
      />{" "}
      <PromptDraftRiskConfirmation
        checked={reviewConfirmed}
        disabled={Boolean(working)}
        show={draftHasReviewIssues && !draftNeedsRevision}
        onChange={setReviewConfirmed}
      />{" "}
      <footer>
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={working === "commit"}
        >
          关闭
        </button>{" "}
        <button
          type="button"
          onClick={commitDraft}
          disabled={!canCommitDraft || working === "commit"}
        >
          {" "}
          {working === "commit" ? "保存中..." : "确认保存"}{" "}
        </button>{" "}
      </footer>{" "}
    </PromptAriaModal>
  );
}
