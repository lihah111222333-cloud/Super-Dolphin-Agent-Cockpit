const FAILURE_FIELDS = [
  ["Missing registry keys", "missingRegistryKeys"],
  ["Registry entries without RPC_METHODS", "registryWithoutRpcMethods"],
  ["Mismatched registry methods", "mismatchedRegistryMethods"],
  ["P0 methods missing Go handlers", "p0MissingBackendHandlers"],
  ["Allowed payload registry drift", "allowedPayloadRegistryDrift"],
  ["Hardcoded payload guards", "hardcodedPayloadGuardFindings"],
  ["Sidebar required field drift", "sidebarRequiredFieldFindings"],
  ["Missing response policies", "missingResponsePolicies"],
  ["Missing frontend response validators", "missingFrontendResponseValidators"],
  ["Invalid facade locators", "invalidFacadeLocators"],
  ["Invalid response policy evidence", "invalidResponsePolicyEvidence"],
];

export function formatRpcAuditReport(report) {
  return [
    `RPC methods: ${report.rpcMethods.length}`,
    `Contract registry entries: ${report.registryEntries.length}`,
    `Go backend handlers: ${report.backendHandlers.length}`,
    ...FAILURE_FIELDS.map(([label, field]) => `${label}: ${report[field].length}`),
  ].join("\n");
}

export function assertAuditPasses(report) {
  if (collectFailures(report).length > 0) throw new Error(formatRpcAuditReport(report));
}

export function runRpcAuditCli(report) {
  console.log(formatRpcAuditReport(report));
  const failures = collectFailures(report);
  if (failures.length === 0) return 0;
  for (const [title, values] of failures) {
    console.error(`\n${title}:`);
    console.error(JSON.stringify(values, null, 2));
  }
  return 1;
}

function collectFailures(report) {
  return FAILURE_FIELDS.map(([title, field]) => [title, report[field]]).filter(
    ([, values]) => values.length > 0,
  );
}
