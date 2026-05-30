import React, { useState, useEffect } from 'react';
import { useProjectStore } from '../../entities/project/model/useProjectStore';
import { getDashboardPage, setPreference } from '../../shared/api/backendApi';
import { useLogStore } from '../../entities/log/model/useLogStore';
import { Sparkles, Save, FileText } from 'lucide-react';

export default function SystemPromptPage() {
  const { requireActionCwd } = useProjectStore();
  const [loading, setLoading] = useState(false);
  const [prompts, setPrompts] = useState([]);
  const [activePromptKey, setActivePromptKey] = useState('');
  const [promptContent, setPromptContent] = useState('');

  const loadPrompts = async () => {
    setLoading(true);
    try {
      const cwd = requireActionCwd('system_prompts');
      const res = await getDashboardPage({ page: 'prompts', cwd });
      if (res && Array.isArray(res.prompts)) {
        setPrompts(res.prompts);
        if (res.prompts.length > 0) {
          setActivePromptKey(res.prompts[0].key || res.prompts[0].id);
          setPromptContent(res.prompts[0].content || '');
        }
      }
    } catch (err) {
      useLogStore.getState().error('prompts.load.failed', { error: err.message });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadPrompts();
  }, []);

  const handleSave = async () => {
    if (!activePromptKey) return;
    try {
      const cwd = requireActionCwd('save_prompt');
      await setPreference({
        cwd,
        key: `systemPrompt.${activePromptKey}`,
        value: promptContent
      });
      useLogStore.getState().info('prompts.saved', { key: activePromptKey });
      alert('系统提示词保存成功！');
    } catch (err) {
      useLogStore.getState().error('prompts.save.failed', { error: err.message });
    }
  };

  return (
    <div className="h-full w-full flex bg-sd-bg/5 overflow-hidden">
      {/* Sidebar listing prompt types */}
      <div className="w-64 border-r border-sd-border/40 bg-sd-surface/20 flex flex-col gap-2 p-3 select-none">
        <h3 className="text-xs font-semibold text-sd-text-primary px-2 mb-1 flex items-center gap-1.5">
          <FileText size={13} className="text-sd-accent" />
          <span>系统提示词模版</span>
        </h3>

        {loading ? (
          <div className="text-center text-xs text-sd-text-secondary py-8">加载中...</div>
        ) : prompts.length === 0 ? (
          <div className="text-center text-xs text-sd-text-secondary py-8">暂无可用提示词</div>
        ) : (
          prompts.map((p) => {
            const key = p.key || p.id;
            const isActive = key === activePromptKey;
            return (
              <button
                key={key}
                onClick={() => {
                  setActivePromptKey(key);
                  setPromptContent(p.content || '');
                }}
                className={`w-full text-left px-3 py-2 rounded-lg text-xs transition-premium ${
                  isActive
                    ? 'bg-sd-accent/10 border border-sd-accent/20 text-sd-accent font-medium'
                    : 'text-sd-text-secondary hover:bg-sd-border/30 hover:text-sd-text-primary border border-transparent'
                }`}
              >
                {p.title || p.name || key}
              </button>
            );
          })
        )}
      </div>

      {/* Editing Workspace */}
      <div className="flex-1 flex flex-col overflow-hidden bg-sd-bg/10">
        <div className="h-12 border-b border-sd-border/40 px-4 flex justify-between items-center bg-sd-surface/10 select-none">
          <span className="text-xs font-medium text-sd-text-primary">编辑提示词: {activePromptKey || '未选择'}</span>
          <button
            onClick={handleSave}
            disabled={!activePromptKey}
            className="flex items-center gap-1 bg-sd-accent hover:bg-sd-accent-hover text-white font-semibold px-3 py-1.5 rounded-lg text-xs transition-premium cursor-pointer disabled:opacity-50"
          >
            <Save size={13} />
            <span>保存变更</span>
          </button>
        </div>

        <div className="flex-1 p-4 flex flex-col gap-3">
          <textarea
            value={promptContent}
            onChange={(e) => setPromptContent(e.target.value)}
            disabled={!activePromptKey}
            className="flex-1 bg-sd-bg/60 border border-sd-border/50 focus:border-sd-accent/80 rounded-xl p-4 text-xs font-mono outline-none resize-none leading-relaxed text-sd-text-primary disabled:opacity-50"
            placeholder="在左边选择模版后在此编辑其提示词细节..."
          />
          <div className="flex items-center gap-2 bg-sd-accent/5 border border-sd-accent/15 rounded-lg px-3 py-2.5 text-[11px] text-sd-text-secondary select-none">
            <Sparkles size={13} className="text-sd-accent shrink-0" />
            <span>系统提示词可用来设定 AI 代理的初始化性格、具备的工具执行前置指令以及回答风格约束。</span>
          </div>
        </div>
      </div>
    </div>
  );
}
