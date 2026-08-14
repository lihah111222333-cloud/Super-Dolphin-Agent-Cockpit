#!/usr/bin/env bash

# local_go_cache_platform_tool_dir 把 Go 返回的 Windows 盘符路径转换为 Git Bash 的绝对路径。
local_go_cache_platform_tool_dir() {
  local tool_dir="$1"
  if ! command -v cygpath >/dev/null 2>&1; then
    echo "local Go cache requires cygpath for Windows GOTOOLDIR" >&2
    return 1
  fi
  cygpath -u -- "$tool_dir"
}
