#!/usr/bin/env bash
# run-debug.sh — 编译并运行 agent-terminal
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"

# 自动加载本地配置（如 SUPER_DOLPHIN_SQLITE_PATH）—— .env 已在 .gitignore，每个开发机器的本地配置可不同
# set -a 让 source 进来的所有变量自动 export 给子进程（agent-terminal 走 os.Getenv 读到）
if [ -f "$PROJECT_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$PROJECT_DIR/.env"
  set +a
fi

ensure_dev_control_session_token() {
  if [ -n "${GO_AGENT_CTL_SESSION_TOKEN:-}" ]; then
    return 0
  fi
  if [ -n "${GO_AGENT_MCP_SESSION_TOKEN:-}" ]; then
    export GO_AGENT_CTL_SESSION_TOKEN="$GO_AGENT_MCP_SESSION_TOKEN"
    return 0
  fi
  export GO_AGENT_CTL_SESSION_TOKEN="dev-local-$(date +%s)-$$"
}

ensure_dev_control_session_token

WORKTREE_DIR="$PROJECT_DIR/.worktrees/test"
FRONTEND_DIR="$PROJECT_DIR/cmd/agent-terminal/frontend"
export GO_GUARD_ALLOW_RAW="run-debug.sh"
FORCE_NPM_REINSTALL="0"
NPM_REGISTRY="https://registry.npmmirror.com"
USE_FRIDA="1"
USE_SERVER="0"
AUTO_CODEMAP_REFRESH="${AUTO_CODEMAP_REFRESH:-1}"
FRIDA_VERSION_FILE_REL="build/frida-version.txt"

checksum_file() {
  local file="$1"
  if command -v md5 >/dev/null 2>&1; then
    md5 -q "$file"
  elif command -v md5sum >/dev/null 2>&1; then
    md5sum "$file" | awk '{print $1}'
  else
    shasum -a 256 "$file" | awk '{print $1}'
  fi
}

maybe_refresh_codemap() {
  local build_dir="$1"
  local codemap_src="$build_dir/scripts/codemap_index.go"
  local codemap_dir="$build_dir/docs/doc/codemap"
  local codemap_readme="$codemap_dir/README.md"
  local codemap_makefile="$build_dir/Makefile"
  local codemap_cache_dir="$build_dir/.build-cache"
  local codemap_bin="$codemap_cache_dir/codemap-index"
  local codemap_hash_file="$codemap_cache_dir/codemap-index.srchash"
  local current_hash=""

  if [ "$AUTO_CODEMAP_REFRESH" != "1" ]; then
    echo "[pre-build] 跳过代码地图索引刷新 (AUTO_CODEMAP_REFRESH=$AUTO_CODEMAP_REFRESH)"
    return 0
  fi

  if [ ! -f "$codemap_src" ] || [ ! -d "$codemap_dir" ] || [ ! -f "$codemap_readme" ] || [ ! -f "$codemap_makefile" ]; then
    echo "[pre-build] 跳过代码地图索引刷新 (缺少 codemap 所需文件)"
    return 0
  fi

  echo "[pre-build] 刷新代码地图索引 (ai-index.json / README.md)..."
  mkdir -p "$codemap_cache_dir"
  current_hash="$(checksum_file "$codemap_src" || echo nohash)"

  if [ ! -f "$codemap_bin" ] || [ ! -f "$codemap_hash_file" ] || [ "$(cat "$codemap_hash_file")" != "$current_hash" ]; then
    echo "  → 编译 codemap_index..."
    if go build -o "$codemap_bin" "$codemap_src"; then
      echo "$current_hash" > "$codemap_hash_file"
    else
      echo "  ⚠️  codemap_index 编译失败，跳过索引刷新并继续启动"
      return 0
    fi
  else
    echo "  → codemap_index 缓存命中，跳过编译"
  fi

  if "$codemap_bin" "$build_dir"; then
    echo "  ✅ ai-index.json / README.md 已刷新"
  else
    echo "  ⚠️  代码地图索引刷新失败，跳过并继续启动"
  fi
}

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
echo "  [4] 直接启动已编译二进制 (debug + vite HMR)"
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

# ── [0/4] Pre-flight 守卫 (merge 冲突 + SQLite runtime path) ─────────────
# 编译/启动前做静态冲突检查，并确保 SQLite runtime 目录存在。
echo "[0/4] Pre-flight 守卫..."

# (a) merge 冲突标记 — 硬失败 (出现 <<<<<<< 必然 parser 崩)
PRE_CONFLICTS=$(grep -rlE '^(<{7}|>{7}|={7}$)' "$BUILD_DIR" \
  --include='*.go' --include='*.js' --include='*.vue' --include='*.ts' \
  --include='*.tsx' --include='*.jsx' --include='*.json' --include='*.sh' \
  --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=dist \
  --exclude-dir=.worktrees --exclude-dir=.build-cache --exclude-dir=vendor \
  2>/dev/null || true)
if [ -n "$PRE_CONFLICTS" ]; then
  echo "  ❌ 检测到未解决的 git merge 冲突标记："
  echo "$PRE_CONFLICTS" | sed 's|^|     - |'
  echo "  解完冲突再来 (这种状态下编译必爆)。"
  exit 1
fi

# (b) SQLite runtime path preflight
export SUPER_DOLPHIN_RUNTIME_MODE=dev
export SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="$BUILD_DIR"
export SUPER_DOLPHIN_DEV_ENTRYPOINT=run-debug.sh
SUPER_DOLPHIN_HOME="${SUPER_DOLPHIN_HOME:-$HOME/.super-dolphin}"
export SUPER_DOLPHIN_HOME
export SUPER_DOLPHIN_SQLITE_PATH="${SUPER_DOLPHIN_SQLITE_PATH:-$SUPER_DOLPHIN_HOME/super-dolphin.db}"
mkdir -p "$(dirname "$SUPER_DOLPHIN_SQLITE_PATH")"
echo "  SQLite DB: $SUPER_DOLPHIN_SQLITE_PATH"

# run-only 模式: 跳过编译，直接启动
if [ "$MODE" = "run-only" ]; then
  if [ ! -f "$BUILD_DIR/super-agent-debug" ]; then
    echo "❌ 未找到已编译的二进制: $BUILD_DIR/super-agent-debug"
    echo "   请先使用选项 1/2/3/5 编译"
    exit 1
  fi
  echo "[1/3] 停止旧进程..."
  pkill -f "super-agent-debug" >/dev/null 2>&1 || true
  # 历史残留：以前 go build ./cmd/agent-terminal/ 产生的同名进程也一并清理
  pkill -f "agent-terminal --debug" >/dev/null 2>&1 || true
  pkill -f "esbuild --service" >/dev/null 2>&1 || true
  # 连带清理 5173，避免后端连到别的项目的 vite 导致黑屏
  lsof -ti :4510 :4511 :5173 2>/dev/null | xargs kill -9 2>/dev/null || true
  sleep 0.5

  echo "[2/3] 清理 webview 缓存..."
  rm -rf "$HOME/Library/Caches/agent-terminal" \
         "$HOME/Library/Caches/com.multi-agent.agent-terminal" \
         "$HOME/Library/WebKit/agent-terminal" \
         "$HOME/Library/WebKit/agent-terminal.cover" \
         "$HOME/Library/WebKit/com.multi-agent.agent-terminal"

  # [3/3] 启动 vite dev server（与 [1]/[2]/[5] 热更新路径保持一致）
  # 没有 vite 时后端会退回到 dist/ 静态资源 —— 若 dist 陈旧就会黑屏
  RUNONLY_FRONT="$BUILD_DIR/cmd/agent-terminal/frontend"
  VITE_DEV_PID=""
  cleanup_vite_runonly() {
    if [ -n "$VITE_DEV_PID" ] && kill -0 "$VITE_DEV_PID" 2>/dev/null; then
      echo "  → 停止 vite dev server (PID: $VITE_DEV_PID)..."
      kill "$VITE_DEV_PID" 2>/dev/null || true
    fi
  }
  trap cleanup_vite_runonly EXIT INT TERM

  if [ -d "$RUNONLY_FRONT/node_modules" ] && [ -f "$RUNONLY_FRONT/package.json" ]; then
    # preflight: Vue template runtime-compile 守卫（根治 webview 黑屏）
    # 任何一个 template 在 runtime compiler 编译失败就 abort —— 窗口都不起来
    if [ -f "$RUNONLY_FRONT/scripts/check-templates.cjs" ]; then
      echo "[3/3] 预检 Vue template（runtime-compiler）..."
      if ! node "$RUNONLY_FRONT/scripts/check-templates.cjs"; then
        echo ""
        echo "  ❌ Vue template 预检失败 —— 启动 webview 会直接黑屏"
        echo "  修复上面列出的 template 再重启；按 Enter 忽略继续（不推荐），Ctrl+C 中止"
        read -r
        echo "  → 已忽略 template 守卫"
      fi
    fi
    echo "[3/3] 启动 vite dev server (端口 5173)..."
    (cd "$RUNONLY_FRONT" && npx vite --port 5173 --strictPort) &
    VITE_DEV_PID=$!
    for i in $(seq 1 30); do
      # 必须同时满足 vite 子进程存活 + 5173 可访问，避免连到别人家的 vite
      if kill -0 "$VITE_DEV_PID" 2>/dev/null && curl -s http://localhost:5173 >/dev/null 2>&1; then
        echo "  → vite dev server 已就绪 ✅ (PID: $VITE_DEV_PID)"
        export VITE_DEV_URL="http://localhost:5173"
        break
      fi
      sleep 0.3
    done
    if [ -z "${VITE_DEV_URL:-}" ]; then
      echo "  ⚠️  vite dev server 启动失败（端口冲突或 vite 进程已退出）"
      echo "      将退回 dist/ 静态资源，若 dist 陈旧则会黑屏"
    fi
  else
    echo "[3/3] 跳过 vite（缺 node_modules 或 package.json），使用 dist/ 静态资源"
  fi

  echo ""
  echo "════════════════════════════════════"
  echo "▶ 直接启动已编译二进制 (debug)..."
  echo "  sha256: $(shasum -a 256 "$BUILD_DIR/super-agent-debug" | awk '{print $1}')"
  if [ -n "${VITE_DEV_URL:-}" ]; then
    echo "▶ 前端热更新 → $VITE_DEV_URL"
  fi
  (sleep 1.0; open "http://localhost:4511" >/dev/null 2>&1 || true) &
  ulimit -n 1048576 2>/dev/null || ulimit -n 65535 2>/dev/null || true
  # 不用 exec：退出后需要跑 cleanup_vite_runonly 清理 vite 子进程
  "$BUILD_DIR/super-agent-debug" --debug "$@"
  AGENT_EXIT=$?
  cleanup_vite_runonly
  trap - EXIT INT TERM
  exit $AGENT_EXIT
fi

# 1) 前端
FRONT="$BUILD_DIR/cmd/agent-terminal/frontend"
VITE_DEV_PID=""

if [ -f "$FRONT/package.json" ]; then
  echo "[1/4] 前端处理..."
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

  # preflight: Vue template runtime-compile 守卫（根治 webview 黑屏）
  # 无论 debug 还是 release，只要 template 在 runtime compiler 里编不过，
  # webview 都会 mount.failed → 黑屏。在这里卡住，比事后翻日志快 100 倍。
  if [ -f "scripts/check-templates.cjs" ]; then
    echo "  → 预检 Vue template（runtime-compiler）..."
    if ! node scripts/check-templates.cjs; then
      echo ""
      echo "  ❌ Vue template 预检失败 —— 启动 webview 会直接黑屏"
      echo "  修复上面列出的 template 再重启；按 Enter 忽略继续（不推荐），Ctrl+C 中止"
      read -r
      echo "  → 已忽略 template 守卫"
    fi
  fi

  if [ "$MODE" = "debug" ]; then
    # ═══ debug 模式：启动 vite dev server（毫秒级热更新，不再 vite build）═══
    # 杀掉残留的 vite 和 esbuild 进程
    lsof -ti :5173 2>/dev/null | xargs kill -9 2>/dev/null || true
    pkill -f "esbuild --service" >/dev/null 2>&1 || true
    echo "  → 启动 vite dev server (端口 5173)..."
    npx vite --port 5173 --strictPort &
    VITE_DEV_PID=$!
    # 等待 vite dev server 就绪
    for i in $(seq 1 30); do
      if curl -s http://localhost:5173 >/dev/null 2>&1; then
        echo "  → vite dev server 已就绪 ✅ (PID: $VITE_DEV_PID)"
        break
      fi
      sleep 0.3
    done
    export VITE_DEV_URL="http://localhost:5173"
  else
    # ═══ release 模式：完整 vite build ═══
    _FRONT_HASH_FILE="$FRONT/.build-cache/frontend-src.hash"
    mkdir -p "$FRONT/.build-cache"
    _FRONT_CUR_HASH=$(find "$FRONT/src" "$FRONT/vite.config.js" "$FRONT/index.html" -type f 2>/dev/null | sort | xargs md5 -q 2>/dev/null | md5 -q 2>/dev/null || echo "nohash")
    _FRONT_SKIP_BUILD=0
    if [ -f "$_FRONT_HASH_FILE" ] && [ "$(cat "$_FRONT_HASH_FILE")" = "$_FRONT_CUR_HASH" ] && [ -d "$FRONT/dist" ]; then
      echo "  → 前端源码无变化，跳过 vite build ✅"
      _FRONT_SKIP_BUILD=1
    fi
    if [ "$_FRONT_SKIP_BUILD" = "0" ]; then
      if npm run build; then
        echo "$_FRONT_CUR_HASH" > "$_FRONT_HASH_FILE"
      else
        echo ""
        echo "⚠️  前端构建/守卫未通过！按 Enter 跳过继续编译，Ctrl+C 中止"
        read -r
        echo "  → 已跳过前端报错，继续..."
      fi
    fi
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

# ── 代码地图索引刷新 ──────────────────────────────────────
maybe_refresh_codemap "$BUILD_DIR"

echo "[3/4] 后端代码守卫检查..."

# 预编译守卫工具并缓存（源码未变则跳过重编译，避免每次 go run）
_GUARD_BIN="$BUILD_DIR/.build-cache/code-size-guard"
_GUARD_HASH_FILE="$BUILD_DIR/.build-cache/code-size-guard.srchash"
mkdir -p "$BUILD_DIR/.build-cache"
_GUARD_CUR_HASH=$(find "$BUILD_DIR/scripts/code_size_guard.go" "$BUILD_DIR/internal/archtest" -name '*.go' 2>/dev/null | sort | xargs md5 -q 2>/dev/null | md5 -q 2>/dev/null || echo "nohash")

rebuild_code_size_guard() {
  echo "  → 编译 code_size_guard..."
  go build -o "$_GUARD_BIN" "$BUILD_DIR/scripts/code_size_guard.go"
  echo "$_GUARD_CUR_HASH" > "$_GUARD_HASH_FILE"
}

if [ ! -f "$_GUARD_BIN" ] || [ ! -f "$_GUARD_HASH_FILE" ] || [ "$(cat "$_GUARD_HASH_FILE")" != "$_GUARD_CUR_HASH" ]; then
  rebuild_code_size_guard
else
  echo "  → code_size_guard 缓存命中，跳过编译"
fi

if "$_GUARD_BIN"; then
  _GUARD_STATUS=0
else
  _GUARD_STATUS=$?
fi
if [ "$_GUARD_STATUS" -eq 126 ] || [ "$_GUARD_STATUS" -eq 137 ]; then
  echo "  ⚠️  code_size_guard 缓存执行失败 (status=$_GUARD_STATUS)，删除缓存后重建重试..."
  rm -f "$_GUARD_BIN" "$_GUARD_HASH_FILE"
  rebuild_code_size_guard
  if "$_GUARD_BIN"; then
    _GUARD_STATUS=0
  else
    _GUARD_STATUS=$?
  fi
fi
if [ "$_GUARD_STATUS" -ne 0 ]; then
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
pkill -f "esbuild --service" >/dev/null 2>&1 || true
lsof -ti :4510 :4511 2>/dev/null | xargs kill -9 2>/dev/null || true
sleep 0.5

# 退出时清理 vite dev server
cleanup_vite() {
  if [ -n "$VITE_DEV_PID" ] && kill -0 "$VITE_DEV_PID" 2>/dev/null; then
    echo "  → 停止 vite dev server (PID: $VITE_DEV_PID)..."
    kill "$VITE_DEV_PID" 2>/dev/null || true
  fi
}
trap cleanup_vite EXIT INT TERM

# 启动
echo ""
echo "════════════════════════════════════"

# 提升文件描述符上限：批量 agent 场景下 codex app-server 需要大量 fd
# (每个 agent session 占用 WS 连接 + MCP server pipe，100 agents ≈ 300+ fd)
ulimit -n 1048576 2>/dev/null || ulimit -n 65535 2>/dev/null || true

# Memory subsystem env defaults. Override in your shell (export
# ENABLE_MEMORY_SYSTEM=0) if you really want memory off; otherwise the
# memory center UI would show the "system off" banner on every launch.
export ENABLE_MEMORY_SYSTEM="${ENABLE_MEMORY_SYSTEM:-1}"
export ENABLE_MEMORY_TOOLS="${ENABLE_MEMORY_TOOLS:-1}"
export MULTI_AGENT_MEMORY_FEATURE_TEAMMEM="${MULTI_AGENT_MEMORY_FEATURE_TEAMMEM:-1}"

# Codex legacy default-home opt-in. P21 P1a 把 codex identity 缺失改成硬
# 报错；如果前端 thread/start payload 没显式传 codexHome，后端
# injectDefaultCodexIdentityForStart 只在该 env 为 "1" 时回落 ~/.codex。
# 默认 opt-in 让 dev 启动 GUI 后能直接对话；想关闭走 P1a 严格模式时
# `export CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=0` 即可。
export CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME="${CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME:-1}"

if [ "$MODE" = "debug" ]; then
  if [ -n "$VITE_DEV_URL" ]; then
    echo "▶ 启动 debug 模式 (前端热更新 → $VITE_DEV_URL)..."
  else
    echo "▶ 启动 debug 模式..."
  fi
  (sleep 1.0; open "http://localhost:4511" >/dev/null 2>&1 || true) &
  "$BUILD_DIR/super-agent-debug" --debug "$@"
  AGENT_EXIT=$?
  cleanup_vite
  trap - EXIT INT TERM
  exit $AGENT_EXIT
else
  echo "▶ 启动正常模式..."
  exec "$BUILD_DIR/super-agent-debug" "$@"
fi
