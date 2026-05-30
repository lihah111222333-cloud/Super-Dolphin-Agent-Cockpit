import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  Archive,
  Bot,
  Boxes,
  Brain,
  ChevronDown,
  CircleDot,
  Code2,
  Copy,
  File,
  FileText,
  Folder,
  FolderOpen,
  GitBranch,
  Image,
  Link2,
  MemoryStick,
  MessageCircle,
  MoreHorizontal,
  Paperclip,
  Plus,
  Search,
  Send,
  Settings,
  Sparkles,
  Workflow,
} from 'lucide-react';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { getBuildInfo, getPreference, setPreference } from './shared/api/backendApi.js';

const navItems = [
  { id: 'chat', label: 'Chat', icon: MessageCircle },
  { id: 'prompts', label: '提示词', icon: FileText },
  { id: 'workflows', label: '任务流程', icon: Workflow },
  { id: 'skills', label: '技能', icon: Sparkles },
  { id: 'memory', label: '记忆中心', icon: Brain, alert: true },
  { id: 'files', label: '共享文件', icon: FolderOpen },
  { id: 'settings', label: '设置', icon: MoreHorizontal },
];

const skills = [
  ['Agent工程学', '当你需要管理多代理工程流程、将复杂实现拆分为可验证的子任务，或制定评估优先和成本敏感的执行策略时使用。', ['agentic-engineering', 'agent', 'workflow', 'subagent']],
  ['MCP协议', '当你需要在 Go 后端构建、扩展或调试 MCP Server，添加工具或资源，或配置 stdio/HTTP 传输时使用。', ['mcp-server-patterns', 'MCP', 'Model', 'Context']],
  ['ui-ux-design', '当你需要设计品牌视觉、界面样式、设计系统、Logo、图标、演示文稿、横幅或社交媒体图片时使用。', ['design', 'logo', 'CIP', 'mockup']],
  ['vue3', 'Vue 3 核心技能（适配 V3 无构建 ESM 架构）。用于 Vue3 基础语法、响应式状态管理、组件设计。', ['vue', 'buildless', 'esm']],
  ['使用git工作区', '开始需要与当前工作区隔离的功能工作，或执行实现计划前使用；通过智能目录选择创建隔离 worktree。', ['git', 'worktree', 'isolation']],
  ['后端', '完整的 Go 后端开发指南，涵盖 Effective Go 最佳实践、V3 架构契约。', ['Go后端', 'golang', 'backend']],
];

const sharedFiles = [
  'phase3-sweep-angle-c.json',
  'phase3-sweep-angle-a.json',
  'review-package-embedded-pg-sweep.json',
  'phase3-sweep-angle-b.json',
  'review-angle-d-removed.json',
];

const memories = [
  ['遵守 TDD', '用户要求后续开发严格遵守 TDD：先写红测并运行确认...', '偏好', '私有'],
  ['同步脚本', '打包/验证脚本修复必须同步 macOS 和 Linux 对应路...', '偏好', '私有'],
];

