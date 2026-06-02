import React from 'react';
function PageHeader({ icon: Icon, title, subtitle, actions }) {
  return (
    <header className="page-header">
      <h1><Icon size={25} /> {title}</h1>
      {subtitle ? <p>{subtitle}</p> : null}
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  );
}

function RetryableSyncError({ className = 'danger-text', message, onRetry }) {
  if (!message) return null;
  return (
    <div className={className} role="alert">
      <span>{message}</span>
      <button type="button" className="ghost" onClick={() => { void onRetry(); }}>重试同步</button>
    </div>
  );
}

function Panel({ title, children }) {
  return (
    <section className="panel">
      <h3>{title}</h3>
      <div>{children}</div>
    </section>
  );
}

export { PageHeader, Panel, RetryableSyncError };
