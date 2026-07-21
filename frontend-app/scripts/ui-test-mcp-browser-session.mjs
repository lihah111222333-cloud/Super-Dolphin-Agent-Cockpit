import { setTimeout as sleep } from "node:timers/promises";
import {
  ToolExecutionError,
  invalidParams,
  toolError,
  toolResult,
} from "./ui-test-mcp-protocol.mjs";

export function createBrowserSession({
  contract,
  baseURL,
  browserFactory,
  config,
  state,
  acceptanceGlobal,
  closeIfPresent,
}) {
  async function waitForHarness(page) {
    if (typeof page.waitForFunction === "function")
      await page.waitForFunction(
        (globalName) => {
          const harness = window[globalName];
          return Boolean(
            harness &&
            typeof harness.snapshot === "function" &&
            typeof harness.frontendLogs === "function" &&
            typeof harness.diagnostics === "function" &&
            typeof harness.recordLog === "function",
          );
        },
        contract.UI_TEST_GLOBAL,
        { timeout: contract.UI_TEST_LIMITS.defaultTimeoutMs },
      );
  }
  async function ensurePage() {
    if (state.page) return state.page;
    if (!state.pageReady)
      state.pageReady = (async () => {
        let browser;
        let page;
        try {
          browser = await browserFactory();
          page = await browser.newPage();
          if (config.acceptanceToken)
            await page.addInitScript(
              ({ globalName, token, isolated }) => {
                Object.defineProperty(window, globalName, {
                  value: { token, isolated },
                  configurable: false,
                  enumerable: false,
                  writable: false,
                });
              },
              {
                globalName: acceptanceGlobal,
                token: config.acceptanceToken,
                isolated: config.allowSubmit && config.acceptanceOwnsUI,
              },
            );
          await page.goto(baseURL.toString(), {
            waitUntil: "domcontentloaded",
          });
          await waitForHarness(page);
          state.browser = browser;
          state.page = page;
          return page;
        } catch (error) {
          await closeIfPresent(page);
          await closeIfPresent(browser);
          state.pageReady = null;
          throw error;
        }
      })();
    return state.pageReady;
  }
  async function readSnapshot() {
    return (await ensurePage()).evaluate(() =>
      window.__SUPER_DOLPHIN_UI_TEST__.snapshot(),
    );
  }
  async function readDiagnostics() {
    return (await ensurePage()).evaluate(() =>
      window.__SUPER_DOLPHIN_UI_TEST__.diagnostics(),
    );
  }
  async function readFrontendLogs(filters) {
    return (await ensurePage()).evaluate(
      (input) => window.__SUPER_DOLPHIN_UI_TEST__.frontendLogs(input),
      filters,
    );
  }
  async function recordActionLog(message, fields) {
    return (await ensurePage()).evaluate(
      (entry) => window.__SUPER_DOLPHIN_UI_TEST__.recordLog(entry),
      { level: "info", source: "ui_test_mcp", message, fields },
    );
  }
  const waitMatched = (snapshot, args) =>
    args.waitState === "frontend_ready"
      ? Boolean(snapshot)
      : args.waitState === "route"
        ? snapshot.route === contract.UI_TEST_ROUTES[args.route] ||
          snapshot.route === args.route
        : args.waitState === "composer_text_length"
          ? snapshot.inputTextLength === args.expected
          : false;
  async function executeAction(args) {
    if (args.action === "navigate") {
      const page = await ensurePage();
      const targetURL = new URL(contract.UI_TEST_ROUTES[args.route], baseURL);
      await page.goto(targetURL.toString(), { waitUntil: "domcontentloaded" });
      await waitForHarness(page);
      await recordActionLog("navigate", {
        route: args.route,
        path: targetURL.pathname,
      });
      return {
        action: args.action,
        route: args.route,
        path: targetURL.pathname,
      };
    }
    if (args.action === "fill_composer") {
      const page = await ensurePage();
      await page.locator('[data-testid="composer-input"]').fill(args.text);
      await recordActionLog("fill_composer", {
        target: "composer_input",
        textLength: args.text.length,
      });
      return {
        action: args.action,
        target: "composer_input",
        textLength: args.text.length,
      };
    }
    if (args.action === "submit_composer") {
      if (
        !config.allowSubmit ||
        !config.acceptanceOwnsUI ||
        !config.acceptanceToken
      )
        return {
          error: {
            tool: "ui_action",
            action: args.action,
            target: "composer_submit",
            timeoutMs: null,
            reason:
              "submit_composer requires server-owned isolated acceptance mode",
          },
        };
      const snapshot = await readSnapshot();
      const enabled =
        Array.isArray(snapshot?.availableActions) &&
        snapshot.availableActions.some(
          (entry) =>
            entry === "submit_composer" ||
            (entry &&
              typeof entry === "object" &&
              (entry.action === "submit_composer" ||
                entry.name === "submit_composer") &&
              entry.enabled !== false),
        );
      if (!enabled)
        return {
          error: {
            tool: "ui_action",
            action: args.action,
            target: "composer_submit",
            timeoutMs: null,
            reason: "submit_composer is not enabled in the current UI state",
          },
        };
      const page = await ensurePage();
      const tokenInput = { token: config.acceptanceToken };
      const verification = await page.evaluate(
        (input) =>
          window.__SUPER_DOLPHIN_UI_TEST__.verifyIsolatedAcceptance(input),
        tokenInput,
      );
      if (!verification?.isolated || !verification?.tokenMatched)
        return {
          error: {
            tool: "ui_action",
            action: args.action,
            target: "composer_submit",
            timeoutMs: null,
            reason: "isolated acceptance token was not verified by the page",
          },
        };
      return {
        action: args.action,
        target: "composer_submit",
        result: await page.evaluate(
          (input) =>
            window.__SUPER_DOLPHIN_UI_TEST__.submitComposerInIsolation(input),
          tokenInput,
        ),
      };
    }
    if (args.action === "wait_for") {
      const started = Date.now();
      while (Date.now() - started <= args.timeoutMs) {
        if (waitMatched(await readSnapshot(), args)) {
          const elapsedMs = Date.now() - started;
          await recordActionLog("wait_for", {
            waitState: args.waitState,
            elapsedMs,
          });
          return { action: args.action, waitState: args.waitState, elapsedMs };
        }
        await sleep(contract.UI_TEST_LIMITS.pollIntervalMs, undefined, {
          signal: state.waitAbortController.signal,
        });
      }
      throw new ToolExecutionError(`timed out waiting for ${args.waitState}`, {
        action: args.action,
        timeoutMs: args.timeoutMs,
      });
    }
    throw invalidParams(`unknown UI test action: ${args.action}`);
  }
  function scenarioSteps(args) {
    const text = args.text || "MCP UI test input";
    const snap = { name: "snapshot_before", kind: "snapshot" };
    if (args.scenario === "chat_composer_probe")
      return [
        snap,
        {
          name: "navigate_chat",
          kind: "action",
          args: { action: "navigate", route: "chat" },
        },
        {
          name: "fill_composer",
          kind: "action",
          args: { action: "fill_composer", target: "composer_input", text },
        },
        {
          name: "wait_composer_text",
          kind: "action",
          args: {
            action: "wait_for",
            waitState: "composer_text_length",
            expected: text.length,
            timeoutMs: args.timeoutMs,
          },
        },
      ];
    if (args.scenario === "frontend_navigation_probe")
      return [
        ...scenarioSteps({ ...args, scenario: "chat_composer_probe" }),
        {
          name: "navigate_observability",
          kind: "action",
          args: { action: "navigate", route: "observability" },
        },
      ];
    if (args.scenario === "observability_logs_probe")
      return [
        snap,
        {
          name: "navigate_observability",
          kind: "action",
          args: { action: "navigate", route: "observability" },
        },
      ];
    if (args.scenario === "settings_open_probe")
      return [
        snap,
        {
          name: "navigate_settings",
          kind: "action",
          args: { action: "navigate", route: "settings" },
        },
      ];
    if (args.scenario === "open_route_probe")
      return [
        snap,
        {
          name: `navigate_${args.route}`,
          kind: "action",
          args: { action: "navigate", route: args.route },
        },
      ];
    throw invalidParams(`unknown UI test scenario: ${args.scenario}`);
  }
  async function runScenario(args) {
    try {
      const steps = [];
      const gate = async (name) => {
        const diagnostics = await readDiagnostics();
        const failures = [
          "consoleErrors",
          "bridgeErrors",
          "unhandledErrors",
        ].filter(
          (key) => Array.isArray(diagnostics[key]) && diagnostics[key].length,
        );
        if (failures.length)
          throw new ToolExecutionError(
            `scenario ${args.scenario} diagnostics contain ${failures.join(", ")}`,
            { stepIndex: steps.length, code: "scenario_diagnostics_failed" },
          );
        steps.push({ index: steps.length, name, status: "passed" });
        return diagnostics;
      };
      await gate("diagnostics_before");
      for (const step of scenarioSteps(args)) {
        const index = steps.length;
        try {
          const result =
            step.kind === "snapshot"
              ? await readSnapshot()
              : await executeAction(step.args);
          if (result?.error)
            throw new ToolExecutionError(result.error.reason, {
              code: "scenario_action_rejected",
              action: step.args.action,
              target: step.args.target || null,
              timeoutMs: step.args.timeoutMs || args.timeoutMs,
            });
          steps.push({ index, name: step.name, status: "passed" });
        } catch (error) {
          throw new ToolExecutionError(
            error.message || "scenario step failed",
            {
              stepIndex: index,
              code: error.details?.code || "scenario_step_failed",
              action: step.args?.action || null,
              target: step.args?.target || null,
              timeoutMs: step.args?.timeoutMs || args.timeoutMs,
            },
          );
        }
      }
      const finalSnapshot = await readSnapshot();
      steps.push({
        index: steps.length,
        name: "snapshot_after",
        status: "passed",
      });
      const diagnostics = await gate("diagnostics_after");
      const logs = await readFrontendLogs(args.logs);
      steps.push({ index: steps.length, name: "logs_after", status: "passed" });
      return toolResult({
        tool: "ui_scenario_run",
        scenario: args.scenario,
        success: true,
        steps,
        finalSnapshot,
        diagnostics,
        logs,
      });
    } catch (error) {
      return toolError({
        tool: "ui_scenario_run",
        scenario: args.scenario,
        stepIndex: error.details?.stepIndex ?? null,
        code: error.details?.code || "scenario_failed",
        action: error.details?.action || null,
        target: error.details?.target || null,
        timeoutMs: error.details?.timeoutMs ?? args.timeoutMs,
        reason: error.message || "scenario failed",
      });
    }
  }
  async function runTool(name, args) {
    try {
      if (name === "ui_snapshot")
        return toolResult({ tool: name, snapshot: await readSnapshot() });
      if (name === "ui_diagnostics")
        return toolResult({ tool: name, diagnostics: await readDiagnostics() });
      if (name === "ui_frontend_logs")
        return toolResult({ tool: name, logs: await readFrontendLogs(args) });
      if (name === "ui_action") {
        const content = await executeAction(args);
        return content?.error ? toolError(content.error) : toolResult(content);
      }
      if (name === "ui_scenario_run") return runScenario(args);
      throw invalidParams(`unknown UI test MCP tool: ${name}`);
    } catch (error) {
      return toolError({
        tool: name,
        action: args.action || null,
        target: args.target || null,
        timeoutMs: args.timeoutMs || null,
        reason: error.message || "UI test MCP tool failed",
      });
    }
  }
  return { runTool };
}
