#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
platform="$goos-$goarch"
if [[ "$goos" != "darwin" ]]; then
  echo "prepare_lsp_bundle_macos.sh must run on macOS; current GOOS=$goos" >&2
  exit 1
fi

lsp_profile="${SUPER_DOLPHIN_LSP_PROFILE:-standard}"
case "$lsp_profile" in
  standard|full)
    ;;
  *)
    echo "unsupported SUPER_DOLPHIN_LSP_PROFILE=$lsp_profile; expected standard or full" >&2
    exit 1
    ;;
esac

lsp_dir="${SUPER_DOLPHIN_LSP_BUNDLE_DIR:-$root/.build-cache/lsp/$lsp_profile/$platform}"
default_node_dist="$(node -p 'require("path").dirname(require("path").dirname(process.execPath))' 2>/dev/null || true)"
node_src="${SUPER_DOLPHIN_NODE_DIST:-$default_node_dist}"
gopls_bin="${SUPER_DOLPHIN_GOPLS_BIN:-$(command -v gopls || true)}"
clangd_bin="${SUPER_DOLPHIN_CLANGD_BIN:-$(command -v clangd || true)}"
sqruff_bin="${SUPER_DOLPHIN_SQRUFF_BIN:-$(command -v sqruff || true)}"
go_toolchain_src="${SUPER_DOLPHIN_GO_TOOLCHAIN_DIR:-$(go env GOROOT)}"
jdtls_home="${SUPER_DOLPHIN_JDTLS_HOME:-/opt/homebrew/Cellar/jdtls/1.58.0/libexec}"
jdk_home="${SUPER_DOLPHIN_JDK_HOME:-/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home}"
npm_bin="${SUPER_DOLPHIN_NPM_BIN:-$(command -v npm || true)}"

resolve_rust_analyzer_bin() {
  local candidate="${1:-}"
  if [[ -z "$candidate" ]]; then
    candidate="$(command -v rust-analyzer || true)"
  fi
  if [[ -z "$candidate" ]]; then
    return 0
  fi
  local target=""
  if [[ -L "$candidate" ]]; then
    target="$(readlink "$candidate" || true)"
  fi
  if [[ "$(basename "$candidate")" == "rust-analyzer" && "$(basename "$target")" == "rustup" ]]; then
    local rustup_bin=""
    if command -v rustup >/dev/null 2>&1; then
      rustup_bin="$(rustup which rust-analyzer 2>/dev/null || true)"
    fi
    if [[ -n "$rustup_bin" && -x "$rustup_bin" ]]; then
      printf '%s\n' "$rustup_bin"
      return 0
    fi
    if [[ -x "/opt/homebrew/bin/rust-analyzer" ]]; then
      printf '%s\n' "/opt/homebrew/bin/rust-analyzer"
      return 0
    fi
    echo "rust-analyzer resolves to rustup shim without a default toolchain; install a standalone rust-analyzer or set SUPER_DOLPHIN_RUST_ANALYZER_BIN" >&2
    return 1
  fi
  printf '%s\n' "$candidate"
}

rust_analyzer_bin="$(resolve_rust_analyzer_bin "${SUPER_DOLPHIN_RUST_ANALYZER_BIN:-}")"

echo "==> preparing $lsp_profile LSP bundle: $lsp_dir"
rm -rf "$lsp_dir"
mkdir -p "$lsp_dir/bin" "$lsp_dir/node/bin"

echo "==> copying slim Node runtime"
test -x "$node_src/bin/node" || { echo "missing node: $node_src/bin/node" >&2; exit 1; }
cp "$node_src/bin/node" "$lsp_dir/node/bin/node"
chmod +x "$lsp_dir/node/bin/node"

test -x "$npm_bin" || { echo "missing npm; set SUPER_DOLPHIN_NPM_BIN" >&2; exit 1; }
echo "==> installing Node-based LSP packages with host npm: $npm_bin"
"$npm_bin" install --prefix "$lsp_dir" \
  typescript-language-server@5.3.0 \
  typescript@5.9.3 \
  vscode-langservers-extracted \
  pyright \
  bash-language-server \
  shellcheck \
  @ast-grep/cli

write_no_system_python_stub() {
  local name="$1"
  cat > "$lsp_dir/bin/$name" <<'WRAP'
#!/bin/sh
echo "Packaged Super Dolphin does not bundle a Python interpreter; skipping system interpreter fallback." >&2
exit 127
WRAP
  chmod +x "$lsp_dir/bin/$name"
}
write_no_system_python_stub python
write_no_system_python_stub python3

write_node_wrapper() {
  local name="$1"
  local target="$2"
  cat > "$lsp_dir/bin/$name" <<WRAP
#!/bin/bash
set -euo pipefail
here="\$(cd "\$(dirname "\${BASH_SOURCE[0]}")" && pwd)"
export PATH="\$here/../node/bin:\${PATH:-}"
exec "\$here/../node/bin/node" "\$here/../node_modules/$target" "\$@"
WRAP
  chmod +x "$lsp_dir/bin/$name"
}
write_node_wrapper typescript-language-server typescript-language-server/lib/cli.mjs
write_node_wrapper vscode-css-language-server vscode-langservers-extracted/bin/vscode-css-language-server
write_node_wrapper pyright-langserver pyright/langserver.index.js

