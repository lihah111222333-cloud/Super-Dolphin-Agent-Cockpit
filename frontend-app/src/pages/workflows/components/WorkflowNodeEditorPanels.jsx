import React, { useCallback, useEffect, useMemo, useReducer, useState } from 'react';
import { appendCurrentModelOption, firstText, textValue } from '../../shared/pageShared.js';
import { FocusTrapDialog } from '../../../shared/ui/FocusTrapDialog.jsx';
import { Panel } from '../../shared/pageComponents.jsx';
import { DAG_WEEKDAY_OPTIONS, cronExprFromSchedule, scheduleLabelFromState, scheduleStateFromCron } from '../services/workflowScheduleModel.js';
import { dagNodeFormFromNode } from '../services/workflowNodeModel.js';

const DAYS_IN_MONTH = 31;

function DagNodeEditor({ nodes, savingNodeKey, onSave }) {
  const { activeNode, form, modelOptions, setActiveNodeKey, update } = useDagNodeEditorState(nodes);
  if (!activeNode) return null;

  return (
    <Panel title="步骤设置">
      <div className="dag-node-editor">
        <DagNodeSelector activeNode={activeNode} nodes={nodes} setActiveNodeKey={setActiveNodeKey} />
        <DagNodeConfigFields form={form} modelOptions={modelOptions} update={update} />
        <DagNodeInstructionFields form={form} update={update} />
        <div className="dag-node-editor-actions">
          <button type="button" onClick={() => { void onSave(form, activeNode); }} disabled={savingNodeKey === activeNode.nodeKey}>
            {savingNodeKey === activeNode.nodeKey ? '保存中...' : '保存步骤'}
          </button>
        </div>
      </div>
    </Panel>
  );
}

function useDagNodeEditorState(nodes) {
  const [activeNodeKey, setActiveNodeKeyState] = useState(firstText(nodes[0]?.nodeKey));
  const effectiveActiveNodeKey = nodes.some((node) => node.nodeKey === activeNodeKey)
    ? activeNodeKey
    : firstText(nodes[0]?.nodeKey);
  const activeNode = useMemo(
    () => nodes.find((node) => node.nodeKey === effectiveActiveNodeKey) || nodes[0] || null,
    [effectiveActiveNodeKey, nodes],
  );
  const [form, setForm] = useState(() => dagNodeFormFromNode(activeNode));
  if (effectiveActiveNodeKey !== activeNodeKey) {
    setActiveNodeKeyState(effectiveActiveNodeKey);
    setForm(dagNodeFormFromNode(activeNode));
  }

  const update = useCallback((key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    setForm((current) => ({ ...current, [key]: value }));
  }, []);
  const setActiveNodeKey = useCallback((nodeKey) => {
    const nextNode = nodes.find((node) => node.nodeKey === nodeKey) || nodes[0] || null;
    setActiveNodeKeyState(firstText(nextNode?.nodeKey));
    setForm(dagNodeFormFromNode(nextNode));
  }, [nodes]);
  const modelOptions = form.execProvider ? appendCurrentModelOption(form.execProvider, form.execModel) : [];
  return { activeNode, form, modelOptions, setActiveNodeKey, update };
}

function DagNodeSelector({ nodes, activeNode, setActiveNodeKey }) {
  return (
    <label>
      步骤
      <select value={activeNode.nodeKey} onChange={(event) => setActiveNodeKey(event.target.value)} aria-label="步骤">
        {nodes.map((node) => <option key={node.nodeKey} value={node.nodeKey}>{node.title}</option>)}
      </select>
    </label>
  );
}

function DagNodeConfigFields({ form, update, modelOptions }) {
  const nodeType = textValue(form.nodeType);
  return (
    <>
      <label>名称<input value={form.title} onChange={update('title')} aria-label="名称" /></label>
      <label>执行者<input value={form.assignedTo} onChange={update('assignedTo')} aria-label="执行者" /></label>
      {(nodeType === 'agent' || nodeType === 'hybrid') ? <DagAgentExecFields form={form} modelOptions={modelOptions} update={update} /> : null}
      {(nodeType === 'automation' || nodeType === 'hybrid') ? <DagAutomationExecFields form={form} update={update} /> : null}
      <label>依赖步骤<input value={form.dependsOn} onChange={update('dependsOn')} aria-label="依赖步骤" /></label>
      <DagNodeOutputFields form={form} update={update} />
    </>
  );
}

