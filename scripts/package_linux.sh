#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
app_name="${APP_NAME:-super-dolphin}"
version="${VERSION:-0.1.0}"
goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
platform="${goos}-${goarch}"
codex_relay_base_url_env="SUPER_DOLPHIN_CODEX_RELAY_BASE_URL"
codex_relay_bootstrap_token_env="SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"
codex_relay_bootstrap_proof_env="SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF"
codex_relay_privileged_api_key_env="SUPER_DOLPHIN_CODEX_RELAY_API_KEY"
packaged_relay_base_url=""
packaged_relay_bootstrap_token=""
packaged_relay_bootstrap_proof=""
codex_artifact_env="SUPER_DOLPHIN_CODEX_ARTIFACT"
codex_sha256_env="SUPER_DOLPHIN_CODEX_SHA256"
codex_version_env="SUPER_DOLPHIN_CODEX_VERSION"
lsp_bundle_dir_env="SUPER_DOLPHIN_LSP_BUNDLE_DIR"
lsp_manifest_name="lsp-manifest.json"
lsp_checksums_name="lsp-checksums.sha256"
require_bundled_codex="${SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX:-1}"
packaged_codex_artifact=""
packaged_codex_sha256=""
packaged_codex_version=""
packaged_lsp_bundle_dir=""
lsp_profile="${SUPER_DOLPHIN_LSP_PROFILE:-standard}"
case "$lsp_profile" in
  standard|full)
    ;;
  *)
    echo "unsupported SUPER_DOLPHIN_LSP_PROFILE=$lsp_profile; expected standard or full" >&2
    exit 1
    ;;
esac
lsp_server_specs=(
  "gopls|bin/gopls"
  "typescript-language-server|bin/typescript-language-server"
  "vscode-langservers-extracted|bin/vscode-css-language-server"
  "pyright|bin/pyright-langserver"
  "rust-analyzer|bin/rust-analyzer"
  "bash-language-server|bin/bash-language-server"
  "sql-language-server|bin/sql-language-server"
  "shellcheck|bin/shellcheck"
  "sg|bin/sg"
  "go|bin/go"
)
lsp_shadow_execs=(python python3)
if [[ "$lsp_profile" == "full" ]]; then
  lsp_server_specs+=("jdtls|bin/jdtls")
fi

# 增量构建缓存：对 phase 产物做哈希指纹，命中则跳过耗时构建。
# 设置 SUPER_DOLPHIN_SKIP_BUILD_CACHE=1 可强制全量重建。
_build_cache_dir="${root}/.build-cache/phases"

_phase_hash() {
  local item
  for item in "$@"; do
    if [[ "$item" == input:* ]]; then
      printf 'input\t%s\n' "${item#input:}"
    elif [[ -f "$item" ]]; then
      printf 'file\t%s\t%s\n' "$item" "$(shasum -a 256 "$item" | awk '{print $1}')"
    elif [[ -d "$item" ]]; then
      find "$item" -type f -print0 | sort -z | while IFS= read -r -d '' file; do
        printf 'file\t%s\t%s\n' "$file" "$(shasum -a 256 "$file" | awk '{print $1}')"
      done
    else
      echo "missing build phase input: $item" >&2
      return 1
    fi
  done | shasum -a 256 | awk '{print $1}'
}

phase_cache_check() {
  [[ "${SUPER_DOLPHIN_SKIP_BUILD_CACHE:-0}" == "1" ]] && return 1
  [[ "${SUPER_DOLPHIN_RELEASE_BUILD:-0}" == "1" ]] && return 1
  local name="$1"; shift
  local hash; hash="$(_phase_hash "$@")"
  if [[ -f "$_build_cache_dir/$name/$hash.ok" ]]; then
    echo "==> [$name] cache hit ($hash), skipping" >&2
    return 0
  fi
  _current_phase_name="$name"; _current_phase_hash="$hash"
  return 1
}

phase_cache_save() {
  [[ "${SUPER_DOLPHIN_SKIP_BUILD_CACHE:-0}" == "1" ]] && return 0
  [[ "${SUPER_DOLPHIN_RELEASE_BUILD:-0}" == "1" ]] && return 0
  local name="${_current_phase_name:-}"; local hash="${_current_phase_hash:-}"
  [[ -z "$name" || -z "$hash" ]] && return 0
  mkdir -p "$_build_cache_dir/$name"
  rm -f "$_build_cache_dir/$name/"*.ok
  touch "$_build_cache_dir/$name/$hash.ok"
}