write_path_wrapper() {
  local name="$1"
  local target="$2"
  test -x "$lsp_dir/$target" || { echo "missing bundled executable target: $lsp_dir/$target" >&2; exit 1; }
  cat > "$lsp_dir/bin/$name" <<WRAP
#!/bin/bash
set -euo pipefail
here="\$(cd "\$(dirname "\${BASH_SOURCE[0]}")" && pwd)"
export PATH="\$here/../node/bin:\${PATH:-}"
exec "\$here/../$target" "\$@"
WRAP
  chmod +x "$lsp_dir/bin/$name"
}
write_path_wrapper bash-language-server node_modules/bash-language-server/out/cli.js
"$lsp_dir/node_modules/.bin/shellcheck" --version >/dev/null
write_path_wrapper sg node_modules/.bin/sg
write_path_wrapper shellcheck node_modules/shellcheck/bin/shellcheck

echo "==> copying native LSP servers"
test -x "$gopls_bin" || { echo "missing gopls: $gopls_bin" >&2; exit 1; }
test -x "$clangd_bin" || { echo "missing clangd; set SUPER_DOLPHIN_CLANGD_BIN" >&2; exit 1; }
test -x "$rust_analyzer_bin" || { echo "missing rust-analyzer: $rust_analyzer_bin" >&2; exit 1; }
test -x "$sqruff_bin" || { echo "missing sqruff; set SUPER_DOLPHIN_SQRUFF_BIN" >&2; exit 1; }
cp "$gopls_bin" "$lsp_dir/bin/gopls"
cp "$clangd_bin" "$lsp_dir/bin/clangd"
cp "$rust_analyzer_bin" "$lsp_dir/bin/rust-analyzer"
cp "$sqruff_bin" "$lsp_dir/bin/sqruff"
chmod +x "$lsp_dir/bin/gopls" "$lsp_dir/bin/clangd" "$lsp_dir/bin/rust-analyzer" "$lsp_dir/bin/sqruff"
"$lsp_dir/bin/clangd" --version >/dev/null

copy_go_toolchain() {
  test -x "$go_toolchain_src/bin/go" || { echo "missing Go toolchain: $go_toolchain_src/bin/go" >&2; exit 1; }
  rsync -a --delete "$go_toolchain_src/" "$lsp_dir/go/"
  rm -rf "$lsp_dir/go/pkg/obj"
}

