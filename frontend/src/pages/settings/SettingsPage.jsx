import React, { useState, useEffect } from 'react';
import { getBuildInfo } from '../../shared/api/backendApi';
import { useProjectStore } from '../../entities/project/model/useProjectStore';
import { Settings, Laptop, FolderClosed } from 'lucide-react';

export default function SettingsPage() {
  const { active: activeProject, projects } = useProjectStore();
  const [buildInfo, setBuildInfo] = useState({});

  useEffect(() => {
    const loadInfo = async () => {
      try {
        const info = await getBuildInfo();
        setBuildInfo(info);
      } catch (err) {
        console.error(err);
      }
    };
    loadInfo();
  }, []);

  return (
    <div className="h-full w-full flex flex-col bg-sd-bg/5 overflow-hidden">

      {/* 1. Header Toolbar */}
      <div className="h-12 border-b border-sd-border/40 px-4 bg-sd-surface/25 backdrop-blur-md flex items-center justify-between select-none shrink-0">
        <span className="text-xs font-semibold text-sd-text-primary flex items-center gap-1.5">
          <Settings size={13} className="text-sd-accent" />
          <span>全局偏好与运行设置</span>
        </span>
      </div>

      {/* 2. Form view */}
      <div className="flex-1 overflow-y-auto p-4 flex flex-col gap-4 max-w-xl font-mono text-xs">

        {/* Wails Version Metadata */}
        <div className="glass-panel p-4 flex flex-col gap-3 bg-sd-surface/30 border border-sd-border/45">
          <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/45 flex items-center gap-1.5">
            <Laptop size={13} className="text-sd-accent" />
            <span>客户端编译信息</span>
          </h4>
          <dl className="grid grid-cols-2 gap-2 text-[11px] leading-normal text-sd-text-secondary">
            <div>版本标签 (Version)</div>
            <div className="text-sd-text-primary font-bold">{buildInfo.version || '0.1.0-dev'}</div>
            <div>提交散列 (Commit)</div>
            <div className="text-sd-text-primary">{buildInfo.commit || 'unknown'}</div>
            <div>运行环境 (Platform)</div>
            <div className="text-sd-text-primary">Linux / wails v3 desktop-shell</div>
          </dl>
        </div>

        {/* CWD scopes */}
        <div className="glass-panel p-4 flex flex-col gap-3 bg-sd-surface/30 border border-sd-border/45">
          <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/45 flex items-center gap-1.5">
            <FolderClosed size={13} className="text-sd-accent" />
            <span>当前项目路径列表</span>
          </h4>
          <div className="flex flex-col gap-2">
            <div className="flex justify-between items-center bg-sd-bg/60 border border-sd-border/50 rounded px-2.5 py-1.5">
              <span className="text-[10px] text-sd-text-secondary font-mono truncate">{activeProject}</span>
              <span className="text-[9px] text-sd-accent bg-sd-accent/10 border border-sd-accent/20 px-1.5 py-0.5 rounded font-semibold uppercase shrink-0">
                活动中
              </span>
            </div>
            {projects.filter(p => p !== activeProject).map((p, idx) => (
              <div key={idx} className="flex justify-between items-center bg-sd-bg/30 border border-sd-border/40 rounded px-2.5 py-1.5">
                <span className="text-[10px] text-sd-text-secondary font-mono truncate">{p}</span>
              </div>
            ))}
          </div>
        </div>

      </div>

    </div>
  );
}
