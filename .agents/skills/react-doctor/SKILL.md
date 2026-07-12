---
name: react-doctor
description: "仅当用户明确点名 `react-doctor` 技能时使用。"
disable_model_invocation: true
version: "1.1.0"
---

# React Doctor

Scans React codebases for security, performance, correctness, and architecture issues. Outputs a 0–100 health score.

## super-agent-v3 default verification

For ordinary `frontend-app` changes in this repository, prefer repo-native validation:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Do not run `npx react-doctor@latest` as the default finishing step. Use it only when the user explicitly asks for react-doctor diagnostics or when the repo has pinned/configured it.

## Explicit react-doctor requests

If the user explicitly asks to run react-doctor and no pinned local binary/config exists, ask before using `npx react-doctor@latest` because it downloads current external code.

For general cleanup or code improvement, run the configured local command first. If the user approves latest, run `npx react-doctor@latest --verbose` and fix issues by severity: errors first, then warnings.

## /doctor - full local triage workflow

When the user types `/doctor`, says "run react doctor", or asks for a full triage / cleanup pass (not just a regression check), fetch the canonical local-triage playbook and follow every step in it:

```bash
curl --fail --silent --show-error \
  --header 'Cache-Control: no-cache' \
  https://www.react.doctor/prompts/react-doctor-agent.md
```

The playbook is the single source of truth — a scan → filter → triage → fix → validate loop that edits the working tree directly (never commits, never opens PRs). Updating the prompt at its source updates every agent on its next fetch — no skill reinstall needed.

Pair it with the matching per-rule prompts at `https://www.react.doctor/prompts/rules/<plugin>/<rule>.md` (fetched on demand inside the playbook) so each fix uses the canonical, reviewer-tested recipe.

## Configuring or explaining rules

When the user wants to understand a rule, disagrees with one, or wants to disable / tune which rules run (not fix code), use the `doctor-explain` skill (alias `/doctor-config`). Start with `npx react-doctor@latest rules explain <rule>`, then apply the narrowest control via `npx react-doctor@latest rules disable|set|category|ignore-tag …`, which edits your `doctor.config.*` (or `package.json#reactDoctor`).

## Optional command

```bash
npx react-doctor@latest --verbose --diff
```

| Flag        | Purpose                                       |
| ----------- | --------------------------------------------- |
| `.`         | Scan current directory                        |
| `--verbose` | Show affected files and line numbers per rule |
| `--diff`    | Only scan changed files vs base branch        |
| `--score`   | Output only the numeric score                 |
