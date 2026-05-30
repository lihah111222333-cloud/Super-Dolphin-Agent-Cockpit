import React, { useState, useEffect } from 'react';
import { useProjectStore } from '../../entities/project/model/useProjectStore';
import { useLogStore } from '../../entities/log/model/useLogStore';
import { getMemorySnapshot } from '../../shared/api/backendApi';
import { Brain, Layers, Shield } from 'lucide-react';

export default function MemoryCenterPage() {
  const { requireActionCwd } = useProjectStore();
  const [loading, setLoading] = useState(false);
  const [entries, setEntries] = useState([]);
  const [teamEntries, setTeamEntries] = useState([]);

  const loadMemory = async () => {
    setLoading(true);
    try {
      const cwd = requireActionCwd('memory_center');
      const res = await getMemorySnapshot({ cwd });
      if (res) {
        if (res.private && Array.isArray(res.private.entries)) {
          setEntries(res.private.entries);
        }
        if (res.team && Array.isArray(res.team.entries)) {
          setTeamEntries(res.team.entries);
        }
      }
    } catch (err) {
      useLogStore.getState().error('memory.load.failed', { error: err.message });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadMemory();
  }, []);

  return (
    <div className="h-full w-full flex flex-col bg-sd-bg/5 overflow-hidden">

      {/* 1. Header Toolbar */}
      <div className="h-12 border-b border-sd-border/40 px-4 bg-sd-surface/25 backdrop-blur-md flex items-center justify-between select-none shrink-0">
        <span className="text-xs font-semibold text-sd-text-primary flex items-center gap-1.5">
          <Brain size={13} className="text-sd-accent" />
          <span>长期记忆中心 (RAG)</span>
        </span>
        <button
          onClick={loadMemory}
          className="text-xs border border-sd-border/60 hover:border-sd-accent hover:text-sd-accent px-2.5 py-1 rounded transition-premium cursor-pointer font-medium"
        >
          重载记忆库
        </button>
      </div>

      {/* 2. Content Sections */}
      <div className="flex-1 overflow-y-auto p-4 flex flex-col gap-4">
        {loading ? (
          <div className="text-center text-xs text-sd-text-secondary py-12">加载中...</div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">

            {/* Private Memory Panel */}
            <div className="glass-panel p-4 flex flex-col gap-3 bg-sd-surface/30 border border-sd-border/45">
              <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/45 flex items-center gap-1.5">
                <Shield size={13} className="text-sd-accent" />
                <span>个人私有记忆 (Local)</span>
              </h4>
              <div className="flex flex-col gap-2 overflow-y-auto max-h-[300px]">
                {entries.length === 0 ? (
                  <div className="text-center text-xs text-sd-text-muted py-8 select-none">
                    暂无私有知识库条目
                  </div>
                ) : (
                  entries.map((entry, idx) => (
                    <div key={idx} className="p-3 bg-sd-bg/40 border border-sd-border/55 rounded-lg text-xs leading-normal">
                      <div className="flex justify-between items-center mb-1">
                        <span className="font-semibold text-sd-text-primary font-mono">{entry.title || entry.key}</span>
                        <span className="text-[9px] text-sd-text-muted">{entry.updatedAt || 'LATEST'}</span>
                      </div>
                      <p className="text-sd-text-secondary">{entry.content || entry.text}</p>
                    </div>
                  ))
                )}
              </div>
            </div>

            {/* Team/Project Memory Panel */}
            <div className="glass-panel p-4 flex flex-col gap-3 bg-sd-surface/30 border border-sd-border/45">
              <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/45 flex items-center gap-1.5">
                <Layers size={13} className="text-sd-accent" />
                <span>团队共享记忆 (Git Context)</span>
              </h4>
              <div className="flex flex-col gap-2 overflow-y-auto max-h-[300px]">
                {teamEntries.length === 0 ? (
                  <div className="text-center text-xs text-sd-text-muted py-8 select-none">
                    暂无团队共享知识条目
                  </div>
                ) : (
                  teamEntries.map((entry, idx) => (
                    <div key={idx} className="p-3 bg-sd-bg/40 border border-sd-border/55 rounded-lg text-xs leading-normal">
                      <div className="flex justify-between items-center mb-1">
                        <span className="font-semibold text-sd-text-primary font-mono">{entry.title || entry.key}</span>
                        <span className="text-[9px] text-sd-text-muted">{entry.updatedAt || 'LATEST'}</span>
                      </div>
                      <p className="text-sd-text-secondary">{entry.content || entry.text}</p>
                    </div>
                  ))
                )}
              </div>
            </div>

          </div>
        )}
      </div>

    </div>
  );
}
