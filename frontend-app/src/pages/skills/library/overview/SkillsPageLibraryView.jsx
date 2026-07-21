import React from "react";
import { FileText } from "lucide-react";
import { scopeLabel } from "../SkillsPageMarkdownModel.js";
import { SkillEditorDialog } from "../editor/SkillsPageEditorView.jsx";
import {
  ConfirmSkillDeleteModal,
  ImportScopeModal,
} from "../app/SkillsPageDialogs.jsx";
import { trimmedText } from "../dashboard/skillsDashboardModel.js";

export function SkillGrid({ copy, model }) {
  const showReadyEmpty =
    !model.isProjectPending &&
    !model.dashboard.isInitialLoading &&
    !model.dashboard.showBlockingSyncError &&
    model.filters.filteredItems.length === 0;
  return (
    <>
      {showReadyEmpty ? (
        <SkillsEmptyState
          copy={copy}
          hasSkills={model.filters.counts.all > 0}
        />
      ) : null}
      {model.filters.filteredItems.length > 0 ? (
        <div className="skill-grid">
          {model.filters.filteredItems.map((skill) => (
            <SkillCard
              copy={copy}
              key={skill.id}
              skill={skill}
              onEdit={model.editor.openEditSkill}
              onDelete={model.editor.onDeleteSkill}
            />
          ))}
        </div>
      ) : null}
      {model.filters.countText ? (
        <p className="skills-inline-tip">{model.filters.countText}</p>
      ) : null}
    </>
  );
}

function SkillsEmptyState({ copy, hasSkills }) {
  if (hasSkills)
    return (
      <div className="empty-state">
        <h3>{copy.noMatchesTitle}</h3>
        <p>{copy.noMatchesText}</p>
      </div>
    );
  return <div className="status-surface-line empty-status">{copy.empty}</div>;
}

export function SkillModals({ model }) {
  const editor = model.editor;
  return (
    <>
      {editor.editorOpen ? <SkillEditorDialog editor={editor} /> : null}
      {editor.deleteTarget ? (
        <ConfirmSkillDeleteModal
          skill={editor.deleteTarget}
          deleting={editor.deleting}
          onClose={editor.closeDelete}
          onConfirm={editor.confirmDeleteSkill}
        />
      ) : null}
      {editor.importScopeOpen ? (
        <ImportScopeModal
          importing={editor.importing}
          onClose={editor.closeImportScope}
          onConfirm={editor.confirmImportScope}
        />
      ) : null}
    </>
  );
}

function SkillCard({ copy, skill, onEdit, onDelete }) {
  const tags = skill.tags.slice(0, 4);
  const extraTagCount = skill.tags.length - tags.length;
  const descriptionText = trimmedText(skill.description);
  const summaryText = trimmedText(skill.summary);
  const description = descriptionText || summaryText || copy.noDescription;
  const shouldShowSummary = Boolean(summaryText && summaryText !== description);
  return (
    <article className="skill-card skill-card-redesign">
      <div className="skill-card-icon" aria-hidden="true">
        <FileText size={20} />
      </div>
      <div className="mcp-tool-main">
        <header className="mcp-tool-title-line">
          <h3>{skill.title}</h3>
        </header>
        <p className="path-text">{skill.dir || copy.noPath}</p>
        <p className="description-text">{description}</p>
        {shouldShowSummary ? (
          <div className="summary-quote">{summaryText}</div>
        ) : null}
        <div className="card-tags">
          {tags.length > 0 ? (
            tags.map((tag) => (
              <span key={tag} className="card-tag">
                {tag}
              </span>
            ))
          ) : (
            <span className="card-tag">{copy.noKeywords}</span>
          )}
          {extraTagCount > 0 ? (
            <span className="card-tag">+{extraTagCount}</span>
          ) : null}
        </div>
      </div>
      <span className="mcp-tool-status is-enabled">
        {scopeLabel(skill.scope)}
      </span>
      <div className="card-actions-redesign">
        <button
          type="button"
          onClick={() => {
            void onEdit(skill);
          }}
          disabled={!skill.dir}
        >
          {copy.editDetails}
        </button>
        <button
          type="button"
          className="danger-btn"
          onClick={() => {
            void onDelete(skill);
          }}
          disabled={!skill.name}
        >
          {copy.delete}
        </button>
      </div>
    </article>
  );
}