write_go_toolchain_wrapper() {
cat > "$lsp_dir/bin/go" <<'WRAP'
#!/bin/bash
set -euo pipefail
source="${BASH_SOURCE[0]}"
while [[ -L "$source" ]]; do
  dir="$(cd "$(dirname "$source")" && pwd)"
  target="$(readlink "$source")"
  if [[ "$target" == /* ]]; then
    source="$target"
  else
    source="$dir/$target"
  fi
done
here="$(cd "$(dirname "$source")" && pwd)"
export GOROOT="$here/../go"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"
exec "$GOROOT/bin/go" "$@"
WRAP
  chmod +x "$lsp_dir/bin/go"
  "$lsp_dir/bin/go" env GOROOT >/dev/null
}
copy_go_toolchain
write_go_toolchain_wrapper

write_java_runtime_wrapper() {
cat > "$lsp_dir/bin/java" <<'WRAP'
#!/bin/bash
set -euo pipefail
source="${BASH_SOURCE[0]}"
while [[ -L "$source" ]]; do
  dir="$(cd "$(dirname "$source")" && pwd)"
  target="$(readlink "$source")"
  if [[ "$target" == /* ]]; then
    source="$target"
  else
    source="$dir/$target"
  fi
done
here="$(cd "$(dirname "$source")" && pwd)"
export JAVA_HOME="$here/../jdk"
exec "$JAVA_HOME/bin/java" "$@"
WRAP
  chmod +x "$lsp_dir/bin/java"
  "$lsp_dir/bin/java" -version >/dev/null
}

if [[ "$lsp_profile" == "full" ]]; then
  echo "==> copying Java runtime and jdtls"
  test -d "$jdtls_home" || { echo "missing jdtls home: $jdtls_home" >&2; exit 1; }
  test -x "$jdk_home/bin/java" || { echo "missing JDK java: $jdk_home/bin/java" >&2; exit 1; }
  rsync -a --delete "$jdtls_home/" "$lsp_dir/jdtls/"
  rsync -a --delete "$jdk_home/" "$lsp_dir/jdk/"
  write_java_runtime_wrapper
  rm -f "$lsp_dir/jdtls/bin/jdtls"
  cat > "$lsp_dir/bin/jdtls" <<'WRAP'
#!/bin/bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export JAVA_HOME="$here/../jdk"
export PATH="$JAVA_HOME/bin:${PATH:-}"
jdtls_home="$here/../jdtls"
config_dir="$jdtls_home/config_mac"
if [[ ! -d "$config_dir" ]]; then
  echo "missing bundled jdtls config: $config_dir" >&2
  exit 1
fi
if [[ -f "$jdtls_home/plugins/org.eclipse.equinox.launcher.jar" ]]; then
  launcher="$jdtls_home/plugins/org.eclipse.equinox.launcher.jar"
else
  launchers=("$jdtls_home"/plugins/org.eclipse.equinox.launcher_*.jar)
  launcher="${launchers[0]}"
fi
if [[ ! -f "$launcher" ]]; then
  echo "missing bundled jdtls equinox launcher under $jdtls_home/plugins" >&2
  exit 1
fi
if [[ "${1:-}" == "-version" || "${1:-}" == "--version" ]]; then
  exec "$JAVA_HOME/bin/java" -version
fi
cache_root="${HOME:-${TMPDIR:-/tmp}}/Library/Caches/jdtls"
cwd_name="$(basename "$PWD")"
data_hash="$(printf '%s' "$cwd_name" | shasum -a 1 | awk '{print $1}')"
data_dir="$cache_root/jdtls-$data_hash"
exec "$JAVA_HOME/bin/java"   -Djdk.xml.maxGeneralEntitySizeLimit=0   -Djdk.xml.totalEntitySizeLimit=0   -Declipse.application=org.eclipse.jdt.ls.core.id1   -Dosgi.bundles.defaultStartLevel=4   -Declipse.product=org.eclipse.jdt.ls.core.product   -Dosgi.checkConfiguration=true   -Dosgi.sharedConfiguration.area="$config_dir"   -Dosgi.sharedConfiguration.area.readOnly=true   -Dosgi.configuration.cascaded=true   -Xms1G   --add-modules=ALL-SYSTEM   --add-opens java.base/java.util=ALL-UNNAMED   --add-opens java.base/java.lang=ALL-UNNAMED   -jar "$launcher"   -data "$data_dir"   "$@"
WRAP
  chmod +x "$lsp_dir/bin/jdtls"
  "$lsp_dir/bin/jdtls" -version >/dev/null
fi

prune_lsp_bundle_runtime_only_artifacts() {
  rm -rf "$lsp_dir/jdk/jmods"
  rm -rf "$lsp_dir/jdk/demo"
  rm -rf "$lsp_dir/jdk/include"
  rm -rf "$lsp_dir/node_modules/@ast-grep/cli-"*
  rm -f "$lsp_dir/node_modules/@ast-grep/cli/ast-grep"
  rm -f "$lsp_dir/node_modules/.bin/ast-grep"
  test -x "$lsp_dir/node_modules/@ast-grep/cli/sg" || { echo "missing pruned ast-grep sg executable" >&2; exit 1; }
}
prune_lsp_bundle_runtime_only_artifacts

echo "==> writing LSP manifest and checksums"
sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}
lsp_specs=(
  'gopls|bin/gopls|["go","gomod","gosum","gowork"]'
  'clangd|bin/clangd|["c","cpp","objective-c","objective-cpp","mql","mql4","mql5","mq4","mq5","mqh"]'
  'typescript-language-server|bin/typescript-language-server|["javascript","javascriptreact","typescript","typescriptreact"]'
  'vscode-langservers-extracted|bin/vscode-css-language-server|["css"]'
  'pyright|bin/pyright-langserver|["python"]'
  'rust-analyzer|bin/rust-analyzer|["rust"]'
  'bash-language-server|bin/bash-language-server|["shellscript"]'
  'sqruff|bin/sqruff|["sql"]'
  'shellcheck|bin/shellcheck|["shellcheck"]'
  'sg|bin/sg|["ast-grep"]'
  'go|bin/go|["go-toolchain"]'
)
if [[ "$lsp_profile" == "full" ]]; then
  lsp_specs+=('java|bin/java|["java-runtime"]')
  lsp_specs+=('jdtls|bin/jdtls|["java"]')
fi
{
  printf '{\n  "schema_version": 1,\n  "bundle_path": "lsp",\n  "profile": "%s",\n  "servers": {\n' "$lsp_profile"
  first=1
  for spec in "${lsp_specs[@]}"; do
    IFS='|' read -r server_id rel_path languages_json <<<"$spec"
    digest="$(sha256_file "$lsp_dir/$rel_path")"
    if [[ "$first" == 0 ]]; then
      printf ',\n'
    fi
    first=0
    printf '    "%s": {"path": "%s", "version": "bundled", "sha256": "%s", "languages": %s}' "$server_id" "$rel_path" "$digest" "$languages_json"
  done
  printf '\n  }\n}\n'
} > "$lsp_dir/lsp-manifest.json"
{
  for spec in "${lsp_specs[@]}"; do
    IFS='|' read -r _ rel_path _ <<<"$spec"
    printf '%s  %s\n' "$(sha256_file "$lsp_dir/$rel_path")" "$rel_path"
  done
} > "$lsp_dir/lsp-checksums.sha256"
(
  cd "$lsp_dir"
  shasum -a 256 -c lsp-checksums.sha256
)

echo "==> LSP bundle ready: $lsp_dir"
du -sh "$lsp_dir"
