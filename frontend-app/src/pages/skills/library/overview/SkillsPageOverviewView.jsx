import React from "react";
import { Search } from "lucide-react";
import { scopeLabel } from "../SkillsPageMarkdownModel.js";
import {
  importSummaryPanelHint,
  importSummaryPanelTitle,
} from "../editor/SkillsPageImportSummaryModel.js";
import { RetryableSyncError } from "../../../shared/pageComponents.jsx";
import { SkillResolutionPanel } from "../resolution/SkillsPageResolutionView.jsx";
import { SkillGrid, SkillModals } from "./SkillsPageLibraryView.jsx";

export function SkillsPageView({ copy, model }) {
  return (
    <div className="plugins-square-container">
      <div className="plugins-square-header">
        <h1>{copy.title}</h1>
        <p className="plugins-square-subtitle">{copy.subtitle}</p>
      </div>
      <div className="subhead">{copy.localLibrary}</div>
      <SkillsOverview copy={copy} model={model} />
      <SkillsToolbar copy={copy} model={model} />
      <SkillsStatus copy={copy} model={model} />
      <SkillImportSummaryPanel editor={model.editor} />
      <SkillResolutionPanel model={model} />
      <SkillGrid copy={copy} model={model} />
      <SkillModals model={model} />
    </div>
  );
}

function SkillsOverview({ copy, model }) {
  const counts = model.filters.counts;
  const conflictValue =
    model.isProjectPending ||
    model.dashboard.isResolutionPending ||
    model.dashboard.resolutionSyncErrorText
      ? copy.pending
      : model.dashboard.resolutionConflicts.length;
  return (
    <section className="skills-overview-compact" aria-label={copy.overviewAria}>
      <dl className="overview-stats-row">
        <div>
          <dt>{copy.localSkills}</dt>
          <dd>{counts.all}</dd>
        </div>
        <div>
          <dt>{copy.projectShared}</dt>
          <dd>{counts.project}</dd>
        </div>
        <div>
          <dt>{copy.personalUse}</dt>
          <dd>{counts.personal}</dd>
        </div>
        <div>
          <dt>{copy.pendingConflicts}</dt>
          <dd>{conflictValue}</dd>
        </div>
      </dl>
    </section>
  );
}

function SkillImportSummaryPanel({ editor }) {
  const drafts = Array.isArray(editor.importSummaryDrafts)
    ? editor.importSummaryDrafts
    : [];
  if (drafts.length === 0) return null;
  return (
    <section
      className="skills-import-summary-panel"
      data-testid="skills-import-summary-panel"
    >
      <div className="skills-import-summary-head">
        <div>
          <strong>{importSummaryPanelTitle(drafts)}</strong>
          <span>{importSummaryPanelHint(drafts)}</span>
        </div>
        <button
          type="button"
          className="ghost"
          data-testid="skills-import-summary-clear"
          onClick={editor.clearImportSummaryDrafts}
        >
          收起
        </button>
      </div>
      {drafts.map((draft, index) => (
        <SkillImportSummaryItem
          draft={draft}
          editor={editor}
          index={index}
          key={draft.id || index}
        />
      ))}
    </section>
  );
}

function SkillImportSummaryItem({ draft, editor, index }) {
  return (
    <article
      className={`skills-import-summary-item is-${draft.status}`}
      data-testid={`skills-import-summary-item-${index}`}
    >
      <div className="skills-import-summary-main">
        <strong>{draft.name || "未命名技能"}</strong>
        <span>{scopeLabel(draft.scope)}</span>
      </div>
      <p className="skills-import-summary-text">
        {draft.status === "ready" || draft.status === "applied"
          ? draft.suggestion
          : draft.error || "技能已正常导入。可以稍后手动补充简介。"}
      </p>
      <div className="skills-import-summary-actions">
        {draft.status === "ready" ? (
          <button
            type="button"
            data-testid={`skills-import-summary-apply-${index}`}
            onClick={() => {
              void editor.applyImportSummaryDraft(draft);
            }}
          >
            采用并编辑
          </button>
        ) : null}
        {draft.status === "applied" ? (
          <span className="skills-inline-tip">已采用，保存后生效</span>
        ) : null}
        {draft.status === "error" ? (
          <button
            type="button"
            data-testid={`skills-import-summary-edit-${index}`}
            onClick={() => {
              void editor.openImportSummaryDraft(draft);
            }}
          >
            编辑简介
          </button>
        ) : null}
        <button
          type="button"
          className="ghost"
          data-testid={`skills-import-summary-dismiss-${index}`}
          onClick={() => editor.dismissImportSummaryDraft(draft)}
        >
          跳过
        </button>
      </div>
    </article>
  );
}

function SkillsToolbar({ copy, model }) {
  return (
    <div className="skills-toolbar skills-toolbar-unified">
      <div className="skill-filter segment">
        {model.filters.scopeOptions.map(([value]) => (
          <button
            key={value}
            type="button"
            className={model.scopeFilter === value ? "active" : ""}
            onClick={() => model.setScopeFilter(value)}
          >
            {copy.scopeLabels?.[value] ||
              (value === "personal"
                ? copy.personalUse
                : value === "project"
                  ? copy.projectShared
                  : copy.scopeAll)}{" "}
            {model.filters.counts[value]}
          </button>
        ))}
      </div>
      <label>
        <Search size={18} />
        <input
          value={model.query}
          onChange={(event) => model.setQuery(event.target.value)}
          placeholder={copy.searchSkillsPlaceholder}
          aria-label={copy.searchSkills}
        />
      </label>
      <div className="skills-toolbar-actions">
        <button
          type="button"
          className="btn-secondary"
          onClick={model.editor.openImportScope}
          disabled={model.editor.importing}
        >
          {copy.importDirs}
        </button>
        <button
          type="button"
          className="btn-primary"
          onClick={model.editor.openCreateEditor}
        >
          {copy.newSkill}
        </button>
      </div>
    </div>
  );
}

function SkillsStatus({ copy, model }) {
  return (
    <>
      {model.isProjectPending ? (
        <div className="status-surface-line info-status">{copy.connecting}</div>
      ) : null}
      {model.dashboard.isInitialLoading ? (
        <div className="status-surface-line loading-status">{copy.loading}</div>
      ) : null}
      {model.notice ? (
        <div className="status-surface-line success-status">{model.notice}</div>
      ) : null}
      {model.dashboard.showCachedSyncError ? (
        <CachedSkillSyncError copy={copy} dashboard={model.dashboard} />
      ) : null}
      {model.dashboard.showBlockingSyncError ? (
        <RetryableSyncError
          className="danger-text skills-sync-alert"
          message={model.dashboard.syncErrorText}
          onRetry={model.dashboard.retrySkillSurface}
        />
      ) : null}
      {model.error ? (
        <div className="status-surface-line error-status" role="alert">
          {model.error}
        </div>
      ) : null}
    </>
  );
}

function CachedSkillSyncError({ copy, dashboard }) {
  return (
    <div className="danger-text skills-sync-alert" role="alert">
      <span>同步失败，显示的是上次成功的数据：{dashboard.syncErrorText}</span>
      <button
        type="button"
        className="ghost"
        onClick={() => {
          void dashboard.retrySkillSurface();
        }}
      >
        {copy.retrySync}
      </button>
    </div>
  );
}
