#!/usr/bin/env bash

assert_equals() {
  local actual="$1"
  local expected="$2"
  if [[ "$actual" != "$expected" ]]; then
    printf 'expected %q, got %q\n' "$expected" "$actual" >&2
    return 1
  fi
}
