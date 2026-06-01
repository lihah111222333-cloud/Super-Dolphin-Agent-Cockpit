#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage:
  scripts/hook_code_checks.sh index <label>
  scripts/hook_code_checks.sh range <label> <local-sha> <rev-range>
USAGE
}

fail_usage() {
  echo "FAIL: hook code checks: $*" >&2
  exit 2
}

ROOT_DIR="$(git rev-parse --show-toplevel)"
SOURCE_ROOT="$ROOT_DIR"

path_statuses=()
path_values=()

cleanup_worktree() {
  if [[ -n "${SNAPSHOT_DIR:-}" && -d "${SNAPSHOT_DIR:-}" ]]; then
    git_without_git_env -C "$SOURCE_ROOT" worktree remove --force "$SNAPSHOT_DIR" >/dev/null 2>&1 || true
  fi
  if [[ -n "${SNAPSHOT_PARENT:-}" && -d "${SNAPSHOT_PARENT:-}" ]]; then
    rm -rf "$SNAPSHOT_PARENT"
  fi
}

trap cleanup_worktree EXIT

git_without_git_env() (
  unset $(git -C "$SOURCE_ROOT" rev-parse --local-env-vars)
  unset GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT
  local name
  while IFS='=' read -r name _; do
    case "$name" in
      GIT_CONFIG_KEY_*|GIT_CONFIG_VALUE_*) unset "$name" ;;
    esac
  done < <(env)
  git "$@"
)

append_name_status_stream() {
  local status old_path new_path path
  while IFS= read -r -d '' status; do
    case "$status" in
      R*|C*)
        IFS= read -r -d '' old_path || fail_usage "rename/copy missing old path"
        IFS= read -r -d '' new_path || fail_usage "rename/copy missing new path"
        path_statuses+=("$status")
        path_values+=("$old_path"$'\t'"$new_path")
        ;;
      *)
        IFS= read -r -d '' path || fail_usage "name-status missing path"
        path_statuses+=("$status")
        path_values+=("$path")
        ;;
    esac
  done
}

collect_index_paths() {
  if git -C "$SOURCE_ROOT" rev-parse --verify --quiet HEAD >/dev/null; then
    append_name_status_stream < <(git -C "$SOURCE_ROOT" diff --cached --name-status -z --diff-filter=ACMRD HEAD --)
  else
    append_name_status_stream < <(git -C "$SOURCE_ROOT" diff --cached --name-status -z --diff-filter=ACMRD --)
  fi
}

collect_range_paths() {
  local range="$1"
  if [[ "$range" == *..* ]]; then
    append_name_status_stream < <(git -C "$SOURCE_ROOT" diff --name-status -z --diff-filter=ACMRD "$range")
  else
    append_name_status_stream < <(git -C "$SOURCE_ROOT" diff-tree --root --no-commit-id --name-status -z -r "$range")
  fi
}

path_exists_in_snapshot() {
  [[ -e "$SNAPSHOT_DIR/$1" ]]
}

is_go_path() {
  case "$1" in
    *.go) return 0 ;;
    *) return 1 ;;
  esac
}

