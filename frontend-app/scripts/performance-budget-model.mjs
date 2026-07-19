const SAMPLE_COUNT = 5;
const WARMUP_COUNT = 1;
const ATTEMPTS_PER_SAMPLE = 1;
const CPU_DURATION_CLOCK = 'p50(production/reference process.cpuUsage(user+system),alternating,500000-iteration-blocks)';
const FEEDBACK_DURATION_CLOCK = 'trimmed-mean-80%(performance.now(feedbackAt-startedAt))';
const RENDER_UPDATE_LIMIT = 1;
const HISTORY_REGRESSION_RATIO = 1.15;
const FEEDBACK_REGRESSION_RATIO = 1.15;
const RESOURCE_REGRESSION_RATIO = 1.05;

function requireFiniteNonNegative(value, label) {
  if (!Number.isFinite(value) || value < 0) {
    throw new TypeError(`${label} must be a finite non-negative number`);
  }
  return value;
}

function requirePositive(value, label) {
  if (!Number.isFinite(value) || value <= 0) {
    throw new TypeError(`${label} must be a finite positive number`);
  }
  return value;
}

function measurementMedian(values, label = 'measurements') {
  if (!Array.isArray(values) || values.length === 0) {
    throw new TypeError(`${label} must be non-empty`);
  }
  const sorted = values.map((value, index) => requireFiniteNonNegative(value, `${label}[${index}]`))
    .sort((left, right) => left - right);
  return sorted[Math.floor(sorted.length / 2)];
}

function measurementTrimmedMean(values, label = 'measurements') {
  if (!Array.isArray(values) || values.length < 5) {
    throw new TypeError(`${label} must contain at least 5 measurements`);
  }
  const sorted = values.map((value, index) => requireFiniteNonNegative(value, `${label}[${index}]`))
    .sort((left, right) => left - right);
  const trimCount = Math.floor(sorted.length * 0.1);
  const retained = sorted.slice(trimCount, sorted.length - trimCount);
  return retained.reduce((total, value) => total + value, 0) / retained.length;
}

function median(values, label = 'samples') {
  if (!Array.isArray(values) || values.length !== SAMPLE_COUNT) {
    throw new TypeError(`${label} must contain exactly ${SAMPLE_COUNT} samples`);
  }
  return measurementMedian(values, label);
}

function requireSubjectSha(subjectSha, currentSha) {
  if (!/^[0-9a-f]{40}$/.test(subjectSha || '')) {
    throw new TypeError('subject must be a full 40-character Git SHA');
  }
  if (subjectSha !== currentSha) {
    throw new Error(`subject mismatch: expected current HEAD ${currentSha}, got ${subjectSha}`);
  }
  return subjectSha;
}

function notVerified(metricId, reason) {
  return Object.freeze({ metricId, status: 'NOT_VERIFIED', reason });
}

function fail(metricId, reason, comparisons = []) {
  return Object.freeze({ metricId, status: 'FAIL', reason, comparisons });
}

function pass(metricId, comparisons) {
  return Object.freeze({ metricId, status: 'PASS', comparisons });
}

function requireFrozenMetric(metricId, metric, { sampled = true } = {}) {
  if (!metric || metric.status !== 'PASS') {
    return notVerified(metricId, `frozen ${metricId} baseline is missing or not PASS`);
  }
  if (!/^[0-9a-f]{40}$/.test(metric.subjectSha || '')) {
    return notVerified(metricId, `frozen ${metricId} subjectSha is missing`);
  }
  if (sampled && (metric.sampleCount !== SAMPLE_COUNT || metric.warmupCount !== WARMUP_COUNT)) {
    return notVerified(metricId, `frozen ${metricId} must use ${WARMUP_COUNT} warmup and ${SAMPLE_COUNT} samples`);
  }
  return null;
}

