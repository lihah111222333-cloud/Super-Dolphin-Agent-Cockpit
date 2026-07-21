export * from "./rpc-audit-test-support-core.mjs";
export {
  REAL_INJECTION_PATH,
  REAL_CONSUMER_PATH,
  REAL_REGRESSION_PATH,
  realResultHandledPolicy,
  realResultHandledSources,
  createRealResultHandledShadow,
  createMutatedSingleHelperResultHandledShadow,
  createMutatedRealResultHandledShadow,
  ignoredResultRegression,
  publishedCallbackConsumer,
  publishedCallbackRegression,
  pageIgnoredResultRegression,
  directWailsIgnoredResultRegression,
  DIRECT_WAILS_IGNORED_RESULT_CONSUMER,
} from "./rpc-audit-test-support-real.mjs";
export { consumerValidatedRegression } from "./rpc-audit-test-support-regression.mjs";
