import React, { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { runBackgroundAction, runUIAction } from '../../../shared/ui/runUIAction.js';
import { textValue } from '../../shared/pageShared.js';
import { getWorkflowTemplate, rollbackWorkflowTemplate } from '../services/workflowPageService.js';
import { EnterpriseTemplateForm } from './WorkflowEnterpriseTemplateForm.jsx';
import { enterpriseTemplateContractError } from './WorkflowEnterpriseTemplateContract.js';
import {
  enterpriseOutputTypes,
  enterpriseRollbackVersion,
  enterpriseTemplateCompatibilityRuntime,
  enterpriseTemplateDescription,
  enterpriseTemplateIcon,
  enterpriseTemplateId,
  enterpriseTemplateNodeTypes,
  enterpriseTemplateSearchText,
  enterpriseTemplateTitle,
  enterpriseTemplateTrustLevel,
  enterpriseTemplateVersion,
} from '../services/workflowEnterpriseTemplateModel.js';

function EnterpriseWorkflowTemplates({ onSelectTemplate, sectionRef, selectedTemplateId, templatesState }) {
  const queryClient = useQueryClient();
  const templates = templatesState.items;
  const [filters, setFilters] = useState({ businessFlow: '', outputType: '', schedule: '', keyword: '' });
  const [rollbackState, setRollbackState] = useState({ target: '', error: '' });
  const businessFlowOptions = useMemo(() => Array.from(new Set(templates.flatMap((template) => {
    const businessFlow = textValue(template.business_flow || template.businessFlow);
    return businessFlow ? [businessFlow] : [];
  }))), [templates]);
  const outputTypeOptions = useMemo(() => Array.from(new Set(templates.flatMap((template) => enterpriseOutputTypes(template).filter(Boolean)))), [templates]);
  const visibleTemplates = useMemo(() => templates.filter((template) => {
    const keyword = textValue(filters.keyword).toLowerCase();
    if (keyword && !enterpriseTemplateSearchText(template).includes(keyword)) return false;
    if (filters.businessFlow && textValue(template.business_flow || template.businessFlow) !== filters.businessFlow) return false;
    if (filters.outputType && !enterpriseOutputTypes(template).includes(filters.outputType)) return false;
    if (filters.schedule === 'scheduled' && !(template.supports_schedule || template.supportsSchedule)) return false;
    if (filters.schedule === 'manual' && (template.supports_schedule || template.supportsSchedule)) return false;
    return true;
  }), [filters, templates]);
  const updateFilter = (key, value) => setFilters((current) => ({ ...current, [key]: value }));
  const rollbackTemplate = (template) => runUIAction('workflow.template.rollback', async () => {
    const templateId = enterpriseTemplateId(template);
    const version = enterpriseRollbackVersion(template);
    if (!templateId || !version) return;
    setRollbackState({ target: `${templateId}:${version}`, error: '' });
    try {
      await rollbackWorkflowTemplate({ templateId, version });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['workflow-templates', 'government-enterprise'] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-template-detail', templateId] }),
      ]);
    } catch (err) {
      setRollbackState({ target: '', error: '回滚模板失败，请重试。' });
      throw err;
    }
    setRollbackState({ target: '', error: '' });
  });
  return (
    <section
      ref={sectionRef}
      className="enterprise-workflow-templates"
      aria-labelledby="enterprise-workflow-templates-title"
      tabIndex={-1}
    >
      <div className="enterprise-template-heading">
        <div>
          <h2 id="enterprise-workflow-templates-title">政企工作流模板库</h2>
          <p>按业务流程选择模板，先填写关键参数并预览 DAG 草案，再交给 DAG 设计器发现资源和创建工作流。</p>
        </div>
        <div className="enterprise-template-capabilities" aria-label="政企自动化能力">
          <span>复核节点</span>
          <span>DAG 草案</span>
          <span>目标输出格式</span>
        </div>
      </div>
      {templatesState.loading ? <p className="enterprise-template-muted">正在加载模板库。</p> : null}
      {templatesState.error ? <p className="danger-text" role="alert">加载模板库失败：{templatesState.error}</p> : null}
      {rollbackState.error ? <p className="danger-text" role="alert">{rollbackState.error}</p> : null}
      <div className="enterprise-template-filters" aria-label="政企模板筛选">
        <label>
          <span>搜索模板</span>
          <input aria-label="搜索模板" value={filters.keyword} onChange={(event) => updateFilter('keyword', event.target.value)} />
        </label>
        <label>
          <span>业务流</span>
          <select value={filters.businessFlow} onChange={(event) => updateFilter('businessFlow', event.target.value)}>
            <option value="">全部</option>
            {businessFlowOptions.map((item) => <option key={item} value={item}>{item}</option>)}
          </select>
        </label>
        <label>
          <span>输出类型</span>
          <select value={filters.outputType} onChange={(event) => updateFilter('outputType', event.target.value)}>
            <option value="">全部</option>
            {outputTypeOptions.map((item) => <option key={item} value={item}>{item.toUpperCase()}</option>)}
          </select>
        </label>
        <label>
          <span>定时</span>
          <select value={filters.schedule} onChange={(event) => updateFilter('schedule', event.target.value)}>
            <option value="">全部</option>
            <option value="scheduled">支持定时</option>
            <option value="manual">手动触发</option>
          </select>
        </label>
      </div>
      <div className="enterprise-template-grid">
        {visibleTemplates.map((template) => (
          <EnterpriseTemplateCard
            key={enterpriseTemplateId(template)}
            onRollbackTemplate={rollbackTemplate}
            onSelectTemplate={onSelectTemplate}
            rollbackTarget={rollbackState.target}
            selectedTemplateId={selectedTemplateId}
            template={template}
          />
        ))}
      </div>
    </section>
  );
}

