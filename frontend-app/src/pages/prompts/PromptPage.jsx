import React from 'react';
import { PromptPageView } from '../../features/prompts/PromptPageView.jsx';
import { promptPageService } from './services/promptPageService.js';

function PromptPage({ copy, projectPath, store, refreshKey = 0 }) {
  return <PromptPageView copy={copy} projectPath={projectPath} refreshKey={refreshKey} resolveLaunchPreferences={store?.resolveLaunchPreferences} promptPageService={promptPageService} />;
}

export { PromptPage };
