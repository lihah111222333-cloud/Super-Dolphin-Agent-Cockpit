#!/usr/bin/env bash
set -euo pipefail

target="${1:-}"
if [[ -z "$target" ]]; then
  echo "usage: $0 /path/to/linux-stage-or-tar.gz" >&2
  exit 2
fi

cleanup_dir=""
if [[ -f "$target" ]]; then
  case "$target" in
    *.tar.gz|*.tgz)
      cleanup_dir="$(mktemp -d "${TMPDIR:-/tmp}/super-dolphin-linux-verify.XXXXXX")"
      tar -xzf "$target" -C "$cleanup_dir"
      root_count="$(find "$cleanup_dir" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
      if [[ "$root_count" != "1" ]]; then
        echo "Linux package tarball must contain exactly one package root" >&2
        exit 1
      fi
      package_root="$(find "$cleanup_dir" -mindepth 1 -maxdepth 1 -type d -print -quit)"
      ;;
    *)
      echo "Linux verifier input must be a stage directory or .tar.gz artifact: $target" >&2
      exit 2
      ;;
  esac
elif [[ -d "$target" ]]; then
  package_root="$target"
else
  echo "Linux verifier input does not exist: $target" >&2
  exit 2
fi
trap 'if [[ -n "$cleanup_dir" ]]; then rm -rf "$cleanup_dir"; fi' EXIT

lsp_smoke_path="$package_root/bin:$package_root/lsp/bin:$package_root/lsp/node/bin:$package_root/lsp/node_modules/.bin:/usr/bin:/bin:/usr/sbin:/sbin"
lsp_server_specs=(
  "gopls|bin/gopls"
  "typescript-language-server|bin/typescript-language-server"
  "vscode-langservers-extracted|bin/vscode-css-language-server"
  "pyright|bin/pyright-langserver"
  "rust-analyzer|bin/rust-analyzer"
  "bash-language-server|bin/bash-language-server"
  "shellcheck|bin/shellcheck"
  "sg|bin/sg"
  "go|bin/go"
)

phase_start() {
  phase_label="$1"
  phase_started_at="$(date +%s)"
  echo "==> [$phase_label] start $(date '+%H:%M:%S')" >&2
}

phase_end() {
  local finished_at elapsed
  finished_at="$(date +%s)"
  elapsed=$((finished_at - phase_started_at))
  echo "==> [$phase_label] done in ${elapsed}s $(date '+%H:%M:%S')" >&2
}

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
    return
  fi
  echo "missing SHA-256 tool; install sha256sum or shasum" >&2
  exit 1
}

manifest_string() {
  local manifest="$1"
  local key="$2"
  awk -F'"' -v key="$key" '$2 == key { print $4; found=1; exit } END { if (!found) exit 1 }' "$manifest"
}

require_sha256_hex() {
  local label="$1"
  local value="$2"
  if [[ ! "$value" =~ ^[[:xdigit:]]{64}$ ]]; then
    echo "$label must be a 64-character SHA-256 hex digest" >&2
    exit 1
  fi
  printf '%s' "$value" | tr 'A-F' 'a-f'
}

