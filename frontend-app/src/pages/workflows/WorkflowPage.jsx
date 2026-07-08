import React from 'react';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { WorkflowPageView } from './components/WorkflowPageView.jsx';
import { useWorkflowPageController } from './hooks/useWorkflowPageController.js';
import './services/workflowPageService.js';
import './WorkflowPage.css';

function WorkflowPage({ copy = APP_COPY.zh.workflow, onWorkflowViewChange, projectPath, store, refreshKey = 0 }) {
  const model = useWorkflowPageController({ projectPath, refreshKey, store });
  return <WorkflowPageView copy={copy} model={model} onWorkflowViewChange={onWorkflowViewChange} />;
}

export { WorkflowPage };
