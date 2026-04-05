#!/usr/bin/env bash
# run-debug.sh — 编译并运行 agent-terminal
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKTREE_DIR="$PROJECT_DIR/.worktrees/test"
FRONTEND_DIR="$PROJECT_DIR/cmd/agent-terminal/frontend"
FORCE_NPM_REINSTALL="0"
NPM_REGISTRY="https://registry.npmmirror.com"
USE_FRIDA="1"
USE_SERVER="0"
FRIDA_VERSION_FILE_REL="build/frida-version.txt"

resolve_frida_version() {
  local base_dir="$1"
  local candidate=""
  for candidate in \
    "$base_dir/$FRIDA_VERSION_FILE_REL" \
    "$PROJECT_DIR/$FRIDA_VERSION_FILE_REL"
  do
    if [ -f "$candidate" ]; then
      tr -d '[:space:]' < "$candidate"
      return 0
    fi
  done
  return 1
}

# frida-bootstrap 预编译缓存路径（和 BUILD_DIR 绑定，不同 worktree 各自缓存）
FRIDA_BOOTSTRAP_BIN=""

# 预编译或复用已有的 frida-bootstrap 二进制
ensure_frida_bootstrap() {
  local src_dir="$1"
  local cache_bin="$src_dir/.build-cache/frida-bootstrap"
  local src_hash_file="$src_dir/.build-cache/frida-bootstrap.srchash"
  mkdir -p "$src_dir/.build-cache"

  # 计算 frida-bootstrap 源码 hash（源码不变则复用缓存二进制）
  local cur_hash
  cur_hash=$(find "$src_dir/cmd/frida-bootstrap" -name '*.go' | sort | xargs md5 -q 2>/dev/null | md5 -q 2>/dev/null || echo "nohash")

  if [ -f "$cache_bin" ] && [ -f "$src_hash_file" ] && [ "$(cat "$src_hash_file")" = "$cur_hash" ]; then
    echo "  → frida-bootstrap 缓存命中，跳过编译"
  else
    echo "  → 编译 frida-bootstrap..."
    CGO_ENABLED=1 go build -o "$cache_bin" "$src_dir/cmd/frida-bootstrap/"
    echo "$cur_hash" > "$src_hash_file"
  fi
  FRIDA_BOOTSTRAP_BIN="$cache_bin"
}

build_debug_binary_with_frida() {
  local output="$1"
  local pkg="$2"
  local frida_version="$3"
  local frida_ldflags="-X github.com/multi-agent/go-agent-v2/pkg/idamcp.defaultFridaVersion=$frida_version"
  # 使用预编译的 frida-bootstrap，不再每次 go run（go run 不走 build cache）
  CGO_ENABLED=1 "$FRIDA_BOOTSTRAP_BIN" --frida-version "$frida_version" -- \
    go build -tags ida,frida -gcflags="-N -l" -ldflags "$frida_ldflags" -o "$output" "$pkg"
}

echo "┌──────────────────────────────────┐"
echo "│    agent-terminal 编译工具       │"
echo "└──────────────────────────────────┘"
echo ""
echo "  [1] 主分支编译 debug (Frida)"
echo "  [2] test 分支编译 debug (Frida)"
echo "  [3] 正常编译"
echo "  [4] 直接启动已编译二进制 (debug)"
echo "  [5] 按 git tag 编译 debug (Frida)"
echo ""
read -rp "选择 (1/2/3/4/5): " choice

