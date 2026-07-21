import React from "react";
import { Radio, RadioGroup } from "react-aria-components";
import { CheckCircle2 } from "lucide-react";
import {
  PROMPT_DRAFT_NOT_READY_MESSAGE,
  PROMPT_DRAFT_REVIEW_MESSAGE,
  PROMPT_KIND_OPTIONS,
} from "./model/promptPageViewSchemas.js";

export function PromptKindTabs({ kind, onChange, autoFocus = false }) {
  return (
    <div className="prompt-kind-tabs" role="tablist" aria-label="内容类型">
      {" "}
      {PROMPT_KIND_OPTIONS.map((option) => (
        <button
          key={option.key}
          type="button"
          role="tab"
          aria-selected={kind === option.key}
          autoFocus={autoFocus && kind === option.key}
          className={kind === option.key ? "active" : ""}
          onClick={() => onChange(option.key)}
        >
          {" "}
          {option.label}{" "}
        </button>
      ))}{" "}
    </div>
  );
}
export function PromptScopeChoice({
  scope,
  onChange,
  ariaLabel = "草稿范围",
  autoFocusProject = false,
}) {
  return (
    <RadioGroup
      aria-label={ariaLabel}
      className="prompt-scope-choice"
      onChange={onChange}
      value={scope === "global" ? "global" : "project"}
    >
      {" "}
      <Radio
        autoFocus={autoFocusProject && scope !== "global"}
        className={({ isSelected }) => (isSelected ? "active" : "")}
        value="project"
      >
        这个项目
      </Radio>
      <Radio
        className={({ isSelected }) => (isSelected ? "active" : "")}
        value="global"
      >
        全局可用
      </Radio>{" "}
    </RadioGroup>
  );
}
function PromptDraftExamples({ draft }) {
  if (!draft.hitExamples.length && !draft.missExamples.length) return null;
  return (
    <div className="prompt-draft-examples">
      {" "}
      {draft.hitExamples.length ? (
        <div>
          {" "}
          <strong>适合的问题</strong>{" "}
          <ul>
            {draft.hitExamples.map((example) => (
              <li key={example}>{example}</li>
            ))}
          </ul>{" "}
        </div>
      ) : null}{" "}
      {draft.missExamples.length ? (
        <div>
          {" "}
          <strong>不适合的问题</strong>{" "}
          <ul>
            {draft.missExamples.map((example) => (
              <li key={example}>{example}</li>
            ))}
          </ul>{" "}
        </div>
      ) : null}{" "}
    </div>
  );
}
export function PromptDraftReview({ draft }) {
  if (!draft) return null;
  return (
    <article className="prompt-draft-review">
      {" "}
      <div className="prompt-draft-title">
        <CheckCircle2 size={16} /> {draft.title}
      </div>{" "}
      {draft.summary ? <p>{draft.summary}</p> : null}{" "}
      {draft.whenToUse ? <p>{draft.whenToUse}</p> : null}
      {draft.whenNotToUse ? <p>{draft.whenNotToUse}</p> : null}{" "}
      {draft.workflow?.length ? (
        <ol>
          {draft.workflow.map((step) => (
            <li key={step}>{step}</li>
          ))}
        </ol>
      ) : null}{" "}
      {draft.saveBoundary ? (
        <p>
          <span>保存边界：</span>
          <span>{draft.saveBoundary}</span>
        </p>
      ) : null}
      {draft.output ? <pre>{draft.output}</pre> : null}{" "}
      <PromptDraftExamples draft={draft} />{" "}
      {draft.issues.length ? (
        <div className="prompt-draft-issues">
          {" "}
          {draft.issues.map((issue) => (
            <div key={`${issue.code}:${issue.message}`}>{issue.message}</div>
          ))}{" "}
        </div>
      ) : null}{" "}
    </article>
  );
}
export function PromptWizardNotice({ draftNeedsRevision, notice }) {
  const noticeIsGuidance =
    notice === PROMPT_DRAFT_NOT_READY_MESSAGE ||
    notice === PROMPT_DRAFT_REVIEW_MESSAGE;
  return (
    <>
      {" "}
      {draftNeedsRevision ? (
        <div className="prompt-notice">{PROMPT_DRAFT_NOT_READY_MESSAGE}</div>
      ) : null}{" "}
      {notice ? (
        <div className={`prompt-notice${noticeIsGuidance ? "" : " error"}`}>
          {notice}
        </div>
      ) : null}{" "}
    </>
  );
}
export function PromptDraftRiskConfirmation({
  checked,
  disabled,
  show,
  onChange,
}) {
  if (!show) return null;
  return (
    <label className="prompt-check">
      {" "}
      <input
        type="checkbox"
        aria-label="我已确认这些风险，仍要保存"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
      />{" "}
      我已确认这些风险，仍要保存{" "}
    </label>
  );
}
