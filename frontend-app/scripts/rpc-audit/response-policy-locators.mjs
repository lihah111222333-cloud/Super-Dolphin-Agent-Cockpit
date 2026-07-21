import * as audit from "../rpc-contract-audit.mjs";
import {
  findResponsePolicyConsumerSymbol,
  findProductionSymbol,
  findModuleLevelSymbol,
  comparePolicyFindings,
  policyFinding,
  readAuditSource,
  responsePolicyRpcMethod,
  resolvePolicyLocator,
} from "./response-policy-resolution.mjs";

async function collectInvalidFacadeLocators(
  auditContext,
  registryEntries,
  frontendSource,
  backendFacadeRpcKeys,
) {
  const backendApiExports = audit.collectNamedExports(frontendSource);
  const serviceSources = new Map();
  const findings = [];
  for (const entry of registryEntries) {
    if ((entry.level !== "P0" && entry.level !== "P1") || entry.responseValidator.trim() !== "")
      continue;
    if (!entry.facade.includes(".")) {
      if (
        !backendApiExports.has(entry.facade) ||
        backendFacadeRpcKeys.get(entry.facade) !== entry.key
      ) {
        findings.push({ key: entry.key, facade: entry.facade, locator: audit.RPC_FACADE_PATH });
      }
      continue;
    }
    const locator = audit.SERVICE_FACADE_LOCATORS.get(entry.key) ?? "";
    if (!locator) {
      findings.push({ key: entry.key, facade: entry.facade, locator });
      continue;
    }
    let source = serviceSources.get(locator);
    if (!source) {
      source = await readAuditSource(auditContext, locator);
      serviceSources.set(locator, source);
    }
    const [serviceName, memberName, ...extra] = entry.facade.split(".");
    if (
      extra.length > 0 ||
      audit.serviceFacadeMemberRpcKey(source, serviceName, memberName, backendFacadeRpcKeys) !==
        entry.key
    ) {
      findings.push({ key: entry.key, facade: entry.facade, locator });
    }
  }
  return findings.sort((a, b) => a.key.localeCompare(b.key));
}

