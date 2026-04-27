#!/usr/bin/env bash
# 创建 repoA / repoB / repoC-symlink 三套工作树供 fingerprint 隔离测试。
# 用法：bash test/fixtures/p21/repos/bootstrap.sh [target_dir]
# 默认 target_dir = $(mktemp -d)，路径打印到 stdout。
set -euo pipefail

target="${1:-$(mktemp -d -t p21-repos-XXXXXX)}"
mkdir -p "$target"

for name in repoA repoB; do
  d="$target/$name"
  mkdir -p "$d"
  cat > "$d/README.md" <<EOF
# $name fixture
P21 fingerprint isolation fixture. Do not place real project code here.
EOF
done

# repoC 是 repoA 的 symlink，验证 EvalSymlinks 后两者 fingerprint 必须相同
if [ ! -e "$target/repoC-symlink" ]; then
  ln -s "$target/repoA" "$target/repoC-symlink"
fi

echo "$target"
