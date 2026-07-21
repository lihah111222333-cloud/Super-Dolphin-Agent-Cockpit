import { createBaseShellFactory } from "./base-shell.jsx";
import { createDefaultsFactory } from "./defaults.jsx";
import { createObservabilityFactory } from "./observability.jsx";
import { createPromptsMemoryFactory } from "./prompts-memory.jsx";
import { createSharedFilesFactory } from "./shared-files.jsx";
import { createWorkflowFactory } from "./workflow.jsx";

export function createAppTestContext({
  backend,
  App,
  resetClientStoreForTests,
}) {
  const ctx = {
    backend,
    App,
    resetClientStoreForTests,
    bridgeCallback: null,
    appOverlayHost: null,
  };
  for (const factory of [
    createBaseShellFactory,
    createDefaultsFactory,
    createObservabilityFactory,
    createPromptsMemoryFactory,
    createSharedFilesFactory,
    createWorkflowFactory,
  ])
    Object.assign(ctx, factory(ctx));
  return ctx;
}
