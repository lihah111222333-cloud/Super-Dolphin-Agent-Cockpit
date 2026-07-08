import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Workflow, ArrowLeft, Clock, ClipboardList } from 'lucide-react';
import { PageHeader, Panel, RetryableSyncError } from '../../shared/pageComponents.jsx';
import { finalOutputPath } from '../adapters/workflowDisplayAdapter.js';
import { WorkflowDiagnostics } from './WorkflowDiagnostics.jsx';
import { WorkflowFinalOutputPanel } from './WorkflowFinalOutputPanel.jsx';
import { EnterpriseTemplateWorkbench, EnterpriseWorkflowTemplates } from './WorkflowEnterpriseTemplates.jsx';
import { WorkflowStageProgress } from './WorkflowStagePanels.jsx';
import { WorkflowAdvanced, WorkflowModals, WorkflowNodeList, WorkflowRunHistory } from './WorkflowDetailPanels.jsx';
import { DAG_CATEGORIES, displayDagStatusLabel, latestDagRunLabel, schedulePlanLabel } from '../services/workflowDagModel.js';
import { openSharedFile, readSharedFile } from '../services/workflowPageService.js';

const EMPTY_STATE_ICON_SIZE = 34;

function WorkflowPageView({ copy, model, onWorkflowViewChange }) {
  const { derived, isProjectPending, list, actions, actionState } = model;
  const isEmpty = !isProjectPending && !derived.blockingLoadError && !list.loading && derived.overviewStats.total === 0;
  const templateSectionRef = useRef(null);
  const [activeWorkflowView, setActiveWorkflowView] = useState('automation');
  const [selectedTemplateId, setSelectedTemplateId] = useState('');
  const handleViewTemplates = useCallback(() => {
    setActiveWorkflowView('templates');
    onWorkflowViewChange?.('templates');
  }, [onWorkflowViewChange]);
  const handleSelectTemplate = useCallback((templateId) => {
    setActiveWorkflowView('templates');
    onWorkflowViewChange?.('templates');
    setSelectedTemplateId(templateId);
  }, [onWorkflowViewChange]);
  const handleStartFreeDesign = useCallback(() => {
    setActiveWorkflowView('freeDesign');
    onWorkflowViewChange?.('freeDesign');
    void actions.startDesignFlow(null, { stayOnWorkflow: true });
  }, [actions, onWorkflowViewChange]);
  const handleReturnAutomation = useCallback(() => {
    setActiveWorkflowView('automation');
    onWorkflowViewChange?.('automation');
    setSelectedTemplateId('');
  }, [onWorkflowViewChange]);

  useEffect(() => {
    if (activeWorkflowView !== 'templates') return;
    const section = templateSectionRef.current;
    if (!section) return;
    if (typeof section.scrollIntoView === 'function') section.scrollIntoView({ block: 'start' });
    section.focus();
  }, [activeWorkflowView, selectedTemplateId]);
  if (activeWorkflowView === 'templates') {
    return (
      <section className="workflow-page">
        <WorkflowSubpageHeader title={copy.templatePageTitle} onBack={handleReturnAutomation} />
        <WorkflowMessages copy={copy} model={model} />
        <EnterpriseWorkflowTemplates
          onSelectTemplate={handleSelectTemplate}
          sectionRef={templateSectionRef}
          selectedTemplateId={selectedTemplateId}
          templatesState={model.templates}
        />
        <EnterpriseTemplateWorkbench
          onAdjustTemplate={(template) => actions.startDesignFlow(template)}
          onStartTemplate={(template) => actions.createAndStartTemplate(template)}
          selectedTemplateId={selectedTemplateId}
          starting={actionState.actioning === 'design' || actionState.actioning === 'template-create'}
          workflowCwd={model.workflowCwd}
        />
        <EnterpriseDesignProgress designSession={model.designSession} store={model.store} />
        <WorkflowModals model={model} />
      </section>
    );
  }

  if (activeWorkflowView === 'freeDesign') {
    return (
      <section className="workflow-page">
        <WorkflowSubpageHeader title={copy.freeDesignPageTitle} onBack={handleReturnAutomation} />
        <WorkflowMessages copy={copy} model={model} />
        <EnterpriseDesignProgress designSession={model.designSession} store={model.store} />
        <WorkflowModals model={model} />
      </section>
    );
  }

  return (
    <section className="workflow-page">
      <WorkflowHeader copy={copy} model={model} />
      <WorkflowMessages copy={copy} model={model} />
      {isEmpty ? (
        <AutomationEmptyState copy={copy} onStartChat={handleStartFreeDesign} onViewTemplates={handleViewTemplates} />
      ) : (
        <>
          <div className="automation-page-actions">
            <AutomationActionButtons copy={copy} onStartChat={handleStartFreeDesign} onViewTemplates={handleViewTemplates} />
          </div>
          <WorkflowGrid copy={copy} model={model} />
        </>
      )}
      <WorkflowModals model={model} />
    </section>
  );
}

