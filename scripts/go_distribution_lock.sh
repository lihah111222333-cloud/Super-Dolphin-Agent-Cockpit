#!/usr/bin/env bash

go_distribution_lock_root() {
  local script_dir
  script_dir="$(cd -- "${BASH_SOURCE[0]%/*}" && pwd)"
  printf '%s\n' "${script_dir%/*}"
}

go_distribution_macos_asset() {
  local arch="$1" root lock
  case "$arch" in
    arm64) ;;
    x86_64|amd64) arch="amd64" ;;
    *)
      echo "❌ 未锁定当前 macOS 架构的 Go 官方发行包: $arch" >&2
      return 1
      ;;
  esac
  root="$(go_distribution_lock_root)"
  lock="$root/internal/devtools/godistribution/go-distribution.lock"
  if [[ ! -r "$lock" ]]; then
    echo "❌ Go 官方发行包锁文件不可读: $lock" >&2
    return 1
  fi
  /usr/bin/awk -F '\t' -v arch="$arch" '$1 == "go1.26.5" && $2 == "darwin" && $3 == arch { print; found=1; exit } END { if (!found) exit 1 }' "$lock" || {
    echo "❌ 未锁定 macOS/$arch 的 Go 1.26.5 官方发行包" >&2
    return 1
  }
}

go_distribution_current_macos_asset() {
  go_distribution_macos_asset "$(/usr/bin/uname -m)"
}
