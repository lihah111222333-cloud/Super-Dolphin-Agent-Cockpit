#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"
cd "$root_dir"
tree=""
guard_args=()
while (($# > 0)); do
  case "$1" in
    --)
      guard_args+=("$@")
      set --
      break
      ;;
    --tree|-tree)
      if (($# < 2)) || [[ -n "$tree" ]] || [[ -z "${2//[[:space:]]/}" ]]; then
        echo "--tree requires exactly one candidate tree" >&2
        exit 1
      fi
      tree="$2"
      shift 2
      ;;
    --tree=*|-tree=*)
      if [[ -n "$tree" ]] || [[ -z "${1#*=}" ]]; then
        echo "--tree requires exactly one candidate tree" >&2
        exit 1
      fi
      tree="${1#*=}"
      shift
      ;;
    --repository|-repository|--repository=*|-repository=*)
      echo "--repository is wrapper-owned and cannot be overridden" >&2
      exit 1
      ;;
    *)
      guard_args+=("$1")
      shift
      ;;
  esac
done
if [[ -z "$tree" ]]; then
  echo "--tree is required; guard source must be candidate-bound" >&2
  exit 1
fi
gate_self="${SUPER_DOLPHIN_GATE_SELF:-}"
if [[ -z "$gate_self" || "$gate_self" != /* || ! -f "$gate_self" || ! -x "$gate_self" || -L "$gate_self" ]]; then
  echo "SUPER_DOLPHIN_GATE_SELF must name a trusted absolute executable" >&2
  exit 1
fi
echo "LOCAL_NON_AUTHORITATIVE NOT_VERIFIED: direct wrapper execution is diagnostic only" >&2
exec "$gate_self" worker mcp-lsp-catalog-guard --repository "$root_dir" --tree "$tree" ${guard_args[@]+"${guard_args[@]}"}
