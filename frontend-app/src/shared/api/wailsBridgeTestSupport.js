import { readFileSync } from "node:fs";
import { join } from "node:path";
import { cwd } from "node:process";
import { vi } from "vitest";
import { requiredAppStoragePort } from "./browser/browserStorage.js";

const runtimeModule = "/wails/runtime.js";
const devRuntimeShimModule =
  "../../../public/wails/runtime.js?test-runtime-shim";

function sharedFilePreviewProducerFields() {
  const source = readFileSync(
    join(cwd(), "..", "internal", "ui", "wails", "sharedfile_open.go"),
    "utf8",
  );
  const match = source.match(
    /type sharedFilePreviewResult struct \{([\s\S]*?)\n\}/,
  );
  if (!match)
    throw new Error("sharedFilePreviewResult producer struct is required");
  return [...match[1].matchAll(/json:"([^"]+)"/g)]
    .map((entry) => entry[1])
    .sort((left, right) => left.localeCompare(right));
}

function captureBridgeLogs(registerBridgeLogStore) {
  const logs = [];
  const write = (level) => (event, fields) =>
    logs.push({ level, event, fields });
  registerBridgeLogStore({
    debug: write("debug"),
    error: write("error"),
    info: write("info"),
    warn: write("warn"),
  });
  return logs;
}

function waitForTraceFlush() {
  return new Promise((resolve) => {
    setTimeout(resolve, 0);
  });
}

function importFreshDevRuntimeShim() {
  vi.resetModules();
  return import("../../../public/wails/runtime.js?test-runtime-shim");
}

function resetWailsRuntimeMocks() {
  vi.resetModules();
  vi.doUnmock(runtimeModule);
}

function resetFrontendTraceEmitter() {
  resetWailsRuntimeMocks();
  delete window.__AO_FRONTEND_TRACE_DEBUG__;
  delete window.__AO_WAILS_RUNTIME_TELEMETRY__;
  requiredAppStoragePort("frontend trace test storage").clear();
}

export {
  runtimeModule,
  devRuntimeShimModule,
  sharedFilePreviewProducerFields,
  captureBridgeLogs,
  waitForTraceFlush,
  importFreshDevRuntimeShim,
  resetWailsRuntimeMocks,
  resetFrontendTraceEmitter,
};
