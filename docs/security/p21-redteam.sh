#!/usr/bin/env bash
# P21 Red Team Assertions
# 来源：docs/plans/迁移/p21/fix01.v2.md §五
# 用法：bash docs/security/p21-redteam.sh
# 退出码：任一断言失败立即非 0；全部 PASS 输出 "ALL P21 RED-TEAM ASSERTIONS PASSED"
#
# 维护原则：
#   1. 每条 ASSERT 必须指向一个真实可跑的 Go 测试或 grep 断言。
#   2. 还未实现的项用 SKIP() 显式标记 + TODO 引用 fix01.v2.md 任务号；
#      SKIP 不算失败，但会在汇总段计数，便于追踪。
#   3. 新增 critical 项请同时在 fix01.v2.md §五登记。

set -u
set -o pipefail

cd "$(git rev-parse --show-toplevel)"

PASS=0
FAIL=0
SKIP_COUNT=0
FAILED_NAMES=()
SKIPPED_NAMES=()

color() {
  local code="$1" text="$2"
  if [ -t 1 ]; then printf "\033[%sm%s\033[0m" "$code" "$text"; else printf "%s" "$text"; fi
}

ASSERT() {
  local id="$1" desc="$2" cmd="$3"
  printf "[%s] %s ... " "$id" "$desc"
  if eval "$cmd" >/tmp/p21-redteam-"$id".log 2>&1; then
    color 32 "PASS"; printf "\n"
    PASS=$((PASS+1))
  else
    color 31 "FAIL"; printf "\n"
    FAIL=$((FAIL+1))
    FAILED_NAMES+=("$id: $desc")
    sed 's/^/    /' /tmp/p21-redteam-"$id".log | tail -40
  fi
}

SKIP() {
  local id="$1" desc="$2" todo="$3"
  printf "[%s] %s ... " "$id" "$desc"
  color 33 "SKIP"; printf "  (TODO: %s)\n" "$todo"
  SKIP_COUNT=$((SKIP_COUNT+1))
  SKIPPED_NAMES+=("$id: $desc -> $todo")
}

echo "=== P21 Red Team Assertions ==="
echo

# --------------------------------------------------------------------
# RT-1  fingerprint 跨实现一致 + 128-bit
# 文档：fix01.v2.md T-06 / harden-followups.md F1
# 现状：已统一到 internal/util/repofingerprint
# --------------------------------------------------------------------
ASSERT RT-1a "RepoFingerprint 128-bit 稳定性" \
  "go test ./internal/util/repofingerprint/... -run 'TestCompute' -count=1"

ASSERT RT-1b "全仓只剩一份 RepoFingerprint 实现（其余必须是 delegator）" \
  "go run ./docs/security/internal/check_repofp_delegators.go"

# --------------------------------------------------------------------
# RT-2  审批 TOCTOU：caller cwd 必须与 candidate fingerprint 一致
# 文档：fix01.v2.md T-01 / harden-followups.md F2
# --------------------------------------------------------------------
ASSERT RT-2 "审批拒绝 caller cwd ≠ candidate repo fingerprint" \
  "go test ./internal/module/skill/... -run 'TestReview_RejectsRepoFingerprintMismatch|TestReview_RequiresCallerRepoFingerprint|TestReview_ProjectScopeRequiresRepoFingerprint' -count=1"

# --------------------------------------------------------------------
# RT-3  List/Lookup 必须按 repo_fingerprint 隔离
# 文档：fix01.v2.md T-02 / harden-followups.md F3
# --------------------------------------------------------------------
ASSERT RT-3 "Lookup/List 按 repo_fingerprint 隔离" \
  "go test ./internal/store/skillcandidate/... -run 'TestStore_LookupApproval_DistinctRepoFingerprintIsolation|TestStore_LookupApproval_ProjectRejectsInvalidFingerprint|TestStore_ListPending_RejectsInvalidFingerprint' -count=1 \
   && go test ./internal/module/skill/... -run 'TestReview_LookupApprovalScopedByFingerprint' -count=1"

