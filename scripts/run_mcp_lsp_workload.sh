#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"
cd "$root_dir"
tree=""
runner_args=()
while (($# > 0)); do
  case "$1" in
    --)
      runner_args+=("$@")
      set --
      break
      ;;
    --tree|-tree)
      if (($# < 2)) || [[ -n "$tree" ]] || [[ -z "${2//[[:space:]]/}" ]]; then
        echo "--tree requires exactly one candidate tree" >&2
        exit 1
      fi
      tree="$2"
      runner_args+=("--tree" "$2")
      shift 2
      ;;
    --tree=*|-tree=*)
      if [[ -n "$tree" ]] || [[ -z "${1#*=}" ]]; then
        echo "--tree requires exactly one candidate tree" >&2
        exit 1
      fi
      tree="${1#*=}"
      runner_args+=("--tree" "$tree")
      shift
      ;;
    --repository|-repository|--repository=*|-repository=*)
      echo "--repository is wrapper-owned and cannot be overridden" >&2
      exit 1
      ;;
    *)
      runner_args+=("$1")
      shift
      ;;
  esac
done
if [[ -z "$tree" ]]; then
  echo "--tree is required; runner source must be candidate-bound" >&2
  exit 1
fi
exec go run ./scripts/mcp_lsp_workload_runner "${runner_args[@]}"