function projectDisplayName(path) {
  const value = (path || '').toString().trim();
  if (!value || value === '未选择项目') return '未选择项目';
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

const SETTINGS_KEYS = Object.freeze({
  stallThreshold: 'stallThresholdSec',
  contextThresholds: 'contextUsageAlerts.thresholds',
  activeProvider: 'settings.provider.active',
});

const SETTINGS_DEFAULTS = Object.freeze({
  stallThresholdSec: 30,
  contextThresholds: [70, 85, 95],
  activeProvider: 'codex',
  codexHome: '~/.codex',
  codexInstanceKey: 'default',
  codexModelProvider: 'openai',
  providerModel: 'gpt-5',
  providerEffort: 'high',
  sandboxPolicy: 'workspaceWrite',
  writableRoots: '',
  networkAccess: false,
});

function providerSettingKey(provider, key) {
  return `settings.provider.${provider}.${key}`;
}

function normalizeSettingsCwd(value) {
  const cwd = (value || '').toString().trim();
  if (!cwd || cwd === '.' || cwd === '未选择项目') {
    throw new Error('settings: cwd is required');
  }
  return cwd;
}

function stringSetting(value, fallback) {
  if (typeof value === 'string' && value.trim()) return value.trim();
  return fallback;
}

function numberSetting(value, fallback) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function normalizeProviderName(value) {
  const provider = stringSetting(value, SETTINGS_DEFAULTS.activeProvider).toLowerCase();
  return provider === 'claude' ? 'claude' : 'codex';
}

function normalizeContextThresholds(value) {
  if (!Array.isArray(value) || value.length < 3) return SETTINGS_DEFAULTS.contextThresholds;
  return [
    numberSetting(value[0], SETTINGS_DEFAULTS.contextThresholds[0]),
    numberSetting(value[1], SETTINGS_DEFAULTS.contextThresholds[1]),
    numberSetting(value[2], SETTINGS_DEFAULTS.contextThresholds[2]),
  ];
}

function sandboxPolicyFromPreference(value) {
  if (typeof value === 'string') return value;
  if (value && typeof value === 'object') {
    return value.type || value.mode || SETTINGS_DEFAULTS.sandboxPolicy;
  }
  return SETTINGS_DEFAULTS.sandboxPolicy;
}

function writableRootsFromPreference(value) {
  if (!value || typeof value !== 'object' || !Array.isArray(value.writableRoots)) return '';
  return value.writableRoots.join('\n');
}

function sandboxPreferenceValue(policy, writableRootsText, networkAccess) {
  if (policy === 'readOnly') return { type: 'readOnly' };
  if (policy === 'dangerFullAccess') return { type: 'dangerFullAccess' };
  const writableRoots = writableRootsText
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean);
  return {
    type: 'workspaceWrite',
    writableRoots,
    networkAccess: Boolean(networkAccess),
  };
}

function parsePositiveInteger(label, value) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isInteger(parsed)) throw new Error(`${label} 必须是整数`);
  return parsed;
}

function validateRuntimeThresholds(form) {
  const stallThresholdSec = parsePositiveInteger('统一超时阈值', form.stallThresholdSec);
  if (stallThresholdSec < 30) throw new Error('统一超时阈值必须大于或等于 30 秒');

  const warn = parsePositiveInteger('Warn 阈值', form.contextWarn);
  const danger = parsePositiveInteger('Danger 阈值', form.contextDanger);
  const critical = parsePositiveInteger('Critical 阈值', form.contextCritical);
  if (!(warn > 0 && warn < danger && danger < critical && critical <= 100)) {
    throw new Error('上下文阈值必须满足 0 < warn < danger < critical <= 100');
  }
  return { stallThresholdSec, contextThresholds: [warn, danger, critical] };
}

