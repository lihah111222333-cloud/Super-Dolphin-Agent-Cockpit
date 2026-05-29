import React, { useState, useRef, useEffect, useMemo } from 'react';
import { logDebug } from '../services/log.js';

export function ProjectSelect({ modelValue = '.', options = [], onUpdateModelValue, onAddProject, onRemoveProject }) {
  const [open, setOpen] = useState(false);
  const [dropdownStyle, setDropdownStyle] = useState({});
  
  const wrapRef = useRef(null);
  const triggerRef = useRef(null);

  const selectedLabel = useMemo(() => {
    const match = (options || []).find((o) => o.value === modelValue);
    return match ? match.label : modelValue || '.';
  }, [options, modelValue]);

  const toggle = () => {
    if (!open && triggerRef.current) {
      const rect = triggerRef.current.getBoundingClientRect();
      setDropdownStyle({
        position: 'fixed',
        top: (rect.bottom + 4) + 'px',
        left: rect.left + 'px',
        minWidth: Math.max(rect.width, 220) + 'px',
      });
    }
    setOpen((prev) => !prev);
  };

  const selectItem = (value) => {
    logDebug('ui', 'projectSelect.changed', { value: value || '.' });
    if (typeof onUpdateModelValue === 'function') onUpdateModelValue(value);
    setOpen(false);
  };

  const removeItem = (ev, value) => {
    ev.stopPropagation();
    logDebug('ui', 'projectSelect.remove', { value });
    if (typeof onRemoveProject === 'function') onRemoveProject(value);
  };

  const handleAddProject = () => {
    logDebug('ui', 'projectSelect.add.click', {});
    if (typeof onAddProject === 'function') onAddProject();
    setOpen(false);
  };

  useEffect(() => {
    const onClickOutside = (ev) => {
      if (wrapRef.current && !wrapRef.current.contains(ev.target)) {
        setOpen(false);
      }
    };

    document.addEventListener('pointerdown', onClickOutside, true);
    return () => {
      document.removeEventListener('pointerdown', onClickOutside, true);
    };
  }, []);

  return (
    <div className="project-select-wrap" ref={wrapRef}>
      <button 
        ref={triggerRef} 
        className="project-selector" 
        onClick={toggle} 
        title={modelValue}
      >
        <svg className="project-selector-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"></path>
        </svg>
        <span className="project-selector-text">{selectedLabel}</span>
        <svg className="project-selector-chevron" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M4 6l4 4 4-4"></path>
        </svg>
      </button>

      {open && (
        <div className="project-dropdown" style={dropdownStyle}>
          {options.map((item) => {
            const isSelected = item.value === modelValue;
            return (
              <div
                key={item.value}
                className={`project-dropdown-item ${isSelected ? 'selected' : ''}`}
                title={item.full}
                onClick={() => selectItem(item.value)}
              >
                <svg className="project-dropdown-item-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  {isSelected && <path d="M3 8l3 3 7-7"></path>}
                </svg>
                <span className="project-dropdown-label">{item.label}</span>
                {item.value !== '.' && (
                  <button
                    className="project-dropdown-remove"
                    title="移除此项目"
                    onClick={(ev) => removeItem(ev, item.value)}
                  >
                    ×
                  </button>
                )}
              </div>
            );
          })}
          <div className="project-dropdown-divider"></div>
          <div className="project-dropdown-item project-dropdown-add" onClick={handleAddProject}>
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ width: '13px', height: '13px', flexShrink: 0, opacity: 0.6 }}>
              <path d="M8 3v10M3 8h10"></path>
            </svg>
            <span>添加项目</span>
          </div>
        </div>
      )}
    </div>
  );
}

export default ProjectSelect;
