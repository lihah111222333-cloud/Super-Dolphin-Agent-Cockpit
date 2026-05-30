import React, { useEffect } from 'react';
import { usePreferenceStore } from '../../entities/preference/model/usePreferenceStore';
import { useProjectStore } from '../../entities/project/model/useProjectStore';
import { useThreadStore } from '../../entities/thread/model/useThreadStore';
import { useLogStore } from '../../entities/log/model/useLogStore';
import { readConfig } from '../../shared/api/backendApi';
import {
  MessageSquare,
  FileText,
  Workflow,
  Sparkles,
  Brain,
  FolderClosed,
  Settings,
  Plus,
  Moon,
  Sun
} from 'lucide-react';

export default function AppShell({ activePage, setActivePage, children }) {
  const { theme, setTheme, initialize: initPrefs } = usePreferenceStore();
  const {
    projects,
    active: activeProject,
    showModal,
    modalPath,
    browsing,
    setActive,
    openModal,
    closeModal,
    confirmModal,
    browseDirectory,
    setScopeCwd,
    reloadProjects
  } = useProjectStore();

  const { initialize: initThreads, refreshSidebarState } = useThreadStore();

  useEffect(() => {
    let cancelled = false;

    const bootstrap = async () => {
      initPrefs();
      initThreads();

      const config = await readConfig();
      const cwd = (config?.cwd || '').toString().trim();
      if (!cwd || cwd === '.') {
        throw new Error('app bootstrap cwd is required');
      }
      if (cancelled) return;

      setScopeCwd(cwd);
      await reloadProjects();
      await refreshSidebarState(cwd);
    };

    bootstrap().catch((error) => {
      useLogStore.getState().error('app.bootstrap.failed', { error: error.message });
    });

    return () => {
      cancelled = true;
    };
  }, [initPrefs, initThreads, refreshSidebarState, reloadProjects, setScopeCwd]);

  const navItems = [
    { key: 'chat', icon: MessageSquare, label: '聊天' },
    { key: 'prompts', icon: FileText, label: '工作台' }, // maps to SystemPromptPage/Workspace
    { key: 'dags', icon: Workflow, label: '任务管理' },
    { key: 'skills', icon: Sparkles, label: '技能' },
    { key: 'memory-center', icon: Brain, label: '记忆中心' },
    { key: 'memory', icon: FolderClosed, label: '共享文件' },
    { key: 'settings', icon: Settings, label: '设置' },
  ];

  return (
    <div className={`flex h-screen w-screen overflow-hidden text-sd-text-primary ${theme === 'light' ? 'theme-marble' : 'theme-granite'}`}>

      {/* 1. Left Nav Rail */}
      <aside className="w-16 flex flex-col items-center justify-between py-4 bg-sd-surface/50 border-r border-sd-border/40 backdrop-blur-lg select-none z-10">
        <div className="flex flex-col items-center gap-6 w-full">
          {/* Logo */}
          <div className="w-9 h-9 rounded-xl bg-sd-accent/15 border border-sd-accent/30 flex items-center justify-center text-sd-accent hover:scale-105 transition-all duration-300">
            <span className="font-bold text-sm">SD</span>
          </div>

          {/* Nav Icons */}
          <nav className="flex flex-col items-center gap-3 w-full">
            {navItems.map((item) => {
              const Icon = item.icon;
              const isActive = activePage === item.key;
              return (
                <button
                  key={item.key}
                  onClick={() => setActivePage(item.key)}
                  className={`relative group w-11 h-11 flex flex-col items-center justify-center rounded-xl transition-premium ${
                    isActive
                      ? 'bg-sd-accent/10 border border-sd-accent/30 text-sd-accent'
                      : 'text-sd-text-secondary border border-transparent hover:bg-sd-border/30 hover:text-sd-text-primary'
                  }`}
                  aria-label={item.label}
                >
                  <Icon size={18} className={isActive ? 'scale-105' : ''} />
                  <span className="text-[9px] mt-0.5 scale-90 font-medium">{item.label}</span>

                  {/* Tooltip */}
                  <div className="absolute left-14 scale-0 group-hover:scale-100 transition-all duration-200 z-50 bg-sd-surface-raised border border-sd-border text-xs px-2.5 py-1.5 rounded shadow-lg whitespace-nowrap pointer-events-none">
                    {item.label}
                  </div>
                </button>
              );
            })}
          </nav>
        </div>

        {/* Profile Avatar */}
        <div className="w-9 h-9 rounded-full overflow-hidden border border-sd-border/60 hover:scale-105 transition-all duration-300">
          <img src="https://images.unsplash.com/photo-1535713875002-d1d0cf377fde?auto=format&fit=crop&w=80&h=80" alt="Avatar" className="w-full h-full object-cover" />
        </div>
      </aside>

      {/* Main Content Pane */}
      <div className="flex-1 flex flex-col overflow-hidden relative">

        {/* 2. Top Header Bar */}
        <header className="h-12 border-b border-sd-border/40 bg-sd-surface/30 backdrop-blur-md flex items-center justify-between px-4 select-none z-10">
          <div className="flex items-center gap-4">
            {/* macOS window controls style */}
            <div className="flex items-center gap-1.5 mr-2">
              <span className="w-3 h-3 rounded-full bg-red-500/80 border border-red-600/30"></span>
              <span className="w-3 h-3 rounded-full bg-yellow-500/80 border border-yellow-600/30"></span>
              <span className="w-3 h-3 rounded-full bg-green-500/80 border border-green-600/30"></span>
            </div>

            {/* App title */}
            <span className="font-semibold text-sm tracking-wider text-sd-text-primary">Super Agent</span>

            {/* Scope / Project CWD Selector */}
            <div className="flex items-center gap-1.5 border border-sd-border/60 bg-sd-surface/50 rounded-lg px-2.5 py-1 text-xs text-sd-text-secondary hover:border-sd-accent/50 transition-premium">
              <span className="font-mono text-sd-text-muted">project/</span>
              <select
                value={activeProject}
                onChange={(e) => setActive(e.target.value)}
                className="bg-transparent border-none outline-none font-medium text-sd-text-primary pr-1 cursor-pointer"
              >
                <option value=".">当前目录 (.)</option>
                {projects.map((p) => (
                  <option key={p} value={p}>
                    {p.split('/').pop() || p}
                  </option>
                ))}
              </select>
              <button
                onClick={() => openModal()}
                className="hover:text-sd-accent transition-premium ml-1 cursor-pointer"
                title="选择新项目"
              >
                <Plus size={13} />
              </button>
            </div>
          </div>

          <div className="flex items-center gap-4">
            {/* Day / Night Theme Toggles */}
            <div className="flex items-center gap-0.5 bg-sd-surface/60 border border-sd-border/65 rounded-lg p-0.5 text-[11px]">
              <button
                onClick={() => setTheme('dark')}
                className={`flex items-center gap-1 px-2.5 py-1 rounded-md transition-premium ${
                  theme === 'dark'
                    ? 'bg-sd-accent/15 text-sd-accent font-semibold'
                    : 'text-sd-text-secondary hover:text-sd-text-primary'
                }`}
              >
                <Moon size={11} />
                <span>夜间</span>
              </button>
              <button
                onClick={() => setTheme('light')}
                className={`flex items-center gap-1 px-2.5 py-1 rounded-md transition-premium ${
                  theme === 'light'
                    ? 'bg-sd-accent/15 text-sd-accent font-semibold'
                    : 'text-sd-text-secondary hover:text-sd-text-primary'
                }`}
              >
                <Sun size={11} />
                <span>白天</span>
              </button>
            </div>

            {/* Status light */}
            <div className="flex items-center gap-1.5 text-xs text-sd-text-secondary bg-sd-surface/50 border border-sd-border/40 px-2 py-1 rounded-lg">
              <span className="relative flex h-2 w-2">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-sd-accent opacity-75"></span>
                <span className="relative inline-flex rounded-full h-2 w-2 bg-sd-accent"></span>
              </span>
              <span>系统状态</span>
              <span className="text-sd-accent font-semibold font-mono">· 优秀</span>
            </div>
          </div>
        </header>

        {/* 3. Main Workspace Area */}
        <div className="flex-1 overflow-hidden relative">
          {children}
        </div>

      </div>

      {/* 4. Project Chooser Modal */}
      {showModal && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[100] animate-fade-in">
          <div className="glass-panel-raised w-full max-w-md p-6 bg-sd-surface-raised flex flex-col gap-4 animate-scale-up select-none">
            <div className="flex justify-between items-center border-b border-sd-border pb-3">
              <h3 className="text-md font-semibold text-sd-text-primary">选择项目目录</h3>
              <button
                onClick={closeModal}
                className="text-sd-text-secondary hover:text-sd-text-primary transition-premium cursor-pointer"
              >
                ✕
              </button>
            </div>

            <div className="flex flex-col gap-2">
              <label className="text-xs text-sd-text-secondary">项目绝对路径</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={modalPath}
                  onChange={(e) => setScopeCwd(e.target.value)}
                  className="flex-1 bg-sd-bg/60 border border-sd-border rounded px-3 py-2 text-sm outline-none font-mono focus:border-sd-accent/80 text-sd-text-primary"
                  placeholder="/abs/path/to/project"
                />
                <button
                  onClick={browseDirectory}
                  disabled={browsing}
                  className="px-3 bg-sd-border/40 hover:bg-sd-border border border-sd-border text-xs rounded text-sd-text-primary transition-premium cursor-pointer disabled:opacity-50"
                >
                  {browsing ? '浏览中...' : '浏览'}
                </button>
              </div>
            </div>

            <div className="flex justify-end gap-2 border-t border-sd-border pt-4 mt-2">
              <button
                onClick={closeModal}
                className="px-4 py-2 border border-sd-border hover:bg-sd-border/30 rounded text-xs text-sd-text-secondary transition-premium cursor-pointer"
              >
                取消
              </button>
              <button
                onClick={confirmModal}
                className="px-4 py-2 bg-sd-accent hover:bg-sd-accent-hover text-white text-xs font-semibold rounded transition-premium cursor-pointer shadow-md"
              >
                确认导入
              </button>
            </div>
          </div>
        </div>
      )}

    </div>
  );
}