frontend_node_version_input() {
  local version
  version="$(node --version)"
  [[ -n "${version//[[:space:]]/}" ]] || { echo "node --version returned empty output" >&2; exit 1; }
  printf '%s\n' "$version"
}

frontend_npm_version_input() {
  local version
  version="$(npm --version)"
  [[ -n "${version//[[:space:]]/}" ]] || { echo "npm --version returned empty output" >&2; exit 1; }
  printf '%s\n' "$version"
}

validate_env_file_value() {
  local label="$1"
  local value="$2"
  if [[ ! "$value" =~ [^[:space:]] ]]; then
    echo "$label is required and must not be whitespace-only" >&2
    exit 1
  fi
  if [[ "$value" == *$'
'* || "$value" == *$'
'* ]]; then
    echo "$label must not contain newline characters" >&2
    exit 1
  fi
}

resolve_packaged_relay_env() {
  if [[ -n "${SUPER_DOLPHIN_CODEX_RELAY_API_KEY:-}" ]]; then
    echo "privileged Codex relay API key env is not allowed for release packaging; set $codex_relay_bootstrap_token_env instead of $codex_relay_privileged_api_key_env" >&2
    exit 1
  fi
  packaged_relay_base_url="${SUPER_DOLPHIN_CODEX_RELAY_BASE_URL:-}"
  packaged_relay_bootstrap_token="${SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN:-}"
  packaged_relay_bootstrap_proof="${SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF:-}"
  validate_env_file_value "$codex_relay_base_url_env" "$packaged_relay_base_url"
  validate_env_file_value "$codex_relay_bootstrap_token_env" "$packaged_relay_bootstrap_token"
  validate_env_file_value "$codex_relay_bootstrap_proof_env" "$packaged_relay_bootstrap_proof"
}

write_packaged_relay_env() {
  local env_path="$stage/.env"
  {
    printf '%s=%s
' "$codex_relay_base_url_env" "$packaged_relay_base_url"
    printf '%s=%s
' "$codex_relay_bootstrap_token_env" "$packaged_relay_bootstrap_token"
  } > "$env_path"
  chmod 600 "$env_path"
}

json_escape() {
  local value="$1"
  value="${value//\/\\}"
  value="$(printf '%s' "$value" | sed 's/"/\\"/g')"
  printf '%s' "$value"
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

resolve_packaged_codex_artifact() {
  packaged_codex_artifact="${SUPER_DOLPHIN_CODEX_ARTIFACT:-}"
  packaged_codex_sha256="${SUPER_DOLPHIN_CODEX_SHA256:-}"
  packaged_codex_version="${SUPER_DOLPHIN_CODEX_VERSION:-}"
  if [[ -z "$packaged_codex_artifact" ]]; then
    if [[ "$require_bundled_codex" == "1" ]]; then
      echo "packaged Codex CLI artifact is required; set $codex_artifact_env to a release artifact and $codex_sha256_env to its trusted SHA-256" >&2
      exit 1
    fi
    echo "Codex CLI artifact not bundled because SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=$require_bundled_codex" >&2
    return
  fi
  if [[ ! -f "$packaged_codex_artifact" ]]; then
    echo "packaged Codex CLI artifact does not exist: $packaged_codex_artifact" >&2
    exit 1
  fi
  if [[ ! -x "$packaged_codex_artifact" ]]; then
    echo "packaged Codex CLI artifact is not executable: $packaged_codex_artifact" >&2
    exit 1
  fi
  if [[ -z "$packaged_codex_sha256" ]]; then
    echo "packaged Codex CLI checksum is required; set $codex_sha256_env from a trusted release manifest or signature verification" >&2
    exit 1
  fi
  if [[ ! "$packaged_codex_sha256" =~ ^[[:xdigit:]]{64}$ ]]; then
    echo "$codex_sha256_env must be a 64-character hex SHA-256" >&2
    exit 1
  fi
  if [[ -z "$packaged_codex_version" ]]; then
    echo "packaged Codex CLI version is required; set $codex_version_env" >&2
    exit 1
  fi
  validate_env_file_value "$codex_sha256_env" "$packaged_codex_sha256"
  validate_env_file_value "$codex_version_env" "$packaged_codex_version"
  local expected actual
  expected="$(printf '%s' "$packaged_codex_sha256" | tr 'A-F' 'a-f')"
  actual="$(sha256_file "$packaged_codex_artifact")"
  if [[ "$actual" != "$expected" ]]; then
    echo "Codex CLI artifact checksum mismatch: $packaged_codex_artifact" >&2
    echo "  expected: $expected" >&2
    echo "  actual:   $actual" >&2
    exit 1
  fi
  packaged_codex_sha256="$expected"
  echo "Codex CLI artifact checksum verified: $packaged_codex_artifact" >&2
}

copy_packaged_codex() {
  local bundle_root="$1"
  local dest="$2"
  if [[ -z "$packaged_codex_artifact" ]]; then
    return
  fi
  mkdir -p "$(dirname "$dest")"
  cp -f "$packaged_codex_artifact" "$dest"
  chmod 755 "$dest"
}

require_lsp_relative_path() {
  local label="$1"
  local rel_path="$2"
  case "$rel_path" in
    ""|/*|..|../*|*/..|*/../*)
      echo "$label must be a relative path inside the LSP bundle: $rel_path" >&2
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

verify_lsp_checksums_file() {
  if (
    cd "$packaged_lsp_bundle_dir"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum -c "$lsp_checksums_name"
      exit $?
    fi
    if command -v shasum >/dev/null 2>&1; then
      shasum -a 256 -c "$lsp_checksums_name"
      exit $?
    fi
    echo "missing SHA-256 tool; install sha256sum or shasum" >&2
    exit 1
  ) >/dev/null; then
    return
  fi
  echo "packaged LSP bundle checksum mismatch: $packaged_lsp_bundle_dir/$lsp_checksums_name" >&2
  exit 1
}

resolve_packaged_lsp_bundle() {
  packaged_lsp_bundle_dir="${SUPER_DOLPHIN_LSP_BUNDLE_DIR:-}"
  if [[ -z "$packaged_lsp_bundle_dir" ]]; then
    echo "packaged LSP bundle is required; set $lsp_bundle_dir_env to a prepared $lsp_profile bundle containing $lsp_manifest_name, $lsp_checksums_name, gopls, typescript-language-server, vscode-langservers-extracted, pyright, rust-analyzer, bash-language-server, sql-language-server, shellcheck, sg, and jdtls only for full profile" >&2
    exit 1
  fi
  if [[ ! -d "$packaged_lsp_bundle_dir" ]]; then
    echo "packaged LSP bundle does not exist: $packaged_lsp_bundle_dir" >&2
    exit 1
  fi
  local manifest="$packaged_lsp_bundle_dir/$lsp_manifest_name"
  local checksums="$packaged_lsp_bundle_dir/$lsp_checksums_name"
  if [[ ! -f "$manifest" ]]; then
    echo "packaged LSP bundle missing manifest: $manifest" >&2
    exit 1
  fi
  if [[ ! -f "$checksums" ]]; then
    echo "packaged LSP bundle missing checksums: $checksums" >&2
    exit 1
  fi
  verify_lsp_checksums_file
  local spec server_id rel_path manifest_path version expected_sha actual_sha src
  for spec in "${lsp_server_specs[@]}"; do
    IFS='|' read -r server_id rel_path <<< "$spec"
    if ! manifest_path="$(lsp_manifest_value "$manifest" "$server_id" path)"; then
      echo "LSP manifest missing path for $server_id: $manifest" >&2
      exit 1
    fi
    require_lsp_relative_path "LSP manifest path for $server_id" "$manifest_path"
    if [[ "$manifest_path" != "$rel_path" ]]; then
      echo "LSP manifest path mismatch for $server_id: expected $rel_path, got $manifest_path" >&2
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
    if [[ ! "$expected_sha" =~ ^[[:xdigit:]]{64}$ ]]; then
      echo "LSP manifest sha256 for $server_id must be a 64-character hex SHA-256" >&2
      exit 1
    fi
    expected_sha="$(printf '%s' "$expected_sha" | tr 'A-F' 'a-f')"
    src="$packaged_lsp_bundle_dir/$rel_path"
    if [[ ! -x "$src" ]]; then
      if [[ "$server_id" == "go" ]]; then
        echo "packaged LSP bundle missing Go toolchain executable: $src" >&2
        exit 1
      fi
      echo "packaged LSP bundle missing executable $server_id: $src" >&2
      exit 1
    fi
    actual_sha="$(sha256_file "$src")"
    if [[ "$actual_sha" != "$expected_sha" ]]; then
      echo "packaged LSP bundle checksum mismatch for $server_id: $src" >&2
      echo "  expected: $expected_sha" >&2
      echo "  actual:   $actual_sha" >&2
      exit 1
    fi
  done
  local shadow_exec
  for shadow_exec in "${lsp_shadow_execs[@]}"; do
    if [[ ! -x "$packaged_lsp_bundle_dir/bin/$shadow_exec" ]]; then
      echo "packaged LSP bundle missing Python shadow executable: $packaged_lsp_bundle_dir/bin/$shadow_exec" >&2
      exit 1
    fi
  done
  echo "LSP bundle checksums verified: $packaged_lsp_bundle_dir" >&2
}

copy_packaged_lsp_bundle() {
  local bundle_root="$1"
  local dest_root="$bundle_root/lsp"
  rm -rf "$dest_root"
  mkdir -p "$dest_root" "$bundle_root/bin"
  rsync -aL --delete "$packaged_lsp_bundle_dir"/ "$dest_root"/
  local spec server_id rel_path bin_name link_path
  for spec in "${lsp_server_specs[@]}"; do
    IFS='|' read -r server_id rel_path <<< "$spec"
    if [[ ! -x "$dest_root/$rel_path" ]]; then
      echo "packaged LSP bundle did not copy executable $server_id: $dest_root/$rel_path" >&2
      exit 1
    fi
    bin_name="$(basename "$rel_path")"
    link_path="$bundle_root/bin/$bin_name"
    rm -f "$link_path"
    ln -s "../lsp/$rel_path" "$link_path"
    if [[ ! -x "$link_path" ]]; then
      echo "packaged LSP bundle did not expose executable $server_id: $link_path" >&2
      exit 1
    fi
  done
  local shadow_exec
  for shadow_exec in "${lsp_shadow_execs[@]}"; do
    if [[ ! -x "$dest_root/bin/$shadow_exec" ]]; then
      echo "packaged LSP bundle did not copy Python shadow executable: $dest_root/bin/$shadow_exec" >&2
      exit 1
    fi
  done
}

write_lsp_manifest() {
  local bundle_root="$1"
  local source_manifest="$bundle_root/lsp/$lsp_manifest_name"
  local manifest_tmp="$bundle_root/lsp/$lsp_manifest_name.tmp"
  if [[ ! -f "$source_manifest" ]]; then
    echo "missing copied LSP manifest before package manifest write: $source_manifest" >&2
    exit 1
  fi
  cat > "$manifest_tmp" <<JSON
{
  "schema_version": 1,
  "bundle_path": "lsp",
  "servers": {
JSON
  local first=1 spec server_id rel_path version languages_json checksum server_json rel_path_json version_json
  for spec in "${lsp_server_specs[@]}"; do
    IFS='|' read -r server_id rel_path <<< "$spec"
    if [[ ! -x "$bundle_root/lsp/$rel_path" ]]; then
      echo "missing packaged LSP server executable before manifest write: $bundle_root/lsp/$rel_path" >&2
      exit 1
    fi
    if ! version="$(lsp_manifest_value "$source_manifest" "$server_id" version)" || [[ -z "$version" ]]; then
      echo "LSP manifest missing version for $server_id before package manifest write: $source_manifest" >&2
      exit 1
    fi
    if ! languages_json="$(lsp_manifest_json_value "$source_manifest" "$server_id" languages)" || [[ -z "$languages_json" ]]; then
      echo "LSP manifest missing languages for $server_id before package manifest write: $source_manifest" >&2
      exit 1
    fi
    case "$languages_json" in
      \[*)
        ;;
      *)
        echo "LSP manifest languages for $server_id must be a JSON array: $source_manifest" >&2
        exit 1
        ;;
    esac
    checksum="$(sha256_file "$bundle_root/lsp/$rel_path")"
    server_json="$(json_escape "$server_id")"
    rel_path_json="$(json_escape "lsp/$rel_path")"
    version_json="$(json_escape "$version")"
    if [[ "$first" != "1" ]]; then
      printf ',\n' >> "$manifest_tmp"
    fi
    first=0
    cat >> "$manifest_tmp" <<JSON
    "$server_json": {
      "path": "$rel_path_json",
      "version": "$version_json",
      "sha256": "$checksum",
      "languages": $languages_json
    }
JSON
  done
  cat >> "$manifest_tmp" <<JSON

  }
}
JSON
  mv "$manifest_tmp" "$source_manifest"
}

write_codex_manifest() {
  local bundle_root="$1"
  if [[ -z "$packaged_codex_artifact" ]]; then
    return
  fi
  local version_json package_sha256
  version_json="$(json_escape "$packaged_codex_version")"
  package_sha256="$(sha256_file "$bundle_root/bin/codex")"
  cat > "$bundle_root/codex-manifest.json" <<JSON
{
  "codex": {
    "path": "bin/codex",
    "version": "$version_json",
    "source_sha256": "$packaged_codex_sha256",
    "package_sha256": "$package_sha256"
  }
}
JSON
}

copy_model_registry() {
  local stage="$1"
  local src="$root/cmd/mcp-orch/tools/modelregistry/models.yaml"
  if [[ ! -f "$src" ]]; then
    echo "missing model registry: $src" >&2
    exit 1
  fi
  cp -f "$src" "$stage/models.yaml"
}

copy_sqlite_migrations() {
  local bundle_root="$1"
  local src="$root/internal/platform/db/sqlite/migrations"
  local dest="$bundle_root/internal/platform/db/sqlite/migrations"
  if [[ ! -d "$src" ]]; then
    echo "missing SQLite migrations directory: $src" >&2
    exit 1
  fi
  if [[ -z "$(find "$src" -type f -print -quit)" ]]; then
    echo "missing SQLite migration files under $src" >&2
    exit 1
  fi
  rm -rf "$dest"
  mkdir -p "$(dirname "$dest")"
  cp -R "$src" "$dest"
}

write_runtime_manifest() {
  local bundle_root="$1"
  cat > "$bundle_root/runtime-manifest.json" <<JSON
{
  "bundled_codex_path": "bin/codex",
  "bundled_gopls_path": "bin/gopls",
  "lsp_bundle_path": "lsp",
  "lsp_manifest_path": "lsp/lsp-manifest.json",
  "model_registry_path": "models.yaml"
}
JSON
}

build_current_frontend_app() {
  if [[ "${SUPER_DOLPHIN_SKIP_FRONTEND_BUILD:-}" != "1" ]]; then
    frontend_cache_inputs=(
      "input:NODE_VERSION=$(frontend_node_version_input)"
      "input:NPM_VERSION=$(frontend_npm_version_input)"
    )
    frontend_cache_paths=(
      "$root/frontend-app/package.json"
      "$root/frontend-app/package-lock.json"
      "$root/frontend-app/vite.config.js"
      "$root/frontend-app/index.html"
      "$root/frontend-app/public"
      "$root/frontend-app/src"
    )
    if ! phase_cache_check "frontend" "${frontend_cache_inputs[@]}" "${frontend_cache_paths[@]}"; then
      (
        cd "$root/frontend-app"
        npm ci
        npm run build
      )
      phase_cache_save
    fi
  elif [[ ! -f "$root/frontend-app/dist/index.html" ]]; then
    echo "frontend dist missing; unset SUPER_DOLPHIN_SKIP_FRONTEND_BUILD or run npm run build first" >&2
    exit 1
  fi

  if [[ ! -f "$root/frontend-app/dist/index.html" ]]; then
    echo "frontend dist missing after build: $root/frontend-app/dist/index.html" >&2
    exit 1
  fi
  rsync -a --delete --exclude .gitkeep "$root/frontend-app/dist"/ "$root/cmd/agent-terminal/web-dist"/
  if [[ ! -f "$root/cmd/agent-terminal/web-dist/index.html" ]]; then
    echo "embedded frontend dist missing after sync: $root/cmd/agent-terminal/web-dist/index.html" >&2
    exit 1
  fi
}

package_linux_main() {
  if [[ "$goos" != "linux" ]]; then
    echo "package_linux.sh must run with GOOS=linux; current GOOS=$goos" >&2
    exit 1
  fi

  resolve_packaged_relay_env
  resolve_packaged_codex_artifact
  resolve_packaged_lsp_bundle

  dist="$root/dist/package/linux"
  stage="$dist/$app_name-$version-$platform"
  rm -rf "$stage" "$stage.tar.gz"
  mkdir -p "$stage/bin"

  build_current_frontend_app

  linux_cgo_enabled="${CGO_ENABLED:-$(go env CGO_ENABLED)}"
  go_binary_cache_paths=(
    "$root/cmd"
    "$root/internal"
    "$root/pkg"
    "$root/go.mod"
    "$root/go.sum"
  )
  go_binary_cache_inputs=(
    "input:GOVERSION=$(go env GOVERSION)"
    "input:GOOS=$goos"
    "input:GOARCH=$goarch"
    "input:CGO_ENABLED=$linux_cgo_enabled"
    "input:CGO_CFLAGS=${CGO_CFLAGS:-}"
    "input:CGO_CXXFLAGS=${CGO_CXXFLAGS:-}"
    "input:CGO_LDFLAGS=${CGO_LDFLAGS:-}"
  )
  if ! phase_cache_check "go-binaries" "${go_binary_cache_inputs[@]}" "${go_binary_cache_paths[@]}"; then
    (
      cd "$root"
      export CGO_ENABLED="$linux_cgo_enabled"
      make build-peer-binaries
      go build -o bin/agent-terminal ./cmd/agent-terminal
      go build -o bin/mcp-ida ./cmd/mcp-ida
    )
    phase_cache_save
  fi

  cp "$root/bin/agent-terminal" "$stage/bin/agent-terminal"
  cp "$root/bin/mcp-orch" "$stage/bin/mcp-orch"
  cp "$root/bin/mcp-lsp" "$stage/bin/mcp-lsp"
  cp "$root/bin/mcp-ida" "$stage/bin/mcp-ida"
  copy_sqlite_migrations "$stage"
  copy_packaged_lsp_bundle "$stage"
  copy_packaged_codex "$stage" "$stage/bin/codex"
  write_codex_manifest "$stage"
  write_lsp_manifest "$stage"
  copy_model_registry "$stage"
  write_packaged_relay_env "$stage"
  write_runtime_manifest "$stage"

  cat > "$stage/run.sh" <<'RUN'
#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
case "$(uname -m)" in
  x86_64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "unsupported Linux architecture: $(uname -m)" >&2; exit 1 ;;
esac
export PROJECT_ROOT="$here"
export SUPER_DOLPHIN_PACKAGE_ROOT="$here"
export SUPER_DOLPHIN_RUNTIME_MODE=packaged
export SUPER_DOLPHIN_PACKAGED_LAUNCHER=1
export SUPER_DOLPHIN_MODEL_REGISTRY="$here/models.yaml"
export PATH="$here/bin:${PATH:-}"
export GO_AGENT_PEER_BIN_DIR="$here/bin"
export SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=1
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="$here/lsp"
export SUPER_DOLPHIN_LSP_MANIFEST="$here/lsp/lsp-manifest.json"
bundled_execs=(mcp-orch mcp-lsp mcp-ida gopls go typescript-language-server vscode-css-language-server pyright-langserver rust-analyzer bash-language-server sql-language-server shellcheck sg)
if grep -q '"jdtls"' "$SUPER_DOLPHIN_LSP_MANIFEST"; then
  bundled_execs+=(jdtls)
fi
for bundled_exec in "${bundled_execs[@]}"; do
  if [[ ! -x "$here/bin/$bundled_exec" ]]; then
    echo "missing bundled executable: $here/bin/$bundled_exec" >&2
    exit 1
  fi
done
exec "$here/bin/agent-terminal" "$@"
RUN
  chmod +x "$stage/run.sh"

  "$root/scripts/verify_packaged_app_linux.sh" "$stage"
  tar -C "$dist" -czf "$stage.tar.gz" "$(basename "$stage")"
  echo "Linux package ready: $stage.tar.gz"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  package_linux_main "$@"
fi
