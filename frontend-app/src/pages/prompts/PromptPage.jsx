import React from 'react';
import { PromptPageView } from '../../features/prompts/PromptPageView.jsx';

function PromptPage({ projectPath, store, refreshKey = 0 }) {
  return <PromptPageView projectPath={projectPath} refreshKey={refreshKey} resolveLaunchPreferences={store?.resolveLaunchPreferences} />;
}

export { PromptPage };
