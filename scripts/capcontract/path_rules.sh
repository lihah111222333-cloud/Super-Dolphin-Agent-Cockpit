#!/usr/bin/env bash

CAPCONTRACT_PATH_RULES_LOADED=0
CAPCONTRACT_PATH_RULE_KINDS=()
CAPCONTRACT_PATH_RULE_PATHS=()

load_capcontract_path_rules() {
  local go_bin="${1:-${CAPCONTRACT_PATH_RULES_GO_BIN:-go}}"
  local output record kind rule_path existing_index
  local sentinel=$'\034'
  if [[ -z "${go_bin}" ]]; then
    printf 'capcontract path rules: Go executable is empty\n' >&2
    return 1
  fi
  if ! output="$("${go_bin}" run ./scripts/capcontract --print-path-rules && printf '%s' "${sentinel}")"; then
    printf 'capcontract path rules: generator command failed\n' >&2
    return 1
  fi
  if [[ "${output}" != *"${sentinel}" ]]; then
    printf 'capcontract path rules: generator output framing failed\n' >&2
    return 1
  fi
  output="${output%"${sentinel}"}"
  if [[ "${output}" == *"${sentinel}"* ]]; then
    printf 'capcontract path rules: generator returned reserved framing byte\n' >&2
    return 1
  fi
  if [[ -z "${output}" ]]; then
    printf 'capcontract path rules: generator returned no rules\n' >&2
    return 1
  fi

  CAPCONTRACT_PATH_RULE_KINDS=()
  CAPCONTRACT_PATH_RULE_PATHS=()
  while [[ -n "${output}" ]]; do
    if [[ "${output}" != *$'\n'* ]]; then
      printf 'capcontract path rules: malformed TSV missing final newline\n' >&2
      return 1
    fi
    record="${output%%$'\n'*}"
    output="${output#*$'\n'}"
    if [[ -z "${record}" || "${record}" != *$'\t'* ]]; then
      printf 'capcontract path rules: malformed TSV line\n' >&2
      return 1
    fi
    kind="${record%%$'\t'*}"
    rule_path="${record#*$'\t'}"
    if [[ -z "${kind}" || -z "${rule_path}" || "${rule_path}" == *$'\t'* ]]; then
      printf 'capcontract path rules: malformed TSV line for %q\n' "${kind}" >&2
      return 1
    fi
    case "${kind}" in
      exact|tree)
        ;;
      *)
        printf 'capcontract path rules: unsupported rule kind %q\n' "${kind}" >&2
        return 1
        ;;
    esac
    if ! validate_capcontract_repository_path "${rule_path}"; then
      printf 'capcontract path rules: invalid rule path %q\n' "${rule_path}" >&2
      return 1
    fi
    for existing_index in "${!CAPCONTRACT_PATH_RULE_PATHS[@]}"; do
      if [[ "${CAPCONTRACT_PATH_RULE_KINDS[existing_index]}" == "${kind}" && "${CAPCONTRACT_PATH_RULE_PATHS[existing_index]}" == "${rule_path}" ]]; then
        printf 'capcontract path rules: duplicate %s rule %q\n' "${kind}" "${rule_path}" >&2
        return 1
      fi
    done
    CAPCONTRACT_PATH_RULE_KINDS+=("${kind}")
    CAPCONTRACT_PATH_RULE_PATHS+=("${rule_path}")
  done
  if [[ "${#CAPCONTRACT_PATH_RULE_PATHS[@]}" -eq 0 ]]; then
    printf 'capcontract path rules: generator returned no usable rules\n' >&2
    return 1
  fi
  CAPCONTRACT_PATH_RULES_LOADED=1
}

is_capcontract_change() {
  local candidate="$1"
  local index kind rule_path
  if [[ "${CAPCONTRACT_PATH_RULES_LOADED}" -ne 1 ]]; then
    printf 'capcontract path rules: matcher used before rules were loaded\n' >&2
    return 2
  fi
  if ! validate_capcontract_repository_path "${candidate}"; then
    printf 'capcontract path rules: invalid changed path %q\n' "${candidate}" >&2
    return 2
  fi
  for index in "${!CAPCONTRACT_PATH_RULE_PATHS[@]}"; do
    kind="${CAPCONTRACT_PATH_RULE_KINDS[index]}"
    rule_path="${CAPCONTRACT_PATH_RULE_PATHS[index]}"
    if [[ "${kind}" == "exact" && "${candidate}" == "${rule_path}" ]]; then
      return 0
    fi
    if [[ "${kind}" == "tree" && ("${candidate}" == "${rule_path}" || "${candidate}" == "${rule_path}/"*) ]]; then
      return 0
    fi
  done
  return 1
}

validate_capcontract_repository_path() {
  local candidate="$1"
  [[ -n "${candidate}" ]] || return 1
  [[ "${candidate}" != /* ]] || return 1
  [[ "${candidate}" != . && "${candidate}" != .. ]] || return 1
  [[ "${candidate}" != ./* && "${candidate}" != ../* ]] || return 1
  [[ "${candidate}" != */../* && "${candidate}" != */.. ]] || return 1
  [[ "${candidate}" != */./* && "${candidate}" != */. ]] || return 1
  case "${candidate}" in
    *\\*) return 1 ;;
  esac
  [[ "${candidate}" != *$'\t'* && "${candidate}" != *$'\r'* && "${candidate}" != *$'\n'* ]] || return 1
}
