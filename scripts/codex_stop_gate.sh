#!/usr/bin/env bash
set -u

gate_bin=$(command -v super-dolphin-gate 2>/dev/null || true)
if [[ -z "$gate_bin" || ! -x "$gate_bin" ]]; then
  printf '%s\n' '{"decision":"block","reason":"Codex gate blocked: trusted super-dolphin-gate CLI is not installed; install and configure it, then stop again."}'
  exit 0
fi

exec "$gate_bin" hook codex