async function collectInvalidResponsePolicyEvidence(
  auditContext,
  registryEntries,
  backendFacadeRpcKeys,
) {
  const findings = [];
  const unusedEntries = registryEntries.filter((entry) => entry.responsePolicy?.kind === "unused");
  if (unusedEntries.length > 0) {
    auditContext.productionFacadeReferenceIndex = await audit.buildProductionFacadeReferenceIndex(
      auditContext,
      unusedEntries,
      backendFacadeRpcKeys,
    );
  }
  for (const entry of registryEntries) {
    const policy = entry.responsePolicy;
    if (!policy) continue;
    if (policy.kind === "unused") {
      findings.push(...audit.collectUnusedPolicyFindings(auditContext, entry));
      continue;
    }
    const consumer = await resolvePolicyLocator(
      auditContext,
      entry,
      "consumer",
      policy.consumer,
      false,
      findings,
    );
    const regressionTest = await resolvePolicyLocator(
      auditContext,
      entry,
      "regressionTest",
      policy.regressionTest,
      true,
      findings,
    );
    const consumerSymbol = consumer
      ? findResponsePolicyConsumerSymbol(consumer.ast, policy.consumer)
      : null;
    let consumerCalls = consumerSymbol
      ? await audit.findFacadeCalls(
          auditContext,
          consumer.ast,
          consumerSymbol,
          consumer.path,
          entry,
          backendFacadeRpcKeys,
        )
      : [];
    const publishedCallbackProof =
      policy.kind === "ignored-result" && policy.outcome && consumerSymbol
        ? await audit.publishedCallbackProductionProof(
            auditContext,
            consumer.ast,
            consumer.path,
            consumerSymbol,
            policy.outcome,
            entry,
          )
        : null;
    if (publishedCallbackProof) consumerCalls = [publishedCallbackProof.call];
    const consumerOutcomeProof =
      policy.kind === "ignored-result" &&
      !policy.outcome &&
      consumerSymbol &&
      consumerCalls.length === 1
        ? await audit.collectIgnoredResultConsumerOutcomeProof(
            auditContext,
            consumer.ast,
            consumerSymbol,
            consumerCalls[0],
            consumer.path,
          )
        : null;
    if (
      regressionTest &&
      !audit.hasRegressionTestEvidence(
        regressionTest.ast,
        regressionTest.path,
        policy.regressionTest.symbol,
        policy.consumer,
        policy.kind,
        entry,
        publishedCallbackProof ?? consumerOutcomeProof,
      )
    ) {
      findings.push(
        policyFinding(
          entry,
          "regressionTest",
          policy.regressionTest,
          "test callback lacks executable assertions tied to the consumer and RPC key",
        ),
      );
    }
    if (!consumer) continue;
    if (!consumerSymbol) {
      findings.push(policyFinding(entry, "consumer", policy.consumer, "symbol was not found"));
      continue;
    }
    if (policy.kind === "ignored-result" && policy.outcome && !publishedCallbackProof) {
      findings.push(
        policyFinding(
          entry,
          "consumer",
          policy.consumer,
          "consumer lacks the exact post-RPC published callback outcome",
        ),
      );
      continue;
    }
    const exactTurnInterruptPolicy = audit.isExactTurnInterruptPolicy(entry);
    const resultRuntimeInjection =
      exactTurnInterruptPolicy && (await audit.provesTurnInterruptInjection(auditContext, entry));
    if (consumerCalls.length === 0 && !resultRuntimeInjection) {
      findings.push(
        policyFinding(
          entry,
          "consumer",
          policy.consumer,
          "symbol does not call the facade for this RPC key",
        ),
      );
      continue;
    }
    if (policy.kind === "ignored-result") {
      if (consumerCalls.some((call) => !audit.isIgnoredCallResult(call.ancestors))) {
        findings.push(
          policyFinding(entry, "consumer", policy.consumer, "consumer reads the RPC result"),
        );
        continue;
      }
      if (consumerCalls.length > 1) {
        findings.push(
          policyFinding(
            entry,
            "consumer",
            policy.consumer,
            "consumer calls the facade more than once",
          ),
        );
      }
      continue;
    }
    if (policy.kind === "result-handled") {
      const handler = await resolvePolicyLocator(
        auditContext,
        entry,
        "handler",
        policy.handler,
        false,
        findings,
      );
      if (!handler) continue;
      const handlerSymbol = findModuleLevelSymbol(handler.ast, policy.handler.symbol);
      if (!handlerSymbol) {
        findings.push(policyFinding(entry, "handler", policy.handler, "symbol was not found"));
        continue;
      }
      const directResultFlow =
        consumerCalls.length === 1 &&
        audit.consumerPassesFacadeResultToHandler(
          consumer.ast,
          consumerSymbol,
          consumerCalls[0]?.node,
          consumer.path,
          policy.handler,
        );
      const runtimeResultFlow =
        exactTurnInterruptPolicy &&
        resultRuntimeInjection &&
        (audit.runtimePassesAwaitedResultToHandler(
          handler.ast,
          handlerSymbol,
          policy.handler.symbol,
          policy.consumer.symbol,
        ) ||
          audit.runtimePassesStrictInterruptResultToHandler(
            handler.ast,
            policy.handler.symbol,
            policy.consumer.symbol,
          ));
      if (!directResultFlow && !runtimeResultFlow) {
        findings.push(
          policyFinding(
            entry,
            "consumer",
            policy.consumer,
            "consumer does not pass the observed RPC result to the located handler",
          ),
        );
        continue;
      }
      if (
        !audit.handlerDirectlyInspectsEnvelope(
          handlerSymbol,
          responsePolicyRpcMethod(entry),
          handler.ast,
          runtimeResultFlow,
        ) ||
        (exactTurnInterruptPolicy &&
          !audit.hasExactTurnInterruptTimeoutHandler(handlerSymbol, handler.ast))
      ) {
        findings.push(
          policyFinding(
            entry,
            "handler",
            policy.handler,
            "handler lacks direct executable envelope outcome handling",
          ),
        );
      }
      continue;
    }
    if (consumerCalls.length !== 1) {
      findings.push(
        policyFinding(
          entry,
          "consumer",
          policy.consumer,
          "consumer must contain exactly one facade call",
        ),
      );
      continue;
    }
    const [consumerCall] = consumerCalls;
    const shape = await resolvePolicyLocator(
      auditContext,
      entry,
      "shape",
      policy.shape,
      false,
      findings,
    );
    if (!shape) continue;
    const shapeSymbol = findProductionSymbol(shape.ast, policy.shape.symbol);
    if (!shapeSymbol) {
      findings.push(policyFinding(entry, "shape", policy.shape, "symbol was not found"));
      continue;
    }
    if (!audit.hasExecutableShapeNarrowing(shapeSymbol, shape.ast)) {
      findings.push(
        policyFinding(entry, "shape", policy.shape, "shape symbol lacks executable narrowing"),
      );
      continue;
    }
    if (
      !audit.shapeDominatesConsumerUse(
        consumer.ast,
        consumerSymbol,
        consumerCall.node,
        consumer.path,
        shape.path,
        policy.shape.symbol,
      )
    ) {
      findings.push(
        policyFinding(entry, "shape", policy.shape, "shape proof does not dominate consumer use"),
      );
    }
  }
  return findings.sort(comparePolicyFindings);
}

export { collectInvalidFacadeLocators, collectInvalidResponsePolicyEvidence };
export {
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
} from "./response-policy-resolution.mjs";