require_manifest_relative_path() {
  local label="$1"
  local rel_path="$2"
  case "$rel_path" in
    ""|/*|..|../*|*/..|*/../*)
      echo "runtime manifest $label must be a relative path under Linux package root: $rel_path" >&2
      exit 1
      ;;
  esac
}

require_runtime_manifest_path() {
  local label="$1"
  local actual="$2"
  local expected="$3"
  local kind="$4"
  require_manifest_relative_path "$label" "$actual"
  if [[ "$actual" != "$expected" ]]; then
    echo "runtime manifest $label mismatch: expected $expected, got $actual" >&2
    exit 1
  fi
  local resolved="$package_root/$actual"
  case "$kind" in
    exec)
      if [[ ! -x "$resolved" ]]; then
        echo "runtime manifest $label points to missing executable: $resolved" >&2
        exit 1
      fi
      ;;
    file)
      if [[ ! -f "$resolved" ]]; then
        echo "runtime manifest $label points to missing file: $resolved" >&2
        exit 1
      fi
      ;;
    dir)
      if [[ ! -d "$resolved" ]]; then
        echo "runtime manifest $label points to missing directory: $resolved" >&2
        exit 1
      fi
      ;;
    *)
      echo "unknown runtime manifest resource kind: $kind" >&2
      exit 1
      ;;
  esac
}

json_value_at_path() {
  local manifest="$1"
  local target_path="$2"
  local mode="$3"
  awk -v target_path="$target_path" -v mode="$mode" '
    function fail() { exit 2 }
    function skip_ws() { while (pos <= length(json) && substr(json, pos, 1) ~ /^[[:space:]]$/) pos++ }
    function child_path(path, key) { return path == "" ? key : path "." key }
    function emit(start) { if (mode == "json") print substr(json, start, pos - start); found = 1; exit }
    function parse_string(    out, c, esc) {
      if (substr(json, pos, 1) != "\"") fail(); pos++; out = ""
      while (pos <= length(json)) {
        c = substr(json, pos, 1)
        if (c == "\"") { pos++; return out }
        if (c == "\\") { pos++; if (pos > length(json)) fail(); esc = substr(json, pos, 1); if (esc == "n") out = out "\n"; else if (esc == "r") out = out "\r"; else if (esc == "t") out = out "\t"; else if (esc == "b") out = out "\b"; else if (esc == "f") out = out "\f"; else if (esc == "u") { out = out "\\u" substr(json, pos + 1, 4); pos += 4 } else out = out esc; pos++; continue }
        out = out c; pos++
      }
      fail()
    }
    function parse_literal(    c) { while (pos <= length(json)) { c = substr(json, pos, 1); if (c == "," || c == "}" || c == "]" || c ~ /^[[:space:]]$/) return; pos++ } }
    function parse_array(path,    c) { pos++; skip_ws(); if (substr(json, pos, 1) == "]") { pos++; return } while (pos <= length(json)) { parse_value(path "[]"); skip_ws(); c = substr(json, pos, 1); if (c == ",") { pos++; skip_ws(); continue } if (c == "]") { pos++; return } fail() } fail() }
    function parse_object(path,    key, c) { pos++; skip_ws(); if (substr(json, pos, 1) == "}") { pos++; return } while (pos <= length(json)) { key = parse_string(); skip_ws(); if (substr(json, pos, 1) != ":") fail(); pos++; parse_value(child_path(path, key)); skip_ws(); c = substr(json, pos, 1); if (c == ",") { pos++; skip_ws(); continue } if (c == "}") { pos++; return } fail() } fail() }
    function parse_value(path,    start, c, value) { skip_ws(); start = pos; c = substr(json, pos, 1); if (c == "{") { parse_object(path); if (path == target_path && mode == "json") emit(start); return } if (c == "[") { parse_array(path); if (path == target_path && mode == "json") emit(start); return } if (c == "\"") { value = parse_string(); if (path == target_path) { if (mode == "string") print value; else print substr(json, start, pos - start); found = 1; exit } return } parse_literal(); if (path == target_path && mode == "json") emit(start) }
    { json = json $0 "\n" }
    END { pos = 1; found = 0; parse_value(""); if (!found) exit 1 }
  ' "$manifest"
}

lsp_manifest_value() {
  local manifest="$1"
  local server="$2"
  local field="$3"
  json_value_at_path "$manifest" "servers.$server.$field" string
}

lsp_server_version_args() {
  local server_id="$1"
  case "$server_id" in
    typescript-language-server|vscode-langservers-extracted|pyright)
      printf '\n'
      ;;
    gopls|go)
      printf '%s\n' "version"
      ;;
    jdtls)
      printf '%s\n' "-version"
      ;;
    bash-language-server)
      printf '%s\n' "--version"
      ;;
    shellcheck)
      printf '%s\n' "--version"
      ;;
    sg)
      printf '%s\n' "--help"
      ;;
    *)
      printf '%s\n' "--version"
      ;;
  esac
}

verify_lsp_manifest_entry() {
  local manifest="$1"
  local server_id="$2"
  local expected_rel_path="$3"
  local manifest_path version expected_sha actual_sha resolved
  if ! manifest_path="$(lsp_manifest_value "$manifest" "$server_id" path)"; then
    echo "LSP manifest missing path for $server_id: $manifest" >&2
    exit 1
  fi
  require_manifest_relative_path "LSP server $server_id path" "$manifest_path"
  if [[ "$manifest_path" != "lsp/$expected_rel_path" ]]; then
    echo "LSP manifest path mismatch for $server_id: expected lsp/$expected_rel_path, got $manifest_path" >&2
    exit 1
  fi
  if ! version="$(lsp_manifest_value "$manifest" "$server_id" version)" || [[ -z "$version" ]]; then
    echo "LSP manifest missing version for $server_id: $manifest" >&2
    exit 1
  fi
  if ! expected_sha="$(lsp_manifest_value "$manifest" "$server_id" sha256)"; then
    echo "LSP manifest missing sha256 for $server_id: $manifest" >&2
    exit 1
  fi
  expected_sha="$(require_sha256_hex "LSP server $server_id digest" "$expected_sha")"
  resolved="$package_root/$manifest_path"
  if [[ ! -x "$resolved" ]]; then
    echo "LSP server $server_id points to missing executable: $resolved" >&2
    exit 1
  fi
  actual_sha="$(sha256_file "$resolved")"
  if [[ "$actual_sha" != "$expected_sha" ]]; then
    echo "LSP packaged digest mismatch: $resolved" >&2
    echo "  expected: $expected_sha" >&2
    echo "  actual:   $actual_sha" >&2
    exit 1
  fi
  local version_args output
  version_args="$(lsp_server_version_args "$server_id")"
  if [[ -z "$version_args" ]]; then
    echo "LSP server executable verified: $server_id"
    return
  fi
  local -a args
  read -r -a args <<< "$version_args"
  if ! output="$(PATH="$lsp_smoke_path" "$resolved" "${args[@]}" 2>&1)"; then
    echo "LSP server $server_id version smoke failed: $resolved $version_args" >&2
    printf '%s\n' "$output" >&2
    exit 1
  fi
  echo "LSP server smoke verified: $server_id"
}

verify_lsp_manifest() {
  local manifest="$package_root/lsp/lsp-manifest.json"
  if [[ ! -f "$manifest" ]]; then
    echo "missing LSP manifest: $manifest" >&2
    exit 1
  fi
  local spec server_id rel_path count
  count=0
  for spec in "${lsp_server_specs[@]}"; do
    IFS='|' read -r server_id rel_path <<< "$spec"
    verify_lsp_manifest_entry "$manifest" "$server_id" "$rel_path"
    count=$((count + 1))
  done
  if lsp_manifest_value "$manifest" "jdtls" path >/dev/null 2>&1; then
    verify_lsp_manifest_entry "$manifest" "jdtls" "bin/jdtls"
    count=$((count + 1))
  fi
  if [[ "$count" -eq 0 ]]; then
    echo "LSP manifest contains no servers: $manifest" >&2
    exit 1
  fi
  echo "LSP manifest verified: $manifest"
}

verify_runtime_manifest() {
  local manifest="$package_root/runtime-manifest.json"
  if [[ ! -f "$manifest" ]]; then
    echo "missing runtime manifest: $manifest" >&2
    exit 1
  fi
  local bundled_codex_path bundled_gopls_path lsp_bundle_path lsp_manifest_path model_registry_path
  for key in bundled_codex_path bundled_gopls_path lsp_bundle_path lsp_manifest_path model_registry_path; do
    if ! value="$(manifest_string "$manifest" "$key")"; then
      echo "runtime manifest missing $key: $manifest" >&2
      exit 1
    fi
    case "$key" in
      bundled_codex_path) bundled_codex_path="$value" ;;
      bundled_gopls_path) bundled_gopls_path="$value" ;;
      lsp_bundle_path) lsp_bundle_path="$value" ;;
      lsp_manifest_path) lsp_manifest_path="$value" ;;
      model_registry_path) model_registry_path="$value" ;;
    esac
  done
  require_runtime_manifest_path bundled_codex_path "$bundled_codex_path" "bin/codex" exec
  require_runtime_manifest_path bundled_gopls_path "$bundled_gopls_path" "bin/gopls" exec
  require_runtime_manifest_path lsp_bundle_path "$lsp_bundle_path" "lsp" dir
  require_manifest_relative_path lsp_manifest_path "$lsp_manifest_path"
  if [[ "$lsp_manifest_path" != "lsp/lsp-manifest.json" || ! -f "$package_root/$lsp_manifest_path" ]]; then
    echo "missing LSP manifest: $package_root/$lsp_manifest_path" >&2
    exit 1
  fi
  require_runtime_manifest_path model_registry_path "$model_registry_path" "models.yaml" file
}

require_launcher_export() {
  local launcher="$1"
  local label="$2"
  local pattern="$3"
  if ! grep -Eq "$pattern" "$launcher"; then
    echo "Linux launcher missing packaged runtime env: $label" >&2
    exit 1
  fi
}

verify_linux_launcher_runtime_env() {
  local launcher="$package_root/run.sh"
  if [[ ! -e "$launcher" ]]; then
    return
  fi
  if [[ ! -f "$launcher" ]]; then
    echo "Linux launcher is not a regular file: $launcher" >&2
    exit 1
  fi
  if [[ ! -x "$launcher" ]]; then
    echo "Linux launcher is not executable: $launcher" >&2
    exit 1
  fi
  require_launcher_export "$launcher" "SUPER_DOLPHIN_PACKAGE_ROOT" '^[[:space:]]*export[[:space:]]+SUPER_DOLPHIN_PACKAGE_ROOT="\$here"[[:space:]]*$'
  require_launcher_export "$launcher" "SUPER_DOLPHIN_RUNTIME_MODE=packaged" '^[[:space:]]*export[[:space:]]+SUPER_DOLPHIN_RUNTIME_MODE="?packaged"?[[:space:]]*$'
  require_launcher_export "$launcher" "SUPER_DOLPHIN_PACKAGED_LAUNCHER=1" '^[[:space:]]*export[[:space:]]+SUPER_DOLPHIN_PACKAGED_LAUNCHER="?1"?[[:space:]]*$'
  if grep -Eq '^[[:space:]]*export[[:space:]]+SUPER_DOLPHIN_HOME=.*(\$here|\$\{?SUPER_DOLPHIN_PACKAGE_ROOT\}?)' "$launcher"; then
    echo "Linux launcher must not resolve SQLite home under package root" >&2
    exit 1
  fi
}

verify_codex_manifest() {
  local manifest="$package_root/codex-manifest.json"
  if [[ ! -f "$manifest" ]]; then
    echo "missing Codex manifest: $manifest" >&2
    exit 1
  fi
  local rel_path source_sha256 package_sha256 actual
  if ! rel_path="$(manifest_string "$manifest" path)"; then
    echo "Codex manifest missing path: $manifest" >&2
    exit 1
  fi
  if [[ "$rel_path" != "bin/codex" ]]; then
    echo "Codex manifest path mismatch: expected bin/codex, got $rel_path" >&2
    exit 1
  fi
  if ! source_sha256="$(manifest_string "$manifest" source_sha256)"; then
    echo "Codex manifest missing source_sha256: $manifest" >&2
    exit 1
  fi
  require_sha256_hex "Codex source digest" "$source_sha256" >/dev/null
  if ! package_sha256="$(manifest_string "$manifest" package_sha256)"; then
    echo "Codex manifest missing package_sha256: $manifest" >&2
    exit 1
  fi
  package_sha256="$(require_sha256_hex "Codex package digest" "$package_sha256")"
  actual="$(sha256_file "$package_root/bin/codex")"
  if [[ "$actual" != "$package_sha256" ]]; then
    echo "Codex packaged digest mismatch: $package_root/bin/codex" >&2
    echo "  expected: $package_sha256" >&2
    echo "  actual:   $actual" >&2
    exit 1
  fi
  if ! "$package_root/bin/codex" app-server --help >/dev/null 2>&1; then
    echo "Codex app-server smoke failed: $package_root/bin/codex" >&2
    exit 1
  fi
}

verify_package_root_links() {
  local broken_symlinks escaped target dir normalized
  broken_symlinks="$(find -L "$package_root" -type l -print 2>/dev/null || true)"
  if [[ -n "$broken_symlinks" ]]; then
    echo "Linux package root contains broken symlinks:" >&2
    printf '%s\n' "$broken_symlinks" >&2
    exit 1
  fi
  while IFS= read -r -d '' link; do
    target="$(readlink "$link")"
    case "$target" in
      /*|..|../*|*/../*)
        echo "package root contains escaped symlink: $link -> $target" >&2
        exit 1
        ;;
    esac
    dir="$(dirname "$link")"
    normalized="$(cd "$dir" && cd "$(dirname "$target")" && pwd -P 2>/dev/null || true)"
    if [[ -z "$normalized" || "$normalized" != "$package_root" && "$normalized" != "$package_root"/* ]]; then
      echo "package root contains escaped symlink: $link -> $target" >&2
      exit 1
    fi
  done < <(find "$package_root" -type l -print0)
}

verify_runtime_manifest
verify_linux_launcher_runtime_env

required_execs=(
  "$package_root/bin/agent-terminal"
  "$package_root/bin/mcp-orch"
  "$package_root/bin/mcp-lsp"
  "$package_root/bin/mcp-ida"
  "$package_root/bin/codex"
  "$package_root/bin/gopls"
  "$package_root/bin/typescript-language-server"
  "$package_root/bin/vscode-css-language-server"
  "$package_root/bin/pyright-langserver"
  "$package_root/bin/rust-analyzer"
  "$package_root/bin/bash-language-server"
  "$package_root/bin/shellcheck"
  "$package_root/lsp/bin/sg"
  "$package_root/lsp/bin/python"
  "$package_root/lsp/bin/python3"
  "$package_root/lsp/bin/go"
)
if [[ -f "$package_root/lsp/lsp-manifest.json" ]] && lsp_manifest_value "$package_root/lsp/lsp-manifest.json" "jdtls" path >/dev/null 2>&1; then
  required_execs+=("$package_root/bin/jdtls")
fi
for path in "${required_execs[@]}"; do
  if [[ ! -x "$path" ]]; then
    echo "missing executable: $path" >&2
    exit 1
  fi
done

sqlite_migrations_dir="$package_root/internal/platform/db/sqlite/migrations"
if [[ ! -d "$sqlite_migrations_dir" || -z "$(find "$sqlite_migrations_dir" -type f -print -quit)" ]]; then
  echo "missing SQLite migration files under $sqlite_migrations_dir" >&2
  exit 1
fi
verify_codex_manifest
verify_lsp_manifest
verify_package_root_links

echo "Linux packaged app verification passed: $package_root"
