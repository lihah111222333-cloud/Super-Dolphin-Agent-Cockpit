import {
  invalidParams,
  isPlainObject,
  strictSchema,
  validateExactObject,
  wrapContractValidation,
} from "./ui-test-mcp-protocol.mjs";

export function createToolDefinitions(contract) {
  const limit = contract.UI_TEST_LIMITS;
  const definitions = {
    ui_snapshot: {
      name: "ui_snapshot",
      description:
        "Read a sanitized snapshot of the local Super Dolphin UI state.",
      inputSchema: strictSchema({}),
    },
    ui_action: {
      name: "ui_action",
      description:
        "Execute an allowlisted local UI action with fixed targets only.",
      inputSchema: strictSchema(
        {
          action: { type: "string", enum: contract.UI_TEST_ACTIONS },
          route: { type: "string", enum: Object.keys(contract.UI_TEST_ROUTES) },
          target: { type: "string", enum: contract.UI_TEST_TARGETS },
          text: { type: "string", maxLength: limit.maxTextLength },
          waitState: { type: "string", enum: contract.UI_TEST_WAIT_STATES },
          timeoutMs: {
            type: "integer",
            minimum: 1,
            maximum: limit.maxTimeoutMs,
          },
          expected: { type: ["number", "string", "boolean"] },
        },
        ["action"],
      ),
    },
    ui_diagnostics: {
      name: "ui_diagnostics",
      description:
        "Read sanitized browser diagnostics from the UI test harness.",
      inputSchema: strictSchema({}),
    },
    ui_frontend_logs: {
      name: "ui_frontend_logs",
      description:
        "Read sanitized frontend log entries from the UI test harness.",
      inputSchema: strictSchema({
        level: { type: "string" },
        source: { type: "string" },
        since: { type: "string" },
        limit: { type: "integer", minimum: 1, maximum: limit.maxLimit },
      }),
    },
    ui_scenario_run: {
      name: "ui_scenario_run",
      description:
        "Run an allowlisted local UI test scenario through existing UI Test MCP primitives.",
      inputSchema: strictSchema(
        {
          scenario: { type: "string", enum: contract.UI_TEST_SCENARIO_IDS },
          route: { type: "string", enum: Object.keys(contract.UI_TEST_ROUTES) },
          text: { type: "string", maxLength: limit.maxTextLength },
          timeoutMs: {
            type: "integer",
            minimum: 1,
            maximum: limit.maxTimeoutMs,
          },
          logs: {
            type: "object",
            additionalProperties: false,
            properties: {
              level: { type: "string" },
              source: { type: "string" },
              since: { type: "string" },
              limit: { type: "integer", minimum: 1, maximum: limit.maxLimit },
            },
          },
        },
        ["scenario"],
      ),
    },
  };
  return contract.UI_TEST_TOOLS.map((toolName) => {
    if (!definitions[toolName])
      throw new Error(`missing MCP tool definition for ${toolName}`);
    return definitions[toolName];
  });
}

