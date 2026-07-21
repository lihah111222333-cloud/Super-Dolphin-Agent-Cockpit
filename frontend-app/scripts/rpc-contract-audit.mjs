import { fileURLToPath } from "node:url";
import { lstat, readFile, readdir, realpath } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { dirname, isAbsolute, join, normalize, relative, resolve } from "node:path";
import {
  findFrozenObjectExport,
  objectPropertiesOnly,
  parseFrontendAst,
  propertyKeyName,
  stringLiteralValue,
  traverseAst,
  unwrapObjectFreezeObject,
} from "./rpc-audit/ast-parsing.mjs";
import {
  auditRpcContracts,
  astReferencesFacadeForTest,
  parseContractMatrixForTest,
  parseRpcMethodsForTest,
} from "./rpc-audit/audit-runner.mjs";
import { assertAuditPasses, formatRpcAuditReport, runRpcAuditCli } from "./rpc-audit/report.mjs";

export {
  auditRpcContracts,
  astReferencesFacadeForTest,
  assertAuditPasses,
  formatRpcAuditReport,
  parseContractMatrixForTest,
  parseRpcMethodsForTest,
};
export {
  dirname,
  fileURLToPath,
  findFrozenObjectExport,
  isAbsolute,
  join,
  lstat,
  normalize,
  objectPropertiesOnly,
  parseFrontendAst,
  propertyKeyName,
  readFile,
  readFileSync,
  readdir,
  realpath,
  relative,
  resolve,
  stringLiteralValue,
  traverseAst,
  unwrapObjectFreezeObject,
};
export * from "./rpc-audit/response-policy-locators.mjs";
export * from "./rpc-audit/ignored-result-policy-evidence.mjs";
export * from "./rpc-audit/state-dismissal-evidence.mjs";
export * from "./rpc-audit/ui-outcome-evidence.mjs";
export * from "./rpc-audit/turn-interrupt-regression-evidence.mjs";
export * from "./rpc-audit/facade-binding-provenance.mjs";
export * from "./rpc-audit/turn-interrupt-runtime-evidence.mjs";
export * from "./rpc-audit/source-index.mjs";
export * from "./rpc-audit/backend-go.mjs";
export * from "./rpc-audit/backend-go-support.mjs";
export * from "./rpc-audit/audit-evidence.mjs";
export * from "./rpc-audit/audit-config.mjs";

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const report = await auditRpcContracts();
  process.exitCode = runRpcAuditCli(report);
}