function App({ skipBootstrap = false }) {
  const store = useClientStore();
  const bootstrap = store.bootstrap;

  useEffect(() => {
    if (skipBootstrap) return undefined;
    let cancelled = false;
    bootstrap().catch((error) => {
      if (!cancelled) {
        console.error('[frontend-app] bootstrap failed', error);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [bootstrap, skipBootstrap]);

  const activeLabel = useMemo(() => (
    navItems.find((item) => item.id === store.activePage)?.label || 'Chat'
  ), [store.activePage]);

  const projectPath = store.activeProject || store.cwd || '未选择项目';

  return (
    <div className="sa-window" data-testid="frontend-app">
      <Titlebar />
      <div className="sa-body">
        <NavRail activePage={store.activePage} setActivePage={store.setActivePage} />
        <main className="sa-main">
          {store.activePage === 'chat' ? <ChatPage store={store} projectPath={projectPath} /> : null}
          {store.activePage === 'prompts' ? <PromptPage projectPath={projectPath} /> : null}
          {store.activePage === 'workflows' ? <WorkflowPage /> : null}
          {store.activePage === 'skills' ? <SkillsPage projectPath={projectPath} /> : null}
          {store.activePage === 'memory' ? <MemoryPage /> : null}
          {store.activePage === 'files' ? <FilesPage /> : null}
          {store.activePage === 'settings' ? <SettingsPage projectPath={projectPath} /> : null}
          <span className="sr-only">当前页面：{activeLabel}</span>
        </main>
      </div>
    </div>
  );
}

function Titlebar() {
  return (
    <header className="titlebar">
      <div className="traffic-lights" aria-hidden="true">
        <span className="red" />
        <span className="yellow" />
        <span className="green" />
      </div>
      <strong>Super Agent</strong>
    </header>
  );
}

function NavRail({ activePage, setActivePage }) {
  return (
    <aside className="nav-rail" data-testid="sidebar-nav">
      <nav>
        {navItems.map((item) => {
          const Icon = item.icon;
          return (
            <button
              key={item.id}
              type="button"
              className={activePage === item.id ? 'active' : ''}
              onClick={() => setActivePage(item.id)}
              aria-label={item.label}
            >
              <Icon size={22} aria-hidden="true" />
              <span>{item.label}</span>
              {item.alert ? <i /> : null}
            </button>
          );
        })}
      </nav>
    </aside>
  );
}

function ChatPage({ store, projectPath }) {
  const activeThreadId = store.activeThreadId;
  const messages = store.timelinesByThread[activeThreadId] || [];
  const tokenUsage = store.tokenUsageByThread[activeThreadId] || null;
  const diffText = store.diffTextByThread[activeThreadId] || '';

  const beginResize = (event) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = store.rightPanelWidth;
    const move = (moveEvent) => {
      const next = Math.max(360, Math.min(780, startWidth - (moveEvent.clientX - startX)));
      store.setRightPanelWidth(next);
    };
    const stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop);
  };

  return (
    <section className="chat-page" data-testid="chat-page">
      <TopCommandBar store={store} projectPath={projectPath} />
      <div
        className="chat-layout"
        style={{ gridTemplateColumns: `320px minmax(0, 1fr) 8px ${store.rightPanelWidth}px` }}
      >
        <ThreadRail store={store} />
        <Conversation
          messages={messages}
          draft={store.draft}
          setDraft={store.setDraft}
          sendMessage={store.sendDraft}
          attachments={store.attachments}
          selectFiles={store.selectFilesForComposer}
          removeAttachment={store.removeAttachment}
          sending={store.sending}
          projectPath={projectPath}
          permission={store.permission}
          setPermission={store.setPermission}
          tokenUsage={tokenUsage}
          activeThreadId={activeThreadId}
        />
        <button
          type="button"
          className="splitter"
          aria-label="调整工作台宽度"
          onPointerDown={beginResize}
        />
        <RuntimePanel
          diffText={diffText}
          tokenUsage={tokenUsage}
          warnings={store.warningEntries}
          activity={store.activityEntries}
        />
      </div>
    </section>
  );
}

function TopCommandBar({ store, projectPath }) {
  return (
    <div className="top-command" data-testid="chat-toolbar">
      <button type="button" className="project-select" title={projectPath}>
        <Folder size={15} />
        <span>{projectPath}</span>
        <ChevronDown size={14} />
      </button>
      <button type="button" className="icon-btn" aria-label="复制当前线程"><Copy size={15} /></button>
      <button type="button" className="icon-btn" aria-label="线程状态"><CircleDot size={15} /></button>
      <button type="button" className="provider"><span /> {store.provider === 'codex' ? 'Codex' : store.provider}</button>
      <button type="button" className="icon-btn" aria-label="中断当前对话" onClick={() => void store.interruptActiveThread()}><AlertTriangle size={15} /></button>
      <button type="button" className="icon-btn" aria-label="压缩当前线程" onClick={() => void store.compactActiveThread()}><Archive size={15} /></button>
      <button type="button" className="icon-btn" aria-label="恢复当前线程" onClick={() => void store.recoverActiveThread()}><Workflow size={15} /></button>
      <button type="button" className="icon-btn" aria-label="选择附件" onClick={() => void store.selectFilesForComposer()}><Paperclip size={15} /></button>
      <select aria-label="权限" value={store.permission} onChange={(event) => store.setPermission(event.target.value)}>
        <option>完全访问权限</option>
        <option>工作区写入</option>
        <option>只读模式</option>
      </select>
      <button type="button" className="project-pill"><Folder size={14} /> {projectDisplayName(projectPath)}</button>
    </div>
  );
}

function ThreadRail({ store }) {
  const threads = store.threads.filter((thread) => !thread.archived);
  return (
    <aside className="thread-rail" data-testid="thread-rail">
      <div className="thread-tools">
        <button type="button" className="round"><Bot size={17} /></button>
        <button type="button" className="count"><Archive size={14} /> {store.threads.length}</button>
        <button type="button" className="round add" aria-label="新建对话" onClick={store.newThread}><Plus size={18} /></button>
        <button
          type="button"
          className="round"
          aria-label="归档当前线程"
          onClick={() => void store.archiveThread(store.activeThreadId, true)}
        >
          <Archive size={15} />
        </button>
      </div>
      <div className="thread-list">
        {threads.length === 0 ? <p className="thread-empty">暂无线程，直接在右侧输入即可开始。</p> : null}
        {threads.map((thread) => {
          const active = store.activeThreadId === thread.id;
          const running = ['running', '工作中', 'pending', 'recovering'].includes((thread.status || '').toLowerCase()) || thread.status === '工作中';
          return (
            <button
              key={thread.id}
              type="button"
              className={`thread-card ${active ? 'active' : ''}`}
              onClick={() => void store.setActiveThread(thread.id)}
            >
              <span className="thread-pin"><GitBranch size={15} /></span>
              <span className="thread-name">{thread.name}</span>
              <b>{thread.provider || 'Codex'}</b>
              <em className={running ? 'running' : ''}>{running ? '工作中' : thread.status || '等待指示'}</em>
            </button>
          );
        })}
      </div>
    </aside>
  );
}

function Conversation({
  messages,
  draft,
  setDraft,
  sendMessage,
  attachments,
  selectFiles,
  removeAttachment,
  sending,
  projectPath,
  permission,
  setPermission,
  tokenUsage,
  activeThreadId,
}) {
  return (
    <section className="conversation">
      <div className="timeline" data-testid="chat-timeline">
        {messages.length === 0 ? (
          <div className="empty-chat">
            <h2>我们应该在 {projectDisplayName(projectPath)} 中构建什么？</h2>
            <p>{projectPath}</p>
          </div>
        ) : null}
        {messages.map((message) => (
          <article key={message.id} className={`message ${message.role}`}>
            <div className="avatar">{message.role === 'user' ? 'U' : 'AI'}</div>
            <div className="bubble">
              <header><span>{message.role === 'user' ? '你' : 'AI'}</span><time>{formatTime(message.time)}</time></header>
              <pre>{message.text}</pre>
            </div>
          </article>
        ))}
      </div>
      <WorkStatus sending={sending} activeThreadId={activeThreadId} tokenUsage={tokenUsage} />
      <footer className="composer composer--docked" data-testid="composer-dock">
        <div className="composer-card">
          {attachments.length > 0 ? (
            <div className="attachments">
              {attachments.map((item) => (
                <button key={item.path} type="button" onClick={() => removeAttachment(item.path)}>
                  {item.name || item.path}
                </button>
              ))}
            </div>
          ) : null}
          <textarea
            data-testid="composer-input"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault();
                void sendMessage();
              }
            }}
            placeholder="输入给 Agent 的内容，Enter 发送，Shift+Enter 换行"
          />
          <div className="composer-meta">
            <button type="button" className="composer-attach" aria-label="添加文件" onClick={() => void selectFiles()}>
              <Plus size={18} />
            </button>
            <label className="permission-chip">
              <span className="sr-only">发送权限</span>
              <select aria-label="发送权限" value={permission} onChange={(event) => setPermission(event.target.value)}>
                <option>完全访问权限</option>
                <option>工作区写入</option>
                <option>只读模式</option>
              </select>
            </label>
            <span className="composer-project" data-testid="composer-project" title={projectPath}>
              <Folder size={14} />
              {projectDisplayName(projectPath)}
            </span>
            <span className="composer-model">openai · 5.4 中</span>
            <button type="button" className="send" aria-label="发送消息" disabled={sending} onClick={() => void sendMessage()}>
              <Send size={18} />
            </button>
          </div>
        </div>
      </footer>
    </section>
  );
}