function EnterpriseTemplateCard(props) {
  const { onRollbackTemplate, onSelectTemplate, rollbackTarget, selectedTemplateId, template } = props;
  const templateId = enterpriseTemplateId(template);
  const selected = templateId === selectedTemplateId;
  const version = enterpriseTemplateVersion(template);
  const rollbackVersion = enterpriseRollbackVersion(template);
  const nextRollbackTarget = `${templateId}:${rollbackVersion}`;
  return (
    <article className={'enterprise-template-card' + (selected ? ' selected' : '')}>
      <div className="enterprise-template-card-top">
        <span className="enterprise-template-icon" aria-hidden="true">
          {React.createElement(enterpriseTemplateIcon(template), { size: 18 })}
        </span>
        <div>
          <h3>{enterpriseTemplateTitle(template)}</h3>
          <p>{enterpriseTemplateDescription(template)}</p>
        </div>
      </div>
      <div className="enterprise-template-chip-group" aria-label={`${enterpriseTemplateTitle(template)}输出格式`}>
        {enterpriseOutputTypes(template).map((item) => <span key={item}>{item.toUpperCase()}</span>)}
      </div>
      <EnterpriseTemplateMeta template={template} version={version} />
      {rollbackVersion ? (
        <button
          type="button"
          className="btn-outline enterprise-template-action"
          disabled={rollbackTarget === nextRollbackTarget}
          onClick={() => { void onRollbackTemplate(template); }}
        >
          {rollbackTarget === nextRollbackTarget ? '回滚中...' : `回滚到 v${rollbackVersion}`}
        </button>
      ) : null}
      <button
        type="button"
        className={selected ? 'btn-dark enterprise-template-action' : 'btn-outline enterprise-template-action'}
        onClick={() => onSelectTemplate(templateId)}
        aria-label={`选择${enterpriseTemplateTitle(template)}模板`}
      >
        {selected ? '已选择' : '选择模板'}
      </button>
    </article>
  );
}

function EnterpriseTemplateMeta({ template, version }) {
  return (
    <dl className="enterprise-template-meta">
      <div>
        <dt>业务流</dt>
        <dd>{textValue(template.business_flow || template.businessFlow)}</dd>
      </div>
      <div>
        <dt>节点</dt>
        <dd>{Number(template.estimated_nodes || template.estimatedNodes || 0) || '-'} 个</dd>
      </div>
      <div>
        <dt>复核</dt>
        <dd>{template.requires_review || template.requiresReview ? '默认包含' : '未配置'}</dd>
      </div>
      <div>
        <dt>版本</dt>
        <dd>{version ? `v${version}` : '-'}</dd>
      </div>
      <div>
        <dt>信任</dt>
        <dd>{enterpriseTemplateTrustLevel(template) || '-'}</dd>
      </div>
      <div>
        <dt>运行时</dt>
        <dd>{enterpriseTemplateCompatibilityRuntime(template) || '-'}</dd>
      </div>
      <div>
        <dt>节点类型</dt>
        <dd>{enterpriseTemplateNodeTypes(template) || '-'}</dd>
      </div>
      <div>
        <dt>定时</dt>
        <dd>{template.supports_schedule || template.supportsSchedule ? '支持' : '手动'}</dd>
      </div>
    </dl>
  );
}

function EnterpriseTemplateWorkbench({ onAdjustTemplate, onStartTemplate, selectedTemplateId, starting, workflowCwd }) {
  const queryClient = useQueryClient();
  const {
    data,
    error,
    isPending,
  } = useQuery({
    queryKey: ['workflow-template-detail', selectedTemplateId],
    queryFn: () => runBackgroundAction('workflow.template-detail.load', () => getWorkflowTemplate({ templateId: selectedTemplateId })),
    enabled: Boolean(selectedTemplateId),
  });
  const template = data?.template || null;

  if (!selectedTemplateId) return null;
  if (isPending) return <section className="enterprise-template-workbench"><p>正在加载模板详情。</p></section>;
  if (error) return <p className="danger-text" role="alert">加载模板详情失败，请重试。</p>;
  if (!template) return null;
  const contractError = enterpriseTemplateContractError(template);
  if (contractError) return <p className="danger-text" role="alert">模板契约错误：{contractError}</p>;

  const refreshTemplateQueries = async (templateId) => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['workflow-templates', 'government-enterprise'] }),
      queryClient.invalidateQueries({ queryKey: ['workflow-template-detail', templateId] }),
    ]);
  };

  return (
    <EnterpriseTemplateForm
      key={enterpriseTemplateId(template)}
      onAdjustTemplate={onAdjustTemplate}
      onStartTemplate={onStartTemplate}
      onTemplateChanged={refreshTemplateQueries}
      starting={starting}
      template={template}
      workflowCwd={workflowCwd}
    />
  );
}

export { EnterpriseTemplateWorkbench, EnterpriseWorkflowTemplates };
