import React, { useState } from "react";
import { FocusTrapDialog } from "../../../../shared/ui/FocusTrapDialog.jsx";
import { isMainSkillFile } from "./SkillsPageCitationModel.js";
import { skillNameFromDisplayName } from "../SkillsPageMarkdownModel.js";
import { SkillMarkdownPreview } from "./SkillMarkdownPreview.jsx";
import {
  resolveSkillPreviewFile,
  skillCitationFromLink,
} from "./SkillMarkdownPreviewModel.js";

export function SkillEditorDialog({ editor }) {
  return (
    <SkillEditorModal
      key={editor.activeSkillPath || "new"}
      form={editor.editorForm}
      setForm={editor.setForm}
      activeSkillPath={editor.activeSkillPath}
      files={editor.skillFiles}
      summarySuggestion={editor.summarySuggestion}
      summarySuggesting={editor.summarySuggesting}
      saving={editor.saving}
      onSuggestSummary={editor.suggestSummary}
      onApplySummary={editor.applySummary}
      onOpenCitation={editor.openSkillCitation}
      onOpenFile={editor.openSkillFile}
      onClose={editor.closeEditor}
      onSave={editor.saveEditor}
    />
  );
}

function SkillEditorModal(props) {
  const {
    form,
    setForm,
    activeSkillPath,
    files,
    summarySuggestion,
    summarySuggesting,
    saving,
    onSuggestSummary,
    onApplySummary,
    onOpenCitation,
    onOpenFile,
    onClose,
    onSave,
  } = props;
  const isMain = !activeSkillPath || isMainSkillFile(activeSkillPath);
  const modalTitle = activeSkillPath ? "编辑技能" : "新建技能";
  const update = (key) => (event) =>
    setForm((current) => ({ ...current, [key]: event.target.value }));
  const updateDisplayName = (event) => {
    const value = event.target.value;
    setForm((current) => ({
      ...current,
      displayName: value,
      name: activeSkillPath ? current.name : skillNameFromDisplayName(value),
    }));
  };
  const [bodyEditing, setBodyEditing] = useState(!activeSkillPath);
  return (
    <FocusTrapDialog
      ariaLabel={modalTitle}
      className="modal-box skills-editor-modal"
      closeDisabled={saving}
      onClose={onClose}
    >
      <SkillEditorHeader modalTitle={modalTitle} />
      <SkillEditorFields
        form={form}
        isMain={isMain}
        summarySuggestion={summarySuggestion}
        summarySuggesting={summarySuggesting}
        update={update}
        updateDisplayName={updateDisplayName}
        setForm={setForm}
        onApplySummary={onApplySummary}
        onSuggestSummary={onSuggestSummary}
      />
      <SkillEditorSubfiles
        activeSkillPath={activeSkillPath}
        files={files}
        onOpenFile={onOpenFile}
      />
      <SkillEditorBody
        activeSkillPath={activeSkillPath}
        bodyEditing={bodyEditing}
        files={files}
        form={form}
        isMain={isMain}
        onOpenCitation={onOpenCitation}
        onOpenFile={onOpenFile}
        setBodyEditing={setBodyEditing}
        update={update}
      />
      <footer>
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={saving}
        >
          取消
        </button>
        <button
          type="button"
          onClick={() => {
            void onSave();
          }}
          disabled={saving}
        >
          {saving ? "保存中..." : isMain ? "保存技能" : "保存文件"}
        </button>
      </footer>
    </FocusTrapDialog>
  );
}

function SkillEditorHeader({ modalTitle }) {
  return (
    <header className="skills-editor-modal-head">
      <div>
        <h2>{modalTitle}</h2>
        <p>你可以修改简介和技能内容。</p>
      </div>
    </header>
  );
}