function AutomationActionButtons({ copy, onStartChat, onViewTemplates }) {
  return (
    <div className="automation-presets-row">
      <button type="button" className="preset-pill" onClick={onStartChat}>
        <Workflow size={16} className="preset-icon" />
        <span>{copy.freeDesignPageTitle}</span>
      </button>
      <button type="button" className="preset-pill" onClick={onViewTemplates}>
        <ClipboardList size={16} className="preset-icon" />
        <span>{copy.viewTemplates}</span>
      </button>
    </div>
  );
}

function AutomationEmptyState({ copy, onStartChat, onViewTemplates }) {
  return (
    <div className="automation-empty-state">
      <div className="empty-clock-wrapper">
        <Clock size={40} className="empty-clock-icon" />
      </div>
      <h2>{copy.createFirst}</h2>
      <AutomationActionButtons copy={copy} onStartChat={onStartChat} onViewTemplates={onViewTemplates} />
    </div>
  );
}

function EnterpriseDesignProgress({ designSession, store }) {
  if (!designSession) return null;
  const activeIndex = enterpriseDesignPhaseIndex(designSession.phase);
  const canOpenThread = Boolean(designSession.threadId);
  const title = designSession.templateKey === 'free-design' ? '自由设计进度' : `${designSession.templateTitle}设计进度`;
  return (
    <section className={'enterprise-design-progress enterprise-design-progress-' + designSession.phase} aria-labelledby="enterprise-design-progress-title" role="status">
      <div className="enterprise-design-progress-heading">
        <div>
          <h2 id="enterprise-design-progress-title">{title}</h2>
          <p>{designSession.message || '正在准备政企自动化设计。'}</p>
        </div>
        <span>{designSession.outputFormat.toUpperCase()}</span>
      </div>
      <ol className="enterprise-design-steps">
        {designSession.phases.map((phase, index) => {
          const state = index < activeIndex ? 'done' : index === activeIndex ? 'active' : 'waiting';
          return (
            <li className={'enterprise-design-step ' + state} key={phase}>
              <span>{index + 1}</span>
              <strong>{phase}</strong>
            </li>
          );
        })}
      </ol>
      {canOpenThread ? (
        <button type="button" className="btn-outline enterprise-design-open" onClick={() => { void openEnterpriseDesignThread(store, designSession.threadId); }}>
          查看设计对话
        </button>
      ) : null}
    </section>
  );
}

function enterpriseDesignPhaseIndex(phase) {
  if (phase === 'starting') return 0;
  if (phase === 'sending') return 2;
  if (phase === 'submitted') return 4;
  if (phase === 'failed') return 1;
  return 0;
}

async function openEnterpriseDesignThread(store, threadId) {
  if (!threadId) return;
  if (typeof store?.setActiveThread === 'function') await store.setActiveThread(threadId);
  if (typeof store?.setActivePage === 'function') store.setActivePage('chat');
}

function WorkflowHeader({ copy, model }) {
  const { isProjectPending } = model;
  return (
    <PageHeader
      icon={Workflow}
      title={copy.title}
      subtitle={isProjectPending ? copy.connecting : ''}
      actions={null}
    />
  );
}

