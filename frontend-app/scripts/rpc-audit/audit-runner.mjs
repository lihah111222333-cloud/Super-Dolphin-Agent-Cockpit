import { lstat, readFile, readdir, realpath } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { dirname, isAbsolute, join, normalize, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { collectFrontendPayloadKeysFromSource as discoverFrontendPayloadKeys } from "./frontend-payload-discovery.mjs";
import { assertAuditPasses, formatRpcAuditReport, runRpcAuditCli } from "./report.mjs";
import {
  findFrozenObjectExport,
  objectPropertiesOnly,
  parseFrontendAst,
  propertyKeyName,
  stringLiteralValue,
  traverseAst,
  unwrapObjectFreezeObject,
} from "./ast-parsing.mjs";
import {
  collectResponseValidatorFindings,
  parseContractMatrix,
  parseRpcMethods,
} from "./registry.mjs";

import {
  collectInvalidFacadeLocators,
  collectInvalidResponsePolicyEvidence,
  responsePolicyRpcMethod,
  resolvePolicyLocator,
  pathContainsSymbolicLink,
  readAuditSource,
  readAuditSourceSync,
  readAuditAst,
  policyFinding,
  comparePolicyFindings,
  findProductionSymbol,
  findResponsePolicyConsumerSymbol,
  findModulePrivateFunctionSymbol,
  findModuleLevelSymbol,
} from "./response-policy-locators.mjs";
import {
  hasRegressionTestEvidence,
  hasDirectFacadeIgnoredResultRegressionEvidence,
  collectRejectedMockBindings,
  nodeCallsIdentifier,
  publishedCallbackProductionProof,
  nestedFunctionBetween,
  mapLocalPathToConsumerParameter,
  pathsEqual,
  callOccursLaterInSameSuccessBlock,
  hasPublishedCallbackRegressionEvidence,
  callbackStatementIndex,
  equivalentConsumerArgumentPaths,
  bodyHasStaticRootDeclaration,
} from "./ignored-result-policy-evidence.mjs";
import {
  collectExactResolvedMalformedMocks,
  exactSpyMatcher,
  exactUndefinedResultAssertion,
  collectIgnoredResultConsumerOutcomeProof,
  memberExpressionPath,
  collectPostCallStateDismissals,
  stateSetterCallee,
  resolveDismissedStateUiDescriptors,
  frontendProductionAstSources,
  findExactStateSetterBindings,
  resolveMemberObjectStateOwners,
  resolveUniqueFunctionCallArgument,
  resolveStateOwnerExpression,
  stateSetterBindingsInOwner,
  functionReturnsStatePair,
  collectStateControlledUiDescriptors,
  returnedStateObjectNamesByPath,
  functionContainsMemberCall,
  functionAcceptsProperty,
} from "./state-dismissal-evidence.mjs";
import {
  nodeContainsStateAccess,
  nodeContainsJsxElement,
  nodeContainsAlias,
  uiDescriptorsFromControlledNode,
  collectJsxVisibleTextValues,
  jsxStaticAttribute,
  jsxStaticAttributeValues,
  intrinsicJsxRole,
  findUniqueFunctionDefinition,
  collectStaticTextValues,
  resolveStaticValueNode,
  findModuleFunctionDeclaration,
  findModuleVariableInitializer,
  isDismissingStateValue,
  hasPageIgnoredResultRegressionEvidence,
  malformedFacadeMockReceiver,
  hasMalformedSentinel,
  statementContainsPageTrigger,
  statementContainsExactFacadeInvocationAssertion,
  statementContainsMatchedUiOutcomeAssertion,
  exactScreenQueryDescriptor,
  nodeContainsNode,
  hasResultHandledRegressionEvidence,
} from "./ui-outcome-evidence.mjs";
import {
  hasRuntimeResultHandledRegressionEvidence,
  isExactFactoryDeclaration,
  isExactFailureRpcDeclaration,
  isExactInterruptTimeoutResult,
  isExactRuntimeAttachSetup,
  isExpectAssertionStatement,
  isExactInterruptFailureAwait,
  exactExpectMatcher,
  isExactInterruptWarningAssertion,
  isExactInterruptNoSuccessAssertion,
  isExactInterruptNoPendingAssertion,
  isExactInterruptAddWarningAssertion,
  isExactInterruptRequestAssertion,
  isStaticMemberNamed,
  isRuntimeMember,
  hasExactStringArguments,
  isExactStringObject,
  hasMalformedFacadeMock,
  isSpecificShapeFailureMatcher,
  functionReturnedObject,
  findMockResolvedValueArgument,
  isMalformedResponseLiteral,
  memberChainContainsName,
  hasNonTestRunnerBinding,
  moduleSpecifierResolvesTo,
  moduleSpecifierResolvedPath,
  findFacadeCalls,
  directFacadeCallProvenance,
  promoteTransparentPromiseWrapperCall,
  transparentPromiseWrapperAt,
  directFacadeRuntimeCallMatches,
  resolveImportedWrapperProvenance,
} from "./turn-interrupt-regression-evidence.mjs";
import {
  wrapperTransparentlyReturnsCall,
  resolveImportedCallTarget,
  findImportedBinding,
  findExportedSymbolPath,
  collectFacadeCallBindings,
  symbolBindsName,
  facadeCallMatchesBindings,
  bindingShadowsNameAt,
  isFunctionNode,
  blockDirectlyBindsName,
  walkAstWithAncestors,
  isIgnoredCallResult,
  isExactTurnInterruptPolicy,
  hasExactTurnInterruptTimeoutHandler,
  isExactTurnInterruptTimeoutPredicate,
  isExactResultMember,
  isExactInterruptTimeoutBranch,
  isExactInterruptTimeoutNotice,
  isExactInterruptTimeoutWarning,
  isExactActionUnconfirmedTemplate,
  isExactThreadIdObject,
  provesTurnInterruptInjection,
  runtimePassesAwaitedResultToHandler,
} from "./facade-binding-provenance.mjs";
import {
  runtimePassesStrictInterruptResultToHandler,
  isExactActionLiteralMatch,
  isExactNamedCall,
  countRuntimeProofBindings,
  hasRuntimeProofParameters,
  isExactOutcomeFailureGate,
  isExactRuntimeCwdDeclaration,
  isExactRuntimeCurrentStateDeclaration,
  isExactRuntimeRequiresActiveTurnDeclaration,
  isExactRuntimeActiveTurnTargetDeclaration,
  isExactRuntimeThreadIdDeclaration,
  isExactRuntimeNoThreadGuard,
  isExactRuntimePayloadDeclaration,
  isExactRuntimePayloadFailureGuard,
  isExactRuntimeSuccessStatement,
  isExactHandlerFailureGate,
  isExactRuntimeHandlerArgument,
  isExactRuntimeOutcomeReturn,
  consumerPassesFacadeResultToHandler,
} from "./turn-interrupt-runtime-evidence.mjs";
import {
  hasExactGenericInterruptFallback,
  isExactActionFailedTemplate,
  isExactGenericWarningFields,
  handlerDirectlyInspectsEnvelope,
  isExactInterruptFailurePredicate,
  moduleHelperReturnsResultError,
  hasExecutableShapeNarrowing,
  isAlwaysFalseExpression,
  containsDirectThrow,
  isSupportedInvalidPredicate,
  expressionRootsInTaint,
  parserCallProvesNarrowing,
  resolveLocalSchemaMethod,
  safeParseImplementationIsProven,
  objectBooleanProperty,
  safeParseFailureDominates,
  nodeContainsIdentifier,
  shapeDominatesConsumerUse,
  collectUnusedPolicyFindings,
} from "./source-index.mjs";
import {
  buildProductionFacadeReferenceIndex,
  collectFacadeReExportPaths,
  listJavaScriptSourceFiles,
  isExcludedProductionScanPath,
  staticMemberExpressionParts,
  astReferencesFacade,
  isReferencedIdentifierAt,
  collectNamedExports,
  collectDeclarationBindingNames,
  collectBindingNames,
  collectBackendFacadeRpcKeys,
  collectRpcMethodReferenceKeysWithHelpers,
} from "./backend-go.mjs";
import {
  collectRpcMethodReferenceKeys,
  staticPropertyKeyName,
  sourceDeclaresFunction,
  sourceContainsStringLiteral,
  serviceFacadeMemberRpcKey,
  assertRpcMethodsFacadeReExport,
  moduleExportName,
  declarationBindsName,
  bindingPatternContainsName,
  collectFrontendResponseValidators,
  collectGoPayloadKeys,
  parseGoStructJSONTags,
  parseRequiredGoStructJSONTags,
  parseSidebarRuntimeRequiredFields,
  hasSidebarRuntimeRequiredCheck,
  isMissingSidebarFieldCheck,
  collectHardcodedPayloadGuardFindings,
} from "./backend-go-support.mjs";
import {
  uniqueSorted,
  collectGoRpcHandlers,
  collectGoRpcConstants,
  collectGoFiles,
  parseLiteralHandlerRegistrations,
  parseConstantHandlerRegistrations,
  handlerEntry,
  uniqueHandlers,
} from "./audit-evidence.mjs";
import {
  collectSidebarRequiredFieldFindingsFromSources,
  collectHardcodedPayloadGuardFindingsFromSources,
  collectPayloadRegistryDrift,
} from "./backend-go-support.mjs";
export { assertAuditPasses, formatRpcAuditReport };
export {
  collectHardcodedPayloadGuardFindingsFromSources,
  collectPayloadRegistryDrift,
  collectSidebarRequiredFieldFindingsFromSources,
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
export {
  parseContractMatrix as parseContractMatrixForTest,
  parseRpcMethods as parseRpcMethodsForTest,
};

import {
  DEFAULT_REPO_ROOT,
  RPC_METHODS_PATH,
  RPC_FACADE_PATH,
  FRONTEND_PAYLOAD_BUILDERS_PATH,
  RPC_MATRIX_PATH,
  RPC_RESPONSE_VALIDATORS_PATH,
  RPC_RESPONSE_VALIDATORS_RUNTIME_PATH,
  SIDEBAR_GO_STATE_PATH,
  GO_PAYLOAD_STRUCTS,
  FRONTEND_PAYLOAD_METHOD_EXEMPTIONS,
  FRONTEND_FACADE_ONLY_PAYLOAD_KEYS,
  collectFrontendPayloadKeysFromSource,
} from "./audit-config.mjs";

export async function auditRpcContracts({ repoRoot = DEFAULT_REPO_ROOT } = {}) {
  repoRoot = await realpath(repoRoot);
  const auditContext = {
    repoRoot,
    sourceByPath: new Map(),
    sourcePromiseByPath: new Map(),
    astByPath: new Map(),
    astPromiseByPath: new Map(),
    productionFacadeReferenceIndex: null,
    auditStats: {
      sourceReads: 0,
      astParses: 0,
      productionFacadeReferenceIndexBuilds: 0,
      productionSourceFilesIndexed: 0,
    },
  };
  const [
    rpcMethodsSource,
    frontendSource,
    payloadBuildersSource,
    matrixSource,
    responseValidatorSource,
  ] = await Promise.all([
    readAuditSource(auditContext, RPC_METHODS_PATH),
    readAuditSource(auditContext, RPC_FACADE_PATH),
    readAuditSource(auditContext, FRONTEND_PAYLOAD_BUILDERS_PATH),
    readAuditSource(auditContext, RPC_MATRIX_PATH),
    readAuditSource(auditContext, RPC_RESPONSE_VALIDATORS_PATH),
  ]);
  assertRpcMethodsFacadeReExport(frontendSource);
  const rpcMethods = parseRpcMethods(rpcMethodsSource);
  const methodsByKey = new Map(rpcMethods.map((entry) => [entry.key, entry]));
  const parsedRegistryEntries = parseContractMatrix(matrixSource);
  const registryEntries = parsedRegistryEntries.map((entry) => ({
    ...entry,
    method: entry.methodReferenceKey
      ? (methodsByKey.get(entry.methodReferenceKey)?.method ?? "")
      : entry.method,
  }));
  const [
    backendHandlers,
    goPayloadKeysByMethod,
    hardcodedPayloadGuardFindings,
    backendFacadeRpcKeys,
  ] = await Promise.all([
    collectGoRpcHandlers(auditContext),
    collectGoPayloadKeys(auditContext),
    collectHardcodedPayloadGuardFindings(auditContext, payloadBuildersSource),
    collectBackendFacadeRpcKeys(auditContext),
  ]);
  const frontendPayloadKeysByMethod = collectFrontendPayloadKeysFromSource(
    payloadBuildersSource,
    new Map(rpcMethods.map((entry) => [entry.key, entry.method])),
    new Set(
      [...GO_PAYLOAD_STRUCTS.keys()].filter(
        (method) =>
          !FRONTEND_PAYLOAD_METHOD_EXEMPTIONS.has(method) &&
          rpcMethods.some((entry) => entry.method === method),
      ),
    ),
  );
  const [sidebarGoSource, sidebarRuntimeSource] = await Promise.all([
    readAuditSource(auditContext, SIDEBAR_GO_STATE_PATH),
    readAuditSource(auditContext, RPC_RESPONSE_VALIDATORS_RUNTIME_PATH),
  ]);
  const sidebarRequiredFieldFindings = collectSidebarRequiredFieldFindingsFromSources({
    goSource: sidebarGoSource,
    runtimeSource: sidebarRuntimeSource,
  });

  const registryByKey = new Map(registryEntries.map((entry) => [entry.key, entry]));
  const handlerMethods = new Set(backendHandlers.map((entry) => entry.method));
  const frontendResponseValidators = collectFrontendResponseValidators(responseValidatorSource);
  const responseContractStrategies = registryEntries
    .concat(rpcMethods.filter((entry) => !registryByKey.has(entry.key)))
    .map((entry) => ({
      key: entry.key,
      method: entry.method,
      matrixPolicy: entry.responseValidator || entry.responsePassthroughReason || "",
      frontendValidator: frontendResponseValidators.has(entry.key),
    }));

  const missingRegistryKeys = rpcMethods
    .filter((entry) => !registryByKey.has(entry.key))
    .map((entry) => entry.key)
    .sort();
  const registryWithoutRpcMethods = registryEntries
    .filter((entry) => !methodsByKey.has(entry.key))
    .map((entry) => entry.key)
    .sort();
  const mismatchedRegistryMethods = parsedRegistryEntries
    .filter((entry) => methodsByKey.has(entry.key))
    .filter(
      (entry) => !entry.methodReferenceKey && entry.method !== methodsByKey.get(entry.key).method,
    )
    .map((entry) => ({
      key: entry.key,
      registryMethod: entry.method,
      rpcMethod: methodsByKey.get(entry.key).method,
    }))
    .sort((a, b) => a.key.localeCompare(b.key));
  const p0MissingBackendHandlers = registryEntries
    .filter((entry) => entry.level === "P0" && !handlerMethods.has(entry.method))
    .map((entry) => ({
      key: entry.key,
      method: entry.method,
    }));
  const allowedPayloadRegistryDrift = collectPayloadRegistryDrift(
    goPayloadKeysByMethod,
    frontendPayloadKeysByMethod,
  );
  const missingResponsePolicies = registryEntries
    .filter((entry) => entry.level === "P0" || entry.level === "P1")
    .filter((entry) => !entry.responseValidator.trim() && !entry.responsePolicy)
    .map((entry) => ({
      key: entry.key,
      method: entry.method,
    }));
  const missingFrontendResponseValidators = collectResponseValidatorFindings(
    registryEntries,
    frontendResponseValidators,
  );
  const invalidFacadeLocators = await collectInvalidFacadeLocators(
    auditContext,
    registryEntries,
    frontendSource,
    backendFacadeRpcKeys,
  );
  const invalidResponsePolicyEvidence = await collectInvalidResponsePolicyEvidence(
    auditContext,
    registryEntries,
    backendFacadeRpcKeys,
  );

  return {
    rpcMethods,
    registryEntries,
    backendHandlers,
    missingRegistryKeys,
    registryWithoutRpcMethods,
    mismatchedRegistryMethods,
    p0MissingBackendHandlers,
    goPayloadKeysByMethod,
    frontendPayloadKeysByMethod,
    allowedPayloadRegistryDrift,
    hardcodedPayloadGuardFindings,
    sidebarRequiredFieldFindings,
    missingResponsePolicies,
    responseContractStrategies,
    missingFrontendResponseValidators,
    invalidFacadeLocators,
    invalidResponsePolicyEvidence,
    auditStats: { ...auditContext.auditStats },
  };
}

export const astReferencesFacadeForTest = astReferencesFacade;
