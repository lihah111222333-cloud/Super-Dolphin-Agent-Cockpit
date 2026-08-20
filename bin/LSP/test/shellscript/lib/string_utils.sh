#!/usr/bin/env bash

trim_string() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

upper_string() {
  printf '%s' "$1" | tr '[:lower:]' '[:upper:]'
}