case "$choice" in
  1)
    BUILD_DIR="$PROJECT_DIR" ; MODE="debug"
    echo ""
    echo "  debug 子选项:"
    echo "    [1] 无 IDA/Frida (精简编译)"
    echo "    [2] 正常编译 (含 Frida)"
    echo "    [3] Server 模式 (浏览器访问 localhost:4511)"
    echo ""
    read -rp "  选择 (1/2/3): " debug_sub
    case "$debug_sub" in
      1) USE_FRIDA="0"; LABEL="main + debug (no Frida)" ;;
      2) USE_FRIDA="1"; LABEL="main + debug (Frida)" ;;
      3) USE_FRIDA="0"; USE_SERVER="1"; LABEL="main + debug (server mode)" ;;
      *) echo "❌ 无效选择"; exit 1 ;;
    esac
    ;;
  2) BUILD_DIR="$WORKTREE_DIR"; MODE="debug" ; LABEL="test + debug" ;;
  3) BUILD_DIR="$PROJECT_DIR" ; MODE="normal"; LABEL="main + normal" ;;
  4) BUILD_DIR="$PROJECT_DIR" ; MODE="run-only"; LABEL="直接启动 (debug)" ;;
  5)
    TAGS=()
    while IFS= read -r tag; do
      [ -n "$tag" ] && TAGS+=("$tag")
    done < <(git -C "$PROJECT_DIR" tag --sort=-version:refname)
    if [ "${#TAGS[@]}" -eq 0 ]; then
      echo "❌ 未找到 git tag"
      exit 1
    fi
    echo ""
    echo "可用 git tag:"
    for i in "${!TAGS[@]}"; do
      printf "  [%d] %s\n" "$((i + 1))" "${TAGS[$i]}"
    done
    echo ""
    read -rp "选择 tag 序号: " tag_choice
    if ! [[ "$tag_choice" =~ ^[0-9]+$ ]] || [ "$tag_choice" -lt 1 ] || [ "$tag_choice" -gt "${#TAGS[@]}" ]; then
      echo "❌ 无效 tag 选择"
      exit 1
    fi
    SELECTED_TAG="${TAGS[$((tag_choice - 1))]}"
    TAG_SAFE_NAME="$(echo "$SELECTED_TAG" | sed 's#[/[:space:]]#_#g')"
    BUILD_DIR="$PROJECT_DIR/.worktrees/tag-$TAG_SAFE_NAME"
    MODE="debug"
    LABEL="tag $SELECTED_TAG + debug"
    if [ -e "$BUILD_DIR" ] && [ ! -d "$BUILD_DIR/.git" ]; then
      echo "❌ 目录已存在且不是 git worktree: $BUILD_DIR"
      exit 1
    fi
    mkdir -p "$PROJECT_DIR/.worktrees"
    if [ -d "$BUILD_DIR/.git" ]; then
      git -C "$BUILD_DIR" checkout --detach "$SELECTED_TAG" >/dev/null
    else
      git -C "$PROJECT_DIR" worktree add --detach "$BUILD_DIR" "$SELECTED_TAG" >/dev/null
    fi
    ;;
  *) echo "❌ 无效选择"; exit 1 ;;
esac

echo ""
echo "▶ 模式: $LABEL"
echo "▶ 目录: $BUILD_DIR"
echo "────────────────────────────────────"

# run-only 模式: 跳过编译，直接启动
if [ "$MODE" = "run-only" ]; then
  if [ ! -f "$BUILD_DIR/super-agent-debug" ]; then
    echo "❌ 未找到已编译的二进制: $BUILD_DIR/super-agent-debug"
    echo "   请先使用选项 1/2/3/5 编译"
    exit 1
  fi
  echo "[1/2] 停止旧进程..."
  pkill -f "super-agent-debug" >/dev/null 2>&1 || true
  lsof -ti :4510 :4511 2>/dev/null | xargs kill -9 2>/dev/null || true
  sleep 0.5

  echo "[2/2] 清理 webview 缓存..."
  rm -rf "$HOME/Library/Caches/agent-terminal" \
         "$HOME/Library/Caches/com.multi-agent.agent-terminal" \
         "$HOME/Library/WebKit/agent-terminal" \
         "$HOME/Library/WebKit/agent-terminal.cover" \
         "$HOME/Library/WebKit/com.multi-agent.agent-terminal"

  echo ""
  echo "════════════════════════════════════"
  echo "▶ 直接启动已编译二进制 (debug)..."
  echo "  sha256: $(shasum -a 256 "$BUILD_DIR/super-agent-debug" | awk '{print $1}')"
  (sleep 1.0; open "http://localhost:4511" >/dev/null 2>&1 || true) &
  exec "$BUILD_DIR/super-agent-debug" --debug "$@"