# --------------------------------------------------------------------
# RT-4  Reviewer 身份不能由 caller 自报
# 文档：fix01.v2.md T-03 / harden-followups.md F4
# 现状：approval 必须非空已覆盖；caller-supplied 替换为 authn 主体仍待落地
# --------------------------------------------------------------------
ASSERT RT-4a "approve 拒绝空 approved_by" \
  "go test ./internal/module/skill/... -run 'TestReview_RequiresApprovedBy' -count=1 \
   && go test ./internal/store/skillcandidate/... -run 'TestStore_Approve_RequiresApprovedBy' -count=1"

SKIP RT-4b "approve 必须忽略 caller-supplied approved_by 字段（由 authn 主体注入）" \
  "fix01.v2.md T-03（reviewer authn middleware）"

# --------------------------------------------------------------------
# RT-5  脱敏覆盖 ≥15 类高价值密钥族
# 文档：fix01.v2.md T-05 / harden-followups.md F5+F6
# 现状：DefaultRedactor 已覆盖 ~20 类
# --------------------------------------------------------------------
ASSERT RT-5a "Redactor 全模式断言" \
  "go test ./internal/module/turn/... -run 'TestRedactor_AllPatterns' -count=1"

ASSERT RT-5b "脱敏夹具语料全部命中" \
  "test -f test/fixtures/p21/secrets/sample.txt \
   && go run ./test/fixtures/p21/secrets/check_redaction.go test/fixtures/p21/secrets/sample.txt"

SKIP RT-5c "错误链路 + 日志输出统一过 Redactor" \
  "fix01.v2.md T-05.1（log/error redaction middleware）"

# --------------------------------------------------------------------
# RT-6  signed skill 不得跳过审批/脱敏
# 文档：p21/README.md §收口口径 / harden-followups.md F14
# --------------------------------------------------------------------
ASSERT RT-6 "trust=signed 仍走完整审批流程（P22 前一律按未验签处理）" \
  "go test ./internal/module/skill/... -run 'TestReview_SignedSkillStillRequiresApproval' -count=1"

# --------------------------------------------------------------------
# RT-7  RedactedSample 不得在跨 repo List 出参中暴露
# 文档：fix01.v2.md T-04 / harden-followups.md F14
# --------------------------------------------------------------------
ASSERT RT-7 "List 出参剔除 RedactedSample" \
  "go test ./internal/module/skill/... -run 'TestReview_ListPendingExcludesRedactedSample' -count=1"

# --------------------------------------------------------------------
# RT-8  collector 只读 observation（架构红线）
# 文档：fix01.v2.md T-07b / harden-followups.md F8
# --------------------------------------------------------------------
ASSERT RT-8 "archtest 锁定 trajectory_collector 只读 observation" \
  "go test ./internal/archtest/... -run 'TestTrajectoryCollectorDoesNotCallObservationWriter' -count=1"

# --------------------------------------------------------------------
# RT-9  webhook SSRF + redirect 重校验
# 文档：fix01.v2.md T-17 / P2
# --------------------------------------------------------------------
ASSERT RT-9 "webhook 拒绝私网/loopback、redirect 重跑 LookupIP、禁 env proxy" \
  "go test ./internal/module/notify/platform/... -run 'TestPostRejectsLoopbackAddress|TestRedirectTargetRejectsLoopbackBeforeDial|TestRedirectTargetRejectsIPv6ZoneID|TestWebhookTransportDisablesEnvProxy|TestCheckRedirectEnforcesHTTPSAgain' -count=1"

# --------------------------------------------------------------------
# 汇总
# --------------------------------------------------------------------
echo
echo "=== P21 Red Team Summary ==="
printf "PASS=%d  FAIL=%d  SKIP=%d\n" "$PASS" "$FAIL" "$SKIP_COUNT"

if [ "${#FAILED_NAMES[@]}" -gt 0 ]; then
  echo
  echo "Failed assertions:"
  for n in "${FAILED_NAMES[@]}"; do echo "  - $n"; done
fi

if [ "${#SKIPPED_NAMES[@]}" -gt 0 ]; then
  echo
  echo "Skipped (待落地):"
  for n in "${SKIPPED_NAMES[@]}"; do echo "  - $n"; done
fi

# 灰度准入：FAIL=0 即可；SKIP 由 fix01.v2.md 任务卡跟踪。
if [ "$FAIL" -gt 0 ]; then
  echo
  color 31 "RED TEAM FAILED"; echo
  exit 1
fi

echo
color 32 "ALL P21 RED-TEAM ASSERTIONS PASSED"; echo
exit 0