function WorkStatus({ sending, activeThreadId, tokenUsage }) {
  return (
    <div className="work-status">
      <span className="spinner" /> {sending ? '发送中' : activeThreadId ? '已连接' : '待启动'}
      <em>{activeThreadId ? `线程 ${activeThreadId}` : '发送首条消息后创建线程'}</em>
      <code>{tokenUsage ? `${tokenUsage.usedTokens} / ${tokenUsage.contextWindowTokens} tokens` : 'token usage 等待后端同步'}</code>
    </div>
  );
}

function RuntimePanel({ diffText, tokenUsage, warnings, activity }) {
  return (
    <aside className="runtime-panel">
      <div className="runtime-toolbar">
        <button type="button"><Image size={14} /> {diffText ? 1 : 0}</button>
        <button type="button"><FileText size={14} /> {activity.length}</button>
        <span className="score good">+{warnings.filter((item) => item.level === 'warn').length}</span>
        <span className="score bad">-{warnings.filter((item) => item.level === 'error').length}</span>
      </div>
      <div className="diff-empty">{diffText ? <pre>{diffText}</pre> : '暂无代码变更'}</div>
      <div className="runtime-icons">
        {[Code2, Boxes, FileText, Link2, GitBranch, AlertTriangle].map((Icon, index) => <Icon key={index} size={16} />)}
        <span>{tokenUsage ? `${tokenUsage.usedPercent.toFixed(1)}% context` : 'context --'}</span>
      </div>
      <div className="log-lines" data-testid="warning-log-panel">
        {warnings.length === 0 ? <p><time>--:--</time> warning log 等待事件</p> : null}
        {warnings.map((entry) => (
          <p key={entry.id}>
            <time>{formatTime(entry.timestamp)}</time> <b>{entry.event}</b> · {JSON.stringify(entry.fields)}
          </p>
        ))}
      </div>
    </aside>
  );
}

