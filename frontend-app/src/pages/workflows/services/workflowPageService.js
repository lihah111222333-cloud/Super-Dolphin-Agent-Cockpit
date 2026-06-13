import {
  applyDagOps as applyDagOpsBackend,
  deleteDag as deleteDagBackend,
  dispatchDagNode as dispatchDagNodeBackend,
  getDashboardPage as getDashboardPageBackend,
  getDagDetail as getDagDetailBackend,
  getDagRun as getDagRunBackend,
  getDagRuns as getDagRunsBackend,
  openSharedFile as openSharedFileBackend,
  readSharedFile as readSharedFileBackend,
  startDag as startDagBackend,
  startThread as startThreadBackend,
  terminateDagRun as terminateDagRunBackend,
} from '../../../shared/api/backendApi.js';

/*
 * workflow page service 只是把页面动作转给 backendApi。
 * 数据整理、缓存刷新和错误文案都留在 WorkflowPage。
 */

export function applyDagOps(payload) {
  return applyDagOpsBackend(payload);
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
  return startThreadBackend(payload);
}

export function terminateDagRun(payload) {
  return terminateDagRunBackend(payload);
}
