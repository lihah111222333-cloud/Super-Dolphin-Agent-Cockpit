import React from 'react';
import ReactDOM from 'react-dom/client';
import 'katex/dist/katex.min.css';
import { AppRoot } from './App.jsx';
import { logError, logInfo } from './services/log.js';

if (typeof window !== 'undefined') {
  window.__REACT_APP_ACTIVE__ = true;
  window.addEventListener('error', (event) => {
    logError('window', 'uncaught_error', {
      message: event.message,
      filename: event.filename,
      lineno: event.lineno,
      colno: event.colno,
      error: event.error,
    });
  });
  window.addEventListener('unhandledrejection', (event) => {
    logError('window', 'unhandled_rejection', {
      reason: event.reason,
    });
  });
}

async function bootstrap() {
  try {
    logInfo('app', 'mount.start', {});
    const container = document.getElementById('app');
    if (!container) throw new Error('Root container #app not found');
    const root = ReactDOM.createRoot(container);
    root.render(
      <React.StrictMode>
        <AppRoot />
      </React.StrictMode>
    );
    logInfo('app', 'mount.done', {});
  } catch (error) {
    logError('app', 'mount.failed', { error });
    throw error;
  }
}

void bootstrap();
