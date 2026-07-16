import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { RecoveryApp } from './features/update-recovery/RecoveryApp.jsx';
import './features/update-recovery/RecoveryApp.css';
import { createRecoveryClient } from './features/update-recovery/recoveryClient.js';

const root = document.getElementById('root');
if (!root) throw new Error('Recovery root element is required');

createRoot(root).render(
  <StrictMode>
    <RecoveryApp client={createRecoveryClient()} />
  </StrictMode>,
);
