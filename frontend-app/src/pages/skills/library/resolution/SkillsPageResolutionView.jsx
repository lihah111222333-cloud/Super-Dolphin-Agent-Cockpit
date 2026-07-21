import React from "react";
import { scopeLabel } from "../SkillsPageMarkdownModel.js";
import {
  resolutionKindLabel,
  resolutionActionLabel,
  resolutionConflictGuide,
  resolutionPreviewIntro,
  resolutionProviderEntryLabel,
  resolutionProviderEntries,
} from "./SkillsPageResolutionLabels.js";
import {
  resolutionActionEntries,
  resolutionActionEntryLabel,
  resolutionActionEntryHelp,
  resolutionActionEntryTarget,
  resolutionApplyKey,
  previewItemPaths,
  resolutionShortHash,
  resolutionManualSteps,
} from "./SkillsPageResolutionActions.js";
import {
  firstTextField,
  optionalArray,
  scopeForSkill,
  textFromValue,
} from "../dashboard/skillsDashboardModel.js";

export function SkillResolutionPanel({ model }) {
  const conflicts = model.dashboard.resolutionConflicts;
  if (!conflicts.length) return null;
  return (
    <section className="skills-resolution-panel">
      <header className="skills-resolution-header fusion-surface">
        <strong>发现 {conflicts.length} 个技能冲突，需要处理后再使用。</strong>
      </header>
      <div className="skills-resolution-list">
        {conflicts.map((conflict, index) => (
          <SkillResolutionConflict
            conflict={conflict}
            index={index}
            key={textFromValue(conflict.conflict_id) || String(index)}
            resolution={model.resolution}
          />
        ))}
        {model.resolution.preview ? (
          <SkillResolutionPreview resolution={model.resolution} />
        ) : null}
      </div>
    </section>
  );
}

function SkillResolutionConflict({ conflict, index, resolution }) {
  const conflictID = textFromValue(conflict.conflict_id) || String(index);
  const promptConflictID = textFromValue(
    resolution.namePrompt?.conflict?.conflict_id,
  );
  const promptApplies =
    resolution.namePrompt &&
    promptConflictID === textFromValue(conflict.conflict_id);
  const manualSteps = resolutionManualSteps(conflict);
  return (
    <article className="skills-resolution-item">
      <header>
        <h3>
          {firstTextField(
            conflict,
            ["name", "skill_name"],
            "skill resolution conflict",
          ) || "未命名技能"}{" "}
          · {resolutionKindLabel(conflict.kind)}
        </h3>
        <span>{scopeLabel(scopeForSkill(conflict))}</span>
      </header>
      <p className="skills-resolution-guide">
        {resolutionConflictGuide(conflict)}
      </p>
      {resolutionProviderEntries(conflict).map((entry, sourceIndex) => (
        <SkillResolutionActionRow
          conflict={conflict}
          conflictID={conflictID}
          providerEntry={entry}
          resolution={resolution}
          sourceIndex={sourceIndex}
          key={`${conflictID}:${sourceIndex}:${resolutionProviderEntryLabel(entry)}`}
        />
      ))}
      {manualSteps.length > 0 ? (
        <ul className="skills-resolution-manual-steps">
          {manualSteps.map((step) => (
            <li key={step}>{step}</li>
          ))}
        </ul>
      ) : null}
      {promptApplies ? (
        <SkillResolutionNamePrompt resolution={resolution} />
      ) : null}
    </article>
  );
}

function SkillResolutionActionRow({
  conflict,
  conflictID,
  providerEntry,
  resolution,
  sourceIndex,
}) {
  const providerEntries = resolutionProviderEntries(conflict);
  return (
    <div className="skills-resolution-actions">
      {providerEntries.length > 1 ? (
        <span className="skills-resolution-source">
          {resolutionProviderEntryLabel(providerEntry)}
        </span>
      ) : null}
      {resolutionActionEntries(conflict).map((actionEntry, actionIndex) => (
        <SkillResolutionActionButton
          actionEntry={actionEntry}
          actionIndex={actionIndex}
          conflict={conflict}
          providerEntry={providerEntry}
          resolution={resolution}
          key={`${conflictID}:${sourceIndex}:${actionIndex}`}
        />
      ))}
    </div>
  );
}