export function createToolArgumentValidator(contract) {
  const normalizeRoute = (route) => {
    if (typeof route !== "string")
      throw invalidParams("route must be a string");
    if (!Object.hasOwn(contract.UI_TEST_ROUTES, route))
      throw invalidParams(`unknown UI test route: ${route}`);
    return route;
  };
  const validateLogArgs = (args) => {
    validateExactObject(
      args,
      ["level", "source", "since", "limit"],
      "ui_frontend_logs arguments",
      contract,
    );
    const normalized = {};
    for (const field of ["level", "source", "since"]) {
      if (args[field] != null) {
        if (typeof args[field] !== "string")
          throw invalidParams(`ui_frontend_logs ${field} must be a string`);
        normalized[field] = args[field];
      }
    }
    if (args.limit != null)
      normalized.limit = wrapContractValidation(() =>
        contract.normalizeLimit(args.limit),
      );
    return normalized;
  };
  const validateAction = (args) => {
    validateExactObject(
      args,
      [
        "action",
        "route",
        "target",
        "text",
        "waitState",
        "timeoutMs",
        "expected",
      ],
      "ui_action arguments",
      contract,
    );
    if (typeof args.action !== "string")
      throw invalidParams("ui_action action must be a string");
    wrapContractValidation(() => contract.assertKnownActionName(args.action));
    const only = (keys) => {
      const extra = Object.keys(args).find(
        (key) => !keys.includes(key) && args[key] != null,
      );
      if (extra)
        throw invalidParams(`${args.action} does not accept field: ${extra}`);
    };
    if (args.action === "navigate") {
      only(["action", "route"]);
      return { action: args.action, route: normalizeRoute(args.route) };
    }
    if (args.action === "fill_composer") {
      only(["action", "target", "text"]);
      if (args.target != null && args.target !== "composer_input")
        throw invalidParams(`${args.target} is not valid for composer_input`);
      if (typeof args.text !== "string")
        throw invalidParams("fill_composer text must be a string");
      if (args.text.length > contract.UI_TEST_LIMITS.maxTextLength)
        throw invalidParams("fill_composer text exceeds maxTextLength");
      return { action: args.action, target: "composer_input", text: args.text };
    }
    if (args.action === "submit_composer") {
      only(["action", "target"]);
      if (args.target != null && args.target !== "composer_submit")
        throw invalidParams(`${args.target} is not valid for composer_submit`);
      return { action: args.action, target: "composer_submit" };
    }
    only(["action", "waitState", "route", "expected", "timeoutMs"]);
    if (typeof args.waitState !== "string")
      throw invalidParams("wait_for waitState must be a string");
    if (!contract.UI_TEST_WAIT_STATES.includes(args.waitState))
      throw invalidParams(`wait_for waitState is unknown: ${args.waitState}`);
    const output = {
      action: args.action,
      waitState: args.waitState,
      timeoutMs: wrapContractValidation(() =>
        contract.normalizeTimeoutMs(args.timeoutMs),
      ),
    };
    if (args.waitState === "route") output.route = normalizeRoute(args.route);
    if (args.waitState === "composer_text_length") {
      if (!Number.isSafeInteger(args.expected) || args.expected < 0)
        throw invalidParams(
          "wait_for composer_text_length expected must be a non-negative integer",
        );
      output.expected = args.expected;
    }
    if (
      args.waitState === "frontend_ready" &&
      (args.route != null || args.expected != null)
    )
      throw invalidParams(
        "wait_for frontend_ready does not accept route or expected",
      );
    return output;
  };
  const validateScenario = (args) => {
    validateExactObject(
      args,
      ["scenario", "route", "text", "timeoutMs", "logs"],
      "ui_scenario_run arguments",
      contract,
    );
    if (typeof args.scenario !== "string")
      throw invalidParams("ui_scenario_run scenario must be a string");
    wrapContractValidation(() =>
      contract.assertKnownScenarioName(args.scenario),
    );
    const normalized = {
      scenario: args.scenario,
      timeoutMs: wrapContractValidation(() =>
        contract.normalizeTimeoutMs(args.timeoutMs),
      ),
    };
    if (args.route != null) {
      if (args.scenario !== "open_route_probe")
        throw invalidParams(
          "ui_scenario_run route is only valid for open_route_probe",
        );
      normalized.route = normalizeRoute(args.route);
    } else if (args.scenario === "open_route_probe")
      throw invalidParams(
        "ui_scenario_run route is required for open_route_probe",
      );
    if (args.text != null) {
      if (
        !["chat_composer_probe", "frontend_navigation_probe"].includes(
          args.scenario,
        )
      )
        throw invalidParams(
          `ui_scenario_run text is not valid for ${args.scenario}`,
        );
      if (typeof args.text !== "string")
        throw invalidParams("ui_scenario_run text must be a string");
      if (args.text.length > contract.UI_TEST_LIMITS.maxTextLength)
        throw invalidParams("ui_scenario_run text exceeds maxTextLength");
      normalized.text = args.text;
    }
    normalized.logs =
      args.logs == null
        ? {
            source: "ui_test_mcp",
            limit: Math.min(20, contract.UI_TEST_LIMITS.maxLimit),
          }
        : validateLogArgs(args.logs);
    return normalized;
  };
  return (params) => {
    validateExactObject(
      params,
      ["name", "arguments"],
      "tools/call params",
      contract,
    );
    if (typeof params.name !== "string")
      throw invalidParams("tools/call params.name must be a string");
    wrapContractValidation(() => contract.assertKnownToolName(params.name));
    const args = params.arguments == null ? {} : params.arguments;
    if (!isPlainObject(args))
      throw invalidParams("tools/call params.arguments must be an object");
    if (params.name === "ui_snapshot" || params.name === "ui_diagnostics") {
      validateExactObject(args, [], `${params.name} arguments`, contract);
      return { name: params.name, args: {} };
    }
    if (params.name === "ui_frontend_logs")
      return { name: params.name, args: validateLogArgs(args) };
    if (params.name === "ui_action")
      return { name: params.name, args: validateAction(args) };
    if (params.name === "ui_scenario_run")
      return { name: params.name, args: validateScenario(args) };
    throw invalidParams(`unknown UI test MCP tool: ${params.name}`);
  };
}