function validateTimingCase(entry, label, durationClock) {
  if (entry?.durationClock !== durationClock
    || entry?.attemptsPerSample !== ATTEMPTS_PER_SAMPLE
    || !Number.isSafeInteger(entry.iterationCount)
    || entry.iterationCount <= 0) {
    throw new TypeError(`${label} deterministic measurement shape is invalid`);
  }
  if (!Array.isArray(entry.durationAttemptSamplesMs)
    || entry.durationAttemptSamplesMs.length !== SAMPLE_COUNT
    || !Array.isArray(entry.durationSamplesMs)
    || entry.durationSamplesMs.length !== SAMPLE_COUNT) {
    throw new TypeError(`${label} must contain ${SAMPLE_COUNT} measured samples`);
  }
  entry.durationAttemptSamplesMs.forEach((attempts, sampleIndex) => {
    if (!Array.isArray(attempts) || attempts.length !== ATTEMPTS_PER_SAMPLE) {
      throw new TypeError(`${label} sample ${sampleIndex} must contain ${ATTEMPTS_PER_SAMPLE} attempts`);
    }
    const measured = attempts.map((value, attemptIndex) => (
      requireFiniteNonNegative(value, `${label}.attempt[${sampleIndex}][${attemptIndex}]`)
    ));
    const selected = requireFiniteNonNegative(
      entry.durationSamplesMs[sampleIndex],
      `${label}.sample[${sampleIndex}]`,
    );
    if (selected !== measured[0]) {
      throw new TypeError(`${label} sample ${sampleIndex} must preserve its raw measurement`);
    }
  });
  if (entry.durationMedianMs !== median(entry.durationSamplesMs, `${label}.durationSamplesMs`)) {
    throw new TypeError(`${label} median does not match its measured samples`);
  }
}

function validatePairedTimingCase(entry, label, durationClock) {
  if (entry?.durationClock !== durationClock
    || entry?.attemptsPerSample !== ATTEMPTS_PER_SAMPLE
    || !Number.isSafeInteger(entry.blockCount)
    || entry.blockCount <= 0
    || !Number.isSafeInteger(entry.blockIterationCount)
    || entry.blockIterationCount <= 0
    || entry.iterationCount !== entry.blockCount * entry.blockIterationCount
    || !Number.isSafeInteger(entry.materializedCount)
    || entry.materializedCount <= 0
    || entry.referenceMaterializedCount !== entry.materializedCount) {
    throw new TypeError(`${label} paired measurement shape is invalid`);
  }
  if (!Array.isArray(entry.sampleDiagnostics)
    || entry.sampleDiagnostics.length !== SAMPLE_COUNT
    || !Array.isArray(entry.normalizedRatioSamples)
    || entry.normalizedRatioSamples.length !== SAMPLE_COUNT) {
    throw new TypeError(`${label} must contain ${SAMPLE_COUNT} paired samples`);
  }
  entry.sampleDiagnostics.forEach((sample, sampleIndex) => {
    const arrays = [
      sample?.blockOrders,
      sample?.productionBlockCpuDurationsMs,
      sample?.referenceBlockCpuDurationsMs,
      sample?.rawNormalizedBlockRatios,
    ];
    if (arrays.some((values) => !Array.isArray(values) || values.length !== entry.blockCount)) {
      throw new TypeError(`${label} sample ${sampleIndex} paired block evidence is incomplete`);
    }
    const recomputedRatios = sample.blockOrders.map((order, blockIndex) => {
      const expectedOrder = (sampleIndex + blockIndex) % 2 === 0
        ? 'production-reference'
        : 'reference-production';
      if (order !== expectedOrder) {
        throw new TypeError(`${label} sample ${sampleIndex} block ${blockIndex} order is invalid`);
      }
      const production = requirePositive(
        sample.productionBlockCpuDurationsMs[blockIndex],
        `${label}.production[${sampleIndex}][${blockIndex}]`,
      );
      const reference = requirePositive(
        sample.referenceBlockCpuDurationsMs[blockIndex],
        `${label}.reference[${sampleIndex}][${blockIndex}]`,
      );
      const recordedRatio = requirePositive(
        sample.rawNormalizedBlockRatios[blockIndex],
        `${label}.ratio[${sampleIndex}][${blockIndex}]`,
      );
      const recomputedRatio = production / reference;
      if (recordedRatio !== recomputedRatio) {
        throw new TypeError(`${label} sample ${sampleIndex} block ${blockIndex} ratio is not reproducible`);
      }
      return recomputedRatio;
    });
    const recomputedSample = measurementMedian(
      recomputedRatios,
      `${label}.rawNormalizedBlockRatios[${sampleIndex}]`,
    );
    const recordedSample = requirePositive(
      entry.normalizedRatioSamples[sampleIndex],
      `${label}.normalizedRatioSamples[${sampleIndex}]`,
    );
    const diagnosticSample = requirePositive(
      sample.normalizedRatio,
      `${label}.sampleDiagnostics[${sampleIndex}].normalizedRatio`,
    );
    if (recordedSample !== recomputedSample || diagnosticSample !== recomputedSample) {
      throw new TypeError(`${label} sample ${sampleIndex} normalized ratio summary is invalid`);
    }
  });
  if (entry.normalizedRatioMedian !== median(
    entry.normalizedRatioSamples,
    `${label}.normalizedRatioSamples`,
  )) {
    throw new TypeError(`${label} normalized ratio median does not match its samples`);
  }
}

