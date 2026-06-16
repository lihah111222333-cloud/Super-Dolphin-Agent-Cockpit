#!/usr/bin/env bash
set -euo pipefail

level="${1:-change}"
root="${2:-.}"

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)"
skills_dir="$(CDPATH= cd -- "$script_dir/../.." && pwd -P)"
cd "$root"
root_dir="$(pwd -P)"
tmp_cover=""

run() {
  echo "+ $*"
  "$@"
}

warn() {
  echo "WARN: $*" >&2
}

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

have() {
  command -v "$1" >/dev/null 2>&1
}

has_go_module() {
  [ -f go.mod ]
}

skip_no_go_mod() {
  echo "SKIP: no go.mod found under $root_dir"
}

case "$level" in
  change|commit|release) ;;
  *)
    echo "usage: $0 [change|commit|release] [project-root]" >&2
    exit 2
    ;;
esac

check_gofmt() {
  if ! has_go_module; then
    skip_no_go_mod
    return
  fi

  tmp_files="$(mktemp)"
  trap 'rm -f "$tmp_files" "$tmp_cover"' EXIT
  find . \
    -type f \
    -name '*.go' \
    ! -path './vendor/*' \
    ! -path './.git/*' \
    -print > "$tmp_files"

  if [ ! -s "$tmp_files" ]; then
    echo "SKIP: no Go files found"
    return
  fi

  unformatted=""
  while IFS= read -r file; do
    out="$(gofmt -l "$file")"
    if [ -n "$out" ]; then
      unformatted="${unformatted}${out}
"
    fi
  done < "$tmp_files"

  if [ -n "$unformatted" ]; then
    echo "Go files need gofmt:" >&2
    printf "%s" "$unformatted" >&2
    fail "run gofmt on the files above"
  fi
}

check_boundary() {
  if ! has_go_module; then
    skip_no_go_mod
    return
  fi

  if have python3; then
    run python3 "$script_dir/check_go_boundaries.py" "$root_dir"
  else
    fail "python3 is required for architecture boundary checks"
  fi
}

check_ast_rules() {
  if ! has_go_module; then
    skip_no_go_mod
    return
  fi

  if have python3; then
    run python3 "$script_dir/check_go_ast_rules.py" "$root_dir"
  else
    fail "python3 is required for Go AST architecture checks"
  fi
}

check_size() {
  if ! has_go_module; then
    skip_no_go_mod
    return
  fi

  if have python3; then
    run python3 "$script_dir/check_go_size.py" "$root_dir"
  else
    fail "python3 is required for Go source size checks"
  fi
}

check_comments() {
  if ! has_go_module; then
    skip_no_go_mod
    return
  fi

  if have python3; then
    run python3 "$script_dir/check_go_comments.py" "$root_dir"
  else
    fail "python3 is required for Go comment checks"
  fi
}

check_secrets() {
  if have python3; then
    run python3 "$script_dir/check_secrets.py" "$root_dir"
  else
    fail "python3 is required for secret scanning"
  fi
}

check_config_policy() {
  if have python3; then
    run python3 "$script_dir/check_config_policy.py" "$root_dir"
  else
    fail "python3 is required for configuration policy checks"
  fi
}

check_actions() {
  if have python3; then
    run python3 "$script_dir/check_github_actions.py" "$root_dir"
  else
    fail "python3 is required for GitHub Actions checks"
  fi
}

check_migrations() {
  if have python3; then
    run python3 "$script_dir/check_migrations.py" "$root_dir"
  else
    fail "python3 is required for migration checks"
  fi
}

check_generated_drift() {
  run "$script_dir/check_generated_drift.sh" "$root_dir"
}

check_project_map_not_tracked() {
  if ! have git || ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    warn "not in a git worktree; cannot verify .project-map tracking status"
    return
  fi

  if ! git check-ignore -q .project-map/ 2>/dev/null && ! git check-ignore -q .project-map 2>/dev/null; then
    fail ".project-map/ must be ignored by git"
  fi

  tracked="$(git ls-files .project-map 2>/dev/null || true)"
  staged="$(git diff --cached --name-only -- .project-map 2>/dev/null || true)"
  if [ -n "$tracked" ] || [ -n "$staged" ]; then
    {
      echo "Tracked or staged project map files are not allowed:"
      printf "%s\n" "$tracked"
      printf "%s\n" "$staged"
    } >&2
    fail "remove .project-map files from git; they are local generated indexes"
  fi
}

