import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { RecoveryApp } from './features/update-recovery/RecoveryApp.jsx';
import './features/update-recovery/RecoveryApp.css';
import { createRecoveryClient } from './features/update-recovery/recoveryClient.js';
import { reportFrontendReadiness } from './shared/api/wails/wailsBridgeRpc.js';

const root = document.getElementById('root');
if (!root) throw new Error('Recovery root element is required');

createRoot(root).render(
  <StrictMode>
    <RecoveryApp client={createRecoveryClient()} />
  </StrictMode>,
);

function reportRecoveryFrontendReadinessAfterPageLoad() {
  void reportFrontendReadiness().catch((error) => {
    console.error('recovery.frontend.readiness.handshake_failed', error);
  });
}

if (document.readyState === 'complete') {
  queueMicrotask(reportRecoveryFrontendReadinessAfterPageLoad);
} else {
  window.addEventListener('load', reportRecoveryFrontendReadinessAfterPageLoad, { once: true });
}