function formatTime(value) {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return '--:--';
  return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
}

function PageHeader({ icon: Icon, title, subtitle, actions }) {
  return (
    <header className="page-header">
      <h1><Icon size={25} /> {title}</h1>
      {subtitle ? <p>{subtitle}</p> : null}
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  );
}

function PromptPage({ projectPath }) {
  return (
    <section className="console-page">
      <PageHeader icon={FileText} title="AI 能力与资料" subtitle={`当前项目：${projectPath}`} />
      <div className="toolbar-line">
        <Segment title="范围" items={['全部范围', '这个项目', '全局可用']} />
        <Segment title="状态" items={['全部状态', '启用中', '已停用']} />
      </div>
      <div className="action-row">
        <button type="button">+ 添加给 AI 的内容</button>
        <button type="button" className="ghost">刷新</button>
      </div>
      <EmptyState icon={File} title="暂无内容" text="点击“添加给 AI 的内容”开始创建。" />
    </section>
  );
}

function Segment({ title, items }) {
  return (
    <div className="segment">
      <span>{title}</span>
      {items.map((item, index) => <button key={item} type="button" className={index === 0 ? 'active' : ''}>{item}</button>)}
    </div>
  );
}

function WorkflowPage() {
  return (
    <section className="workflow-page">
      <PageHeader icon={Workflow} title="任务流程" subtitle="AI 设计流程" />
      <div className="workflow-grid">
        <aside className="workflow-list">
          <div className="tabs"><button>进行中 0</button><button>定时任务 0</button><button className="active">历史记录 3</button></div>
          {['子 agent 随笔任务', 'Run pwd in test1 worktree', 'Run pwd in test1 worktree'].map((name, index) => (
            <button type="button" key={name + index} className={index === 0 ? 'active' : ''}>
              <strong>{name}</strong>
              <span>{index === 0 ? 'essay_agent_20260526' : `test1-pwd-20260526-002${9 - index}`}</span>
              <em>草稿 手动 {index === 2 ? '失败' : index === 1 ? '成功' : '已取消'}</em>
            </button>
          ))}
        </aside>
        <section className="workflow-detail">
          <div className="detail-top"><h2>子 agent 随笔任务</h2><button className="danger">删除</button><button>创建定时任务</button><button disabled>运行</button></div>
          <Panel title="最终结果">当前运行尚未标记最终结果。</Panel>
          <div className="stat-grid">
            <Panel title="任务状态">草稿</Panel>
            <Panel title="运行计划">手动</Panel>
            <Panel title="最近运行">已取消</Panel>
            <Panel title="最终结果">-</Panel>
          </div>
        </section>
      </div>
    </section>
  );
}

function SkillsPage({ projectPath }) {
  return (
    <section className="console-page">
      <PageHeader icon={Sparkles} title="技能管理" />
      <div className="subhead">技能列表</div>
      <div className="skills-toolbar">
        <button>批量导入技能目录</button>
        <button className="ghost">新建技能</button>
        <label><Search size={18} /><input placeholder="搜索技能名称、简介、关键词..." /></label>
      </div>
      <div className="skill-filter"><span>私人使用 0</span><span>项目共享 {skills.length}</span><span className="active">全部 {skills.length}</span></div>
      <div className="skill-grid">
        {skills.map(([title, text, tags]) => <SkillCard key={title} projectPath={projectPath} title={title} text={text} tags={tags} />)}
      </div>
    </section>
  );
}

