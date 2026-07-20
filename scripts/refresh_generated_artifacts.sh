#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mode="all"
check=0
list_outputs=0
extra_args=()

usage() {
  cat <<'EOF'
Usage:
  scripts/refresh_generated_artifacts.sh [all|codemap|project-map|capcontract] [--check|--list-outputs] [--repo PATH] [-- EXTRA_ARGS...]

Modes:
  all          Refresh or check all generated codemap, project-map, and capability-contract artifacts.
  codemap      Refresh or check docs/doc/codemap/README.md and ai-index.json.
  project-map  Refresh or check docs/doc/codemap/project-map artifacts.
  capcontract  Refresh or check docs/doc/codemap/capability-contract/capability_manifest.json.

Options:
  --check      Run read-only stale checks instead of refreshing files.
  --list-outputs
               Print the generated output ownership contract as KIND<TAB>PATH.
               KIND is file for one path or tree for an owned directory.
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
    --list-outputs)
      list_outputs=1
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

list_codemap_outputs() {
  printf '%s\n' \
    $'file\tREADME.md' \
    $'file\tdocs/doc/codemap/13-archtest-boundaries.md' \
    $'file\tdocs/doc/codemap/README.md' \
    $'file\tdocs/doc/codemap/ai-index.json'
}

list_capcontract_outputs() {
  printf '%s\n' $'file\tdocs/doc/codemap/capability-contract/capability_manifest.json'
}

list_project_map_outputs() {
  printf '%s\n' $'tree\tdocs/doc/codemap/project-map'
}

list_owned_outputs() {
  case "$mode" in
    all)
      list_codemap_outputs
      list_capcontract_outputs
      list_project_map_outputs
      ;;
    codemap)
      list_codemap_outputs
      ;;
    capcontract)
      list_capcontract_outputs
      ;;
    project-map)
      list_project_map_outputs
      ;;
    *)
      echo "Unknown mode: $mode" >&2
      usage >&2
      return 2
      ;;
  esac
}

if [ "$list_outputs" -eq 1 ]; then
  if [ "$check" -eq 1 ] || [ ${#extra_args[@]} -gt 0 ]; then
    echo "--list-outputs cannot be combined with --check or forwarded arguments" >&2
    exit 2
  fi
  list_owned_outputs
  exit 0
fi

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
    refresh_capcontract
    refresh_project_map
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
