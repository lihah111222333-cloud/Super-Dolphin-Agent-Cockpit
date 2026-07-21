import { cleanup, render, screen, waitFor } from "@testing-library/react";
import React from "react";
import { beforeEach, expect, it, vi } from "vitest";
import {
  createBoundCapabilities,
  createDefaultBackendResponses,
  createDeferred,
  createDeferredThreadMessagesPage,
  createThreadMessageFixtures,
  flushAssistantDeltaBatch as flushAssistantDeltaBatchSupport,
  flushPromises,
  interruptSuccessResult,
} from "./test-support/clientStoreTestSupport.js";
import * as frontendBreadcrumbs from "../../../shared/diagnostics/frontendBreadcrumbs.js";
import {
  clearFrontendHealth,
  frontendHealthSnapshot,
  resetFrontendHealthForTest,
} from "../../../shared/diagnostics/frontendHealthStore.js";
import {
  systemClockMillis,
  parseRequiredJsonObject,
} from "./contractStoreModel.js";

import {
  clientStore,
  resetClientStoreForTests,
  setClientStoreClockMillisForTests,
} from "./clientStoreTestApi.js";
let fixtures;
let boundCapabilities;
const threadMessage = (...args) => fixtures.message(...args);
const threadMessagesPage = (...args) => fixtures.page(...args);
const optionalUiArray = () => [];
const deferred = createDeferred;
const deferredThreadMessagesPage = () =>
  createDeferredThreadMessagesPage(threadMessagesPage);
const flushAssistantDeltaBatch = () =>
  flushAssistantDeltaBatchSupport(vi.advanceTimersByTime);
function diagnosticBreadcrumbs() {
  return frontendBreadcrumbs
    .snapshotFrontendBreadcrumbsForTests()
    .map(({ actionCode, routeId, phase }) => ({ actionCode, routeId, phase }));
}
function registerBridgeEventHandlersForTest() {
  const pending = clientStore.getState().initializeEvents();
  void pending.catch((error) => {
    if (error?.message !== "runtime event initialization superseded")
      throw error;
  });
  return pending;
}
export function registerClientStoreTestHooks({ runtime, backend }) {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    frontendBreadcrumbs.resetFrontendBreadcrumbsForTests?.();
    resetFrontendHealthForTest();
    clearFrontendHealth();
    runtime.bridgeCallback = null;
    runtime.bridgeOptions = null;
    runtime.runtimeReconnectCallback = null;
    fixtures = createThreadMessageFixtures();
    boundCapabilities = createBoundCapabilities();
    resetClientStoreForTests();
    const defaults = createDefaultBackendResponses(threadMessagesPage);
    for (const [method, value] of Object.entries(defaults.resolved))
      backend[method].mockResolvedValue(value);
    backend.getPreference.mockImplementation(defaults.preference);
    backend.readSharedFile.mockImplementation(defaults.readSharedFile);
    backend.interruptTurn.mockImplementation((params) =>
      Promise.resolve(interruptSuccessResult(params)),
    );
  });
}
export {
  boundCapabilities,
  deferred,
  deferredThreadMessagesPage,
  diagnosticBreadcrumbs,
  expect,
  flushAssistantDeltaBatch,
  flushPromises,
  frontendBreadcrumbs,
  frontendHealthSnapshot,
  it,
  optionalUiArray,
  parseRequiredJsonObject,
  React,
  registerBridgeEventHandlersForTest,
  resetClientStoreForTests,
  screen,
  setClientStoreClockMillisForTests,
  systemClockMillis,
  threadMessage,
  threadMessagesPage,
  clientStore as useClientStore,
  vi,
  waitFor,
  render,
};
