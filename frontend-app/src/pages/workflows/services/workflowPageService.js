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

function applyDagOps(payload) { return applyDagOpsBackend(payload); }

function createAndStartDag(payload) { return createAndStartDagBackend(payload); }

function deleteDag(payload) {
  return deleteDagBackend(payload);
}

function dispatchDagNode(payload) {
  return dispatchDagNodeBackend(payload);
}

function getDashboardPage(payload) {
  return getDashboardPageBackend(payload);
}

function getDagDetail(payload) {
  return getDagDetailBackend(payload);
}

function getDagRun(payload) {
  return getDagRunBackend(payload);
}

function getDagRuns(payload) {
  return getDagRunsBackend(payload);
}

function getWorkflowTemplate(payload) {
  return getWorkflowTemplateBackend(payload);
}

function listWorkflowTemplates(payload) {
  return listWorkflowTemplatesBackend(payload);
}

function renderWorkflowTemplateDraft(payload) {
  return renderWorkflowTemplateDraftBackend(payload);
}

function rollbackWorkflowTemplate(payload) {
  return rollbackWorkflowTemplateBackend(payload);
}

function saveWorkflowTemplate(payload) {
  return saveWorkflowTemplateBackend(payload);
}

function openSharedFile(payload) {
  return openSharedFileBackend(payload);
}

function readSharedFile(payload) {
  return readSharedFileBackend(payload);
}

function startDag(payload) {
  return startDagBackend(payload);
}

function startThread(payload) {
  return sessionApi.start(payload);
}

function startTurn(payload) {
  return sessionApi.startTurn(payload);
}

function terminateDagRun(payload) {
  return terminateDagRunBackend(payload);
}

function writeWorkflowMaterial(payload) {
  return writeWorkflowMaterialBackend(payload);
}

export {
  applyDagOps, createAndStartDag, deleteDag, dispatchDagNode, getDashboardPage, getDagDetail, getDagRun, getDagRuns, getWorkflowTemplate, listWorkflowTemplates, openSharedFile, readSharedFile, renderWorkflowTemplateDraft, rollbackWorkflowTemplate, saveWorkflowTemplate, startDag,
  startThread, startTurn, terminateDagRun, writeWorkflowMaterial,
};
