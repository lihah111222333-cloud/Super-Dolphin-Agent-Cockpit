import React from 'react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';

export function AddSkillToolDialog(props) {
  const { availableSkills, copy, enabled, loadState, onChangeDescription, onChangeEnabled, onClose, onSave, onSelectSkill, registeredCount, saveError, saving, selection } = props;
  const descriptionText = props.description.trim();
  const ready = loadState.status === 'ready';
  const loading = loadState.status === 'loading';
  const loadFailed = loadState.status === 'error';
  const emptyReady = ready && availableSkills.length === 0;
  const saveDisabled = saving || loading || loadFailed || emptyReady || !selection || !descriptionText;
  return (
    <FocusTrapDialog ariaLabel={copy.dialogTitle} className="modal-box" closeDisabled={saving} onClose={onClose}>
      <header className="skills-editor-modal-head">
        <div>
          <h2>{copy.dialogTitle}</h2>
          <p>{copy.dialogIntro}</p>
        </div>
      </header>
      {loading ? <p className="skills-inline-tip">{copy.loadingSkills}</p> : null}
      {loadFailed ? <p className="skills-inline-tip error-status" role="alert">{copy.loadFailedPrefix}{loadState.error}</p> : null}
      {emptyReady ? (
        <p className="skills-inline-tip">
          {registeredCount > 0 ? copy.allRegistered : copy.noSkills}
        </p>
      ) : null}
      {ready && availableSkills.length > 0 ? (
        <div className="form-grid">
          <label className="wide">
            {copy.selectSkill}
            <select aria-label={copy.selectSkill} value={selection} onChange={(event) => onSelectSkill(event.target.value)} disabled={saving}>
              {availableSkills.map((skill) => (
                <option key={skill.id} value={skill.name}>{skill.title}（{skill.name}）</option>
              ))}
            </select>
          </label>
          {registeredCount > 0 ? <p className="skills-inline-tip">{copy.alreadyRegisteredHint}{registeredCount}</p> : null}
          <label className="wide">
            {copy.descriptionLabel}
            <textarea aria-label={copy.descriptionLabel} rows={3} value={props.description} onChange={(event) => onChangeDescription(event.target.value)} disabled={saving} placeholder={copy.descriptionPlaceholder} />
          </label>
          <label className="skill-tool-enabled-field">
            <input aria-label={copy.enabledLabel} type="checkbox" checked={enabled} onChange={(event) => onChangeEnabled(event.target.checked)} disabled={saving} />
            <span>{copy.enabledText}</span>
          </label>
        </div>
      ) : null}
      {ready && availableSkills.length > 0 && !descriptionText ? <p className="skills-inline-tip">{copy.descriptionRequired}</p> : null}
      {saveError ? <p className="skills-inline-tip error-status" role="alert">{saveError}</p> : null}
      <footer>
        <button type="button" className="ghost" onClick={onClose} disabled={saving}>{copy.cancel}</button>
        <button type="button" onClick={() => { void onSave(); }} disabled={saveDisabled}>{saving ? copy.saving : copy.action}</button>
      </footer>
    </FocusTrapDialog>
  );
}