is_frontend_code_path() {
  case "$1" in
    cmd/agent-terminal/frontend/node_modules/*|\
    cmd/agent-terminal/frontend/.vite-cache/*|\
    cmd/agent-terminal/frontend/.build-cache/*|\
    cmd/agent-terminal/frontend/dist/*|\
    cmd/agent-terminal/frontend/playwright-report/*|\
    cmd/agent-terminal/frontend/test-results/*|\
    cmd/agent-terminal/frontend/review/*|\
    cmd/agent-terminal/frontend/full_test_output.txt|\
    cmd/agent-terminal/frontend/.DS_Store|\
    frontend-app/node_modules/*|\
    frontend-app/dist/*|\
    frontend-app/playwright-report/*|\
    frontend-app/test-results/*|\
    frontend-app/.playwright-cli/*|\
    frontend-app/.DS_Store)
      return 1
      ;;
    cmd/agent-terminal/frontend/*|frontend-app/*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

frontend_root_for_path() {
  case "$1" in
    cmd/agent-terminal/frontend/*) printf '%s\n' "cmd/agent-terminal/frontend" ;;
    frontend-app/*) printf '%s\n' "frontend-app" ;;
  esac
}

pkg_for_path() {
  local dir
  dir="$(dirname "$1")"
  if [[ "$dir" == "." ]]; then
    printf '.\n'
  else
    printf './%s\n' "$dir"
  fi
}

array_contains() {
  local needle="$1"
  local existing
  shift
  for existing in "$@"; do
    if [[ "$existing" == "$needle" ]]; then
      return 0
    fi
  done
  return 1
}

go_pkg_candidates=()
gofmt_files=()
frontend_roots=()

append_go_pkg_candidate() {
  set +u
  if ! array_contains "$1" "${go_pkg_candidates[@]}"; then
    go_pkg_candidates+=("$1")
  fi
  set -u
}

append_gofmt_file() {
  set +u
  if ! array_contains "$1" "${gofmt_files[@]}"; then
    gofmt_files+=("$1")
  fi
  set -u
}

append_frontend_root() {
  set +u
  if ! array_contains "$1" "${frontend_roots[@]}"; then
    frontend_roots+=("$1")
  fi
  set -u
}

append_changed_import() {
  set +u
  if ! array_contains "$1" "${changed_imports[@]}"; then
    changed_imports+=("$1")
  fi
  set -u
}

append_affected_go_pkg() {
  set +u
  if ! array_contains "$1" "${affected_go_pkgs[@]}"; then
    affected_go_pkgs+=("$1")
  fi
  set -u
}

classify_paths() {
  local i status value old_path new_path path frontend_root
  for i in "${!path_statuses[@]}"; do
    status="${path_statuses[$i]}"
    value="${path_values[$i]}"
    case "$status" in
      R*|C*)
        old_path="${value%%$'\t'*}"
        new_path="${value#*$'\t'}"
        if is_go_path "$old_path"; then
          append_go_pkg_candidate "$(pkg_for_path "$old_path")"
        fi
        if is_go_path "$new_path"; then
          append_go_pkg_candidate "$(pkg_for_path "$new_path")"
          if path_exists_in_snapshot "$new_path"; then
            append_gofmt_file "$new_path"
          fi
        fi
        if is_frontend_code_path "$new_path"; then
          frontend_root="$(frontend_root_for_path "$new_path")"
          append_frontend_root "$frontend_root"
        fi
        ;;
      D)
        path="$value"
        if is_go_path "$path"; then
          append_go_pkg_candidate "$(pkg_for_path "$path")"
        fi
        if is_frontend_code_path "$path"; then
          frontend_root="$(frontend_root_for_path "$path")"
          append_frontend_root "$frontend_root"
        fi
        ;;
      *)
        path="$value"
        if is_go_path "$path"; then
          append_go_pkg_candidate "$(pkg_for_path "$path")"
          if path_exists_in_snapshot "$path"; then
            append_gofmt_file "$path"
          fi
        fi
        if is_frontend_code_path "$path"; then
          frontend_root="$(frontend_root_for_path "$path")"
          append_frontend_root "$frontend_root"
        fi
        ;;
    esac
  done
}

create_index_snapshot() {
  local tree commit
  tree="$(git -C "$SOURCE_ROOT" write-tree)"
  if git -C "$SOURCE_ROOT" rev-parse --verify --quiet HEAD >/dev/null; then
    commit="$(printf 'hook index snapshot\n' | git -C "$SOURCE_ROOT" commit-tree "$tree" -p HEAD)"
  else
    commit="$(printf 'hook index snapshot\n' | git -C "$SOURCE_ROOT" commit-tree "$tree")"
  fi
  create_commit_snapshot "$commit"
}

create_commit_snapshot() {
  local commit="$1"
  SNAPSHOT_PARENT="$(mktemp -d -t hook-code-checks.XXXXXX)"
  SNAPSHOT_DIR="$SNAPSHOT_PARENT/worktree"
  git_without_git_env -C "$SOURCE_ROOT" worktree add --detach --quiet "$SNAPSHOT_DIR" "$commit"
  link_runtime_dirs
}

link_runtime_dirs() {
  local root
  for root in cmd/agent-terminal/frontend frontend-app; do
    if [[ -d "$SOURCE_ROOT/$root/node_modules" && ! -e "$SNAPSHOT_DIR/$root/node_modules" ]]; then
      mkdir -p "$SNAPSHOT_DIR/$root"
      ln -s "$SOURCE_ROOT/$root/node_modules" "$SNAPSHOT_DIR/$root/node_modules"
    fi
  done
}

run_without_git_env() (
  unset $(git -C "$SOURCE_ROOT" rev-parse --local-env-vars)
  unset GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT
  local name
  while IFS='=' read -r name _; do
    case "$name" in
      GIT_CONFIG_KEY_*|GIT_CONFIG_VALUE_*) unset "$name" ;;
    esac
  done < <(env)
  "$@"
)

resolve_changed_imports() {
  local p pkg_dir import_path go_list_err
  changed_imports=()
  for p in "${go_pkg_candidates[@]}"; do
    pkg_dir="${p#./}"
    if [[ "$p" == "." ]]; then
      pkg_dir="."
    fi
    if [[ ! -d "$SNAPSHOT_DIR/$pkg_dir" ]]; then
      continue
    fi
    go_list_err="$(mktemp -t hook-code-checks-golist.XXXXXX)"
    if import_path="$(cd "$SNAPSHOT_DIR" && run_without_git_env go list -f '{{.ImportPath}}' "$p" 2>"$go_list_err")"; then
      append_changed_import "$import_path"
      rm -f "$go_list_err"
      continue
    fi
    if grep -q "no Go files" "$go_list_err"; then
      rm -f "$go_list_err"
      continue
    fi
    echo "❌ go list 失败：$p" >&2
    cat "$go_list_err" >&2
    rm -f "$go_list_err"
    exit 1
  done
}

resolve_affected_go_pkgs() {
	local list_file line import_path dir deps changed rel
	affected_go_pkgs=()
  if [[ ${#changed_imports[@]} -eq 0 ]]; then
    return 0
  fi
  list_file="$(mktemp -t hook-code-checks-golist.XXXXXX)"
	local patterns=()
	add_go_list_pattern patterns cmd/mcp-ida ./cmd/mcp-ida/...
	add_go_list_pattern patterns cmd/mcp-lsp ./cmd/mcp-lsp/...
	add_go_list_pattern patterns cmd/mcp-orch ./cmd/mcp-orch/...
	add_go_list_pattern patterns cmd/snake ./cmd/snake/...
	add_go_list_pattern patterns internal ./internal/...
	add_go_list_pattern patterns pkg ./pkg/...
	add_go_list_pattern patterns scripts ./scripts/...
	if [[ ${#patterns[@]} -eq 0 ]]; then
		return 0
	fi
	if ! (cd "$SNAPSHOT_DIR" && run_without_git_env go list -f '{{.ImportPath}}{{printf "\t"}}{{.Dir}}{{printf "\t"}}{{join .Deps "\t"}}' "${patterns[@]}" >"$list_file"); then
		echo "❌ go list failed, unable to compute affected packages" >&2
		exit 1
	fi
  while IFS=$'\t' read -r import_path dir deps; do
    for changed in "${changed_imports[@]}"; do
      if [[ "$import_path" == "$changed" || $'\t'"$deps"$'\t' == *$'\t'"$changed"$'\t'* ]]; then
        rel="${dir#$SNAPSHOT_DIR/}"
        if [[ "$rel" == "$dir" ]]; then
          rel="."
        fi
        if [[ "$rel" == "." ]]; then
          append_affected_go_pkg "."
        else
          append_affected_go_pkg "./$rel"
        fi
        break
      fi
    done
  done <"$list_file"
	rm -f "$list_file"
}

add_go_list_pattern() {
	local var_name="$1"
	local dir="$2"
	local pattern="$3"
	if [[ ! -d "$SNAPSHOT_DIR/$dir" ]]; then
		return 0
	fi
	if ! find "$SNAPSHOT_DIR/$dir" -type f -name '*.go' -print -quit | grep -q .; then
		return 0
	fi
	eval "${var_name}+=(\"\$pattern\")"
}

run_go_checks() {
  local label="$1"
  local fmt_out
  if [[ ${#go_pkg_candidates[@]} -eq 0 ]]; then
    return 0
  fi
  if [[ ${#gofmt_files[@]} -gt 0 ]]; then
    echo "[$label] gofmt..."
    fmt_out="$(mktemp -t hook-code-checks-gofmt.XXXXXX)"
    if ! (cd "$SNAPSHOT_DIR" && gofmt -l "${gofmt_files[@]}") >"$fmt_out" 2>&1; then
      echo "❌ gofmt 自身执行失败：" >&2
      cat "$fmt_out" >&2
      exit 1
    fi
    if [[ -s "$fmt_out" ]]; then
      echo "❌ 以下 staged/pushed Go 文件未格式化：" >&2
      cat "$fmt_out" >&2
      exit 1
    fi
    rm -f "$fmt_out"
  else
    echo "[$label] gofmt... (skipped: only deleted Go files)"
  fi

  resolve_changed_imports
  resolve_affected_go_pkgs
  if [[ ${#affected_go_pkgs[@]} -eq 0 ]]; then
    echo "[$label] go package tests... (skipped: no remaining Go package)"
    return 0
  fi

  echo "[$label] go vet: ${affected_go_pkgs[*]}"
  (cd "$SNAPSHOT_DIR" && run_without_git_env go vet "${affected_go_pkgs[@]}")

  echo "[$label] go package tests: ${affected_go_pkgs[*]}"
  (cd "$SNAPSHOT_DIR" && run_without_git_env ./scripts/test_with_guard.sh "${affected_go_pkgs[@]}" -count=1)
}

run_frontend_checks() {
  local label="$1"
  local root
  if [[ ${#frontend_roots[@]} -eq 0 ]]; then
    return 0
  fi
  for root in "${frontend_roots[@]}"; do
    case "$root" in
      cmd/agent-terminal/frontend)
        echo "[$label] frontend codebase guard: $root"
        (cd "$SNAPSHOT_DIR/$root" && node scripts/size-guard.cjs)
        echo "[$label] frontend package tests: $root"
        (cd "$SNAPSHOT_DIR/$root" && npx vitest run)
        ;;
      frontend-app)
        echo "[$label] frontend package tests: $root"
        (cd "$SNAPSHOT_DIR/$root" && npm test)
        ;;
      *)
        echo "❌ unknown frontend root: $root" >&2
        exit 1
        ;;
    esac
  done
}

run_checks() {
  local label="$1"
  classify_paths
  if [[ ${#go_pkg_candidates[@]} -eq 0 && ${#frontend_roots[@]} -eq 0 ]]; then
    echo "[$label] code checks... (skipped: no backend/frontend changes)"
    return 0
  fi
  run_go_checks "$label"
  run_frontend_checks "$label"
}

case "${1:-}" in
  index)
    [[ $# -eq 2 ]] || fail_usage "index requires <label>"
    collect_index_paths
    if [[ ${#path_statuses[@]} -eq 0 ]]; then
      echo "[$2] code checks... (skipped: no staged changes)"
      exit 0
    fi
    create_index_snapshot
    run_checks "$2"
    ;;
  range)
    [[ $# -eq 4 ]] || fail_usage "range requires <label> <local-sha> <rev-range>"
    collect_range_paths "$4"
    if [[ ${#path_statuses[@]} -eq 0 ]]; then
      echo "[$2] code checks... (skipped: no pushed changes)"
      exit 0
    fi
    create_commit_snapshot "$3"
    run_checks "$2"
    ;;
  --help|-h)
    usage
    ;;
  *)
    usage
    exit 2
    ;;
esac
