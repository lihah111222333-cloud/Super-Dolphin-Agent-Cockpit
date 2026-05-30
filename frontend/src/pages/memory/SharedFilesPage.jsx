import React, { useState, useEffect } from 'react';
import { useProjectStore } from '../../entities/project/model/useProjectStore';
import { useLogStore } from '../../entities/log/model/useLogStore';
import { getDashboardPage } from '../../shared/api/backendApi';
import { FolderCheck, FileText, Info } from 'lucide-react';

export default function SharedFilesPage() {
  const { requireActionCwd } = useProjectStore();
  const [loading, setLoading] = useState(false);
  const [files, setFiles] = useState([]);
  const [finalRefs, setFinalRefs] = useState([]);

  const loadFiles = async () => {
    setLoading(true);
    try {
      const cwd = requireActionCwd('shared_files');
      const res = await getDashboardPage({ page: 'memory', cwd });
      if (res) {
        if (Array.isArray(res.memory)) setFiles(res.memory);
        if (Array.isArray(res.finalOutputRefs)) setFinalRefs(res.finalOutputRefs);
      }
    } catch (err) {
      useLogStore.getState().error('shared_files.load.failed', { error: err.message });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadFiles();
  }, []);

  return (
    <div className="h-full w-full flex flex-col bg-sd-bg/5 overflow-hidden">

      {/* 1. Header Toolbar */}
      <div className="h-12 border-b border-sd-border/40 px-4 bg-sd-surface/25 backdrop-blur-md flex items-center justify-between select-none shrink-0">
        <span className="text-xs font-semibold text-sd-text-primary flex items-center gap-1.5">
          <FolderCheck size={13} className="text-sd-accent" />
          <span>共享文件与交付归集</span>
        </span>
        <button
          onClick={loadFiles}
          className="text-xs border border-sd-border/60 hover:border-sd-accent hover:text-sd-accent px-2.5 py-1 rounded transition-premium cursor-pointer font-medium"
        >
          刷新归集列表
        </button>
      </div>

      {/* 2. Grid items */}
      <div className="flex-1 overflow-y-auto p-4 flex flex-col gap-4">
        {/* Banner */}
        <div className="flex items-start gap-2.5 bg-sd-accent/5 border border-sd-accent/15 rounded-xl px-4 py-3 text-xs leading-relaxed text-sd-text-secondary select-none">
          <Info size={15} className="text-sd-accent shrink-0 mt-0.5" />
          <div className="flex flex-col gap-0.5">
            <span className="font-semibold text-sd-text-primary">共享文件归集提示</span>
            <span>适合放置命令输出、分析摘要、交接日志。已确认值得长期保留的内容建议转到“记忆中心”沉淀为长期记忆。</span>
          </div>
        </div>

        {loading ? (
          <div className="text-center text-xs text-sd-text-secondary py-12">加载中...</div>
        ) : (
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">

            {/* Delivery Files Panel */}
            <div className="glass-panel p-4 flex flex-col gap-3 bg-sd-surface/30 border border-sd-border/45">
              <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/45 flex items-center gap-1.5">
                <FileText size={13} className="text-sd-accent" />
                <span>交付结果汇总 (Outputs)</span>
              </h4>
              <div className="flex flex-col gap-2 overflow-y-auto max-h-[350px]">
                {finalRefs.length === 0 ? (
                  <div className="text-center text-xs text-sd-text-muted py-8 select-none font-mono">
                    暂无待交付的结果归集
                  </div>
                ) : (
                  finalRefs.map((ref, idx) => (
                    <div key={idx} className="p-2.5 bg-sd-bg/40 border border-sd-border/55 rounded-lg text-xs flex justify-between items-center gap-2">
                      <span className="font-mono text-sd-text-primary truncate">{ref.path || ref}</span>
                      <span className="text-[10px] text-sd-accent font-semibold px-2 py-0.5 bg-sd-accent/10 border border-sd-accent/20 rounded">
                        已就绪
                      </span>
                    </div>
                  ))
                )}
              </div>
            </div>

            {/* Other files Panel */}
            <div className="glass-panel p-4 flex flex-col gap-3 bg-sd-surface/30 border border-sd-border/45">
              <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/45 flex items-center gap-1.5">
                <FolderCheck size={13} className="text-sd-accent" />
                <span>共享上下文文件列表</span>
              </h4>
              <div className="flex flex-col gap-2 overflow-y-auto max-h-[350px]">
                {files.length === 0 ? (
                  <div className="text-center text-xs text-sd-text-muted py-8 select-none font-mono">
                    暂无文件挂载记录
                  </div>
                ) : (
                  files.map((file, idx) => (
                    <div key={idx} className="p-2.5 bg-sd-bg/40 border border-sd-border/55 rounded-lg text-xs leading-normal">
                      <div className="flex justify-between items-center mb-1">
                        <span className="font-mono text-sd-text-primary truncate">{file.path || file.name}</span>
                        <span className="text-[9px] text-sd-text-muted">{file.updatedAt || 'LATEST'}</span>
                      </div>
                      <div className="flex justify-between text-[10px] text-sd-text-secondary mt-1 border-t border-sd-border/30 pt-1.5">
                        <span>大小: {file.sizeBytes || file.size || '未知'} B</span>
                        <span>更新者: {file.updatedBy || 'Operator'}</span>
                      </div>
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