function DagAgentExecFields({ form, update, modelOptions }) {
  return (
    <>
      <label>
        执行引擎
        <select value={form.execProvider} onChange={update('execProvider')} aria-label="执行引擎">
          <option value="">默认</option>
          <option value="claude">claude</option>
          <option value="codex">codex</option>
        </select>
      </label>
      <label>
        模型
        <select value={form.execModel} onChange={update('execModel')} aria-label="模型">
          <option value="">默认</option>
          {modelOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
        </select>
      </label>
      <label>Agent Key<input value={form.execAgentKey} onChange={update('execAgentKey')} aria-label="Agent Key" /></label>
      <label>Prompt Key<input value={form.execPromptKey} onChange={update('execPromptKey')} aria-label="Prompt Key" /></label>
      <label>执行 cwd<input value={form.execCwd} onChange={update('execCwd')} aria-label="执行 cwd" /></label>
    </>
  );
}

function DagAutomationExecFields({ form, update }) {
  return (
    <>
      <label>
        自动化类型
        <select value={form.automationKind} onChange={update('automationKind')} aria-label="自动化类型">
          <option value="command_card">command_card</option>
        </select>
      </label>
      <label>命令卡片<input value={form.commandRef} onChange={update('commandRef')} aria-label="命令卡片" /></label>
    </>
  );
}

function DagNodeOutputFields({ form, update }) {
  return (
    <>
      <label>输出 sharedfile<input value={form.outputSharedfilePath} onChange={update('outputSharedfilePath')} aria-label="输出 sharedfile" /></label>
      <label>
        写入模式
        <select value={form.outputSharedfileLockMode} onChange={update('outputSharedfileLockMode')} aria-label="写入模式">
          <option value="exclusive">exclusive</option>
          <option value="append">append</option>
          <option value="shared">shared</option>
        </select>
      </label>
      <label className="inline-field">
        <input type="checkbox" checked={form.outputToNodeResult} onChange={update('outputToNodeResult')} aria-label="结果写入节点摘要" />
        结果写入节点摘要
      </label>
    </>
  );
}

function DagNodeInstructionFields({ form, update }) {
  if (textValue(form.nodeType) !== 'agent') return null;
  return (
    <>
      <label className="wide">指令<textarea value={form.firstTurn} onChange={update('firstTurn')} aria-label="指令" /></label>
    </>
  );
}

function DagScheduleModal({ cron, actionLabel, saving, onClose, onSave }) {
  const schedule = useDagScheduleForm(cron, onSave);
  return (
    <FocusTrapDialog ariaLabel={actionLabel} closeDisabled={saving} onClose={onClose}>
        <header><h2>{actionLabel}</h2><button type="button" className="ghost" onClick={onClose} disabled={saving}>关闭</button></header>
        <div className="dag-node-editor">
          <DagSchedulePresetField saving={saving} schedule={schedule} />
          <DagScheduleConditionalFields saving={saving} schedule={schedule} />
          <DagScheduleTimeField saving={saving} schedule={schedule} />
        </div>
        {schedule.previewText ? <p className="settings-status">{schedule.previewText} 自动运行</p> : null}
        {schedule.inputError ? <p className="danger-text" role="alert">{schedule.inputError}</p> : null}
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
          <button type="button" onClick={schedule.confirm} disabled={saving}>{saving ? '保存中...' : actionLabel}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function dagScheduleFields(schedule) {
  return {
    preset: schedule.preset,
    time: schedule.time,
    weekday: schedule.weekday,
    monthDay: schedule.monthDay,
    inputError: textValue(schedule.warning),
  };
}

function dagScheduleReducer(state, action) {
  if (action.type === 'reset') return dagScheduleFields(action.schedule);
  if (action.type === 'change') return { ...state, [action.key]: action.value, inputError: '' };
  if (action.type === 'error') return { ...state, inputError: action.error };
  throw new Error(`unknown dag schedule action: ${action.type}`);
}

function useDagScheduleForm(cron, onSave) {
  const initialSchedule = useMemo(() => scheduleStateFromCron(cron), [cron]);
  const [state, dispatch] = useReducer(dagScheduleReducer, initialSchedule, dagScheduleFields);
  const monthDays = useMemo(() => Array.from({ length: DAYS_IN_MONTH }, (_item, index) => (index + 1).toString()), []);
  const previewText = scheduleLabelFromState(state);

  useEffect(() => {
    dispatch({ type: 'reset', schedule: initialSchedule });
  }, [initialSchedule]);

  const choose = (key) => (event) => dispatch({ type: 'change', key, value: event.target.value });
  const setInputError = (error) => dispatch({ type: 'error', error });

  const confirm = () => confirmDagSchedule({ ...state, onSave, setInputError });
  return { ...state, choose, confirm, monthDays, previewText };
}

function confirmDagSchedule(schedule) {
  const { monthDay, onSave, preset, setInputError, time, weekday } = schedule;
  const { cronExpr, error } = cronExprFromSchedule(preset, time, weekday, monthDay);
  if (error) {
    setInputError(error);
    return;
  }
  void onSave(cronExpr);
}

function DagSchedulePresetField({ schedule, saving }) {
  return (
    <label>
      运行频率
      <select value={schedule.preset} onChange={schedule.choose('preset')} disabled={saving} aria-label="运行频率">
        <option value="daily">每天</option>
        <option value="weekdays">工作日</option>
        <option value="weekly">每周</option>
        <option value="monthly">每月</option>
      </select>
    </label>
  );
}

function DagScheduleConditionalFields({ schedule, saving }) {
  if (schedule.preset === 'weekly') {
    return (
      <label>
        星期几
        <select value={schedule.weekday} onChange={schedule.choose('weekday')} disabled={saving} aria-label="星期几">
          {DAG_WEEKDAY_OPTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
        </select>
      </label>
    );
  }
  if (schedule.preset === 'monthly') {
    return (
      <label>
        每月几号
        <select value={schedule.monthDay} onChange={schedule.choose('monthDay')} disabled={saving} aria-label="每月几号">
          {schedule.monthDays.map((day) => <option key={day} value={day}>{day} 日</option>)}
        </select>
      </label>
    );
  }
  return null;
}

function DagScheduleTimeField({ schedule, saving }) {
  return (
    <label>
      运行时间
      <input value={schedule.time} type="time" onChange={schedule.choose('time')} disabled={saving} aria-label="运行时间" />
    </label>
  );
}

function ConfirmDagDeleteModal({ dag, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="删除自动化" closeDisabled={deleting} onClose={onClose}>
        <header><h2>删除自动化</h2><button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button></header>
        <p>确定删除自动化 “{dag.title}” 吗？该操作会删除配置和运行关联信息，无法恢复。</p>
        <p className="path">{dag.dagKey}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

export { ConfirmDagDeleteModal, DagNodeEditor, DagScheduleModal };
