import React, { useEffect, useState } from 'react';
import { errorMessage } from './pageShared.js';
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
  const [retryError, setRetryError] = useState('');
  useEffect(() => {
    setRetryError('');
  }, [message]);
  if (!message) return null;
  const handleRetry = () => {
    setRetryError('');
    try {
      Promise.resolve(onRetry()).catch((error) => {
        setRetryError(`重试同步失败：${errorMessage(error)}`);
      });
    }
    catch (error) {
      setRetryError(`重试同步失败：${errorMessage(error)}`);
    }
  };
  return (
    <div className={className} role="alert">
      <span>{message}</span>
      <button type="button" className="ghost" onClick={handleRetry}>重试同步</button>
      {retryError ? <span>{retryError}</span> : null}
    </div>
  );
}

function Panel({ className = '', title, children }) {
  const panelClassName = ['panel', className].filter(Boolean).join(' ');
  return (
    <section className={panelClassName}>
      <h3>{title}</h3>
      <div>{children}</div>
    </section>
  );
}

export { PageHeader, Panel, RetryableSyncError };
