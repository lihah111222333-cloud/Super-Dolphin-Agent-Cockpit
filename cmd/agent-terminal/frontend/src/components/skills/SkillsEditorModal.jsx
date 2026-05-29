import React from 'react';

export function SkillsEditorModal({
  isEditorOpen, form, isEditingMainSkillFile, summarySuggesting, onSuggestSkillSummary,
  summarySuggestion, applySummarySuggestion, generatedSummaryPreview, scenarioKeywordsText,
  showRelatedSkillFiles, skillFiles, activeSkillFilePath, onOpenSkillSubfile,
  isBodyEditing, bodyEditorFocused, startBodyEdit, finishBodyEdit, skillBodyMarkdownHtml,
  onSkillPreviewClick, onBodyFocus, onBodyBlur, notice, saving, onSaveSkill, saveButtonLabel,
  closeEditor, vm
}) {
  if (!isEditorOpen) return null;

  return (
    <div
      className="modal-overlay skills-editor-overlay"
      data-testid="skills-editor-modal-overlay"
      tabIndex={0}
      onClick={(e) => { if (e.target === e.currentTarget) closeEditor(); }}
      onKeyDown={(e) => { if (e.key === 'Escape') closeEditor(); }}
    >
      <div className={`modal-box skills-editor-modal ${isBodyEditing || bodyEditorFocused ? 'is-body-expanded' : ''}`} role="dialog" aria-modal="true" data-testid="skills-editor-panel">
        <div className="skills-editor-modal-head">
          <div>
            <div className="modal-title">编辑技能</div>
            <div className="skills-inline-tip">你可以修改简介和技能内容。</div>
          </div>
          <button className="btn btn-ghost" data-testid="skills-editor-close-button" onClick={closeEditor}>关闭</button>
        </div>
        <div className="skills-editor-panel">
          <div className="skills-field">
            <label>技能名称</label>
            <input
              value={form.name}
              disabled={!isEditingMainSkillFile}
              className="modal-input"
              data-testid="skills-editor-name-input"
              placeholder="例如：backend"
              onChange={(e) => { form.name = e.target.value; }}
            />
          </div>
          <div className="skills-field">
            <label>显示名称</label>
            <input
              value={form.displayName}
              disabled={!isEditingMainSkillFile}
              className="modal-input"
              data-testid="skills-editor-display-name-input"
              placeholder="例如：后端开发"
              onChange={(e) => { form.displayName = e.target.value; }}
            />
          </div>
          <div className="skills-field">
            <div className="skills-editor-label-row">
              <label>技能简介</label>
              <button
                className="btn btn-ghost btn-sm"
                data-testid="skills-summary-suggest-button"
                disabled={!isEditingMainSkillFile || summarySuggesting || (!form.name.trim() && !form.body.trim())}
                onClick={onSuggestSkillSummary}
              >
                {summarySuggesting ? '生成中' : '帮我生成'}
              </button>
            </div>
            <input
              value={form.description}
              disabled={!isEditingMainSkillFile}
              className="modal-input"
              data-testid="skills-editor-summary-input"
              placeholder="一句话说明你会在什么情况下使用这个技能"
              onChange={(e) => { form.description = e.target.value; }}
            />
            {summarySuggestion && (
              <div className="skills-inline-tip" data-testid="skills-summary-suggestion">
                建议：{summarySuggestion}
                <button className="btn btn-ghost btn-sm" data-testid="skills-summary-apply-button" onClick={applySummarySuggestion}>采用</button>
                <button className="btn btn-ghost btn-sm" data-testid="skills-summary-regenerate-button" onClick={onSuggestSkillSummary}>重新生成</button>
              </div>
            )}
            {!summarySuggestion && generatedSummaryPreview && <div className="skills-inline-tip">根据正文预览：{generatedSummaryPreview}</div>}
            <div className="skills-inline-tip">建议写成“当你需要……时使用”。</div>
          </div>
          <div className="skills-field">
            <label>使用范围</label>
            <div className="skills-segmented skills-editor-scope" data-testid="skills-editor-scope-group">
              <label className={`skills-segmented-item ${form.scope === 'project' ? 'active' : ''} ${!isEditingMainSkillFile ? 'disabled' : ''}`}>
                <input
                  type="radio"
                  name="editorScope"
                  value="project"
                  checked={form.scope === 'project'}
                  data-testid="skills-editor-scope-project"
                  disabled={!isEditingMainSkillFile}
                  onChange={(e) => { form.scope = e.target.value; }}
                />
                <span className="skills-scope-dot skills-scope-dot-project" aria-hidden="true"></span>
                <span>项目共享</span>
              </label>
              <label className={`skills-segmented-item ${form.scope === 'personal' ? 'active' : ''} ${!isEditingMainSkillFile ? 'disabled' : ''}`}>
                <input
                  type="radio"
                  name="editorScope"
                  value="personal"
                  checked={form.scope === 'personal'}
                  data-testid="skills-editor-scope-personal"
                  disabled={!isEditingMainSkillFile}
                  onChange={(e) => { form.scope = e.target.value; }}
                />
                <span className="skills-scope-dot skills-scope-dot-personal" aria-hidden="true"></span>
                <span>私人使用</span>
              </label>
            </div>
          </div>
          <div className="skills-field">
            <label>关键词</label>
            <input
              value={scenarioKeywordsText}
              disabled={!isEditingMainSkillFile}
              className="modal-input"
              data-testid="skills-editor-trigger-input"
              placeholder="例如：bug、调试、部署、后端"
              onChange={(e) => { vm.scenarioKeywordsText.value = e.target.value; }}
            />
            <div className="skills-inline-tip">可选填入，用于辅助匹配使用技能</div>
          </div>
          {showRelatedSkillFiles && (
            <div className="skills-field">
              <label>附加内容</label>
              <div className="skills-subfile-list" data-testid="skills-subfiles-list">
                {skillFiles.map((file, fileIdx) => (
                  <button
                    key={file.path || (file.name + '-' + fileIdx)}
                    className={`skills-subfile-item ${activeSkillFilePath === file.path ? 'active' : ''}`}
                    data-testid={`skills-subfile-item-${fileIdx}`}
                    onClick={() => onOpenSkillSubfile(file)}
                  >
                    <span className="skills-subfile-name">{file.name}</span>
                    {file.isMain && <span className="skills-subfile-main-tag">主要文件</span>}
                  </button>
                ))}
              </div>
              <div className="skills-inline-tip">这里是这个技能附带的示例、模板或脚本。</div>
            </div>
          )}
          <div className="skills-field skills-field-body">
            <div className="skills-body-head">
              <label>{isEditingMainSkillFile ? '技能内容' : '关联文件内容'}</label>
              <div className="skills-body-head-actions">
                {!isBodyEditing ? (
                  <button
                    className="btn btn-secondary btn-xs skills-body-toggle"
                    data-testid="skills-editor-body-edit-button"
                    onClick={startBodyEdit}
                  >
                    编辑正文
                  </button>
                ) : (
                  <button
                    className="btn btn-ghost btn-xs skills-body-toggle"
                    data-testid="skills-editor-body-preview-button"
                    onClick={finishBodyEdit}
                  >
                    预览正文
                  </button>
                )}
              </div>
            </div>
            {!isBodyEditing ? (
              <div
                className="skills-body-preview chat-item-markdown agent-markdown-root"
                data-testid="skills-editor-body-preview"
                dangerouslySetInnerHTML={{ __html: skillBodyMarkdownHtml }}
                onClick={onSkillPreviewClick}
              ></div>
            ) : (
              <textarea
                ref={(el) => { if (el && vm.bodyInputRef.value !== el) vm.bodyInputRef.value = el; }}
                value={form.body}
                className={`modal-input skills-body-input ${isBodyEditing || bodyEditorFocused ? 'is-expanded' : ''}`}
                data-testid="skills-editor-body-input"
                placeholder="输入技能内容"
                onFocus={onBodyFocus}
                onBlur={onBodyBlur}
                onChange={(e) => { form.body = e.target.value; }}
              ></textarea>
            )}
            <div className="skills-inline-tip">点击“编辑正文”展开编辑；切回“预览正文”查看效果。</div>
            {!isEditingMainSkillFile && <div className="skills-inline-tip">当前正在编辑关联文件。</div>}
          </div>
          <div className="skills-actions-row" data-testid="skills-editor-actions">
            {notice.message && (
              <div className={`skills-notice skills-editor-notice is-${notice.level}`} data-testid="skills-editor-notice">
                {notice.message}
              </div>
            )}
            <button className="btn btn-ghost" data-testid="skills-editor-cancel-button" onClick={closeEditor}>取消</button>
            <button className="btn btn-primary skills-save-btn" data-testid="skills-save-button" disabled={saving} onClick={onSaveSkill}>
              {saveButtonLabel}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
