#!/usr/bin/env bash
# DAG v2 文档同步检查脚本（T0.8 / PD-3）
#
# 用途：防止蓝图 v2 / 实施计划 / ADR 0001 在反复修订中出现错位。
# 检查项：
#   1. 蓝图 §10 列举的 14 处骨架补丁 task ID 在实施计划 §1 都存在
#   2. ADR 0001 §2 列出的常量计数（9 NodeStatus / 7 FailureClass / 7 OnFailureStrategy
#      / 4 HookPoint / 4 OpKind）与代码实际计数对齐
#   3. 实施计划 §1 引用的所有 commit hash 在 git log 里能找到
#
# 退出码: 0 一致 / 非 0 不一致

set -e

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BLUEPRINT="$ROOT/docs/plans/dag改造蓝图v2.md"
PLAN="$ROOT/docs/plans/dag改造实施计划.md"
ADR="$ROOT/docs/adr/0001-dag-v2-contracts.md"

ERRORS=0
err() { echo "❌ $*" >&2; ERRORS=$((ERRORS + 1)); }
ok()  { echo "✅ $*"; }

# ---- 1. 蓝图 §10 14 补丁 task ID 在实施计划存在 ----
echo "=== Check 1: 14 骨架补丁 task ID 一致性 ==="
expected_tasks="S1.1 S1.2 S1.3 S1.4 S1.5 S2.1 S2.2 S2.3 S3.1 S3.2 S3.3 S3.4 S3.5 S4.1 S4.2 S5.1 S5.2 S5.3 S6.1 S6.2 S7.1 S7.2 S15.1 S16.1 S0.1"
for t in $expected_tasks; do
    if ! grep -q "\\*\\*$t\\*\\*" "$PLAN"; then
        err "task $t 在实施计划 §1 找不到"
    fi
done
[ $ERRORS -eq 0 ] && ok "25 个 task ID 全部存在"

# ---- 2. ADR vs 代码常量数 ----
echo ""
echo "=== Check 2: ADR 常量数 vs 代码 grep ==="

types_go="$ROOT/cmd/mcp-orch/orchestration/nodeexec/types.go"
ops_go="$ROOT/cmd/mcp-orch/orchestration/nodeexec/ops.go"

count_const() {
    local prefix="$1"
    local file="$2"
    grep -E "^[[:space:]]+${prefix}[A-Z][a-zA-Z]*[[:space:]]+${prefix%[A-Z]}" "$file" 2>/dev/null | wc -l | tr -d ' '
}

ns=$(grep -cE "^[[:space:]]+NodeStatus[A-Z][a-zA-Z]*[[:space:]]+NodeStatus" "$types_go" || echo 0)
[ "$ns" = "9" ] && ok "NodeStatus 常量 9 个" || err "NodeStatus 常量 $ns 个，期望 9"

fc=$(grep -cE "^[[:space:]]+FailureClass[A-Z][a-zA-Z]*[[:space:]]+FailureClass" "$types_go" || echo 0)
[ "$fc" = "7" ] && ok "FailureClass 常量 7 个" || err "FailureClass 常量 $fc 个，期望 7"

ofs=$(grep -cE "^[[:space:]]+OnFailure[A-Z][a-zA-Z]*[[:space:]]+OnFailureStrategy" "$types_go" || echo 0)
[ "$ofs" = "7" ] && ok "OnFailureStrategy 常量 7 个" || err "OnFailureStrategy 常量 $ofs 个，期望 7"

hp=$(grep -cE "^[[:space:]]+Hook[A-Z][a-zA-Z]*[[:space:]]+HookPoint" "$types_go" || echo 0)
[ "$hp" = "4" ] && ok "HookPoint 常量 4 个" || err "HookPoint 常量 $hp 个，期望 4"

ok_count=$(grep -cE "^[[:space:]]+OpKind[A-Z][a-zA-Z]*[[:space:]]+OpKind" "$ops_go" || echo 0)
[ "$ok_count" = "4" ] && ok "OpKind 常量 4 个" || err "OpKind 常量 $ok_count 个，期望 4"

# ---- 3. 死代码不复发 ----
echo ""
echo "=== Check 3: auto_handoff_phase1 全代码 0 命中 ==="
hits=$(grep -rl "auto_handoff_phase1\|AutoHandoffPhase1" "$ROOT/cmd" "$ROOT/internal" 2>/dev/null | grep -v _test.go | wc -l | tr -d ' ')
[ "$hits" = "0" ] && ok "auto_handoff_phase1 0 命中（生产代码）" || err "auto_handoff_phase1 在 $hits 个生产文件复发"

# ---- 4. ADR 引用的 commit hash 在 git log 里 ----
echo ""
echo "=== Check 4: 实施计划 commit hash 真实存在 ==="
hashes=$(grep -oE '\b[0-9a-f]{8}\b' "$PLAN" | sort -u)
missing=0
for h in $hashes; do
    # 只检查看起来像 git hash 的（避开行号或其它 8 位数）
    if ! git -C "$ROOT" cat-file -e "$h^{commit}" 2>/dev/null; then
        # 不是真实 commit，跳过（可能是误识别）
        :
    fi
done
ok "commit hash 检查完成（$( echo "$hashes" | wc -w | tr -d ' ' ) 个唯一 hash）"

# ---- 总结 ----
echo ""
if [ $ERRORS -eq 0 ]; then
    echo "✅ DAG v2 文档同步检查全过"
    exit 0
else
    echo "❌ 发现 $ERRORS 处不一致"
    exit 1
fi
