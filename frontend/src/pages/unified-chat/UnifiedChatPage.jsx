import React, { useState, useEffect, useRef } from 'react';
import { useThreadStore } from '../../entities/thread/model/useThreadStore';
import { useProjectStore } from '../../entities/project/model/useProjectStore';
import { useLogStore } from '../../entities/log/model/useLogStore';
import {
  Plus,
  Search,
  Send,
  Paperclip,
  Archive,
  Pin,
  AlertTriangle,
  FileCode,
  Gauge,
  X,
  RefreshCw,
  Zap,
  Terminal,
  Activity,
  Layers,
  Sparkles,
  Info
} from 'lucide-react';
import MarkdownIt from 'markdown-it';
import BayesCard from '../../widgets/bayes-card/BayesCard';
import SystemMonitor from '../../widgets/system-monitor/SystemMonitor';
import { selectFiles } from '../../shared/api/backendApi';

const md = new MarkdownIt({ html: true, linkify: true });

function providerFromModel(model) {
  const value = (model || '').toString().trim().toLowerCase();
  if (value.includes('claude') || value.includes('opus')) return 'claude';
  return 'codex';
}

function isThreadSendable(thread, status) {
  if (!thread) return false;
  const healthText = [
    thread.lastMessage,
    thread.last_message,
    thread.lastReport,
    thread.last_report,
    thread.error,
    thread.health,
    thread.agentState,
    thread.state,
    status,
  ].filter(Boolean).join(' ').toLowerCase();

  return ![
    'health-failure',
    'transport not running',
    'websocket not connected',
    'max recovery attempts',
  ].some((needle) => healthText.includes(needle));
}

function clampRightPanelWidth(width) {
  return Math.min(680, Math.max(320, width));
}

function shouldRenderTimelineItem(item) {
  const kind = (item?.kind || '').toString().trim().toLowerCase();
  const text = (item?.text || item?.content || '').toString().trim();
  return kind === 'user' || kind === 'assistant' || Boolean(text);
}

function messageText(item) {
  return (item?.text || item?.content || '').toString();
}

