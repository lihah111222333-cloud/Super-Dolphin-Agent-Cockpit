#!/usr/bin/env bash

find_codex_hook_node_bin() {
  local path_entry runtime_root
  while IFS= read -r path_entry; do
    case "$path_entry" in
      */codex-primary-runtime/dependencies/bin/override|*/codex-primary-runtime/dependencies/bin/fallback)
        runtime_root=${path_entry%/bin/override}
        runtime_root=${runtime_root%/bin/fallback}
        printf '%s/node/bin\n' "$runtime_root"
        return 0
        ;;
    esac
  done < <(printf '%s' "$PATH" | tr ':' '\n')
  return 1
}

validate_hook_node_bin() {
  local node_bin=$1
  if [ ! -x "$node_bin/node" ]; then
    echo "❌ git hook Node 不可执行：$node_bin/node" >&2
    return 1
  fi
  if ! "$node_bin/node" -e 'process.exit(0)'; then
    echo "❌ git hook Node 启动失败：$node_bin/node" >&2
    return 1
  fi
}

configure_hook_node_runtime() {
  local node_bin source_label
  node_bin=
  source_label=

  if [ -n "${SUPER_DOLPHIN_HOOK_NODE_BIN:-}" ]; then
    node_bin=${SUPER_DOLPHIN_HOOK_NODE_BIN%/}
    source_label=SUPER_DOLPHIN_HOOK_NODE_BIN
  elif node_bin=$(find_codex_hook_node_bin); then
    source_label="Codex bundled runtime"
  fi

  if [ -n "$node_bin" ]; then
    validate_hook_node_bin "$node_bin" || return 1
    export PATH="$node_bin:$PATH"
    hash -r
    echo "[git-hook] Node runtime: $source_label -> $node_bin/node"
  fi

  if ! command -v node >/dev/null 2>&1; then
    echo "❌ git hook 缺少 Node.js；请安装 Node 或设置 SUPER_DOLPHIN_HOOK_NODE_BIN" >&2
    return 1
  fi
  if ! node -e 'process.exit(0)'; then
    echo "❌ git hook Node 启动失败：$(command -v node)" >&2
    return 1
  fi
  if ! command -v npm >/dev/null 2>&1; then
    echo "❌ git hook 缺少 npm：Node=$(command -v node)" >&2
    return 1
  fi
}