function SkillEditorFields({
  form,
  isMain,
  summarySuggestion,
  summarySuggesting,
  update,
  updateDisplayName,
  setForm,
  onApplySummary,
  onSuggestSummary,
}) {
  return (
    <div className="form-grid">
      <label className="wide">
        技能名称
        <input
          value={form.displayName}
          onChange={updateDisplayName}
          disabled={!isMain}
        />
      </label>
      <SkillDescriptionField
        form={form}
        isMain={isMain}
        summarySuggestion={summarySuggestion}
        summarySuggesting={summarySuggesting}
        update={update}
        onApplySummary={onApplySummary}
        onSuggestSummary={onSuggestSummary}
      />
      <div className="skills-field">
        <span>使用范围</span>
        <fieldset className="skills-scope-segmented">
          <legend className="sr-only">使用范围</legend>
          <button
            type="button"
            className={form.scope === "project" ? "active" : ""}
            disabled={!isMain}
            onClick={() =>
              setForm((current) => ({ ...current, scope: "project" }))
            }
          >
            项目共享
          </button>
          <button
            type="button"
            className={form.scope === "personal" ? "active" : ""}
            disabled={!isMain}
            onClick={() =>
              setForm((current) => ({ ...current, scope: "personal" }))
            }
          >
            私人使用
          </button>
        </fieldset>
      </div>
      <label>
        关键词
        <input
          value={form.keywords}
          onChange={update("keywords")}
          disabled={!isMain}
          aria-label="关键词"
        />
      </label>
    </div>
  );
}

function SkillDescriptionField({
  form,
  isMain,
  summarySuggestion,
  summarySuggesting,
  update,
  onApplySummary,
  onSuggestSummary,
}) {
  return (
    <div className="skills-field wide">
      <div className="skills-editor-label-row">
        <label htmlFor="skills-description-input">技能简介</label>
        <button
          type="button"
          className="ghost"
          onClick={() => {
            void onSuggestSummary();
          }}
          disabled={
            !isMain ||
            summarySuggesting ||
            (!form.name.trim() && !form.body.trim())
          }
        >
          {summarySuggesting ? "生成中" : "帮我生成"}
        </button>
      </div>
      <input
        id="skills-description-input"
        value={form.description}
        onChange={update("description")}
        disabled={!isMain}
        aria-label="技能简介"
      />
      {summarySuggestion ? (
        <div
          className="skills-inline-tip skills-summary-suggestion"
          data-testid="skills-summary-suggestion"
        >
          <span>建议：{summarySuggestion}</span>
          <button type="button" onClick={onApplySummary}>
            采用
          </button>
        </div>
      ) : null}
      <div className="skills-inline-tip">建议写成“当你需要……时使用”。</div>
    </div>
  );
}

function SkillEditorSubfiles({ activeSkillPath, files, onOpenFile }) {
  if (!files.some((file) => !file.isMain)) return null;
  return (
    <div className="skills-subfiles-wrap">
      <span>附加内容</span>
      <div className="skills-subfiles">
        {files.map((file) => (
          <button
            key={file.path}
            type="button"
            className={file.path === activeSkillPath ? "active" : ""}
            onClick={() => {
              void onOpenFile(file);
            }}
          >
            {file.name}
            {file.isMain ? " · 主要文件" : ""}
          </button>
        ))}
      </div>
      <div className="skills-inline-tip">
        这里是这个技能附带的示例、模板或脚本。
      </div>
    </div>
  );
}

function SkillEditorBody({
  activeSkillPath,
  bodyEditing,
  files,
  form,
  isMain,
  onOpenCitation,
  onOpenFile,
  setBodyEditing,
  update,
}) {
  const openPreviewPath = (path, label) => {
    if (skillCitationFromLink(path, label)) {
      void onOpenCitation(path, label);
      return;
    }
    const file = resolveSkillPreviewFile(path, files, activeSkillPath);
    if (file) void onOpenFile(file);
  };
  return (
    <div className="skills-body-field">
      <div className="skills-body-head">
        <span>{isMain ? "技能内容" : "关联文件内容"}</span>
        {bodyEditing ? (
          <button
            type="button"
            className="ghost"
            onClick={() => setBodyEditing(false)}
          >
            预览正文
          </button>
        ) : (
          <button type="button" onClick={() => setBodyEditing(true)}>
            编辑正文
          </button>
        )}
      </div>
      {bodyEditing ? (
        <textarea
          value={form.body}
          onChange={update("body")}
          aria-label={isMain ? "技能内容" : "关联文件内容"}
        />
      ) : (
        <div
          className="skills-body-preview"
          data-testid="skills-editor-body-preview"
        >
          <SkillMarkdownPreview
            content={form.body}
            onOpenPath={openPreviewPath}
          />
        </div>
      )}
      <div className="skills-inline-tip">
        点击“编辑正文”展开编辑；切回“预览正文”查看效果。
      </div>
    </div>
  );
}