generate_project_map() {
  map_script="$skills_dir/mapping-go-projects/scripts/generate_go_project_map.go"
  if [ ! -f "$map_script" ]; then
    fail "project map generator not found: $map_script"
  fi
  if ! have go; then
    fail "go is required to generate the project map"
  fi

  check_project_map_not_tracked
  run go run "$map_script" -root "$root_dir" -out "$root_dir/.project-map"
  check_project_map_not_tracked
}

check_change() {
  check_secrets
  check_config_policy
  check_actions
  check_gofmt
  check_size
  check_comments
  if has_go_module; then
    run go test ./...
  fi
  check_boundary
  check_ast_rules
  check_lint
}

check_lint() {
  if ! has_go_module; then
    return
  fi

  if have golangci-lint; then
    run golangci-lint run
  elif [ "${GO_GUARD_REQUIRE_GOLANGCI:-0}" = "1" ]; then
    fail "install golangci-lint, or unset GO_GUARD_REQUIRE_GOLANGCI"
  elif have staticcheck; then
    run staticcheck ./...
  elif [ "${GO_GUARD_STRICT_TOOLS:-0}" = "1" ]; then
    fail "install golangci-lint or staticcheck, or unset GO_GUARD_STRICT_TOOLS"
  else
    warn "golangci-lint/staticcheck not installed; skipping optional lint"
  fi
}

check_cmd_build() {
  if ! has_go_module; then
    return
  fi

  if [ ! -d cmd ]; then
    echo "SKIP: no cmd directory found"
    return
  fi
  if go list ./cmd/... >/dev/null 2>&1; then
    run go build ./cmd/...
  else
    echo "SKIP: no buildable cmd packages found"
  fi
}

check_mod_tidy() {
  if ! has_go_module; then
    return
  fi

  before_status=""
  if have git && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    before_status="$(git status --short -- go.mod go.sum)"
  fi
  run go mod tidy
  if have git && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    after_status="$(git status --short -- go.mod go.sum)"
    if [ "$after_status" != "$before_status" ]; then
      echo "$after_status" >&2
      fail "go mod tidy changed go.mod/go.sum"
    fi
  else
    warn "not in a git worktree; cannot verify go.mod/go.sum status"
  fi
}

check_mod_verify() {
  if has_go_module; then
    run go mod verify
  fi
}

check_commit() {
  check_change
  check_mod_tidy
  check_mod_verify
  generate_project_map
  check_generated_drift
  check_migrations
  if has_go_module; then
    run go vet ./...
  fi
  check_cmd_build
}

check_optional_tool() {
  tool="$1"
  shift
  if have "$tool"; then
    run "$tool" "$@"
  elif [ "${GO_GUARD_STRICT_TOOLS:-0}" = "1" ]; then
    fail "install $tool, or unset GO_GUARD_STRICT_TOOLS"
  else
    warn "$tool not installed; skipping optional $tool check"
  fi
}

check_coverage() {
  if ! has_go_module; then
    return
  fi

  if [ -z "${GO_GUARD_COVERAGE_MIN:-}" ]; then
    return
  fi
  tmp_cover="$(mktemp)"
  run go test ./... -coverprofile="$tmp_cover"
  coverage="$(go tool cover -func="$tmp_cover" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
  awk -v actual="$coverage" -v min="$GO_GUARD_COVERAGE_MIN" 'BEGIN { exit !(actual + 0 >= min + 0) }' \
    || fail "coverage ${coverage}% is below required ${GO_GUARD_COVERAGE_MIN}%"
}

check_release() {
  check_commit
  if has_go_module; then
    run go test -race ./...
    check_optional_tool govulncheck ./...
    check_optional_tool gosec ./...
  fi
  check_coverage
}

case "$level" in
  change) check_change ;;
  commit) check_commit ;;
  release) check_release ;;
esac

echo "Go project guard passed: $level"