function WorkflowSubpageHeader({ onBack, title }) {
  return (
    <PageHeader
      icon={Workflow}
      title={title}
      actions={(
        <button type="button" className="btn-outline workflow-return-button" onClick={onBack}>
          <ArrowLeft size={16} />
          <span>返回自动化</span>
        </button>
      )}
    />
  );
}

function WorkflowMessages({ model }) {
  const { actionState, derived, refresh } = model;
  return (
    <>
      <RetryableSyncError className="danger-text workflow-sync-alert" message={derived.syncError} onRetry={refresh.refreshWorkflowSurface} />
      {actionState.error ? <p className="danger-text" role="alert">{actionState.error}</p> : null}
      <RetryableSyncError className="danger-text workflow-sync-alert" message={derived.blockingLoadError} onRetry={refresh.refreshWorkflowSurface} />
    </>
  );
}

function WorkflowGrid({ copy, model }) {
  return (
    <div className="workflow-grid">
      <WorkflowList copy={copy} model={model} />
      <WorkflowDetail copy={copy} model={model} />
    </div>
  );
}

function WorkflowList({ copy, model }) {
  const { derived, isProjectPending, list, selection } = model;
  return (
    <aside className="workflow-list">
      <WorkflowCategoryTabs copy={copy} selection={selection} />
      {!isProjectPending && list.loading ? <p className="console-message">{copy.loading}</p> : null}
      {!isProjectPending && !derived.blockingLoadError && !list.loading && selection.visibleItems.length === 0 ? <p className="console-message">{copy.noTasks}</p> : null}
      {selection.visibleItems.map((item) => <WorkflowListItem copy={copy} item={item} key={item.id} selection={selection} />)}
    </aside>
  );
}

function WorkflowCategoryTabs({ copy, selection }) {
  return (
    <div className="tabs" role="tablist" aria-label={copy.categoriesAria}>
      {DAG_CATEGORIES.map((category) => (
        <button
          key={category.key}
          type="button"
          role="tab"
          aria-selected={selection.activeCategory === category.key ? 'true' : 'false'}
          className={selection.activeCategory === category.key ? 'active' : ''}
          onClick={() => selection.chooseCategory(category.key)}
        >
          {copy.categories[category.key]} {selection.counts[category.key] || 0}
        </button>
      ))}
    </div>
  );
}

function WorkflowListItem({ copy, item, selection }) {
  const recentLabel = latestDagRunLabel(item);
  return (
    <button type="button" className={item.dagKey === selection.selectedDagKey ? 'active' : ''} onClick={() => selection.setSelectedDagKey(item.dagKey)}>
      <strong>{item.title}</strong>
      <span>{recentLabel === '-' ? copy.noRuns : copy.recentRunPrefix + recentLabel}</span>
      <em>{displayDagStatusLabel(item)} · {schedulePlanLabel(item)} · {recentLabel}</em>
    </button>
  );
}

function WorkflowDetail({ copy, model }) {
  if (!model.derived.activeDetailDag) {
    return (
      <section className="workflow-detail">
        <WorkflowOverview copy={copy} derived={model.derived} />
        <EmptyState icon={Workflow} title={copy.noAutomationTitle} text={copy.noAutomationText} />
      </section>
    );
  }
  return <WorkflowDetailContent copy={copy} model={model} />;
}

function WorkflowDetailContent({ copy, model }) {
  const { derived, detail, notices, selection } = model;
  const previewSharedOutputFile = useCallback((payload) => openSharedFile({ ...payload, preview: true }), []);
  return (
    <section className="workflow-detail">
      <WorkflowDetailTop model={model} />
      <WorkflowOverview copy={copy} derived={derived} />
      {detail.detailLoading ? <p className="console-message">正在加载详情...</p> : null}
      {notices.notice?.message && notices.notice.dagKey === selection.selectedDagKey ? <p className="settings-status">{notices.notice.message}</p> : null}
      {derived.startDisabledReason ? <p className="console-message">{derived.startDisabledReason}</p> : null}
      <WorkflowFinalOutputPanel
        key={finalOutputPath(derived.finalOutput) || 'inline-output'}
        finalOutput={derived.finalOutput}
        openFile={(payload) => openSharedFile(payload)}
        previewFile={previewSharedOutputFile}
        previewText={derived.finalText}
        readFile={(payload) => readSharedFile(payload)}
        workflowCwd={model.workflowCwd}
      />
      <WorkflowStageProgress model={model} />
      <WorkflowStatGrid derived={derived} selection={selection} />
      <WorkflowDiagnostics model={model} />
      <WorkflowRunHistory model={model} />
      <WorkflowNodeList model={model} />
      <WorkflowAdvanced model={model} />
    </section>
  );
}

