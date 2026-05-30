import React, { useState, useEffect } from 'react';
import { useProjectStore } from '../../entities/project/model/useProjectStore';
import { useLogStore } from '../../entities/log/model/useLogStore';
import { getDashboardPage } from '../../shared/api/backendApi';
import { Sparkles, Puzzle, Award, CheckCircle } from 'lucide-react';

export default function SkillsPage() {
  const { requireActionCwd } = useProjectStore();
  const [loading, setLoading] = useState(false);
  const [skills, setSkills] = useState([]);

  const loadSkills = async () => {
    setLoading(true);
    try {
      const cwd = requireActionCwd('skills');
      const res = await getDashboardPage({ page: 'skills', cwd });
      if (res && Array.isArray(res.skills)) {
        setSkills(res.skills);
      }
    } catch (err) {
      useLogStore.getState().error('skills.load.failed', { error: err.message });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadSkills();
  }, []);

  return (
    <div className="h-full w-full flex flex-col bg-sd-bg/5 overflow-hidden">

      {/* 1. Header Toolbar */}
      <div className="h-12 border-b border-sd-border/40 px-4 bg-sd-surface/25 backdrop-blur-md flex items-center justify-between select-none shrink-0">
        <span className="text-xs font-semibold text-sd-text-primary flex items-center gap-1.5">
          <Sparkles size={13} className="text-sd-accent" />
          <span>核心能力 & 技能库</span>
        </span>
        <button
          onClick={loadSkills}
          className="text-xs border border-sd-border/60 hover:border-sd-accent hover:text-sd-accent px-2.5 py-1 rounded transition-premium cursor-pointer font-medium"
        >
          刷新技能库
        </button>
      </div>

      {/* 2. Grid items view */}
      <div className="flex-1 overflow-y-auto p-4 flex flex-col gap-4">
        {loading ? (
          <div className="text-center text-xs text-sd-text-secondary py-12">加载中...</div>
        ) : skills.length === 0 ? (
          <div className="glass-panel p-8 text-center text-sd-text-muted flex flex-col items-center gap-2">
            <Puzzle size={28} className="opacity-30" />
            <span className="text-xs">该项目工作区暂未注册自定义技能</span>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
            {skills.map((skill, idx) => (
              <div key={skill.key || idx} className="glass-panel p-4 flex flex-col gap-2 transition-premium hover-glow bg-sd-surface/30">
                <div className="flex justify-between items-start gap-2">
                  <span className="text-xs font-semibold text-sd-text-primary truncate">{skill.title || skill.name || skill.key}</span>
                  <Award size={14} className="text-sd-accent shrink-0" />
                </div>
                <p className="text-[11px] text-sd-text-secondary leading-normal flex-1 line-clamp-3">
                  {skill.description || '无具体说明'}
                </p>
                <div className="flex justify-between items-center text-[10px] text-sd-text-muted mt-2 border-t border-sd-border/30 pt-2 font-mono">
                  <span>版本: {skill.version || '1.0.0'}</span>
                  <div className="flex items-center gap-1 text-sd-success">
                    <CheckCircle size={10} />
                    <span>已激活</span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

    </div>
  );
}
