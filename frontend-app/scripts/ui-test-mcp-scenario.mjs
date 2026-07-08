import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';

import {
  DEFAULT_STOP_TIMEOUT_MS,
  acceptanceConfig,
  assertPortFree,
  callTool,
  startMCPServer,
  startVite,
  stopProcess,
  waitForHTTP,
  waitForProcessExit,
} from './ui-test-mcp-acceptance.mjs';

const DEFAULT_SCENARIO = 'frontend_navigation_probe';
const TEXT_SCENARIOS = Object.freeze([
  'chat_composer_probe',
  'frontend_navigation_probe',
]);

export function scenarioAcceptanceConfig(env = process.env) {
  const base = acceptanceConfig(env);
  return {
    ...base,
    scenario: env.SUPER_DOLPHIN_UI_TEST_SCENARIO || DEFAULT_SCENARIO,
    text: env.SUPER_DOLPHIN_UI_TEST_SCENARIO_TEXT || 'MCP UI test input',
    route: env.SUPER_DOLPHIN_UI_TEST_SCENARIO_ROUTE || '',
    outputDir: path.join(base.frontendRoot, '.tmp', 'ui-test-mcp-scenarios'),
  };
}

export async function runUITestMCPScenarioAcceptance(config = scenarioAcceptanceConfig()) {
  let viteProcess;
  let client;
  try {
    if (config.startVite) {
      await assertPortFree('127.0.0.1', config.port);
      viteProcess = startVite(config);
      await waitForHTTP(config.baseURL, config.timeoutMs, viteProcess, 'Vite dev server');
    }

    client = startMCPServer(config);
    await client.request('initialize', {
      protocolVersion: '2024-11-05',
      capabilities: {},
      clientInfo: {
        name: 'super-dolphin-ui-test-mcp-scenario',
        version: '0.1.0',
      },
    });
    await client.notify('notifications/initialized', {});

    const result = await callTool(client, 'ui_scenario_run', scenarioArguments(config));
    await writeScenarioResult(config, result);

    await client.request('shutdown', {});
    await client.notify('exit', {});
    await waitForProcessExit(client.child, DEFAULT_STOP_TIMEOUT_MS, 'MCP server did not exit after scenario shutdown/exit');
    console.log(`UI test MCP scenario passed: ${config.scenario}`);
  }
  finally {
    if (client && !client.stopped()) {
      await stopProcess(client.child, 'MCP scenario server', DEFAULT_STOP_TIMEOUT_MS);
    }
    if (viteProcess) {
      await stopProcess(viteProcess, 'Vite dev server', DEFAULT_STOP_TIMEOUT_MS);
    }
  }
}

function scenarioArguments(config) {
  const args = {
    scenario: config.scenario,
    timeoutMs: Math.min(15000, config.timeoutMs),
  };
  if (TEXT_SCENARIOS.includes(config.scenario)) args.text = config.text;
  if (config.scenario === 'open_route_probe') {
    if (!config.route) {
      throw new Error('SUPER_DOLPHIN_UI_TEST_SCENARIO_ROUTE is required for open_route_probe');
    }
    args.route = config.route;
  }
  return args;
}

async function writeScenarioResult(config, result) {
  await mkdir(config.outputDir, { recursive: true });
  const filename = `${Date.now()}-${config.scenario}.json`;
  await writeFile(path.join(config.outputDir, filename), `${JSON.stringify(result, null, 2)}\n`);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  runUITestMCPScenarioAcceptance().catch((error) => {
    console.error(error.stack || error.message);
    process.exitCode = 1;
  });
}
