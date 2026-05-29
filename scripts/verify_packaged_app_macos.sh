#!/usr/bin/env bash
set -euo pipefail

app="${1:-}"
if [[ -z "$app" || ! -d "$app/Contents" ]]; then
  echo "usage: $0 /path/to/Super Dolphin.app" >&2
  exit 2
fi

resources="$app/Contents/Resources"
macos="$app/Contents/MacOS"
lsp_smoke_path="$resources/bin:$resources/lsp/bin:$resources/lsp/node/bin:$resources/lsp/node_modules/.bin:/usr/bin:/bin:/usr/sbin:/sbin"

detect_platform() {
  local os arch
  case "$(uname -s)" in
    Darwin)
      os="darwin"
      ;;
    Linux)
      os="linux"
      ;;
    *)
      echo "unsupported packaged app verification OS: $(uname -s)" >&2
      exit 1
      ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64)
      arch="amd64"
      ;;
    arm64|aarch64)
      arch="arm64"
      ;;
    *)
      echo "unsupported packaged app verification architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
  printf '%s-%s\n' "$os" "$arch"
}

platform="$(detect_platform)"
pg="$resources/postgres/$platform"
lsp_server_specs=(
  "gopls|bin/gopls"
  "typescript-language-server|bin/typescript-language-server"
  "vscode-langservers-extracted|bin/vscode-css-language-server"
  "pyright|bin/pyright-langserver"
  "rust-analyzer|bin/rust-analyzer"
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

is_macho() {
  file -b "$1" 2>/dev/null | grep -q 'Mach-O'
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
      echo "runtime manifest $label must be a relative path under Contents/Resources: $rel_path" >&2
      exit 1
      ;;
  esac
}

canonical_existing_path() {
  local target="$1"
  if [[ -d "$target" ]]; then
    (cd -P "$target" && pwd)
    return
  fi
  local dir base physical_dir
  dir="$(dirname "$target")"
  base="$(basename "$target")"
  physical_dir="$(cd -P "$dir" && pwd)"
  printf '%s/%s\n' "$physical_dir" "$base"
}

require_resource_path_inside() {
  local label="$1"
  local actual="$2"
  local resolved="$3"
  local physical_resources physical_resolved
  physical_resources="$(cd -P "$resources" && pwd)"
  physical_resolved="$(canonical_existing_path "$resolved")"
  case "$physical_resolved" in
    "$physical_resources"|"$physical_resources"/*)
      ;;
    *)
      echo "runtime manifest $label escapes Contents/Resources: $actual -> $physical_resolved" >&2
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
  local resolved="$resources/$actual"
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
  require_resource_path_inside "$label" "$actual" "$resolved"
}

json_value_at_path() {
  local manifest="$1"
  local target_path="$2"
  local mode="$3"
  awk -v target_path="$target_path" -v mode="$mode" '
    function fail() { exit 2 }
    function skip_ws() {
      while (pos <= length(json) && substr(json, pos, 1) ~ /^[[:space:]]$/) {
        pos++
      }
    }
    function child_path(path, key) {
      if (path == "") {
        return key
      }
      return path "." key
    }
    function emit(start) {
      if (mode == "json") {
        print substr(json, start, pos - start)
      }
      found = 1
      exit
    }
    function parse_string(    out, c, esc) {
      if (substr(json, pos, 1) != "\"") {
        fail()
      }
      pos++
      out = ""
      while (pos <= length(json)) {
        c = substr(json, pos, 1)
        if (c == "\"") {
          pos++
          return out
        }
        if (c == "\\") {
          pos++
          if (pos > length(json)) {
            fail()
          }
          esc = substr(json, pos, 1)
          if (esc == "n") {
            out = out "\n"
            pos++
            continue
          }
          if (esc == "r") {
            out = out "\r"
            pos++
            continue
          }
          if (esc == "t") {
            out = out "\t"
            pos++
            continue
          }
          if (esc == "b") {
            out = out "\b"
            pos++
            continue
          }
          if (esc == "f") {
            out = out "\f"
            pos++
            continue
          }
          if (esc == "u") {
            out = out "\\u" substr(json, pos + 1, 4)
            pos += 5
            continue
          }
          out = out esc
          pos++
          continue
        }
        out = out c
        pos++
      }
      fail()
    }
    function parse_literal(    c) {
      while (pos <= length(json)) {
        c = substr(json, pos, 1)
        if (c == "," || c == "}" || c == "]" || c ~ /^[[:space:]]$/) {
          return
        }
        pos++
      }
    }
    function parse_array(path,    c) {
      pos++
      skip_ws()
      if (substr(json, pos, 1) == "]") {
        pos++
        return
      }
      while (pos <= length(json)) {
        parse_value(path "[]")
        skip_ws()
        c = substr(json, pos, 1)
        if (c == ",") {
          pos++
          skip_ws()
          continue
        }
        if (c == "]") {
          pos++
          return
        }
        fail()
      }
      fail()
    }
    function parse_object(path,    key, c) {
      pos++
      skip_ws()
      if (substr(json, pos, 1) == "}") {
        pos++
        return
      }
      while (pos <= length(json)) {
        key = parse_string()
        skip_ws()
        if (substr(json, pos, 1) != ":") {
          fail()
        }
        pos++
        parse_value(child_path(path, key))
        skip_ws()
        c = substr(json, pos, 1)
        if (c == ",") {
          pos++
          skip_ws()
          continue
        }
        if (c == "}") {
          pos++
          return
        }
        fail()
      }
      fail()
    }
    function parse_value(path,    start, c, value) {
      skip_ws()
      start = pos
      c = substr(json, pos, 1)
      if (c == "{") {
        parse_object(path)
        if (path == target_path && mode == "json") {
          emit(start)
        }
        return
      }
      if (c == "[") {
        parse_array(path)
        if (path == target_path && mode == "json") {
          emit(start)
        }
        return
      }
      if (c == "\"") {
        value = parse_string()
        if (path == target_path) {
          if (mode == "string") {
            print value
          } else {
            print substr(json, start, pos - start)
          }
          found = 1
          exit
        }
        return
      }
      parse_literal()
      if (path == target_path && mode == "json") {
        emit(start)
      }
    }
    {
      json = json $0 "\n"
    }
    END {
      pos = 1
      found = 0
      parse_value("")
      if (!found) {
        exit 1
      }
    }
  ' "$manifest"
}

lsp_manifest_value() {
  local manifest="$1"
  local server="$2"
  local field="$3"
  json_value_at_path "$manifest" "servers.$server.$field" string
}

lsp_manifest_json_value() {
  local manifest="$1"
  local server="$2"
  local field="$3"
  json_value_at_path "$manifest" "servers.$server.$field" json
}

lsp_server_version_args() {
  local server_id="$1"
  case "$server_id" in
    typescript-language-server|vscode-langservers-extracted|pyright)
      printf '\n'
      ;;
    gopls)
      printf '%s\n' "version"
      ;;
    go)
      printf '%s\n' "version"
      ;;
    jdtls)
      printf '%s\n' "-version"
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
  resolved="$resources/$manifest_path"
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
  local manifest="$resources/lsp/lsp-manifest.json"
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
  local manifest="$resources/runtime-manifest.json"
  if [[ ! -f "$manifest" ]]; then
    echo "missing runtime manifest: $manifest" >&2
    exit 1
  fi
  local bundled_codex_path bundled_gopls_path lsp_bundle_path lsp_manifest_path model_registry_path embedded_postgres_resource_path
  if ! bundled_codex_path="$(manifest_string "$manifest" bundled_codex_path)"; then
    echo "runtime manifest missing bundled_codex_path: $manifest" >&2
    exit 1
  fi
  if ! bundled_gopls_path="$(manifest_string "$manifest" bundled_gopls_path)"; then
    echo "runtime manifest missing bundled_gopls_path: $manifest" >&2
    exit 1
  fi
  if ! lsp_bundle_path="$(manifest_string "$manifest" lsp_bundle_path)"; then
    echo "runtime manifest missing lsp_bundle_path: $manifest" >&2
    exit 1
  fi
  if ! lsp_manifest_path="$(manifest_string "$manifest" lsp_manifest_path)"; then
    echo "runtime manifest missing lsp_manifest_path: $manifest" >&2
    exit 1
  fi
  if ! model_registry_path="$(manifest_string "$manifest" model_registry_path)"; then
    echo "runtime manifest missing model_registry_path: $manifest" >&2
    exit 1
  fi
  if ! embedded_postgres_resource_path="$(manifest_string "$manifest" embedded_postgres_resource_path)"; then
    echo "runtime manifest missing embedded_postgres_resource_path: $manifest" >&2
    exit 1
  fi
  require_runtime_manifest_path bundled_codex_path "$bundled_codex_path" "bin/codex" exec
  require_runtime_manifest_path bundled_gopls_path "$bundled_gopls_path" "bin/gopls" exec
  require_runtime_manifest_path lsp_bundle_path "$lsp_bundle_path" "lsp" dir
  require_manifest_relative_path lsp_manifest_path "$lsp_manifest_path"
  if [[ "$lsp_manifest_path" != "lsp/lsp-manifest.json" ]]; then
    echo "runtime manifest lsp_manifest_path mismatch: expected lsp/lsp-manifest.json, got $lsp_manifest_path" >&2
    exit 1
  fi
  if [[ ! -f "$resources/$lsp_manifest_path" ]]; then
    echo "missing LSP manifest: $resources/$lsp_manifest_path" >&2
    exit 1
  fi
  require_runtime_manifest_path model_registry_path "$model_registry_path" "models.yaml" file
  require_runtime_manifest_path embedded_postgres_resource_path "$embedded_postgres_resource_path" "postgres/$platform" dir
}

verify_codex_manifest() {
  local manifest="$resources/codex-manifest.json"
  if [[ ! -f "$manifest" ]]; then
    echo "missing Codex manifest: $manifest" >&2
    exit 1
  fi
  local rel_path package_sha256 source_sha256 actual
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
  actual="$(sha256_file "$resources/bin/codex")"
  if [[ "$actual" != "$package_sha256" ]]; then
    echo "Codex packaged digest mismatch: $resources/bin/codex" >&2
    echo "  expected: $package_sha256" >&2
    echo "  actual:   $actual" >&2
    exit 1
  fi
  if ! "$resources/bin/codex" app-server --help >/dev/null 2>&1; then
    echo "Codex app-server smoke failed: $resources/bin/codex" >&2
    exit 1
  fi
}


make_lsp_smoke_workspace() {
  mktemp -d "${TMPDIR:-/tmp}/super-dolphin-lsp-smoke.XXXXXX"
}

verify_packaged_go_lsp_smoke() {
  phase_start "packaged go lsp smoke"
  local go_bin workspace output
  if ! go_bin="$(PATH="$lsp_smoke_path" command -v go 2>/dev/null)"; then
    echo "packaged Go LSP semantic smoke skipped: go toolchain is not bundled" >&2
    phase_end
    return
  fi
  workspace="$(make_lsp_smoke_workspace)"
  trap 'rm -rf "$workspace"' RETURN
  cat >"$workspace/go.mod" <<'EOF_GO_MOD'
module smoke.test

go 1.25
EOF_GO_MOD
  cat >"$workspace/main.go" <<'EOF_GO'
package main

func answer() int { return 42 }

func main() { _ = answer() }
EOF_GO
  if ! output="$(cd "$workspace" && PATH="$(dirname "$go_bin"):$lsp_smoke_path" "$resources/lsp/bin/gopls" check "$workspace/main.go" 2>&1)"; then
    echo "packaged Go LSP smoke failed: gopls check in $workspace" >&2
    printf '%s\n' "$output" >&2
    exit 1
  fi
  phase_end
}

verify_packaged_java_lsp_smoke() {
  if [[ ! -x "$resources/lsp/bin/jdtls" ]]; then
    return
  fi
  phase_start "packaged java lsp smoke"
  local workspace output
  workspace="$(make_lsp_smoke_workspace)"
  trap 'rm -rf "$workspace"' RETURN
  cat >"$workspace/Main.java" <<'EOF_JAVA'
public class Main {
  public static void main(String[] args) {}
}
EOF_JAVA
  if ! output="$(cd "$workspace" && PATH="$lsp_smoke_path" "$resources/lsp/bin/jdtls" -version 2>&1)"; then
    echo "packaged Java LSP smoke failed: jdtls wrapper in $workspace" >&2
    printf '%s\n' "$output" >&2
    exit 1
  fi
  phase_end
}

verify_packaged_ast_grep_smoke() {
  phase_start "packaged ast-grep smoke"
  local workspace output
  workspace="$(make_lsp_smoke_workspace)"
  trap 'rm -rf "$workspace"' RETURN
  cat >"$workspace/app.js" <<'EOF_JS'
console.log("smoke")
EOF_JS
  if ! output="$(PATH="$lsp_smoke_path" "$resources/lsp/bin/sg" run -p 'console.log($A)' --lang javascript "$workspace/app.js" 2>&1)"; then
    echo "packaged ast-grep smoke failed: sg run" >&2
    printf '%s\n' "$output" >&2
    exit 1
  fi
  phase_end
}

verify_packaged_python_shadow_smoke() {
  phase_start "packaged python shadow smoke"
  local output
  if ! output="$("$resources/lsp/bin/python3" --version 2>&1)"; then
    if [[ "$output" != *"Packaged Super Dolphin does not bundle a Python interpreter"* ]]; then
      echo "packaged Python shadow smoke returned unexpected output" >&2
      printf '%s\n' "$output" >&2
      exit 1
    fi
    phase_end
    return
  fi
  echo "packaged Python shadow executable unexpectedly succeeded: $resources/lsp/bin/python3" >&2
  exit 1
}

required_execs=(
  "$macos/agent-terminal"
  "$resources/bin/mcp-orch"
  "$resources/bin/mcp-lsp"
  "$resources/bin/mcp-ida"
  "$resources/bin/codex"
  "$resources/bin/gopls"
  "$resources/bin/typescript-language-server"
  "$resources/bin/vscode-css-language-server"
  "$resources/bin/pyright-langserver"
  "$resources/bin/rust-analyzer"
  "$resources/lsp/bin/sg"
  "$resources/lsp/bin/python"
  "$resources/lsp/bin/python3"
  "$resources/lsp/bin/go"
  "$resources/bin/git"
  "$pg/bin/postgres"
  "$pg/bin/initdb"
  "$pg/bin/pg_ctl"
  "$pg/bin/pg_config"
)

verify_runtime_manifest
if [[ -f "$resources/lsp/lsp-manifest.json" ]] && lsp_manifest_value "$resources/lsp/lsp-manifest.json" "jdtls" path >/dev/null 2>&1; then
  required_execs+=("$resources/bin/jdtls")
fi

for path in "${required_execs[@]}"; do
  if [[ ! -x "$path" ]]; then
    echo "missing executable: $path" >&2
    exit 1
  fi
done

if [[ ! -d "$resources/migrations" ]]; then
  echo "missing migrations directory: $resources/migrations" >&2
  exit 1
fi

verify_codex_manifest
verify_lsp_manifest
verify_packaged_go_lsp_smoke
verify_packaged_java_lsp_smoke
verify_packaged_ast_grep_smoke
verify_packaged_python_shadow_smoke

if [[ -z "$(find "$resources/migrations" -type f -print -quit)" ]]; then
  echo "missing migration files under $resources/migrations" >&2
  exit 1
fi

if [[ ! -d "$pg/share" ]]; then
  echo "missing PostgreSQL share directory: $pg/share" >&2
  exit 1
fi

if [[ -z "$(find "$pg/share" -name postgres.bki -type f -print -quit)" ]]; then
  echo "missing postgres.bki under $pg/share" >&2
  exit 1
fi

broken_symlinks="$(find -L "$app" -type l -print 2>/dev/null || true)"
if [[ -n "$broken_symlinks" ]]; then
  echo "packaged app contains broken symlinks:" >&2
  printf '%s\n' "$broken_symlinks" >&2
  exit 1
fi

homebrew_pattern='(/opt/homebrew/|/usr/local/(opt|Cellar|lib)/)'
homebrew_found=0
phase_start "homebrew dylib scan"
while IFS= read -r -d '' file; do
  is_macho "$file" || continue
  refs="$(otool -L "$file" 2>/dev/null | grep -E "$homebrew_pattern" || true)"
  if [[ -n "$refs" ]]; then
    if [[ "$homebrew_found" == "0" ]]; then
      echo "packaged app still references Homebrew dylibs:" >&2
    fi
    homebrew_found=1
    echo "  $file" >&2
    printf '%s\n' "$refs" >&2
  fi
done < <(find "$app" -type f -print0)
phase_end

if [[ "$homebrew_found" != "0" ]]; then
  exit 1
fi

echo "packaged app verification passed: $app"
