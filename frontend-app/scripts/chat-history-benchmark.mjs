import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { performance } from 'node:perf_hooks';
import process from 'node:process';
import { buildChatHistoryFixture } from '../src/pages/chat/model/chatHistoryBenchmarkFixture.js';
import {
  TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
  selectMaterializedTimeline,
} from '../src/pages/chat/model/timelineMaterializationModel.js';

const SCRIPT_PATH = fileURLToPath(import.meta.url);
const REPOSITORY_ROOT = resolve(dirname(SCRIPT_PATH), '..', '..');
const DEFAULT_TURNS = Object.freeze([200, 1_000, 5_000]);
const EXTENDED_TURNS = 10_000;
const TOOL_COUNTS = Object.freeze([1, 3]);

function buildChatHistoryBenchmarkCases({ extended }) {
  if (typeof extended !== 'boolean') throw new TypeError('extended must be a boolean');
  const turns = extended ? [...DEFAULT_TURNS, EXTENDED_TURNS] : DEFAULT_TURNS;
  return Object.freeze(turns.flatMap((turnCount) => TOOL_COUNTS.map((toolsPerTurn) => Object.freeze({
    turns: turnCount,
    toolsPerTurn,
  }))));
}

function requireMeasurementMetadata({ caseName, turns, toolsPerTurn, node, commit }) {
  if (typeof caseName !== 'string' || caseName.length === 0) throw new TypeError('caseName is required');
  if (!Number.isSafeInteger(turns) || turns <= 0) throw new TypeError('turns must be a positive integer');
  if (!Number.isSafeInteger(toolsPerTurn) || toolsPerTurn <= 0) {
    throw new TypeError('toolsPerTurn must be a positive integer');
  }
  if (typeof node !== 'string' || node.length === 0) throw new TypeError('node is required');
  if (typeof commit !== 'string' || commit.length === 0) throw new TypeError('commit is required');
}

function measureChatHistoryCase(history, metadata) {
  requireMeasurementMetadata(metadata);
  const heapBefore = process.memoryUsage().heapUsed;
  const startedAt = performance.now();
  const materialized = selectMaterializedTimeline(history, TIMELINE_INITIAL_MATERIALIZED_MESSAGES);
  const durationMs = performance.now() - startedAt;
  const heapDeltaBytes = process.memoryUsage().heapUsed - heapBefore;
  return Object.freeze({
    case: metadata.caseName,
    turns: metadata.turns,
    toolsPerTurn: metadata.toolsPerTurn,
    materializedCount: materialized.length,
    durationMs,
    heapDeltaBytes,
    node: metadata.node,
    commit: metadata.commit,
  });
}

function currentCommit() {
  return execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: REPOSITORY_ROOT,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}

function runChatHistoryBenchmark({ extended }) {
  const commit = currentCommit();
  return buildChatHistoryBenchmarkCases({ extended }).map(({ turns, toolsPerTurn }) => {
    const history = buildChatHistoryFixture({ archived: true, seed: 7, toolsPerTurn, turns });
    return measureChatHistoryCase(history, {
      caseName: `turns-${turns}-tools-${toolsPerTurn}`,
      turns,
      toolsPerTurn,
      node: process.version,
      commit,
    });
  });
}

function extendedFromArguments(args) {
  if (args.length === 0) return false;
  if (args.length === 1 && args[0] === '--extended') return true;
  throw new TypeError('chat history benchmark accepts only --extended');
}

if (process.argv[1] && resolve(process.argv[1]) === SCRIPT_PATH) {
  const report = runChatHistoryBenchmark({ extended: extendedFromArguments(process.argv.slice(2)) });
  process.stdout.write(`${JSON.stringify(report)}\n`);
}

export {
  buildChatHistoryBenchmarkCases,
  measureChatHistoryCase,
  runChatHistoryBenchmark,
};
