import React from 'react';
import { logDebug } from '../services/log.js';

export function SidebarNav({ page, items, badges = {}, onChange }) {
  const handleNavClick = (target) => {
    logDebug('ui', 'sidebar.change', {
      from: page,
      to: target,
    });
    onChange(target);
  };

  return (
    <nav id="sidebar" data-testid="sidebar-nav">
      {items.map((item) => {
        const hasBadge = !!badges[item.key];
        const isActive = item.key === page;
        
        return (
          <button
            key={item.key}
            className={`sidebar-btn ${isActive ? 'active' : ''}`}
            data-testid={`nav-${item.key}`}
            onClick={() => handleNavClick(item.key)}
          >
            {item.icon.includes('<svg') ? (
              <span 
                className="sb-icon" 
                dangerouslySetInnerHTML={{ __html: item.icon }}
              />
            ) : (
              <span className="sb-icon">{item.icon}</span>
            )}
            <span className="sb-label">{item.label}</span>
            {hasBadge && (
              <span 
                className="sb-badge-dot" 
                title={`${badges[item.key]} 条待审批`}
              />
            )}
          </button>
        );
      })}
      <div className="sidebar-spacer"></div>
    </nav>
  );
}
