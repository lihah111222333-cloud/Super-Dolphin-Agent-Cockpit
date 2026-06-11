import React, { useEffect, useMemo, useRef, useState } from 'react';
import { ChevronDown, Folder, Plus, X } from 'lucide-react';
import { runUIAction } from './chatUiActions.js';
import { normalizeProjectPath, projectDisplayName, projectOptionsFor } from './projectSelectorModel.js';

function ProjectDropdown({ options, selectedValue, onSelect, onRemove, onAdd }) {
  return (
    <div className="project-dropdown" role="menu" aria-label="项目列表">
      {options.map((item) => (
        <div key={item.value} className={`project-dropdown-row ${item.value === selectedValue ? 'selected' : ''}`} role="none" title={item.full}>
          <button
            type="button"
            className="project-dropdown-item"
            role="menuitem"
            onClick={() => runUIAction(() => onSelect(item.value))}
          >
            <span className="project-option-check" aria-hidden="true">{item.value === selectedValue ? '✓' : ''}</span>
            <span className="project-dropdown-label">{item.label}</span>
          </button>
          {item.value !== '.' ? (
            <button
              type="button"
              className="project-dropdown-remove"
              aria-label={`移除此项目 ${item.label}`}
              title="移除此项目"
              onClick={(event) => runUIAction(() => onRemove(event, item.value))}
            >
              <X size={12} />
            </button>
          ) : null}
        </div>
      ))}
      <div className="project-dropdown-divider" />
      <button
        type="button"
        className="project-dropdown-item project-dropdown-add"
        role="menuitem"
        onClick={() => runUIAction(onAdd)}
      >
        <Plus size={13} />
        <span>添加项目</span>
      </button>
    </div>
  );
}

export function ProjectSelector({ store, projectPath }) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef(null);
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

  useEffect(() => {
    if (!open) return undefined;
    const onPointerDown = (event) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target)) {
        setOpen(false);
      }
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    return () => document.removeEventListener('pointerdown', onPointerDown, true);
  }, [open, wrapRef]);

  const selectProject = (value) => {
    setOpen(false);
    return store.setActiveProjectPath?.(value);
  };

  const addProject = () => {
    setOpen(false);
    return store.addProjectFromPicker?.();
  };

  const removeProject = (event, value) => {
    event.stopPropagation();
    return store.removeProjectPath?.(value);
  };

  return (
    <div className="project-select-wrap" ref={wrapRef}>
      <button
        type="button"
        className="project-select"
        aria-label="选择项目"
        aria-haspopup="menu"
        aria-expanded={open}
        title={selected.full === '.' ? projectPath : selected.full}
        onClick={() => setOpen((value) => !value)}
      >
        <Folder size={15} />
        <span>{selectedButtonLabel}</span>
        <ChevronDown size={14} />
      </button>
      {open ? (
        <ProjectDropdown
          options={options}
          selectedValue={selected.value}
          onSelect={selectProject}
          onRemove={removeProject}
          onAdd={addProject}
        />
      ) : null}
    </div>
  );
}
