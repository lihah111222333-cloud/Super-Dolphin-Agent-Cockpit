function strictPositiveApprovalRequestId(value) {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : 0;
}

function positiveApprovalRequestIdFromFields(source, keys = ['requestId', 'request_id']) {
  if (!source || typeof source !== 'object') return 0;
  for (const key of keys) {
    if (!Object.prototype.hasOwnProperty.call(source, key)) continue;
    return strictPositiveApprovalRequestId(source[key]);
  }
  return 0;
}

function approvalTextFromFields(source, keys, fieldName, context) {
  let value;
  let present = false;
  for (const key of keys) {
    if (!Object.prototype.hasOwnProperty.call(source, key)) continue;
    const raw = source[key];
    let normalized = '';
    if (raw !== undefined && raw !== null && raw !== '') {
      if (typeof raw !== 'string' || !raw.trim()) {
        throw new TypeError(`${context}: ${fieldName} must be a non-empty string`);
      }
      normalized = raw.trim();
    }
    if (present && value !== normalized) {
      throw new Error(`${context}: conflicting ${fieldName} values`);
    }
    value = normalized;
    present = true;
  }
  return { present, value: present ? value : '' };
}

function normalizedApprovalRequestId(raw, context) {
  if (raw === undefined || raw === null || raw === '' || raw === 0) return 0;
  const normalized = strictPositiveApprovalRequestId(raw);
  if (normalized <= 0) {
    throw new TypeError(`${context}: requestId must be a positive integer`);
  }
  return normalized;
}

function approvalRequestIdDetails(source, context) {
  let value;
  let present = false;
  for (const key of ['requestId', 'request_id']) {
    if (!Object.prototype.hasOwnProperty.call(source, key)) continue;
    const normalized = normalizedApprovalRequestId(source[key], context);
    if (present && value !== normalized) {
      throw new Error(`${context}: conflicting requestId values`);
    }
    value = normalized;
    present = true;
  }
  return { present, value: value || 0 };
}

function approvalIdentityFromFields(source, context = 'approval') {
  if (!source || typeof source !== 'object' || Array.isArray(source)) {
    throw new TypeError(`${context}: approval identity must be an object`);
  }
  const sessionScope = approvalTextFromFields(source, ['sessionScope', 'session_scope'], 'sessionScope', context);
  const callId = approvalTextFromFields(source, ['callId', 'call_id'], 'callId', context);
  const requestId = approvalRequestIdDetails(source, context);
  return {
    sessionScope: sessionScope.value,
    callId: callId.value,
    requestId: requestId.value,
    complete: Boolean(sessionScope.value && callId.value && requestId.value > 0),
    present: sessionScope.present || callId.present || requestId.present,
  };
}

function requireApprovalIdentity(source, context = 'approval') {
  const identity = approvalIdentityFromFields(source, context);
  if (!identity.sessionScope) throw new Error(`${context}: sessionScope is required`);
  if (!identity.callId) throw new Error(`${context}: callId is required`);
  if (identity.requestId <= 0) throw new Error(`${context}: requestId is required`);
  return {
    sessionScope: identity.sessionScope,
    callId: identity.callId,
    requestId: identity.requestId,
  };
}

function approvalIdentityKey(source) {
  const identity = requireApprovalIdentity(source);
  return JSON.stringify([identity.sessionScope, identity.callId, identity.requestId]);
}

export {
  approvalIdentityFromFields,
  approvalIdentityKey,
  positiveApprovalRequestIdFromFields,
  requireApprovalIdentity,
  strictPositiveApprovalRequestId,
};