function SkillCard({ projectPath, title, text, tags }) {
  return (
    <article className="skill-card">
      <header><h3>{title}</h3><span>项目共享</span></header>
      <p className="path">{projectPath}/.agent/skills/...</p>
      <p>{text}</p>
      <div className="quote">{text}</div>
      <small>关键词</small>
      <div className="tags">{tags.slice(0, 4).map((tag) => <span key={tag}>{tag}</span>)}<span>+7</span></div>
      <footer><button>编辑详情</button><button className="text-danger">删除</button></footer>
    </article>
  );
}

function FilesPage() {
  return (
    <section className="console-page">
      <PageHeader icon={FolderOpen} title="文件产物" subtitle="最新更新 · 全部28 最终产物0 工作文件28" actions={<button>刷新</button>} />
      <div className="file-intro">
        <FolderOpen size={29} />
        <h2>共享文件 · Agent 协作中转站</h2>
        <p>Agent 在运行过程中产生的所有数据产物都保存在这里。</p>
        <button>打开记忆中心</button>
      </div>
      <div className="file-list">
        {sharedFiles.map((file) => (
          <article key={file} className="file-row">
            <h3>{file}</h3>
            <p>工作文件 2026/5/27 14:09:33 151 B</p>
            <code>_internal/auto-continue/state/{file}</code>
            <pre>{'{"schemaVersion":1,"threadId":"' + file.replace('.json', '') + '","manualAbortAt":null,"watchdogPokeCount":5}'}</pre>
            <footer><button>打开</button><button>导出</button><button>删除</button><button>用此文件继续对话</button></footer>
          </article>
        ))}
      </div>
    </section>
  );
}

function MemoryPage() {
  return (
    <section className="memory-page">
      <PageHeader icon={MemoryStick} title="记忆中心" actions={<><label><Search size={17} /><input placeholder="搜索 name / description / path" /></label><button>刷新</button><button className="light">+ 新建</button></>} />
      <div className="memory-stats">
        <Panel title="总览"><strong className="big">8</strong><p><span className="orange-dot" />2 偏好 <span />6 项目</p></Panel>
        <Panel title="健康度"><p>偏好 <meter value="2" max="15" /> 2 / 15</p><p>项目 <meter value="6" max="15" /> 6 / 15</p><p><span className="green-dot" /> 综合良好</p></Panel>
        <Panel title="自动沉淀"><p>已关闭</p><button>开启</button></Panel>
      </div>
      <div className="similar-alert"><AlertTriangle size={20} /> 1 组条目内容相似 <button>展开</button></div>
      <div className="memory-tabs"><button className="active">偏好 2</button><button>项目 6</button><button>全部 8</button></div>
      <div className="memory-cards">{memories.map(([title, text, tag, scope]) => <MemoryCard key={title} title={title} text={text} tag={tag} scope={scope} />)}</div>
    </section>
  );
}

function MemoryCard({ title, text, tag, scope }) {
  return (
    <article className="memory-card">
      <header><h3>{title}</h3><span>{tag}</span><em>{scope}</em></header>
      <p>{text}</p>
      <code># {title.replace(/\s+/g, ' ')}</code>
      <footer><time>5/28 23:19</time><button>编辑</button><button className="danger">删除</button></footer>
    </article>
  );
}

