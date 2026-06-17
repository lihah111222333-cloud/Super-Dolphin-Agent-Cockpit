import React from 'react';
import { PromptPageView } from '../../features/prompts/PromptPageView.jsx';

function PromptPage({ copy, projectPath, store, refreshKey = 0 }) {
  return <PromptPageView copy={copy} projectPath={projectPath} refreshKey={refreshKey} resolveLaunchPreferences={store?.resolveLaunchPreferences} />;
}

export { PromptPage };
