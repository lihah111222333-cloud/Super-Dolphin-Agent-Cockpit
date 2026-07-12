# Reasonix Frontend Next — Surface C Deterministic Benchmark Evidence

## Execution facts and serial ownership

- Execution HEAD: `8db4361fcf2c48002699da40b09200a8197b894e`
- Live `origin/main`: `7c3af8c390cce5157fe2824043165fe5d6dc9bba`
- Execution base: `a7df089e32e4135a90f10a52f6ef10069cab8353`
- Branch/worktree: `codex/reasonix-frontend-next-serial` at `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-frontend-next-serial`
- Serial handoff: Tasks 0-7 predecessor implementer -> Task 8 first replacement implementer (Surface A/B repair and C review) -> Task 8 second replacement implementer (C evidence, adjudicated C repair, and full gates). Root stopped the first replacement after its execution channel became unresponsive and before the second began; no parallel implementer or lost file is claimed.

## Owned benchmark surface

- `frontend-app/scripts/chat-history-benchmark.mjs`
- `frontend-app/scripts/chat-history-benchmark.test.mjs`
- `frontend-app/src/pages/chat/model/chatHistoryBenchmarkFixture.js`
- `frontend-app/src/pages/chat/model/chatHistoryBenchmarkFixture.test.js`
- `frontend-app/src/pages/chat/model/timelineMaterializationModel.js`
- `frontend-app/src/pages/chat/model/timelineMaterializationModel.test.js`
- `frontend-app/src/pages/chat/hooks/useTimelineMaterialization.js`
- `frontend-app/package.json`

The benchmark uses the same pure `selectMaterializedTimeline` owner consumed by the production hook. Fixture generation is deterministic and occurs outside the measured selector interval. The report is structural evidence only; no wall-clock or heap threshold is used.

## Focused GREEN retained from the live review

```text
npm exec -- vitest run scripts/chat-history-benchmark.test.mjs src/pages/chat/model/chatHistoryBenchmarkFixture.test.js src/pages/chat/model/timelineMaterializationModel.test.js src/pages/chat/hooks/useTimelineMaterialization.test.jsx --no-file-parallelism --maxWorkers=1
```

Exit `0`: 4/4 files and 21/21 tests passed, duration `1.69s`.

## Report contract verification

Default mode:

- one JSON array document;
- exactly six rows for the cross-product `turns=200/1000/5000` and `toolsPerTurn=1/3`;
- exact key order per row: `case`, `turns`, `toolsPerTurn`, `materializedCount`, `durationMs`, `heapDeltaBytes`, `node`, `commit`;
- `materializedCount=80` for every case;
- finite numeric `durationMs` and `heapDeltaBytes`;
- no `synthetic-message-body`, `fixture_tool_`, or message `content` in serialized output.

Extended mode:

- exactly eight rows;
- the default six rows remain unchanged in shape;
- only `10000 × toolsPerTurn 1/3` are appended as the final two cases.

The runner rejects arguments other than the optional single `--extended`, obtains the commit from the fixed repository root, and prints only the JSON array to stdout. Consumers use `npm run --silent benchmark:chat-history` so npm lifecycle headers do not contaminate the JSON document.

## Review disposition and remaining work

- Benchmark finding: none. Root accepted the exact matrix, eight-key/private report, real materialization owner, and absence of machine-wide thresholds.
- LSP: the benchmark files are part of the 16-file Surface C diagnostics batch; zero diagnostics, including no Hint.
- Generated artifacts: unchanged during Task 8 evidence work; no generator run.
- Full gates: the later Task 8 targeted/full frontend/backend/repository gates passed; first failures and final reruns are retained in `05-full-gates.md`.
- Remaining: exact-path staging, commit-hook evidence, and final commit.