function evaluatePairedMedianCases({
  baselineMetric,
  currentMetric,
  durationClock,
  metricId,
  ratioKey,
  requiredRatio,
}) {
  const missing = requireFrozenMetric(metricId, baselineMetric);
  if (missing) return missing;
  if (baselineMetric[ratioKey] !== requiredRatio) {
    return notVerified(metricId, `${ratioKey} must equal the plan ratio ${requiredRatio}`);
  }
  const baselineCases = baselineMetric.cases;
  const currentCases = currentMetric.cases;
  if (!baselineCases || !currentCases || typeof baselineCases !== 'object' || typeof currentCases !== 'object') {
    return notVerified(metricId, 'baseline and current cases are required');
  }
  const baselineNames = Object.keys(baselineCases).sort();
  const currentNames = Object.keys(currentCases).sort();
  if (JSON.stringify(baselineNames) !== JSON.stringify(currentNames) || baselineNames.length === 0) {
    return notVerified(metricId, 'baseline and current case sets must match exactly and be non-empty');
  }
  try {
    for (const caseName of baselineNames) {
      validatePairedTimingCase(
        baselineCases[caseName],
        `${metricId}.${caseName}.baseline`,
        durationClock,
      );
    }
  } catch (error) {
    return notVerified(metricId, `frozen ${metricId} paired schema is invalid: ${error.message}`);
  }
  const comparisons = baselineNames.map((caseName) => {
    const baselineCase = baselineCases[caseName];
    const currentCase = currentCases[caseName];
    validatePairedTimingCase(currentCase, `${metricId}.${caseName}.current`, durationClock);
    for (const field of [
      'blockCount',
      'blockIterationCount',
      'iterationCount',
      'materializedCount',
      'referenceMaterializedCount',
    ]) {
      if (baselineCase[field] !== currentCase[field]) {
        throw new TypeError(`${metricId}.${caseName} ${field} mismatch`);
      }
    }
    const baselineValue = requirePositive(
      baselineCase.normalizedRatioMedian,
      `${metricId}.${caseName}.baseline`,
    );
    const currentValue = requirePositive(
      currentCase.normalizedRatioMedian,
      `${metricId}.${caseName}.current`,
    );
    const threshold = baselineValue * requiredRatio;
    return Object.freeze({ case: caseName, baseline: baselineValue, current: currentValue, threshold });
  });
  const failures = comparisons.filter(({ current, threshold }) => current > threshold);
  return failures.length > 0
    ? fail(metricId, `${failures.length} case(s) exceed the frozen budget`, comparisons)
    : pass(metricId, comparisons);
}

function evaluateMedianCases({
  baselineMetric,
  currentMetric,
  durationClock,
  metricId,
  ratioKey,
  requiredRatio,
  valueKey,
}) {
  const missing = requireFrozenMetric(metricId, baselineMetric);
  if (missing) return missing;
  if (baselineMetric[ratioKey] !== requiredRatio) {
    return notVerified(metricId, `${ratioKey} must equal the plan ratio ${requiredRatio}`);
  }
  const baselineCases = baselineMetric.cases;
  const currentCases = currentMetric.cases;
  if (!baselineCases || !currentCases || typeof baselineCases !== 'object' || typeof currentCases !== 'object') {
    return notVerified(metricId, 'baseline and current cases are required');
  }
  const baselineNames = Object.keys(baselineCases).sort();
  const currentNames = Object.keys(currentCases).sort();
  if (JSON.stringify(baselineNames) !== JSON.stringify(currentNames) || baselineNames.length === 0) {
    return notVerified(metricId, 'baseline and current case sets must match exactly and be non-empty');
  }
  const comparisons = baselineNames.map((caseName) => {
    validateTimingCase(baselineCases[caseName], `${metricId}.${caseName}.baseline`, durationClock);
    validateTimingCase(currentCases[caseName], `${metricId}.${caseName}.current`, durationClock);
    if (baselineCases[caseName].iterationCount !== currentCases[caseName].iterationCount) {
      throw new TypeError(`${metricId}.${caseName} iterationCount mismatch`);
    }
    const baselineValue = requirePositive(baselineCases[caseName]?.[valueKey], `${metricId}.${caseName}.baseline`);
    const currentValue = requireFiniteNonNegative(currentCases[caseName]?.[valueKey], `${metricId}.${caseName}.current`);
    const threshold = baselineValue * requiredRatio;
    return Object.freeze({ case: caseName, baseline: baselineValue, current: currentValue, threshold });
  });
  const failures = comparisons.filter(({ current, threshold }) => current > threshold);
  return failures.length > 0
    ? fail(metricId, `${failures.length} case(s) exceed the frozen budget`, comparisons)
    : pass(metricId, comparisons);
}

