import React from 'react';
import { Menu, MenuItem, Separator } from 'react-aria-components';
import { Plus, X } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { runUIAction } from '../model/chatUiActions.js';

function ProjectDropdown({
  copy = APP_COPY.zh.workbench,
  options,
  selectedValue,
  onSelect,
  onRemove,
  onAdd,
}) {
  const runProjectAction = (key) => {
    const actionKey = String(key);
    if (actionKey === 'add-project') return runUIAction(onAdd);
    const value = actionKey.startsWith('select:') ? actionKey.slice('select:'.length) : '';
    const item = value ? options.find((option) => option.value === value) : null;
    if (!item) throw new Error(`Unknown project selector action: ${actionKey}`);
    return runUIAction(() => onSelect(item.value));
  };
  const removeProject = (event, value) => {
    event.preventDefault();
    event.stopPropagation();
    return runUIAction(() => onRemove(value));
  };
  return (
    <Menu className="project-dropdown" aria-label={copy.projectList || copy.projects} onAction={runProjectAction}>
      {options.map((item) => (
        <MenuItem key={item.value} id={`select:${item.value}`} className={`project-dropdown-row ${item.value === selectedValue ? 'selected' : ''}`} textValue={item.label} title={item.full}>
          <span className="project-dropdown-item">
            <span className="project-option-check" aria-hidden="true">{item.value === selectedValue ? '✓' : ''}</span>
            <span className="project-dropdown-label">{item.label}</span>
          </span>
          {item.value !== '.' ? (
            <button
              type="button"
              className="project-dropdown-remove"
              aria-label={`${copy.removeProject} ${item.label}`}
              title={copy.removeProject}
              onClick={(event) => removeProject(event, item.value)}
            >
              <X size={12} />
            </button>
          ) : null}
        </MenuItem>
      ))}
      <Separator className="project-dropdown-divider" />
      <MenuItem id="add-project" className="project-dropdown-item project-dropdown-add" textValue={copy.addProjectMenu || copy.addProject}>
        <Plus size={13} />
        <span>{copy.addProjectMenu || copy.addProject}</span>
      </MenuItem>
    </Menu>
  );
}

export { ProjectDropdown };
