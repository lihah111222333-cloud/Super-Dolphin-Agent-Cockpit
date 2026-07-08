#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mode="all"
check=0
extra_args=()

usage() {
  cat <<'EOF'
Usage:
  scripts/refresh_generated_artifacts.sh [all|codemap|project-map|capcontract] [--check] [--repo PATH] [-- EXTRA_ARGS...]

Modes:
  all          Refresh or check all generated codemap, project-map, and capability-contract artifacts.
  codemap      Refresh or check docs/doc/codemap/README.md and ai-index.json.
  project-map  Refresh or check docs/doc/codemap/project-map artifacts.
  capcontract  Refresh or check docs/doc/codemap/capability-contract/capability_manifest.json.

Options:
  --check      Run read-only stale checks instead of refreshing files.
  --repo PATH  Run from a specific repository root.
  --           Forward remaining arguments to project-map via PROJECT_MAP_ARGS.
EOF
}

if [ $# -gt 0 ] && [[ "$1" != --* ]]; then
  mode="$1"
  shift
fi

while [ $# -gt 0 ]; do
  case "$1" in
    --check)
      check=1
      shift
      ;;
    --repo)
      if [ $# -lt 2 ] || [ -z "$2" ]; then
        echo "--repo requires a path" >&2
        exit 2
      fi
      repo_root="$2"
      shift 2
      ;;
    --)
      shift
      extra_args=("$@")
      break
      ;;
    -h|--help|help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

run_make() {
  (
    cd "$repo_root"
    make "$@"
  )
}

project_map_args_value() {
  local joined=""
  local arg
  for arg in "${extra_args[@]}"; do
    if [ -n "$joined" ]; then
      joined+=" "
    fi
    joined+="$arg"
  done
  printf '%s' "$joined"
}

refresh_codemap() {
  if [ "$check" -eq 1 ]; then
    echo "[generated] check codemap artifacts"
    run_make codemap-check
    return 0
  fi
  echo "[generated] refresh codemap artifacts"
  run_make codemap-refresh
}

refresh_project_map() {
  local target="project-map-refresh"
  if [ "$check" -eq 1 ]; then
    target="project-map-check"
    echo "[generated] check AI project map"
  else
    echo "[generated] refresh AI project map"
  fi
  if [ ${#extra_args[@]} -gt 0 ]; then
    run_make "$target" PROJECT_MAP_ARGS="$(project_map_args_value)"
    return 0
  fi
  run_make "$target"
}

refresh_capcontract() {
  if [ "$check" -eq 1 ]; then
    echo "[generated] check capability contract manifest"
    run_make capcontract-check
    return 0
  fi
  echo "[generated] refresh capability contract manifest"
  run_make capcontract-refresh
}

case "$mode" in
  all)
    refresh_codemap
    refresh_project_map
    refresh_capcontract
    ;;
  codemap)
    refresh_codemap
    ;;
  project-map)
    refresh_project_map
    ;;
  capcontract)
    refresh_capcontract
    ;;
  *)
    echo "Unknown mode: $mode" >&2
    usage >&2
    exit 2
    ;;
esac
