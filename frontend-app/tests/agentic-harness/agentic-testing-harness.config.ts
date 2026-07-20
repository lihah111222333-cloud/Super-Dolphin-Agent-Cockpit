import { execFileSync } from "node:child_process";
import { realpathSync } from "node:fs";
import { fileURLToPath } from "node:url";

import type { AgenticTestingHarnessConfig } from "@agentic-testing-harness/sdk";

import {
  BUILD_IDENTITY,
  BUILD_IDENTITY_HEADER,
  HEALTH_PATH,
  SOURCE_ROOT,
  SOURCE_ROOT_HEADER,
  TARGET_NAME,
} from "./identity.mjs";

const launcherPath = fileURLToPath(new URL("./launch-super-dolphin.mjs", import.meta.url));
const runsRoot = fileURLToPath(new URL("../../../.tmp/agentic-testing-harness/runs", import.meta.url));
const scenarioRegistryRoot = fileURLToPath(new URL("../../../.agentic-testing-harness/scenarios", import.meta.url));
const goModuleCache = realpathSync(execFileSync("go", ["env", "GOMODCACHE"], {
  encoding: "utf8",
  stdio: ["ignore", "pipe", "pipe"],
}).trim());

export default {
  schemaVersion: "1",
  runsRoot,
  scenarioRegistryRoot,
  targets: {
    [TARGET_NAME]: {
      kind: "web",
      command: { executable: process.execPath, args: [launcherPath, goModuleCache] },
      health: { path: HEALTH_PATH, timeoutMs: 120_000, pollIntervalMs: 100 },
      identity: {
        sourceRootHeader: SOURCE_ROOT_HEADER,
        buildIdentityHeader: BUILD_IDENTITY_HEADER,
        sourceRoot: SOURCE_ROOT,
        buildIdentity: BUILD_IDENTITY,
      },
      qualification: {
        selfTtlMs: null,
        sourceInputs: [
          "cmd",
          "internal",
          "frontend-app/src",
          "frontend-app/public",
          "frontend-app/package.json",
          "frontend-app/vite.config.js",
          "go.mod",
          "run-new-ui-desktop.sh",
        ],
        launcherInputs: [
          "frontend-app/tests/agentic-harness/identity.mjs",
          "frontend-app/tests/agentic-harness/launch-super-dolphin.mjs",
          "frontend-app/vite.config.js",
          "run-new-ui-desktop.sh",
        ],
        dependencyLockInputs: ["go.sum", "frontend-app/package-lock.json"],
        excludedRelativePaths: [".tmp", ".workspace", ".worktrees", "frontend-app/dist"],
      },
      headless: true,
    },
  },
  budget: {
    maxActions: 10,
    maxDurationMs: 300_000,
    maxNetworkEvents: 1_000,
    maxEvidenceBytes: 1_048_576,
  },
  evidence: {
    maxEvents: 100,
    maxBytes: 1_048_576,
    sensitiveFields: ["password", "token", "apiKey", "authorization", "cookie"],
  },
} satisfies AgenticTestingHarnessConfig;
