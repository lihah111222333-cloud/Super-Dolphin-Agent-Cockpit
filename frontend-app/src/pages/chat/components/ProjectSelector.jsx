import React, { useRef, useMemo, useState } from 'react';
import { Button as AriaButton, Menu, MenuItem, MenuTrigger, Popover, Separator } from 'react-aria-components';
import { ChevronDown, Folder, Plus, X } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { runUIAction } from './chatUiActions.js';
import { normalizeProjectPath, projectDisplayName, projectOptionsFor } from './projectSelectorModel.js';
import './ProjectSelector.css';

function ProjectDropdown({ copy = APP_COPY.zh.workbench, options, selectedValue, onSelect, onRemove, onAdd }) {
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
            <button type="button" className="project-dropdown-remove" aria-label={`${copy.removeProject} ${item.label}`} title={copy.removeProject} onClick={(event) => removeProject(event, item.value)}>
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

export function ProjectSelector({ copy = APP_COPY.zh.workbench, store, projectPath }) {
  /*
   * ProjectSelector 只管菜单开关和点击。
   * 选择、添加、移除项目都交给 store，不在组件里改项目状态。
   */
  const [open, setOpen] = useState(false);
  const triggerRef = useRef(null);
  const activeProject = store.activeProject || projectPath;
  const options = useMemo(
    () => projectOptionsFor(store.projects, activeProject, projectPath),
    [store.projects, activeProject, projectPath],
  );
  const selectedValue = normalizeProjectPath(activeProject) || '.';
  const selected = options.find((item) => item.value === selectedValue)
    || options.find((item) => item.value === '.')
    || { value: '.', label: '当前目录 (.)', full: '.' };
  const selectedButtonLabel = selected.value === '.'
    ? projectDisplayName(projectPath)
    : projectDisplayName(selected.full || selected.value);
  const setProjectMenuOpen = (isOpen) => {
    setOpen(isOpen);
    if (isOpen) return;
    const focus = () => triggerRef.current?.focus?.();
    if (typeof globalThis.queueMicrotask === 'function') globalThis.queueMicrotask(focus);
    else focus();
  };

  const selectProject = (value) => {
    setOpen(false);
    return store.setActiveProjectPath?.(value);
  };

  const addProject = () => {
    setOpen(false);
    return store.addProjectFromPicker?.();
  };

  const removeProject = (value) => store.removeProjectPath?.(value);

  return (
    <div className="project-select-wrap">
      <MenuTrigger isOpen={open} onOpenChange={setProjectMenuOpen}>
        <AriaButton ref={triggerRef} type="button" className="project-select" aria-label={copy.selectProject} title={selected.full === '.' ? projectPath : selected.full}>
          <Folder size={15} />
          <span>{selectedButtonLabel}</span>
          <ChevronDown size={14} />
        </AriaButton>
        <Popover className="project-selector-popover" placement="bottom start">
          <ProjectDropdown copy={copy} options={options} selectedValue={selected.value} onSelect={selectProject} onRemove={removeProject} onAdd={addProject} />
        </Popover>
      </MenuTrigger>
    </div>
  );
}
