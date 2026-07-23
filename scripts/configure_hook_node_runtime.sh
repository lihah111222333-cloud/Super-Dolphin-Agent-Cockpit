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
  local canonical_exec_path canonical_version node_bin source_label
  node_bin=
  source_label=

  if [ -n "${SUPER_DOLPHIN_HOOK_NODE_BIN:-}" ]; then
    node_bin=${SUPER_DOLPHIN_HOOK_NODE_BIN%/}
    source_label=SUPER_DOLPHIN_HOOK_NODE_BIN
  elif node_bin=$(find_codex_hook_node_bin); then
    source_label="Codex bundled runtime"
  fi

  if [ -z "$node_bin" ]; then
    echo "❌ git hook 缺少受管 Node.js；请设置 SUPER_DOLPHIN_HOOK_NODE_BIN 或使用 Codex bundled runtime" >&2
    return 1
  fi

  validate_hook_node_bin "$node_bin" || return 1
  export PATH="$node_bin:$PATH"
  hash -r

  if ! canonical_exec_path=$(node -p 'require("node:fs").realpathSync(process.execPath)'); then
    echo "❌ git hook 无法解析 canonical Node execPath：$node_bin/node" >&2
    return 1
  fi
  if ! canonical_version=$(node -p 'process.version'); then
    echo "❌ git hook 无法读取 canonical Node version：$node_bin/node" >&2
    return 1
  fi
  if [ -z "$canonical_exec_path" ] || [ -z "$canonical_version" ]; then
    echo "❌ git hook canonical Node 身份为空：$node_bin/node" >&2
    return 1
  fi
  export SUPER_DOLPHIN_CANONICAL_NODE_EXEC_PATH="$canonical_exec_path"
  export SUPER_DOLPHIN_CANONICAL_NODE_VERSION="$canonical_version"
  echo "[git-hook] Node runtime: $source_label -> $canonical_exec_path ($canonical_version)"

  if ! command -v npm >/dev/null 2>&1; then
    echo "❌ git hook 缺少 npm：Node=$(command -v node)" >&2
    return 1
  fi
}
