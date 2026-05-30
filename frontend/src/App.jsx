import React, { useState } from 'react';
import AppShell from './widgets/app-shell/AppShell';
import UnifiedChatPage from './pages/unified-chat/UnifiedChatPage';
import SystemPromptPage from './pages/unified-chat/SystemPromptPage';
import DagsPage from './pages/dags/DagsPage';
import SkillsPage from './pages/skills/SkillsPage';
import MemoryCenterPage from './pages/memory/MemoryCenterPage';
import SharedFilesPage from './pages/memory/SharedFilesPage';
import SettingsPage from './pages/settings/SettingsPage';

function App() {
  const [activePage, setActivePage] = useState('chat');

  const renderPage = () => {
    switch (activePage) {
      case 'chat':
        return <UnifiedChatPage />;
      case 'prompts':
        return <SystemPromptPage />;
      case 'dags':
        return <DagsPage />;
      case 'skills':
        return <SkillsPage />;
      case 'memory-center':
        return <MemoryCenterPage />;
      case 'memory':
        return <SharedFilesPage />;
      case 'settings':
        return <SettingsPage />;
      default:
        return <UnifiedChatPage />;
    }
  };

  return (
    <AppShell activePage={activePage} setActivePage={setActivePage}>
      {renderPage()}
    </AppShell>
  );
}

export default App;
