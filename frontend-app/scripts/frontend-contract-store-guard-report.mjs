import { ratchetLimits } from './frontend-contract-store-guard-rules.mjs';

export function summarizeContractStoreGuardViolations(violations) {
  const counts = new Map();
  for (const violation of violations) counts.set(violation.kind, (counts.get(violation.kind) || 0) + 1);
  return counts;
}

export function contractStoreGuardRatchetFailures(violations, limits = ratchetLimits) {
  const counts = summarizeContractStoreGuardViolations(violations);
  return Object.entries(limits).map(([kind, limit]) => ({ kind, count: counts.get(kind) || 0, limit })).filter((item) => item.count > item.limit);
}

export function reportContractStoreGuard(violations) {
  const failures = contractStoreGuardRatchetFailures(violations);
  if (failures.length > 0) {
    console.error('frontend contract/store guard failed:');
    for (const failure of failures) {
      console.error(`- ${failure.kind}: ${failure.count} exceeds ratchet limit ${failure.limit}`);
      for (const violation of violations.filter((item) => item.kind === failure.kind).slice(0, 20)) console.error(`  ${violation.file}:${violation.line} ${violation.snippet}`);
    }
    return 1;
  }
  const counts = summarizeContractStoreGuardViolations(violations);
  const summary = Object.entries(ratchetLimits).map(([kind, limit]) => `${kind}=${counts.get(kind) || 0}/${limit}`).join(', ');
  console.log(`frontend contract/store guard passed: ${summary}`);
  return 0;
}