fi

# 1) 前端
FRONT="$BUILD_DIR/cmd/agent-terminal/frontend"

if [ -f "$FRONT/package.json" ]; then
  echo "[1/4] 编译前端..."
  cd "$FRONT"
  # 三级依赖安装策略（按速度优先）：
  #   1. node_modules 不存在 → 全新安装 (npm ci)
  #   2. package-lock.json 比 node_modules 新 → lock 变了，需 ci 洁净安装
  #   3. package.json 比 node_modules 新 → 有新增依赖，增量 install 即可
  #   4. 其他 → 跳过，不安装
  if [ ! -d "node_modules" ] || [ "$FORCE_NPM_REINSTALL" = "1" ]; then
    echo "  → npm ci (首次/全量安装)..."
    npm ci --registry="$NPM_REGISTRY"
  elif [ -f "package-lock.json" ] && [ "package-lock.json" -nt "node_modules" ]; then
    echo "  → npm ci (lock 文件已更新，洁净安装)..."
    npm ci --registry="$NPM_REGISTRY"
  elif [ "package.json" -nt "node_modules" ]; then
    echo "  → npm install (增量追加新依赖)..."
    npm install --registry="$NPM_REGISTRY"
  else
    echo "  → 依赖无变化，跳过安装"
  fi
  if ! npm run build; then
    echo ""
    echo "⚠️  前端构建/守卫未通过！按 Enter 跳过继续编译，Ctrl+C 中止"
    read -r
    echo "  → 已跳过前端报错，继续..."
  fi
else
  echo "[1/4] 跳过前端 (无 package.json)"
fi

# 2) 清理缓存
echo "[2/4] 清理 webview 缓存..."
rm -rf "$HOME/Library/Caches/agent-terminal" \
       "$HOME/Library/Caches/com.multi-agent.agent-terminal" \
       "$HOME/Library/WebKit/agent-terminal" \
       "$HOME/Library/WebKit/agent-terminal.cover" \
       "$HOME/Library/WebKit/com.multi-agent.agent-terminal"

# 3) 后端代码守卫 + 编译
cd "$BUILD_DIR"
echo "[3/4] 后端代码守卫检查..."

# 预编译守卫工具并缓存（源码未变则跳过重编译，避免每次 go run）
_GUARD_BIN="$BUILD_DIR/.build-cache/code-size-guard"
_GUARD_HASH_FILE="$BUILD_DIR/.build-cache/code-size-guard.srchash"
mkdir -p "$BUILD_DIR/.build-cache"
_GUARD_CUR_HASH=$(md5 -q "$BUILD_DIR/scripts/code_size_guard.go" 2>/dev/null || echo "nohash")
if [ ! -f "$_GUARD_BIN" ] || [ ! -f "$_GUARD_HASH_FILE" ] || [ "$(cat "$_GUARD_HASH_FILE")" != "$_GUARD_CUR_HASH" ]; then
  echo "  → 编译 code_size_guard..."
  go build -o "$_GUARD_BIN" "$BUILD_DIR/scripts/code_size_guard.go"
  echo "$_GUARD_CUR_HASH" > "$_GUARD_HASH_FILE"
else
  echo "  → code_size_guard 缓存命中，跳过编译"
fi

if ! "$_GUARD_BIN"; then
  echo ""
  echo "⚠️  代码守卫检查未通过！按 Enter 跳过继续编译，Ctrl+C 中止"
  read -r
  echo "  → 已跳过守卫检查，继续编译..."
fi

