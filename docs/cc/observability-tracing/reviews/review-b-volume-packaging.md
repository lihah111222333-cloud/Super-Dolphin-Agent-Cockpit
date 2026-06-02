Severity: High
Section: 14 Operational Guardrails / 16 Recommended Answers
Issue: Trace files are enabled by default, rotated by size, but never retained by total bytes or age.
Why true: Section 9 caps individual file size at 64MB, while Section 16 explicitly says Phase 1 has no deletion by default. A long-running packaged app can therefore accumulate unlimited JSONL files under the user log directory even when memory is bounded.
Fix: Add a Phase 1 retention cap before implementation: max total trace bytes and/or max age per project, default enabled for packaged users, with safe prune-on-start/rotate behavior and a status counter for pruned files.

Severity: High
Section: 4.2 Core Data Model / 6.4 Payload Safety / 5 Bounded Index Design
Issue: Privacy truncation covers metadata/stack/preview fields, but not the top-level Error string and other free-form string fields.
Why true: TraceEvent exposes Error as a raw string, and the required truncation path only names metadata, stack, and preview fields. Real errors commonly include command lines, file paths, provider messages, tool excerpts, or request fragments, so this is a direct path to forbidden payload leakage in JSONL and indexes.
Fix: Define one sanitizer for every TraceEvent string field before indexing or writing: max bytes, secret patterns, multiline normalization, and an allowlist for identifiers. Add tests that Error cannot persist prompts, tokens, env values, tool results, or large content.

Severity: Medium
Section: 3.2 JSONL File Output / 9 JSONL Writer and Rotation
Issue: JSONL file and directory permissions are unspecified.
Why true: Trace JSONL will contain thread IDs, agent IDs, local project paths, error summaries, timing, and tool metadata. The plan names the path and writer mechanics but never requires 0700 directories or 0600 files, so permissions fall back to process umask/platform defaults.
Fix: Require secure creation semantics: create the project trace directory with owner-only permissions where supported, create trace files as 0600, preserve permissions across rotation, and test the mode on Unix platforms.

Severity: Medium
Section: 8 Dashboard Query API
Issue: Tail fallback can do repeated 20MB JSONL scans on UI requests without a stated timeout, cache, or concurrency limit.
Why true: The plan says missing memory hits may scan the recent JSONL tail up to OBS_JSONL_QUERY_TAIL_MB, default 20MB, and only forbids unbounded historical scans. A dashboard polling or multiple trace searches can repeatedly decode 20MB on the UI path, which is enough to cause visible latency or CPU spikes.
Fix: Add a query budget: context deadline, singleflight per file/range, small LRU cache or remembered tail offsets, max concurrent tail scans, and a partial result when the deadline is exceeded.

Severity: Medium
Section: 8 Dashboard Query API / 9 JSONL Writer and Rotation
Issue: Corrupt or partial JSONL line handling is not specified.
Why true: The writer uses append-only lines with best-effort shutdown flush, so crashes or forced exits can leave a partial final line. The dashboard fallback later scans JSONL, but the plan only says return partial when the tail scan exceeds a size limit, not when a line cannot decode.
Fix: Define reader behavior: skip or quarantine malformed final lines, count decode errors, continue returning valid events with truncated=true or warnings, and add a test with a partial trailing JSON object.

Severity: Medium
Section: 11 Configuration / 5 Bounded Index Design / 8 Dashboard Query API
Issue: Environment knobs have defaults but no validation bounds.
Why true: OBS_INDEX_MAX_EVENTS, OBS_EVENT_MAX_BYTES, OBS_JSONL_MAX_FILE_MB, and OBS_JSONL_QUERY_TAIL_MB directly control memory, disk, and query CPU. The plan requires hard limits but does not say invalid, zero, negative, or extremely large configured values must be rejected.
Fix: Add fail-fast config validation with documented min/max ranges for every OBS_* size/count/duration variable. Refuse startup or disable tracing with an explicit error according to project policy; do not silently clamp unsafe values.

Severity: Medium
Section: 4.2 Core Data Model / 13 Test Strategy
Issue: The privacy test plan is too weak for the stated forbidden-payload guarantees.
Why true: Manual smoke only checks that one JSONL file does not contain a prompt/full payload. It does not exercise sanitizer paths for errors, metadata maps, tool summaries, frontend params, stack truncation, oversized events, or secret-like values.
Fix: Add unit/golden tests that feed forbidden payloads through every event construction path and assert sanitized JSONL output and sanitized in-memory query output.

Severity: Medium
Section: 4.2 Core Data Model / 4.3 Storage Strategy
Issue: Future SQLite compatibility is underspecified because event schema has no version and Metadata is unconstrained map[string]any.
Why true: The plan promises a later SQLiteSink behind the same interface, but the persisted JSONL format can evolve before SQLite arrives. Without schema_version and typed metadata rules, later migration must infer old shapes and handle arbitrary values that may not map cleanly to indexed SQLite columns.
Fix: Add schema_version to TraceEvent now and define typed/allowed metadata value shapes. Store arbitrary extras only in a bounded JSON blob while keeping planned query keys as stable typed fields.
