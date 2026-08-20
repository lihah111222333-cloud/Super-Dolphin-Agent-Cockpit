#!/usr/bin/env bash

backup_path() {
  local source_path="$1"
  printf '%s.bak' "$source_path"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  backup_path "${1:-fixture.txt}"
  printf '\n'
fi
