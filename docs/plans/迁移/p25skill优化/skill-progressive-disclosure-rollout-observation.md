# Skill Progressive Disclosure Rollout Observation

Status: **P25-HIGH-02d rollout observation record template**. This artifact is the
copy/paste record used before enabling default progressive disclosure. It is not a
success report until the 30-day window has real samples.

Related runbook: `docs/plans/迁移/p25skill优化/p25skill优化.md §9.3`.
Related alerts: `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-alerts.yml`.
Optional helpers:
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-report.sh`
  queries Prometheus, can optionally run
  `skill-progressive-disclosure-rollout-smoke.sh`, and prints a copy/paste
  daily observation row.
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-append.sh`
  appends one generated daily observation row, fails closed on duplicate dates or
  incomplete evidence, and prevents no-sample rows from being marked continue.
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-status.sh`
  summarizes sampled days, no-sample rows, non-ok rate, blockers, and the next
  phase actions before running the 30-day rollout gate.
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-daily.sh`
  runs report -> append -> status in one daily command and preserves report /
  append / status artifacts for audit.
- `docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-gate.sh`
  is the 30-day rollout gate verifier; it fails closed on no-sample rows,
  missing smoke PASS results, rollback trigger drift, or non-ok rate threshold
  breaches.

## Gate

Do **not** enable default `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=true` or delete the
codexapp Summary override until this artifact has at least **30 days** of daily
records and every rollback trigger is either green or explicitly waived. The
final pre-switch check should run `skill-progressive-disclosure-rollout-gate.sh`
against this file with `SKILL_PD_REQUIRED_SAMPLE_DAYS=30`.

## No-sample rule

If `Total host tool calls` is `0`, mark the row as **`无样本` / `no samples`**.
Do not report a success rate, do not treat `0` errors as `99%` success, and do
not use the row to close the rollout gate.

## Required record fields

| Field | Source / query | Required value |
|---|---|---|
| Observation date | release calendar | `YYYY-MM-DD` |
| Version / commit | `git rev-parse --short HEAD` or release tag | commit SHA / tag |
| `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE` switch state | runtime env / config snapshot | `false`, `true`, or canary percentage |
| Metrics collection window | dashboard / Prometheus range | e.g. `24h`, `7d`, `30d` |
| Total host tool calls | `sum(increase(host_tool_calls_total[24h]))` | integer |
| `ok` calls | `sum(increase(host_tool_calls_total{outcome="ok"}[24h]))` | integer |
| `error` calls | `sum(increase(host_tool_calls_total{outcome="error"}[24h]))` | integer |
| `cwd_missing` calls | `sum(increase(host_tool_calls_total{outcome="cwd_missing"}[24h]))` | integer |
| `approval_required` calls | `sum(increase(host_tool_calls_total{outcome="approval_required"}[24h]))` | integer |
| `enrich_failure` / `enrich_failures_total` | `increase(enrich_failures_total[24h])` | integer |
| Artifact approval cache miss | `increase(skill_artifact_approval_miss_total[24h])` | integer / note |
| Manual smoke result | run the smoke checklist below | `PASS`, `FAIL`, or `SKIP(no samples)` |
| Production Prometheus smoke result | run `skill-progressive-disclosure-rollout-smoke.sh` after applying scrape/rule config | `PASS`, `FAIL`, or `SKIP(not applied)` |
| Rollback drill result | follow `p25skill优化.md §9.3` | `PASS`, `FAIL`, or `SKIP(no release window)` |
| Rollback trigger | alert / manual finding | trigger name or `none` |
| Decision | release owner | `continue`, `hold`, or `rollback` |

## Manual smoke checklist

Record the command / environment used for each line.

1. `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=false`: selected/name-only skills still
   work and no default full catalog is exposed to the model.
2. `ENABLE_SKILL_PROGRESSIVE_DISCLOSURE=true` in canary: catalog groups render,
   untrusted metadata remains redacted, and `skill_expand_body` can be called.
3. Approval path: first project-scope artifact expansion asks for approval;
   approved continues, denied returns a structured tool result.
4. Resume/recovery path: a resumed codexapp session can still call
   `skill_expand_body`.
5. Alert path: confirm the dashboard or Prometheus scrape can read
   `host_tool_calls_total{outcome}` and `enrich_failures_total`.

## Daily observation row template

| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|
| YYYY-MM-DD | `<commit>` | `false` / `true` / canary | 24h | 0 | 0 | 0 | 0 | 0 | 0 | `SKIP(no samples)` | `SKIP(not applied)` | `SKIP(no release window)` | none | hold | `无样本` / `no samples`; gate remains open |

## 30-day summary template

| Metric | 30-day value | Gate |
|---|---:|---|
| Observation days with samples | TODO | must be 30 before default switch |
| Total host tool calls | TODO | must be > 0 |
| Non-ok host tool calls | TODO | error rate must stay below `SkillHostToolHighErrorRate` threshold |
| cwd_missing calls | TODO | must be 0 or have an accepted incident note |
| approval_required stuck incidents | TODO | must be 0 open incidents |
| enrich_failures_total increase | TODO | must be 0 or have protocol-drift fix merged |
| Manual smoke failures | TODO | must be 0 open failures |
| Production Prometheus smoke failures | TODO | must be 0 open failures |
| Rollback drill failures | TODO | must be 0 open failures |

Final decision: `continue` / `hold` / `rollback`.
