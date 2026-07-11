function createThreadOpenCoordinator() {
  let nextIntentId = 0;
  let currentIntent = null;

  const isCurrent = (intent) => intent !== null && intent === currentIntent;
  const canReleaseTarget = (intent) => intent !== null && (
    isCurrent(intent)
    || currentIntent === null
    || currentIntent.targetThreadId !== intent.targetThreadId
  );
  const begin = (targetThreadId) => {
    if (typeof targetThreadId !== 'string' || !targetThreadId.trim()) {
      throw new Error('thread open intent target is required');
    }
    if (nextIntentId >= Number.MAX_SAFE_INTEGER) {
      throw new Error('thread open intent id exhausted');
    }
    nextIntentId += 1;
    currentIntent = Object.freeze({
      selectionIntentId: nextIntentId,
      targetThreadId,
    });
    return currentIntent;
  };
  const cancel = (intent) => {
    if (!isCurrent(intent)) return false;
    currentIntent = null;
    return true;
  };
  const invalidate = () => {
    const invalidatedIntent = currentIntent;
    currentIntent = null;
    return invalidatedIntent;
  };

  return { begin, cancel, invalidate, isCurrent, canReleaseTarget };
}

export { createThreadOpenCoordinator };