function resolutionActionVisualKind(actionEntry) {
  const action = (actionEntry.action || actionEntry).toString();
  if (action === "view_diff" || action === "view_unmanaged")
    return "ghost resolution-btn-secondary";
  if (action === "delete") return "danger-button";
  return actionEntry.recommended || actionEntry.preferred
    ? "suiyuan-btn-fusion resolution-btn-primary"
    : "suiyuan-btn-fusion-ghost resolution-btn-secondary";
}

function SkillResolutionActionButton({
  actionEntry,
  actionIndex,
  conflict,
  providerEntry,
  resolution,
}) {
  const action = (actionEntry.action || actionEntry).toString();
  const applyKey = resolutionApplyKey(
    conflict,
    action,
    resolutionActionEntryTarget(actionEntry, providerEntry),
  );
  return (
    <button
      key={`${applyKey}:${actionIndex}`}
      type="button"
      className={resolutionActionVisualKind(actionEntry)}
      title={resolutionActionEntryHelp(actionEntry)}
      onClick={() => {
        void resolution.runAction(conflict, actionEntry, providerEntry);
      }}
      disabled={resolution.actioning === applyKey}
    >
      {resolution.actioning === applyKey
        ? "处理中..."
        : resolutionActionEntryLabel(actionEntry)}
    </button>
  );
}

function SkillResolutionNamePrompt({ resolution }) {
  return (
    <div className="skills-resolution-name-field">
      <label>
        新技能名称
        <input
          value={resolution.nameInput}
          onChange={(event) => resolution.setNameInput(event.target.value)}
          aria-label="新技能名称"
        />
      </label>
      <button
        type="button"
        onClick={() => {
          void resolution.confirmName();
        }}
        disabled={resolution.actioning === resolution.namePrompt.applyKey}
      >
        {resolutionPromptActionLabel(resolution)}
      </button>
      <button
        type="button"
        className="ghost"
        onClick={() => {
          resolution.setNamePrompt(null);
          resolution.setNameInput("");
        }}
      >
        取消
      </button>
    </div>
  );
}

function resolutionPromptActionLabel(resolution) {
  if (resolution.actioning === resolution.namePrompt?.applyKey)
    return resolution.namePrompt?.autoApply ? "处理中..." : "生成中...";
  return resolution.namePrompt?.autoApply ? "确认处理" : "生成预览";
}

function SkillResolutionPreview({ resolution }) {
  return (
    <article className="skills-resolution-preview">
      <header>
        <h3>{resolutionActionLabel(resolution.preview.action)}</h3>
        {resolution.preview.requiresApply ? (
          <button
            type="button"
            onClick={() => {
              void resolution.confirmPreview();
            }}
            disabled={resolution.actioning === "confirm"}
          >
            {resolution.actioning === "confirm" ? "应用中..." : "确认应用"}
          </button>
        ) : null}
        <button
          type="button"
          className="ghost"
          onClick={() => resolution.setPreview(null)}
        >
          取消
        </button>
      </header>
      <p className="skills-resolution-guide">
        {resolutionPreviewIntro(resolution.preview)}
      </p>
      {optionalArray(resolution.preview.items).map((item, index) => (
        <SkillResolutionPreviewItem
          action={resolution.preview.action}
          item={item}
          key={textFromValue(item.preview_id) || String(index)}
        />
      ))}
    </article>
  );
}

function SkillResolutionPreviewItem({ action, item }) {
  const sourceHash = resolutionShortHash(item.source_hash || item.sourceHash);
  const targetHash = resolutionShortHash(item.target_hash || item.targetHash);
  const diff = textFromValue(item.diff);
  return (
    <div className="skills-resolution-preview-item">
      {previewItemPaths(item, action).map((pathItem) => (
        <p key={`${pathItem.label}:${pathItem.value}`}>
          <span>{pathItem.label}</span>
          <code>{pathItem.value}</code>
        </p>
      ))}
      {sourceHash || targetHash || diff ? (
        <details className="skills-resolution-technical" open>
          <summary>技术信息</summary>
          {sourceHash ? (
            <div className="skills-resolution-preview-path">
              外部版本号：{sourceHash}
            </div>
          ) : null}
          {targetHash ? (
            <div className="skills-resolution-preview-path">
              管理版本号：{targetHash}
            </div>
          ) : null}
          {diff ? <pre className="skills-resolution-diff">{diff}</pre> : null}
        </details>
      ) : null}
    </div>
  );
}
