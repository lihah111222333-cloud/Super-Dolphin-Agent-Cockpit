import {
  applyDagOps as applyDagOpsBackend,
  createAndStartDag as createAndStartDagBackend,
  deleteDag as deleteDagBackend,
  dispatchDagNode as dispatchDagNodeBackend,
  getDashboardPage as getDashboardPageBackend,
  getDagDetail as getDagDetailBackend,
  getDagRun as getDagRunBackend,
  getDagRuns as getDagRunsBackend,
  getWorkflowTemplate as getWorkflowTemplateBackend,
  listWorkflowTemplates as listWorkflowTemplatesBackend,
  openSharedFile as openSharedFileBackend,
  readSharedFile as readSharedFileBackend,
  renderWorkflowTemplateDraft as renderWorkflowTemplateDraftBackend,
  rollbackWorkflowTemplate as rollbackWorkflowTemplateBackend,
  saveWorkflowTemplate as saveWorkflowTemplateBackend,
  startDag as startDagBackend,
  terminateDagRun as terminateDagRunBackend,
  writeWorkflowMaterial as writeWorkflowMaterialBackend,
} from '../../../shared/api/backendApi.js';
import { sessionApi } from '../../../shared/api/sessionApi.js';

/*
 * workflow page service 只是把页面动作转给 backendApi。
 * 数据整理、缓存刷新和错误文案都留在 WorkflowPage。
 */

export function applyDagOps(payload) {
  return applyDagOpsBackend(payload);
}

export function createAndStartDag(payload) {
  return createAndStartDagBackend(payload);
}

export function deleteDag(payload) {
  return deleteDagBackend(payload);
}

export function dispatchDagNode(payload) {
  return dispatchDagNodeBackend(payload);
}

export function getDashboardPage(payload) {
  return getDashboardPageBackend(payload);
}

export function getDagDetail(payload) {
  return getDagDetailBackend(payload);
}

export function getDagRun(payload) {
  return getDagRunBackend(payload);
}

export function getDagRuns(payload) {
  return getDagRunsBackend(payload);
}

export function getWorkflowTemplate(payload) {
  return getWorkflowTemplateBackend(payload);
}

export function listWorkflowTemplates(payload) {
  return listWorkflowTemplatesBackend(payload);
}

export function renderWorkflowTemplateDraft(payload) {
  return renderWorkflowTemplateDraftBackend(payload);
}

export function rollbackWorkflowTemplate(payload) {
  return rollbackWorkflowTemplateBackend(payload);
}

export function saveWorkflowTemplate(payload) {
  return saveWorkflowTemplateBackend(payload);
}

export function openSharedFile(payload) {
  return openSharedFileBackend(payload);
}

export function readSharedFile(payload) {
  return readSharedFileBackend(payload);
}

export function startDag(payload) {
  return startDagBackend(payload);
}

export function startThread(payload) {
  return sessionApi.start(payload);
}

export function startTurn(payload) {
  return sessionApi.startTurn(payload);
}

export function terminateDagRun(payload) {
  return terminateDagRunBackend(payload);
}

export function writeWorkflowMaterial(payload) {
  return writeWorkflowMaterialBackend(payload);
}
