#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"
REAL_GO="${1:-}"
REQUESTED_MODULE="${2:-}"
GIT_BINARY="${SUPER_DOLPHIN_GATE_GIT:-}"

if { [ "$#" -ne 1 ] && [ "$#" -ne 2 ]; } || [[ "$REAL_GO" != /* ]] || [ ! -x "$REAL_GO" ]; then
  echo "usage: check_nested_go_modules.sh /absolute/path/to/go [exact-tracked-module]" >&2
  exit 2
fi

if [ -z "$GIT_BINARY" ]; then
	 GIT_BINARY="$(command -v git || true)"
fi
if [[ "$GIT_BINARY" != /* ]] || [ ! -x "$GIT_BINARY" ]; then
	 echo "check_nested_go_modules.sh requires executable git; set SUPER_DOLPHIN_GATE_GIT to the fixed worker runtime path" >&2
	 exit 2
fi

modules=()
module_list="$(mktemp)"
trap 'rm -f "$module_list"' EXIT
"$GIT_BINARY" -C "$ROOT_DIR" ls-files -z -- '*/go.mod' >"$module_list"
while IFS= read -r -d '' go_mod; do
  module_dir="${go_mod%/go.mod}"
  if [ -z "$module_dir" ] || [[ "$module_dir" == /* ]] || [[ "$module_dir" == *..* ]] || [ ! -f "$ROOT_DIR/$go_mod" ]; then
    echo "invalid tracked nested Go module path: $go_mod" >&2
    exit 1
  fi
  modules+=("$module_dir")
done <"$module_list"

if [ "${#modules[@]}" -eq 0 ]; then
  echo "no tracked nested Go modules found" >&2
  exit 1
fi

if [ -n "$REQUESTED_MODULE" ]; then
  matched=""
  for module_dir in "${modules[@]}"; do
    if [ "$module_dir" = "$REQUESTED_MODULE" ]; then
      matched="$module_dir"
      break
    fi
  done
  if [ -z "$matched" ]; then
    echo "requested nested Go module is not tracked: $REQUESTED_MODULE" >&2
    exit 2
  fi
  modules=("$matched")
fi

for module_dir in "${modules[@]}"; do
  echo "nested go list: module=$module_dir packages=./..."
  (cd "$ROOT_DIR/$module_dir" && "$REAL_GO" list ./...)
  echo "nested go vet: module=$module_dir packages=./..."
  (cd "$ROOT_DIR/$module_dir" && "$REAL_GO" vet ./...)
done