function SettingsPage({ projectPath }) {
  const [buildInfo, setBuildInfo] = useState(null);
  const [form, setForm] = useState({
    stallThresholdSec: String(SETTINGS_DEFAULTS.stallThresholdSec),
    contextWarn: String(SETTINGS_DEFAULTS.contextThresholds[0]),
    contextDanger: String(SETTINGS_DEFAULTS.contextThresholds[1]),
    contextCritical: String(SETTINGS_DEFAULTS.contextThresholds[2]),
    activeProvider: SETTINGS_DEFAULTS.activeProvider,
    codexHome: SETTINGS_DEFAULTS.codexHome,
    codexInstanceKey: SETTINGS_DEFAULTS.codexInstanceKey,
    codexModelProvider: SETTINGS_DEFAULTS.codexModelProvider,
    providerModel: SETTINGS_DEFAULTS.providerModel,
    providerEffort: SETTINGS_DEFAULTS.providerEffort,
    sandboxPolicy: SETTINGS_DEFAULTS.sandboxPolicy,
    writableRoots: SETTINGS_DEFAULTS.writableRoots,
    networkAccess: SETTINGS_DEFAULTS.networkAccess,
  });
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');

  const settingsCwd = projectPath;

  const refreshBuildInfo = useCallback(async () => {
    setError('');
    try {
      const info = await getBuildInfo();
      if (!info || typeof info !== 'object') throw new Error('build info response must be an object');
      setBuildInfo(info);
      setStatus('构建信息已刷新');
    } catch (err) {
      setError(err.message || String(err));
    }
  }, []);

  const loadPreferences = useCallback(async () => {
    setError('');
    const cwd = normalizeSettingsCwd(settingsCwd);
    const [stallValue, contextValue, activeProviderValue] = await Promise.all([
      getPreference({ cwd, key: SETTINGS_KEYS.stallThreshold }),
      getPreference({ cwd, key: SETTINGS_KEYS.contextThresholds }),
      getPreference({ cwd, key: SETTINGS_KEYS.activeProvider }),
    ]);
    const activeProvider = normalizeProviderName(activeProviderValue);
    const providerPrefix = `settings.provider.${activeProvider}`;
    const [
      codexHome,
      codexInstanceKey,
      codexModelProvider,
      providerModel,
      providerEffort,
      sandbox,
    ] = await Promise.all([
      getPreference({ cwd, key: providerSettingKey('codex', 'codexHome') }),
      getPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey') }),
      getPreference({ cwd, key: providerSettingKey('codex', 'codexModelProvider') }),
      getPreference({ cwd, key: `${providerPrefix}.model` }),
      getPreference({ cwd, key: `${providerPrefix}.effort` }),
      getPreference({ cwd, key: `${providerPrefix}.sandbox` }),
    ]);
    const contextThresholds = normalizeContextThresholds(contextValue);
    setForm({
      stallThresholdSec: String(numberSetting(stallValue, SETTINGS_DEFAULTS.stallThresholdSec)),
      contextWarn: String(contextThresholds[0]),
      contextDanger: String(contextThresholds[1]),
      contextCritical: String(contextThresholds[2]),
      activeProvider,
      codexHome: stringSetting(codexHome, SETTINGS_DEFAULTS.codexHome),
      codexInstanceKey: stringSetting(codexInstanceKey, SETTINGS_DEFAULTS.codexInstanceKey),
      codexModelProvider: stringSetting(codexModelProvider, SETTINGS_DEFAULTS.codexModelProvider),
      providerModel: stringSetting(providerModel, SETTINGS_DEFAULTS.providerModel),
      providerEffort: stringSetting(providerEffort, SETTINGS_DEFAULTS.providerEffort),
      sandboxPolicy: sandboxPolicyFromPreference(sandbox),
      writableRoots: writableRootsFromPreference(sandbox),
      networkAccess: Boolean(sandbox && typeof sandbox === 'object' && sandbox.networkAccess),
    });
  }, [settingsCwd]);

  useEffect(() => {
    refreshBuildInfo().catch(() => {});
  }, [refreshBuildInfo]);

  useEffect(() => {
    loadPreferences().catch((err) => {
      setError(err.message || String(err));
    });
  }, [loadPreferences]);

  const updateForm = (key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    setForm((current) => ({ ...current, [key]: value }));
  };

  const saveRuntimeSettings = async () => {
    setError('');
    setStatus('');
    try {
      const cwd = normalizeSettingsCwd(settingsCwd);
      const { stallThresholdSec, contextThresholds } = validateRuntimeThresholds(form);
      await setPreference({ cwd, key: SETTINGS_KEYS.stallThreshold, value: stallThresholdSec });
      await setPreference({ cwd, key: SETTINGS_KEYS.contextThresholds, value: contextThresholds });
      setStatus('运行阈值已保存');
    } catch (err) {
      setError(err.message || String(err));
    }
  };

  const saveProviderSettings = async () => {
    setError('');
    setStatus('');
    try {
      const cwd = normalizeSettingsCwd(settingsCwd);
      const provider = normalizeProviderName(form.activeProvider);
      await setPreference({ cwd, key: SETTINGS_KEYS.activeProvider, value: provider });
      await setPreference({ cwd, key: providerSettingKey(provider, 'model'), value: form.providerModel.trim() });
      await setPreference({ cwd, key: providerSettingKey(provider, 'effort'), value: form.providerEffort.trim() });
      await setPreference({
        cwd,
        key: providerSettingKey(provider, 'sandbox'),
        value: sandboxPreferenceValue(form.sandboxPolicy, form.writableRoots, form.networkAccess),
      });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexHome'), value: form.codexHome.trim() });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey'), value: form.codexInstanceKey.trim() });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexModelProvider'), value: form.codexModelProvider.trim() });
      setStatus('Provider 设置已保存');
    } catch (err) {
      setError(err.message || String(err));
    }
  };

  return (
    <section className="settings-page">
      <PageHeader icon={Settings} title="设置" actions={<button type="button" onClick={() => void refreshBuildInfo()}>刷新构建信息</button>} />
      <Panel title="ABOUT">
        <dl>
          <dt>版本</dt><dd>Agent Orchestrator {buildInfo?.version || 'unknown'}</dd>
          <dt>运行时</dt><dd>{buildInfo?.runtime || 'unknown'}</dd>
          <dt>构建时间</dt><dd>{buildInfo?.buildTime || 'unknown'}</dd>
          <dt>Commit</dt><dd>{buildInfo?.commit || 'unknown'}</dd>
          <dt>当前项目</dt><dd>{settingsCwd}</dd>
        </dl>
      </Panel>
      <Panel title="TURN TRACKER">
        <div className="form-line">
          <label>统一超时阈值<input aria-label="统一超时阈值" type="number" min="30" value={form.stallThresholdSec} onChange={updateForm('stallThresholdSec')} /> 秒</label>
          <button type="button" onClick={() => void saveRuntimeSettings()}>保存超时阈值</button>
        </div>
      </Panel>
      <Panel title="CONTEXT USAGE ALERT">
        <div className="form-line">
          <label>Warn 阈值<input type="number" min="1" max="100" value={form.contextWarn} onChange={updateForm('contextWarn')} /></label>
          <label>Danger 阈值<input type="number" min="1" max="100" value={form.contextDanger} onChange={updateForm('contextDanger')} /></label>
          <label>Critical 阈值<input type="number" min="1" max="100" value={form.contextCritical} onChange={updateForm('contextCritical')} /></label>
          <button type="button" onClick={() => void saveRuntimeSettings()}>保存运行阈值</button>
        </div>
      </Panel>
      <Panel title="PROVIDER">
        <div className="form-grid">
          <label>Active Provider<select value={form.activeProvider} onChange={updateForm('activeProvider')}><option value="codex">Codex</option><option value="claude">Claude</option></select></label>
          <label>Provider Model<input value={form.providerModel} onChange={updateForm('providerModel')} /></label>
          <label>Provider Effort<input value={form.providerEffort} onChange={updateForm('providerEffort')} /></label>
          <label>Codex Home<input value={form.codexHome} onChange={updateForm('codexHome')} /></label>
          <label>Instance Key<input value={form.codexInstanceKey} onChange={updateForm('codexInstanceKey')} /></label>
          <label>Model Provider<input value={form.codexModelProvider} onChange={updateForm('codexModelProvider')} /></label>
          <label>Sandbox Policy<select value={form.sandboxPolicy} onChange={updateForm('sandboxPolicy')}><option value="workspaceWrite">workspaceWrite</option><option value="readOnly">readOnly</option><option value="dangerFullAccess">dangerFullAccess</option></select></label>
          <label className="checkbox-line"><input type="checkbox" checked={form.networkAccess} onChange={updateForm('networkAccess')} /> Network Access</label>
          <label className="wide">Writable Roots<textarea value={form.writableRoots} onChange={updateForm('writableRoots')} placeholder="每行一个绝对路径" /></label>
        </div>
        <div className="settings-actions"><button type="button" onClick={() => void saveProviderSettings()}>保存 Provider 设置</button></div>
      </Panel>
      {status ? <p className="settings-status">{status}</p> : null}
      {error ? <p className="danger-text" role="alert">{error}</p> : null}
    </section>
  );
}

function Panel({ title, children }) {
  return (
    <section className="panel">
      <h3>{title}</h3>
      <div>{children}</div>
    </section>
  );
}

function EmptyState({ icon: Icon, title, text }) {
  return (
    <div className="empty-state">
      <span><Icon size={34} /></span>
      <h2>{title}</h2>
      <p>{text}</p>
    </div>
  );
}

export default App;
