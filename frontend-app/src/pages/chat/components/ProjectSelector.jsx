import React, { useLayoutEffect, useMemo, useRef, useState } from 'react';
import { Button as AriaButton, MenuTrigger, Popover } from 'react-aria-components';
import { ChevronDown, Folder } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { normalizeProjectPath, projectDisplayName, projectOptionsFor } from '../model/projectSelectorModel.js';
import { ProjectDropdown } from './ProjectDropdown.jsx';
import './ProjectSelector.css';

export function ProjectSelector({ copy = APP_COPY.zh.workbench, store, projectPath, isDisabled = false }) {
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
  useLayoutEffect(() => {
    if (isDisabled) setOpen(false);
  }, [isDisabled]);
  const setProjectMenuOpen = (isOpen) => {
    if (isDisabled) {
      setOpen(false);
      return;
    }
    setOpen(isOpen);
    if (isOpen) return;
    const focus = () => triggerRef.current?.focus?.();
    if (typeof globalThis.queueMicrotask === 'function') globalThis.queueMicrotask(focus);
    else focus();
  };

  const selectProject = (value) => {
    setOpen(false);
    if (isDisabled) return false;
    return store.setActiveProjectPath?.(value);
  };

  const addProject = () => {
    setOpen(false);
    if (isDisabled) return false;
    return store.addProjectFromPicker?.();
  };

  const removeProject = (value) => {
    if (isDisabled) return false;
    return store.removeProjectPath?.(value);
  };

  return (
    <div className="project-select-wrap">
      <MenuTrigger isDisabled={isDisabled} isOpen={open} onOpenChange={setProjectMenuOpen}>
        <AriaButton ref={triggerRef} type="button" className="project-select" aria-label={copy.selectProject} title={selected.full === '.' ? projectPath : selected.full} isDisabled={isDisabled}>
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