function evaluateRenderIsolation(currentMetric, baselineMetric) {
  const metricId = 'P01-render-isolation';
  const missing = requireFrozenMetric(metricId, baselineMetric, { sampled: false });
  if (missing) return missing;
  if (baselineMetric.absoluteUpdateLimit !== RENDER_UPDATE_LIMIT) {
    return notVerified(metricId, `absoluteUpdateLimit must equal ${RENDER_UPDATE_LIMIT}`);
  }
  if (currentMetric.updateCount !== 20 || baselineMetric.updateCount !== 20) {
    return notVerified(metricId, 'render isolation requires exactly 20 unrelated store updates');
  }
  if (currentMetric.warmupUpdates <= 0 || baselineMetric.warmupUpdates <= 0) {
    return notVerified(metricId, 'render isolation requires a non-zero warmup update count');
  }
  if (currentMetric.mutationDetected !== true) {
    return fail(metricId, 'broad-subscription mutation was not detected');
  }
  const fields = ['mainPageUpdateCommits', 'unrelatedSubtreeUpdateCommits'];
  const comparisons = fields.map((field) => {
    const baselineValue = requireFiniteNonNegative(baselineMetric[field], `${metricId}.${field}.baseline`);
    const currentValue = requireFiniteNonNegative(currentMetric[field], `${metricId}.${field}.current`);
    return Object.freeze({
      case: field,
      baseline: baselineValue,
      current: currentValue,
      threshold: Math.min(RENDER_UPDATE_LIMIT, baselineValue),
    });
  });
  const failures = comparisons.filter(({ current, threshold }) => current > threshold);
  return failures.length > 0
    ? fail(metricId, `${failures.length} render count(s) exceed the absolute or frozen limit`, comparisons)
    : pass(metricId, comparisons);
}

function evaluateResourceBudget(currentMetric, baselineMetric) {
  const metricId = 'P04-resource-budget';
  const missing = requireFrozenMetric(metricId, baselineMetric, { sampled: false });
  if (missing) return missing;
  if (baselineMetric.maxRegressionRatio !== RESOURCE_REGRESSION_RATIO) {
    return notVerified(metricId, `maxRegressionRatio must equal the plan ratio ${RESOURCE_REGRESSION_RATIO}`);
  }
  const comparisons = ['totalBundleBytes', 'maxChunkBytes', 'heapUsedMedianBytes'].map((field) => {
    const baselineValue = requirePositive(baselineMetric[field], `${metricId}.${field}.baseline`);
    const currentValue = requirePositive(currentMetric[field], `${metricId}.${field}.current`);
    return Object.freeze({
      case: field,
      baseline: baselineValue,
      current: currentValue,
      threshold: baselineValue * RESOURCE_REGRESSION_RATIO,
    });
  });
  const failures = comparisons.filter(({ current, threshold }) => current > threshold);
  return failures.length > 0
    ? fail(metricId, `${failures.length} resource metric(s) exceed the frozen budget`, comparisons)
    : pass(metricId, comparisons);
}

export {
  ATTEMPTS_PER_SAMPLE,
  CPU_DURATION_CLOCK,
  FEEDBACK_REGRESSION_RATIO,
  FEEDBACK_DURATION_CLOCK,
  HISTORY_REGRESSION_RATIO,
  RENDER_UPDATE_LIMIT,
  RESOURCE_REGRESSION_RATIO,
  SAMPLE_COUNT,
  WARMUP_COUNT,
  evaluateMedianCases,
  evaluatePairedMedianCases,
  evaluateRenderIsolation,
  evaluateResourceBudget,
  measurementMedian,
  measurementTrimmedMean,
  median,
  requireFiniteNonNegative,
  requirePositive,
  requireSubjectSha,
};
