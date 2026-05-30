import React, { useState, useEffect } from 'react';
import { useProjectStore } from '../../entities/project/model/useProjectStore';
import { useLogStore } from '../../entities/log/model/useLogStore';
import { getDashboardPage, startDag, terminateDag } from '../../shared/api/backendApi';
import {
  Play,
  Square,
  Workflow,
  ListFilter,
  CheckCircle,
  XCircle,
  HelpCircle,
  RefreshCw,
} from 'lucide-react';

export default function DagsPage() {
  const { requireActionCwd } = useProjectStore();
  const [loading, setLoading] = useState(false);
  const [dags, setDags] = useState([]);
  const [selectedKey, setSelectedKey] = useState('');

  const loadDags = async () => {
    setLoading(true);
    try {
      const cwd = requireActionCwd('dags');
      const res = await getDashboardPage({ page: 'dags', cwd });
      if (res && Array.isArray(res.dags)) {
        setDags(res.dags);
        if (res.dags.length > 0) {
          setSelectedKey(res.dags[0].key || res.dags[0].id || res.dags[0].dag_key || res.dags[0].dagKey);
        }
      }
    } catch (err) {
      useLogStore.getState().error('dags.load.failed', { error: err.message });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadDags();
  }, []);

  const handleStart = async (key) => {
    try {
      const cwd = requireActionCwd('start_dag');
      await startDag({ cwd, dagKey: key, triggerSource: 'manual' });
      useLogStore.getState().info('dags.started', { dagKey: key });
      loadDags();
    } catch (err) {
      useLogStore.getState().error('dags.start.failed', { error: err.message });
    }
  };

  const handleStop = async (key) => {
    try {
      const cwd = requireActionCwd('stop_dag');
      await terminateDag({ cwd, dagKey: key, reason: 'user_requested' });
      useLogStore.getState().info('dags.stopped', { dagKey: key });
      loadDags();
    } catch (err) {
      useLogStore.getState().error('dags.stop.failed', { error: err.message });
    }
  };

  const getStatusIcon = (status) => {
    switch (status?.toLowerCase()) {
      case 'running':
        return <RefreshCw size={14} className="text-sd-accent animate-spin" />;
      case 'succeeded':
      case 'done':
        return <CheckCircle size={14} className="text-sd-success" />;
      case 'failed':
        return <XCircle size={14} className="text-sd-danger" />;
      default:
        return <HelpCircle size={14} className="text-sd-text-muted" />;
    }
  };

  const getStatusLabel = (status) => {
    const labels = {
      draft: '草稿',
      ready: '等待运行',
      running: '运行中',
      succeeded: '运行成功',
      done: '成功',
      failed: '失败',
      cancelled: '已取消',
    };
    return labels[status?.toLowerCase()] || status || '空闲';
  };

  const selectedDag = dags.find(
    (d) => (d.key || d.id || d.dag_key || d.dagKey) === selectedKey
  );

  return (
    <div className="h-full w-full flex bg-sd-bg/5 overflow-hidden">

      {/* 1. Left List Rail */}
      <div className="w-80 border-r border-sd-border/40 bg-sd-surface/20 flex flex-col overflow-hidden select-none">
        <div className="p-3 border-b border-sd-border/40 flex justify-between items-center gap-2">
          <span className="text-xs font-semibold text-sd-text-primary flex items-center gap-1.5">
            <Workflow size={13} className="text-sd-accent" />
            <span>任务执行流程</span>
          </span>
          <button
            onClick={loadDags}
            className="p-1 hover:bg-sd-border/30 rounded text-sd-text-secondary hover:text-sd-text-primary transition-premium cursor-pointer"
          >
            <RefreshCw size={12} />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-2 flex flex-col gap-1.5">
          {loading ? (
            <div className="text-center text-xs text-sd-text-secondary py-8">加载中...</div>
          ) : dags.length === 0 ? (
            <div className="text-center text-xs text-sd-text-secondary py-8">暂无流程实例</div>
          ) : (
            dags.map((dag, idx) => {
              const key = dag.key || dag.id || dag.dag_key || dag.dagKey;
              const isActive = key === selectedKey;
              return (
                <div
                  key={key || idx}
                  onClick={() => setSelectedKey(key)}
                  className={`glass-panel p-3 flex flex-col gap-1 cursor-pointer transition-premium hover-glow ${
                    isActive ? 'active-glow border-sd-accent/60 bg-sd-accent/5' : 'bg-sd-surface/30'
                  }`}
                >
                  <div className="flex justify-between items-center">
                    <span className="text-xs font-semibold truncate text-sd-text-primary flex-1">{dag.title || dag.name || '步骤流程'}</span>
                    {getStatusIcon(dag.status)}
                  </div>
                  <div className="flex justify-between text-[10px] text-sd-text-secondary mt-1">
                    <span>状态: {getStatusLabel(dag.status)}</span>
                    <span className="font-mono text-[9px] text-sd-text-muted">ID: {key?.slice(0, 8)}</span>
                  </div>
                </div>
              );
            })
          )}
        </div>
      </div>

      {/* 2. Detail Workspace Panel */}
      <div className="flex-1 flex flex-col bg-sd-bg/10 overflow-hidden">
        {selectedDag ? (
          <div className="flex-1 flex flex-col overflow-hidden">
            {/* Header toolbar */}
            <div className="h-12 border-b border-sd-border/40 px-4 flex justify-between items-center bg-sd-surface/10 select-none">
              <div>
                <h3 className="text-xs font-semibold text-sd-text-primary">{selectedDag.title || selectedDag.name}</h3>
                <span className="text-[9px] text-sd-text-secondary font-mono">{selectedKey}</span>
              </div>
              <div className="flex items-center gap-2">
                {selectedDag.status?.toLowerCase() === 'running' ? (
                  <button
                    onClick={() => handleStop(selectedKey)}
                    className="flex items-center gap-1.5 bg-sd-danger/10 hover:bg-sd-danger/25 border border-sd-danger/30 text-sd-danger font-semibold px-3 py-1.5 rounded-lg text-xs transition-premium cursor-pointer"
                  >
                    <Square size={12} fill="currentColor" />
                    <span>停止</span>
                  </button>
                ) : (
                  <button
                    onClick={() => handleStart(selectedKey)}
                    className="flex items-center gap-1.5 bg-sd-accent hover:bg-sd-accent-hover text-white font-semibold px-3 py-1.5 rounded-lg text-xs transition-premium cursor-pointer shadow-md"
                  >
                    <Play size={12} fill="currentColor" />
                    <span>运行</span>
                  </button>
                )}
              </div>
            </div>

            {/* Content list */}
            <div className="flex-1 overflow-y-auto p-4 flex flex-col gap-4">
              {/* Stats and metadata cards */}
              <div className="grid grid-cols-3 gap-3">
                <div className="glass-panel p-3 flex flex-col gap-1.5">
                  <span className="text-[10px] text-sd-text-muted">当前触发器</span>
                  <span className="text-xs font-semibold text-sd-text-primary">
                    {selectedDag.triggerLabel || '手动运行'}
                  </span>
                </div>
                <div className="glass-panel p-3 flex flex-col gap-1.5">
                  <span className="text-[10px] text-sd-text-muted">总步骤数</span>
                  <span className="text-xs font-semibold text-sd-text-primary">
                    {Array.isArray(selectedDag.nodes) ? selectedDag.nodes.length : 3} 步
                  </span>
                </div>
                <div className="glass-panel p-3 flex flex-col gap-1.5">
                  <span className="text-[10px] text-sd-text-muted">连接状况</span>
                  <span className="text-xs font-semibold text-sd-success">活跃正常</span>
                </div>
              </div>

              {/* Topology / Step items */}
              <div className="glass-panel p-4 flex flex-col gap-3">
                <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/40 flex items-center gap-1.5">
                  <ListFilter size={13} className="text-sd-accent" />
                  <span>执行阶段步骤</span>
                </h4>

                <div className="flex flex-col gap-2 font-mono">
                  {Array.isArray(selectedDag.nodes) && selectedDag.nodes.length > 0 ? (
                    selectedDag.nodes.map((node, index) => (
                      <div key={index} className="flex justify-between items-center px-3 py-2 bg-sd-surface/20 border border-sd-border/40 rounded-lg text-xs">
                        <div className="flex items-center gap-2">
                          <span className="w-5 h-5 rounded bg-sd-border/60 flex items-center justify-center text-[10px] text-sd-text-secondary">
                            {index + 1}
                          </span>
                          <span className="text-sd-text-primary font-semibold">{node.title || node.name}</span>
                        </div>
                        <span className="text-sd-text-secondary text-[10px]">{node.status || '等待中'}</span>
                      </div>
                    ))
                  ) : (
                    /* Mock placeholder steps */
                    ['解析源代码基线', '运行静态代码架构守护规则 (ratchet check)', '编译最终交付构建产物'].map((stepName, index) => (
                      <div key={index} className="flex justify-between items-center px-3 py-2 bg-sd-surface/20 border border-sd-border/40 rounded-lg text-xs">
                        <div className="flex items-center gap-2">
                          <span className="w-5 h-5 rounded bg-sd-border/60 flex items-center justify-center text-[10px] text-sd-text-secondary">
                            {index + 1}
                          </span>
                          <span className="text-sd-text-primary font-semibold">{stepName}</span>
                        </div>
                        <span className="text-sd-text-secondary text-[10px]">等待中</span>
                      </div>
                    ))
                  )}
                </div>
              </div>
            </div>
          </div>
        ) : (
          <div className="flex-1 flex flex-col items-center justify-center text-sd-text-muted gap-2 select-none">
            <Workflow size={32} className="opacity-30 text-sd-accent" />
            <span className="text-xs">选择或导入一个执行流程进行查看</span>
          </div>
        )}
      </div>

    </div>
  );
}
