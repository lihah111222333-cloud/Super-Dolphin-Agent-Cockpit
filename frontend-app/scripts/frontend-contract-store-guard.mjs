import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { contractStoreGuardViolationsFromSources, contractStoreGuardViolationsInSource } from './frontend-contract-store-guard-ast.mjs';
import { collectContractStoreGuardViolations } from './frontend-contract-store-guard-source.mjs';
import { contractStoreGuardRatchetFailures, reportContractStoreGuard, summarizeContractStoreGuardViolations } from './frontend-contract-store-guard-report.mjs';

export {
  collectContractStoreGuardViolations,
  contractStoreGuardRatchetFailures,
  contractStoreGuardViolationsFromSources,
  contractStoreGuardViolationsInSource,
  summarizeContractStoreGuardViolations,
};

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  process.exitCode = reportContractStoreGuard(collectContractStoreGuardViolations());
}
