import React from 'react';
import { PanelLeftClose, PanelLeftOpen, Settings as SettingsIcon } from 'lucide-react';

export function WorkbenchActivityBar({
  activePage,
  items,
  onSelect,
  onToggleSidebar,
  sidebarOpen,
}) {
  return (
    <nav className="workbench-activity-bar" aria-label="工作台活动栏">
      <button
        type="button"
        className="workbench-activity-button workbench-activity-toggle"
        aria-label={sidebarOpen ? '收起主侧栏' : '展开主侧栏'}
        aria-expanded={sidebarOpen}
        onClick={onToggleSidebar}
      >
        {sidebarOpen
          ? <PanelLeftClose size={18} aria-hidden="true" />
          : <PanelLeftOpen size={18} aria-hidden="true" />}
      </button>
      <div className="workbench-activity-destinations">
        {items.map((item) => {
          const Icon = item.icon;
          const active = activePage === item.id;
          return (
            <button
              key={item.id}
              type="button"
              className={`workbench-activity-button${active ? ' is-selected' : ''}`}
              aria-label={item.label}
              aria-current={active ? 'page' : undefined}
              title={item.label}
              onClick={() => onSelect(item.id)}
            >
              <Icon size={18} aria-hidden="true" />
            </button>
          );
        })}
      </div>
      <button
        type="button"
        className={`workbench-activity-button workbench-activity-settings${activePage === 'settings' ? ' is-selected' : ''}`}
        aria-label="设置"
        aria-current={activePage === 'settings' ? 'page' : undefined}
        title="设置"
        onClick={() => onSelect('settings')}
      >
        <SettingsIcon size={18} aria-hidden="true" />
      </button>
    </nav>
  );
}