if [ "$MODE" = "debug" ] && [ "$USE_FRIDA" = "1" ]; then
  if ! FRIDA_DEVKIT_VERSION="$(resolve_frida_version "$BUILD_DIR")"; then
    echo "❌ debug 模式默认需要 Frida 版本文件，未找到:"
    echo "   - $BUILD_DIR/$FRIDA_VERSION_FILE_REL"
    echo "   - $PROJECT_DIR/$FRIDA_VERSION_FILE_REL"
    exit 1
  fi
  if [ ! -f "./cmd/frida-bootstrap/main.go" ]; then
    echo "❌ 当前源码树缺少 ./cmd/frida-bootstrap，无法执行 Frida debug 编译"
    exit 1
  fi
  # 预编译 frida-bootstrap（源码不变则复用缓存，不再每次 go run）
  ensure_frida_bootstrap "$BUILD_DIR"
  echo "[3/4] 编译后端 (debug + IDA + Frida: -tags ida,frida, -gcflags='-N -l')..."
  build_debug_binary_with_frida ./super-agent-debug ./cmd/agent-terminal "$FRIDA_DEVKIT_VERSION"
  build_debug_binary_with_frida ./mcp-orch ./cmd/mcp-orch "$FRIDA_DEVKIT_VERSION"
  # 去掉 all= 前缀：仅本包禁优化，stdlib/第三方包仍走增量缓存
  CGO_ENABLED=1 go build -gcflags="-N -l" -o ./mcp-lsp ./cmd/mcp-lsp/
elif [ "$MODE" = "debug" ] && [ "$USE_SERVER" = "1" ]; then
  echo "[3/4] 编译后端 (debug server 模式: -gcflags='-N -l')..."
  CGO_ENABLED=1 go build -gcflags="-N -l" -o ./super-agent-debug ./cmd/agent-terminal/
  CGO_ENABLED=1 go build -gcflags="-N -l" -o ./mcp-orch ./cmd/mcp-orch/
  CGO_ENABLED=1 go build -gcflags="-N -l" -o ./mcp-lsp ./cmd/mcp-lsp/
elif [ "$MODE" = "debug" ] && [ "$USE_FRIDA" = "0" ]; then
  echo "[3/4] 编译后端 (debug 无 Frida: -gcflags='-N -l')..."
  CGO_ENABLED=1 go build -gcflags="-N -l" -o ./super-agent-debug ./cmd/agent-terminal/
  CGO_ENABLED=1 go build -gcflags="-N -l" -o ./mcp-orch ./cmd/mcp-orch/
  CGO_ENABLED=1 go build -gcflags="-N -l" -o ./mcp-lsp ./cmd/mcp-lsp/
else
  echo "[3/4] 编译后端 (release)..."
  go build -o ./super-agent-debug ./cmd/agent-terminal/
  go build -o ./mcp-orch ./cmd/mcp-orch/
  go build -o ./mcp-lsp ./cmd/mcp-lsp/
fi
echo "  ✅ super-agent-debug $(shasum -a 256 ./super-agent-debug | awk '{print "sha256: " $1}')"
echo "  ✅ mcp-orch       $(shasum -a 256 ./mcp-orch | awk '{print "sha256: " $1}')"
echo "  ✅ mcp-lsp        $(shasum -a 256 ./mcp-lsp | awk '{print "sha256: " $1}')"

# 4) 停旧进程
echo "[4/4] 停止旧进程..."
pkill -f "super-agent-debug" >/dev/null 2>&1 || true
lsof -ti :4510 :4511 2>/dev/null | xargs kill -9 2>/dev/null || true
sleep 0.5

# 启动
echo ""
echo "════════════════════════════════════"
if [ "$MODE" = "debug" ]; then
  echo "▶ 启动 debug 模式 (Frida + 调试 UI)..."
  (sleep 1.0; open "http://localhost:4511" >/dev/null 2>&1 || true) &
  exec "$BUILD_DIR/super-agent-debug" --debug "$@"
else
  echo "▶ 启动正常模式..."
  exec "$BUILD_DIR/super-agent-debug" "$@"
fi
