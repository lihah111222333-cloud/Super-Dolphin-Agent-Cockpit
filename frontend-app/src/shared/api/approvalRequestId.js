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

export { positiveApprovalRequestIdFromFields, strictPositiveApprovalRequestId };
