#!/usr/bin/env bash

# local_go_cache_platform_tool_dir 保持 POSIX Go 工具目录的原始绝对路径。
local_go_cache_platform_tool_dir() {
  printf '%s\n' "$1"
}
