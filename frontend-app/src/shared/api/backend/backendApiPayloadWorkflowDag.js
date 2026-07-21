import { RPC_METHODS } from './backendRpcMethods.js';
import {
  assertPlainObject,
  cleanObject,
  hasOwn,
  normalizeString,
  optionalPayloadObject,
  requireKey,
} from './backendApiCommon.js';

/** @param {string} method @param {unknown} params @param {string} key */
function requireNumber(method, params, key) {
  const payload = assertPlainObject(method, params);
  if (!hasOwn(payload, key) || payload[key] === null || payload[key] === '') {
    throw new Error(`${method}: ${key} is required`);
  }
  const value = Number(payload[key]);
  if (!Number.isFinite(value)) throw new Error(`${method}: ${key} must be a number`);
  return { ...payload, [key]: value };
}

/** @param {unknown} value @returns {number | undefined} */
function optionalInteger(value) {
  if (value === undefined || value === null || value === '') return undefined;
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return undefined;
  return Math.trunc(parsed);
}

/** @param {unknown} params */
function dashboardDagStartPayload(params) {
  const method = RPC_METHODS.DASHBOARD_DAG_START;
  const payload = requireKey(method, assertPlainObject(method, params), 'dagKey');
  return cleanObject({
    dagKey: payload.dagKey,
    triggerSource: normalizeString(payload.triggerSource),
    idempotencyKey: normalizeString(payload.idempotencyKey),
  });
}

/** @param {unknown} params */
function dashboardDagCreateAndStartPayload(params) {
  const method = RPC_METHODS.DASHBOARD_DAG_CREATE_AND_START;
  const payload = requireKey(
    method,
    requireKey(method, assertPlainObject(method, params), 'dagKey'),
    'title',
  );
  if (!Array.isArray(payload.nodes) || payload.nodes.length === 0) {
    throw new Error(`${method}: nodes must be a non-empty array`);
  }
  if (payload.metadata != null && (typeof payload.metadata !== 'object' || Array.isArray(payload.metadata))) {
    throw new Error(`${method}: metadata must be an object`);
  }
  return cleanObject({
    dagKey: payload.dagKey,
    title: payload.title,
    description: normalizeString(payload.description),
    finalNodeKey: normalizeString(payload.finalNodeKey || payload.final_node_key),
    metadata: optionalPayloadObject(payload.metadata),
    nodes: payload.nodes,
    idempotencyKey: normalizeString(payload.idempotencyKey),
  });
}

/** @param {unknown} params */
function dashboardDagDispatchNodePayload(params) {
  const method = RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE;
  const payload = requireNumber(
    method,
    requireKey(
      method,
      requireKey(method, assertPlainObject(method, params), 'dagKey'),
      'nodeKey',
    ),
    'runId',
  );
  const assignedTo = normalizeString(payload.assignedTo || payload.assigned_to);
  if (!assignedTo) throw new Error(`${method}: assignedTo is required`);
  return {
    dagKey: payload.dagKey,
    runId: payload.runId,
    nodeKey: payload.nodeKey,
    assignedTo,
  };
}

/** @param {unknown} params */
function dashboardDagsPayload(params = {}) {
  const payload = assertPlainObject(RPC_METHODS.DASHBOARD_DAGS, params);
  return cleanObject({
    keyword: normalizeString(payload.keyword),
    status: normalizeString(payload.status),
    limit: optionalInteger(payload.limit),
  });
}

/** @param {unknown} params */
function dashboardDagRunsPayload(params) {
  const method = RPC_METHODS.DASHBOARD_DAG_RUNS;
  const payload = requireKey(method, assertPlainObject(method, params), 'dagKey');
  return cleanObject({
    dagKey: payload.dagKey,
    status: normalizeString(payload.status),
    limit: optionalInteger(payload.limit),
  });
}

/** @param {unknown} params */
function dashboardDagTerminatePayload(params) {
  const method = RPC_METHODS.DASHBOARD_DAG_TERMINATE;
  const payload = requireKey(
    method,
    requireKey(method, assertPlainObject(method, params), 'dagKey'),
    'runKey',
  );
  return cleanObject({
    dagKey: payload.dagKey,
    runKey: payload.runKey,
    reason: normalizeString(payload.reason),
  });
}

/** @param {unknown} params */
function dashboardDagApplyOpsPayload(params) {
  const method = RPC_METHODS.DASHBOARD_DAG_APPLY_OPS;
  const payload = requireNumber(
    method,
    requireKey(method, assertPlainObject(method, params), 'dagKey'),
    'baseVersion',
  );
  if (!Array.isArray(payload.ops)) throw new Error(`${method}: ops must be an array`);
  return {
    dagKey: payload.dagKey,
    baseVersion: payload.baseVersion,
    ops: payload.ops,
  };
}

export {
  requireNumber,
  optionalInteger,
  dashboardDagStartPayload,
  dashboardDagCreateAndStartPayload,
  dashboardDagDispatchNodePayload,
  dashboardDagsPayload,
  dashboardDagRunsPayload,
  dashboardDagTerminatePayload,
  dashboardDagApplyOpsPayload,
};
