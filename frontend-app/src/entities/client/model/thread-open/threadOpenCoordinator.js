function createThreadOpenCoordinator() {
  let nextIntentId = 0;
  let currentIntent = null;
  let generation = Object.freeze({});

  const isCurrent = (intent) => intent !== null && intent === currentIntent;
  const capture = () => generation;
  const advanceGeneration = () => {
    generation = Object.freeze({});
  };
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
    advanceGeneration();
    currentIntent = Object.freeze({
      selectionIntentId: nextIntentId,
      targetThreadId,
    });
    return currentIntent;
  };
  const cancel = (intent) => {
    if (!isCurrent(intent)) return false;
    currentIntent = null;
    advanceGeneration();
    return true;
  };
  const invalidate = () => {
    const invalidatedIntent = currentIntent;
    currentIntent = null;
    advanceGeneration();
    return invalidatedIntent;
  };
  const beginIfUnchanged = (snapshot, targetThreadId) => (
    snapshot === generation ? begin(targetThreadId) : null
  );

  return { begin, beginIfUnchanged, cancel, capture, invalidate, isCurrent, canReleaseTarget };
}

export { createThreadOpenCoordinator };