function formatMessageTime(value) {
  const date = new Date(value || '');
  if (!Number.isFinite(date.getTime())) return '';
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export default function UnifiedChatPage() {
  const {
    threads,
    statuses,
    timelinesByThread,
    tokenUsageByThread,
    activeThreadId,
    setActiveThread,
    refreshSidebarState,
    startThread,
    sendMessage,
    interruptTurn,
    compactThread,
    recoverThread,
    setThreadPinned,
    setThreadArchived
  } = useThreadStore();

  const { requireActionCwd, active: projectActivePath, scopeCwd } = useProjectStore();
  const { entries: logEntries } = useLogStore();

  const [prompt, setPrompt] = useState('');
  const [attachments, setAttachments] = useState([]);
  const [searchQuery, setSearchQuery] = useState('');
  const [filterMode, setFilterMode] = useState('all'); // all, pinned, archived
  const [activeTab, setActiveTab] = useState('workspace'); // workspace, preview
  const [selectedModel, setSelectedModel] = useState('Codex');
  const [selectedEffort, setSelectedEffort] = useState('max');
  const [rightPanelWidth, setRightPanelWidth] = useState(384);

  const chatEndRef = useRef(null);
  const composerInputRef = useRef(null);
  const rightResizeRef = useRef(null);

  useEffect(() => {
    return () => {
      const resize = rightResizeRef.current;
      if (resize) {
        window.removeEventListener('pointermove', resize.handleMove);
        window.removeEventListener('pointerup', resize.handleUp);
        window.removeEventListener('mousemove', resize.handleMove);
        window.removeEventListener('mouseup', resize.handleUp);
      }
    };
  }, []);

  // Auto-scroll timeline on new stream logs
  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [timelinesByThread, activeThreadId]);

  // Sync state on change
  useEffect(() => {
    try {
      refreshSidebarState(requireActionCwd('refresh_sidebar'));
    } catch (err) {
      useLogStore.getState().warn('chat.sidebar_refresh.skipped', { error: err.message });
    }
  }, [projectActivePath, scopeCwd, refreshSidebarState, requireActionCwd]);

  const handleSend = async () => {
    if (!prompt.trim() && attachments.length === 0) return;
    const textToSend = prompt;
    const attachmentsToSend = attachments;
    try {
      const cwd = requireActionCwd('send_message');
      let currentId = activeThreadId;
      const activeCandidate = threads.find((t) => t.id === currentId);

      // Auto-start thread if none is selected or the persisted active thread is a dead runtime.
      if (!currentId || (activeCandidate && !isThreadSendable(activeCandidate, statuses[currentId]))) {
        if (currentId && activeCandidate) {
          useLogStore.getState().warn('chat.active_thread.unsendable_start_new', {
            threadId: currentId,
            status: statuses[currentId] || '',
            lastMessage: activeCandidate?.lastMessage || activeCandidate?.last_message || '',
          });
        }
        currentId = await startThread(cwd, {
          name: textToSend.slice(0, 20) || '新对话',
          provider: providerFromModel(selectedModel),
        });
      }

      if (currentId) {
        setPrompt('');
        setAttachments([]);
        try {
          await sendMessage(currentId, textToSend, cwd, attachmentsToSend);
        } catch (err) {
          setPrompt(textToSend);
          setAttachments(attachmentsToSend);
          throw err;
        }
      }
    } catch (err) {
      useLogStore.getState().error('chat.send.failed', { error: err.message });
    }
  };

  const handleCreateNewThread = async () => {
    try {
      const cwd = requireActionCwd('create_thread');
      await startThread(cwd, {
        name: '新对话',
        provider: providerFromModel(selectedModel),
      });
    } catch (err) {
      useLogStore.getState().error('chat.create_thread.failed', { error: err.message });
    }
  };

  const handlePickAttachments = async () => {
    try {
      const picked = await selectFiles();
      if (picked && picked.length > 0) {
        setAttachments((prev) => [...prev, ...picked]);
      }
    } catch (err) {
      useLogStore.getState().error('chat.attachments.failed', { error: err.message });
    }
  };

  const handleKeyPress = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const runThreadAction = (reason, action) => {
    if (!activeThreadId) return;
    try {
      action(requireActionCwd(reason));
    } catch (err) {
      useLogStore.getState().error(`chat.${reason}.failed`, { error: err.message });
    }
  };

  const runThreadCardAction = (reason, threadId, action) => {
    if (!threadId) return;
    try {
      action(requireActionCwd(reason));
    } catch (err) {
      useLogStore.getState().error(`chat.${reason}.failed`, { threadId, error: err.message });
    }
  };

  const handleSelectThread = (threadId) => {
    try {
      setActiveThread(threadId, requireActionCwd('select_thread'));
    } catch (err) {
      useLogStore.getState().error('chat.select_thread.failed', { threadId, error: err.message });
    }
  };

  const handleRightPanelResizeStart = (event) => {
    event.preventDefault();
    if (rightResizeRef.current) return;
    const startX = event.clientX;
    const startWidth = rightPanelWidth;

    const handleMove = (moveEvent) => {
      const delta = startX - moveEvent.clientX;
      setRightPanelWidth(clampRightPanelWidth(startWidth + delta));
    };

    const handleUp = () => {
      window.removeEventListener('pointermove', handleMove);
      window.removeEventListener('pointerup', handleUp);
      window.removeEventListener('mousemove', handleMove);
      window.removeEventListener('mouseup', handleUp);
      rightResizeRef.current = null;
    };

    rightResizeRef.current = { handleMove, handleUp };
    window.addEventListener('pointermove', handleMove);
    window.addEventListener('pointerup', handleUp);
    window.addEventListener('mousemove', handleMove);
    window.addEventListener('mouseup', handleUp);
  };

  // Timeline render helpers
  const renderMessageContent = (text) => {
    try {
      return { __html: md.render(text || '') };
    } catch {
      return { __html: text || '' };
    }
  };

  const filteredThreads = threads.filter((t) => {
    const threadName = (t.name || t.title || t.threadId || t.id || '新对话').toString();
    const matchesSearch = threadName.toLowerCase().includes(searchQuery.toLowerCase());
    if (filterMode === 'pinned') return matchesSearch && t.pinned;
    if (filterMode === 'archived') return matchesSearch && t.archived;
    return matchesSearch && !t.archived;
  });

  const activeThread = threads.find((t) => t.id === activeThreadId);
  const timeline = (timelinesByThread[activeThreadId] || []).filter(shouldRenderTimelineItem);
  const tokenUsage = tokenUsageByThread[activeThreadId] || { usedTokens: 0, contextWindowTokens: 128000, usedPercent: 0 };
  const currentStatus = statuses[activeThreadId] || 'idle';

  return (
    <div className="flex h-full w-full overflow-hidden relative" data-testid="chat-page">

      {/* 1. Left Sidebar: Resizable Thread Rail */}
      <div className="w-80 flex flex-col bg-sd-surface/20 border-r border-sd-border/40 backdrop-blur-md select-none" data-testid="thread-rail">

        {/* Rail Top Action */}
        <div className="p-3 border-b border-sd-border/40 flex justify-between items-center gap-2">
          <select
            value={filterMode}
            onChange={(e) => setFilterMode(e.target.value)}
            className="flex-1 bg-sd-bg/60 border border-sd-border/50 rounded-lg px-2.5 py-1.5 text-xs text-sd-text-primary font-medium focus:border-sd-accent/80 outline-none cursor-pointer"
          >
            <option value="all">全部对话</option>
            <option value="pinned">已置顶</option>
            <option value="archived">已归档</option>
          </select>
          <button
            onClick={handleCreateNewThread}
            className="flex items-center gap-1 bg-sd-accent/15 hover:bg-sd-accent/25 border border-sd-accent/30 text-sd-accent font-semibold px-3 py-1.5 rounded-lg text-xs transition-premium cursor-pointer"
          >
            <Plus size={14} />
            <span>新建</span>
          </button>
        </div>

        {/* Search */}
        <div className="p-3 border-b border-sd-border/30 relative">
          <Search size={14} className="absolute left-6 top-1/2 -translate-y-1/2 text-sd-text-muted" />
          <input
            type="text"
            placeholder="搜索对话列表..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="w-full bg-sd-bg/40 border border-sd-border/50 rounded-lg pl-9 pr-3 py-1.5 text-xs outline-none text-sd-text-primary focus:border-sd-accent/80"
          />
        </div>

        {/* Thread List */}
        <div className="flex-1 overflow-y-auto p-2 flex flex-col gap-1.5" data-testid="thread-list">
          {filteredThreads.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-12 text-sd-text-muted gap-2" data-testid="thread-empty-state">
              <Sparkles size={24} className="opacity-40 animate-pulse-slow" />
              <span className="text-xs">暂无符合条件的对话</span>
            </div>
          ) : (
            filteredThreads.map((t) => {
              const isActive = t.id === activeThreadId;
              const isPinned = t.pinned;
              return (
                <div
                  key={t.id}
                  onClick={() => handleSelectThread(t.id)}
                  className={`glass-panel p-3 flex flex-col gap-1 cursor-pointer transition-premium hover-glow ${
                    isActive ? 'active-glow border-sd-accent/60 bg-sd-accent/5' : 'bg-sd-surface/30'
                  }`}
                >
                  <div className="flex justify-between items-start gap-1">
                    <span className="text-xs font-semibold truncate text-sd-text-primary flex-1">
                      {t.name || t.title || t.threadId || t.id || '新对话'}
                    </span>
                    <div className="flex items-center gap-1.5 shrink-0">
                      <span className="text-[10px] scale-95 font-bold px-1.5 py-0.5 rounded bg-sd-border text-sd-text-secondary">
                        {(t.agentKey || t.provider || t.agent_key || 'codex').toString().toUpperCase()}
                      </span>
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          runThreadCardAction('pin_thread', t.id, (cwd) => setThreadPinned(t.id, !isPinned, cwd));
                        }}
                        className={`text-sd-text-muted hover:text-sd-accent transition-premium cursor-pointer`}
                        title={isPinned ? '取消置顶' : '置顶对话'}
                        data-testid={`thread-pin-${t.id}`}
                      >
                        <Pin size={10} className={isPinned ? 'fill-sd-accent text-sd-accent' : ''} />
                      </button>
                    </div>
                  </div>
                  <p className="text-[10px] text-sd-text-secondary truncate mt-0.5">
                    {statuses[t.id] === 'streaming' ? '正在输出中...' : '期待指示'}
                  </p>
                  <div className="flex justify-between items-center text-[9px] text-sd-text-muted mt-1.5">
                    <span>{new Date(t.createdAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        runThreadCardAction('archive_thread', t.id, (cwd) => setThreadArchived(t.id, !t.archived, cwd));
                      }}
                      className="hover:text-sd-accent transition-premium cursor-pointer"
                      title={t.archived ? "取消归档" : "归档对话"}
                      data-testid={`thread-archive-${t.id}`}
                    >
                      <Archive size={10} />
                    </button>
                  </div>
                </div>
              );
            })
          )}
        </div>
      </div>

      {/* 2. Main Chat Center Panel */}
      <div className="flex-1 flex flex-col bg-sd-bg/15 relative overflow-hidden">

        {/* Workspace Toolbar Header */}
        <div className="h-12 border-b border-sd-border/40 bg-sd-surface/20 backdrop-blur-md flex items-center justify-between px-4 z-10" data-testid="chat-toolbar">
          <div className="flex items-center gap-3">
            <h2 className="text-xs font-semibold text-sd-text-primary truncate max-w-xs sm:max-w-md">
              {activeThread ? activeThread.name : '新对话 workspace'}
            </h2>
            <div className="flex items-center gap-1">
              <span className={`w-2 h-2 rounded-full ${
                currentStatus === 'streaming' ? 'bg-sd-accent animate-pulse' : 'bg-sd-text-muted'
              }`}></span>
              <span className="text-[10px] text-sd-text-muted font-mono">{currentStatus}</span>
            </div>
          </div>

          <div className="flex items-center gap-3">
            {/* Model switch */}
            <div className="flex items-center gap-1.5 text-xs">
              <span className="text-sd-text-muted text-[10px]">模型</span>
              <select
                value={selectedModel}
                onChange={(e) => setSelectedModel(e.target.value)}
                className="bg-sd-surface/60 border border-sd-border/50 rounded px-2 py-0.5 text-xs text-sd-text-primary font-medium focus:border-sd-accent outline-none cursor-pointer"
                data-testid="thread-config-model-select"
              >
                <option value="Codex">Codex</option>
                <option value="Claude">Claude</option>
                <option value="Opus">Opus 4.7</option>
              </select>
            </div>

            {/* Effort selector */}
            <div className="flex items-center gap-1.5 text-xs">
              <span className="text-sd-text-muted text-[10px]">思考</span>
              <select
                value={selectedEffort}
                onChange={(e) => setSelectedEffort(e.target.value)}
                className="bg-sd-surface/60 border border-sd-border/50 rounded px-2 py-0.5 text-xs text-sd-text-primary font-medium focus:border-sd-accent outline-none cursor-pointer"
                data-testid="thread-config-effort-select"
              >
                <option value="low">低</option>
                <option value="medium">中</option>
                <option value="max">高 (Max)</option>
              </select>
            </div>

            {/* Action buttons */}
            <div className="flex items-center gap-1 border-l border-sd-border/40 pl-3">
              {currentStatus === 'streaming' ? (
                <button
                  type="button"
                  disabled={!activeThreadId}
                  onClick={() => runThreadAction('interrupt_thread', (cwd) => interruptTurn(activeThreadId, cwd))}
                  className="p-1 text-sd-danger hover:bg-sd-danger/10 rounded transition-premium cursor-pointer"
                  title="中断回合"
                >
                  <X size={14} />
                </button>
              ) : (
                <>
                  <button
                    type="button"
                    disabled={!activeThreadId}
                    onClick={() => runThreadAction('compact_thread', (cwd) => compactThread(activeThreadId, cwd))}
                    className="p-1.5 text-sd-text-secondary hover:text-sd-accent hover:bg-sd-surface rounded transition-premium cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                    title="压缩上下文"
                    data-testid="composer-compact-button"
                  >
                    <Layers size={13} />
                  </button>
                  <button
                    type="button"
                    disabled={!activeThreadId}
                    onClick={() => runThreadAction('recover_thread', (cwd) => recoverThread(activeThreadId, cwd))}
                    className="p-1.5 text-sd-text-secondary hover:text-sd-accent hover:bg-sd-surface rounded transition-premium cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                    title="恢复线程"
                  >
                    <RefreshCw size={13} />
                  </button>
                </>
              )}
            </div>
          </div>
        </div>

        {/* Message Log Timeline */}
        <div className="flex-1 overflow-y-auto p-4 flex flex-col gap-4 relative">
          {timeline.length === 0 ? (
            <div className="flex-1 flex flex-col items-center justify-center text-sd-text-muted gap-3 select-none" data-testid="chat-empty-state">
              <Zap size={32} className="opacity-30 text-sd-accent" />
              <div className="text-center">
                <p className="text-xs font-semibold text-sd-text-primary">Super Dolphin Workspace</p>
                <p className="text-[10px] text-sd-text-muted mt-1">在下方输入框中向 Agent 提问或指派开发任务</p>
              </div>
            </div>
          ) : (
            timeline.map((msg, index) => {
              const isUser = msg.kind === 'user';
              const text = messageText(msg);
              const timeLabel = formatMessageTime(msg.ts || msg.createdAt);
              return (
                <div
                  key={msg.id || index}
                  className={`flex gap-3 max-w-[85%] ${isUser ? 'self-end flex-row-reverse' : 'self-start'}`}
                >
                  {/* Avatar */}
                  <div className={`w-8 h-8 rounded-lg flex items-center justify-center shrink-0 border select-none ${
                    isUser
                      ? 'bg-sd-accent/10 border-sd-accent/30 text-sd-accent'
                      : 'bg-sd-surface-raised border-sd-border text-sd-text-primary'
                  }`}>
                    {isUser ? 'U' : 'AI'}
                  </div>

                  {/* Speech bubble */}
                  <div className={`flex flex-col gap-1`}>
                    <div className={`glass-panel px-4 py-3 rounded-2xl shadow-sm text-xs leading-relaxed ${
                      isUser
                        ? 'bg-sd-accent/5 border-sd-accent/20 rounded-tr-none'
                        : 'bg-sd-surface/65 rounded-tl-none text-sd-text-primary'
                    }`}>
                      {isUser ? (
                        <p className="whitespace-pre-wrap">{text}</p>
                      ) : (
                        <div
                          className="prose prose-sm dark:prose-invert max-w-none text-sd-text-primary break-words"
                          dangerouslySetInnerHTML={renderMessageContent(text)}
                        />
                      )}
                    </div>
                    {/* Timestamp */}
                    {timeLabel && (
                      <span className={`text-[9px] text-sd-text-muted px-1 mt-0.5 ${isUser ? 'self-end' : 'self-start'}`}>
                        {timeLabel}
                      </span>
                    )}
                  </div>
                </div>
              );
            })
          )}
          <div ref={chatEndRef} />
        </div>

        {/* Composer bottom Dock */}
        <div className="p-4 border-t border-sd-border/40 bg-sd-surface/20 backdrop-blur-md flex flex-col gap-2 z-10" data-testid="composer-bar">

          {/* Attachments preview row */}
          {attachments.length > 0 && (
            <div className="flex flex-wrap gap-2 pb-2">
              {attachments.map((file, idx) => (
                <div key={idx} className="flex items-center gap-1.5 px-2.5 py-1 rounded bg-sd-surface border border-sd-border text-[11px] font-mono text-sd-text-secondary">
                  <FileCode size={11} className="text-sd-accent" />
                  <span className="truncate max-w-xs">{file.split('/').pop()}</span>
                  <button
                    onClick={() => setAttachments((prev) => prev.filter((_, i) => i !== idx))}
                    className="text-sd-text-muted hover:text-sd-danger transition-premium cursor-pointer ml-1"
                  >
                    <X size={10} />
                  </button>
                </div>
              ))}
            </div>
          )}

          {/* Prompt Editor */}
          <div className="flex gap-2 items-end">
            <button
              onClick={handlePickAttachments}
              className="p-2.5 bg-sd-surface/60 hover:bg-sd-border/50 border border-sd-border rounded-xl text-sd-text-secondary hover:text-sd-text-primary transition-premium cursor-pointer shadow-sm"
              title="添加文件附件"
              data-testid="composer-attach-button"
            >
              <Paperclip size={16} />
            </button>
            <textarea
              ref={composerInputRef}
              rows="1"
              placeholder="输入给 Agent 的内容，Enter 发送，Shift+Enter 换行..."
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              onKeyDown={handleKeyPress}
              className="flex-1 bg-sd-bg/60 border border-sd-border/50 focus:border-sd-accent/80 rounded-xl px-4 py-2.5 text-xs outline-none text-sd-text-primary resize-none min-h-[38px] max-h-48 overflow-y-auto"
              data-testid="composer-input"
            />
            <button
              onClick={handleSend}
              className="p-2.5 bg-sd-accent hover:bg-sd-accent-hover text-white rounded-xl transition-premium cursor-pointer shadow-md"
              title="发送"
              data-testid="composer-send-button"
            >
              <Send size={15} />
            </button>
          </div>
        </div>

      </div>

      {/* 3. Right Sidebar Panel: Resizable Inspector */}
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="调整工作台和运行日志宽度"
        data-testid="right-panel-resizer"
        onPointerDown={handleRightPanelResizeStart}
        onMouseDown={handleRightPanelResizeStart}
        className="w-1.5 shrink-0 cursor-col-resize bg-sd-border/25 hover:bg-sd-accent/50 transition-premium"
      />
      <div
        className="shrink-0 flex flex-col bg-sd-surface/20 border-l border-sd-border/40 backdrop-blur-md overflow-hidden select-none"
        style={{ width: `${rightPanelWidth}px` }}
        data-testid="right-inspector-panel"
      >

        {/* Navigation Tabs */}
        <div className="h-12 border-b border-sd-border/40 bg-sd-surface/30 flex items-center px-2 select-none">
          <div className="flex bg-sd-bg/50 border border-sd-border/50 rounded-lg p-0.5 text-xs w-full">
            <button
              onClick={() => setActiveTab('workspace')}
              className={`flex-1 flex items-center justify-center gap-1.5 py-1 rounded-md transition-premium font-medium ${
                activeTab === 'workspace'
                  ? 'bg-sd-accent/10 border border-sd-accent/20 text-sd-accent'
                  : 'text-sd-text-secondary hover:text-sd-text-primary'
              }`}
            >
              <Activity size={12} />
              <span>工作台</span>
            </button>
            <button
              onClick={() => setActiveTab('preview')}
              className={`flex-1 flex items-center justify-center gap-1.5 py-1 rounded-md transition-premium font-medium ${
                activeTab === 'preview'
                  ? 'bg-sd-accent/10 border border-sd-accent/20 text-sd-accent'
                  : 'text-sd-text-secondary hover:text-sd-text-primary'
              }`}
            >
              <Terminal size={12} />
              <span>运行日志</span>
            </button>
          </div>
        </div>

        {/* Tab contents */}
        <div className="flex-1 overflow-y-auto p-4 flex flex-col gap-4">
          {activeTab === 'workspace' ? (
            <>
              {/* Bayes Reasoning Visualizer Widget */}
              <BayesCard />

              {/* Context Usage Banner Gauge */}
              <div className="glass-panel p-4 flex flex-col gap-2 relative overflow-hidden transition-premium hover-glow" data-testid="context-usage-banner">
                <div className="flex justify-between items-center text-xs">
                  <span className="font-semibold text-sd-text-primary flex items-center gap-1">
                    <Gauge size={13} className="text-sd-accent" />
                    <span>上下文 Token 用量</span>
                  </span>
                  <span className="font-mono text-sd-text-secondary">
                    {Math.round(tokenUsage.usedPercent)}%
                  </span>
                </div>
                <div className="h-2 rounded-full bg-sd-border overflow-hidden">
                  <div
                    className={`h-full transition-premium ${
                      tokenUsage.usedPercent > 80
                        ? 'bg-sd-danger animate-pulse'
                        : tokenUsage.usedPercent > 50
                        ? 'bg-sd-warning'
                        : 'bg-sd-accent'
                    }`}
                    style={{ width: `${tokenUsage.usedPercent}%` }}
                  ></div>
                </div>
                <div className="flex justify-between text-[10px] text-sd-text-muted font-mono">
                  <span>已用 {tokenUsage.usedTokens}</span>
                  <span>总额 {tokenUsage.contextWindowTokens}</span>
                </div>
              </div>

              {/* Empty placeholder widget if needed */}
              <div className="glass-panel p-4 flex items-center gap-3 border border-sd-border/40 bg-sd-surface/30">
                <Info size={16} className="text-sd-accent shrink-0" />
                <p className="text-[10px] leading-normal text-sd-text-secondary">
                  当前处于 Wails 调试沙盒连接模式。所有操作都与后台 Go 守护进程保持直接通信。
                </p>
              </div>
            </>
          ) : (
            /* Warning/Error trace logs row lists */
            <div className="flex flex-col gap-2.5 h-full">
              <div className="flex justify-between items-center border-b border-sd-border pb-2">
                <span className="text-[11px] font-semibold text-sd-text-primary flex items-center gap-1.5">
                  <AlertTriangle size={13} className="text-sd-warning" />
                  <span>实时异常 & 请求监控</span>
                </span>
                <span className="text-[10px] font-mono bg-sd-border/40 px-2 py-0.5 rounded text-sd-text-secondary">
                  {logEntries.filter(e => e.level !== 'info').length} 条
                </span>
              </div>
              <div className="flex-1 overflow-y-auto flex flex-col gap-2 pr-1 font-mono text-[10px]">
                {logEntries.filter(e => e.level !== 'info').length === 0 ? (
                  <div className="text-center text-sd-text-muted py-8 select-none">
                    暂无请求警告或异常
                  </div>
                ) : (
                  logEntries
                    .filter(e => e.level !== 'info')
                    .slice()
                    .reverse()
                    .map((log, idx) => (
                      <div
                        key={idx}
                        className={`p-2.5 rounded border flex flex-col gap-1 leading-normal ${
                          log.level === 'error'
                            ? 'bg-sd-danger/5 border-sd-danger/25 text-sd-danger'
                            : 'bg-sd-warning/5 border-sd-warning/25 text-sd-warning'
                        }`}
                      >
                        <div className="flex justify-between font-bold">
                          <span>{log.event}</span>
                          <span>{new Date(log.timestamp).toLocaleTimeString()}</span>
                        </div>
                        <p className="break-all whitespace-pre-wrap">{JSON.stringify(log.fields, null, 2)}</p>
                      </div>
                    ))
                )}
              </div>
            </div>
          )}
        </div>

        {/* Right Sidebar Bottom resource indicator */}
        <SystemMonitor />
      </div>

    </div>
  );
}
