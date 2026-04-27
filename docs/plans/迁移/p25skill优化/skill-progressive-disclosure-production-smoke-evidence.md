# Skill Progressive Disclosure Production Smoke Evidence

Evidence type: production-smoke
Status: P25-HIGH-02k evidence template. Attach one filled copy for every
Phase 3 preflight run.

## Required fields

| Field | Required value |
|---|---|
| Evidence date | YYYY-MM-DD |
| Version / commit | short SHA or release tag |
| Operator | release owner / on-call |
| Metrics URL | production `SUPER_DOLPHIN_METRICS_URL` |
| Prometheus URL | production `PROMETHEUS_URL` |
| Alertmanager URL | production `ALERTMANAGER_URL` |
| Observation row date | date of the matching rollout observation row |
| Total host tool calls | positive integer; must be > 0 |
| Production smoke result | `P25-HIGH-02g smoke passed.` |
| Real traffic statement | `real traffic is non-zero` |

## Optional generator

After `skill-progressive-disclosure-rollout-daily.sh` or `skill-progressive-disclosure-rollout-report.sh`
produces a report artifact with raw production smoke output, generate a filled
copy with:

```bash
SKILL_PD_ROLLOUT_REPORT_FILE="/path/to/rollout-report-YYYY-MM-DD.md" \
SKILL_PD_PRODUCTION_SMOKE_EVIDENCE_OUT="/path/to/production-smoke-evidence.md" \
SKILL_PD_OPERATOR="release-owner" \
SUPER_DOLPHIN_METRICS_URL="https://example.invalid/metrics" \
ALERTMANAGER_URL="https://alertmanager.example.invalid" \
docs/plans/迁移/p25skill优化/skill-progressive-disclosure-production-smoke-evidence-generate.sh
```

The generator fails closed unless the report has positive `Total host tool calls`,
`Production Prometheus smoke result` is `PASS`, and raw smoke output contains
`P25-HIGH-02g rollout smoke passed.`.

## Filled evidence

Evidence date: TODO
Version / commit: TODO
Operator: TODO
Metrics URL: TODO
Prometheus URL: TODO
Alertmanager URL: TODO
Observation row date: TODO
Total host tool calls: 0
Production smoke result: TODO
Real traffic statement: TODO

## Raw smoke output

Paste the complete output from `skill-progressive-disclosure-rollout-smoke.sh` here.

```text
TODO
```