function WorkflowOverview({ copy, derived }) {
  const stats = derived.overviewStats;
  const metrics = [
    [copy.metrics.total, stats.total],
    [copy.metrics.running, stats.running],
    [copy.metrics.scheduled, stats.scheduled],
    [copy.metrics.startable, stats.startable],
    [copy.metrics.finalOutputs, stats.finalOutputs],
  ];
  return (
    <section className="workflow-overview" aria-label={copy.overviewAria}>
      <div className="workflow-overview-copy">
        <span>{copy.currentAssets}</span>
        <h2>{copy.overviewTitle}</h2>
      </div>
      <dl>
        {metrics.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function WorkflowDetailTop({ model }) {
  const { actionState, actions, derived } = model;
  return (
    <div className="detail-top">
      <h2>{derived.activeDetailDag.title}</h2>
      <button type="button" className="danger" onClick={() => actionState.setDeleteTarget(derived.activeDetailDag)} disabled={Boolean(derived.deleteDisabledReason) || actionState.actioning === 'delete'} title={derived.deleteDisabledReason}>删除</button>
      <button type="button" onClick={actionState.openScheduleModal} disabled={derived.baseVersion === null || actionState.actioning === 'schedule'}>{derived.scheduleActionLabel}</button>
      {derived.scheduleToggleVisible ? <WorkflowScheduleToggle model={model} /> : null}
      {derived.activeRunKey ? <WorkflowStopButton model={model} /> : null}
      <button type="button" onClick={() => { void actions.runSelectedDag(); }} disabled={Boolean(derived.startDisabledReason) || actionState.actioning === 'start'} title={derived.startDisabledReason}>{actionState.actioning === 'start' ? '启动中...' : '运行'}</button>
    </div>
  );
}

function WorkflowScheduleToggle({ model }) {
  const { actionState, actions, derived } = model;
  return (
    <button type="button" onClick={() => { void actions.toggleScheduleEnabled(); }} disabled={derived.baseVersion === null || actionState.actioning === 'schedule-toggle'}>
      {derived.activeDetailDag.scheduleEnabled ? '暂停自动运行' : '启用自动运行'}
    </button>
  );
}

function WorkflowStopButton({ model }) {
  const { actionState, actions } = model;
  return (
    <button type="button" className="danger" onClick={() => { void actions.stopSelectedDag(); }} disabled={actionState.actioning === 'stop'}>
      {actionState.actioning === 'stop' ? '停止中...' : '停止运行'}
    </button>
  );
}

function WorkflowStatGrid({ derived, selection }) {
  return (
    <div className="stat-grid">
      <Panel title="任务状态">{displayDagStatusLabel(derived.activeDetailDag)}</Panel>
      <Panel title="运行计划">{schedulePlanLabel(derived.activeDetailDag)}</Panel>
      <Panel title="最近运行">{derived.recentRunPanelLabel === '-' ? latestDagRunLabel(selection.selectedDag) : derived.recentRunPanelLabel}</Panel>
      <Panel title="最终结果">{derived.finalText ? '已生成' : '-'}</Panel>
    </div>
  );
}

function EmptyState({ icon: Icon, title, text }) {
  return (
    <div className="empty-state">
      <span><Icon size={EMPTY_STATE_ICON_SIZE} /></span>
      <h2>{title}</h2>
      <p>{text}</p>
    </div>
  );
}

export { WorkflowPageView };
