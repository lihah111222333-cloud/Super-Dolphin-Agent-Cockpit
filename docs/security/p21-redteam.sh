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
# RT-2  provider-native context 身份必须 canonical 且 provider 白名单闭合
# 旧 candidate/approval 链已退役；start/resume 只接受结构化 provider 身份和安全 CWD。
# --------------------------------------------------------------------
ASSERT RT-2 "provider-native context 拒绝非法 provider/CWD 并锁定 canonical identity" \
  "go test ./internal/contract/... -run 'TestCanonicalProviderNativeProviderWhitelist|TestProviderNativeContextValidate$|TestProviderNativeContextCanonicalCWD' -count=1"

# --------------------------------------------------------------------
# RT-3  provider-native context 严格序列化，不携带旧 skill/memory 正文
# 结构化快照只允许 provider/cwd/home/profile/capability 元数据。
# --------------------------------------------------------------------
ASSERT RT-3 "provider-native context 严格 JSON 拒绝未知/损坏字段" \
  "go test ./internal/contract/... -run 'TestProviderNativeContextJSONRoundTrip|TestProviderNativeContextStrictJSON|TestProviderNativeContextJSONFailureDoesNotMutate' -count=1"

# --------------------------------------------------------------------
# RT-4  provider-native capability 必须显式探测，禁止乐观默认
# 旧 reviewer/approval contract 已退役；未知能力必须保持 unknown 并 fail-closed。
# --------------------------------------------------------------------
ASSERT RT-4a "provider-native capability 要求显式证据并拒绝隐式默认" \
  "go test ./internal/provider/shared/... -run 'TestBuildProviderNativeContextCanonicalizesCWDAndKeepsCapabilitiesExplicit|TestBuildProviderNativeContextRejectsImplicitCapabilities|TestValidateProviderNativeContextRequiresOnlyExplicitFeatures' -count=1"

# 该项保留为迁移记录，不再绑定已删除的 approval 包。
SKIP RT-4b "provider capability evidence 必须来自受信 probe（而非 caller 自报）" \
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
# RT-6  turn skill 输入仅保留 provider-native 元数据，旧 candidate 正文必须拒绝
# 正文由 provider-native discovery 负责，不得从 V3 turn payload 注入。
# --------------------------------------------------------------------
ASSERT RT-6 "turn skill 仅接受 provider-native metadata 并拒绝旧 candidate" \
  "go test ./internal/module/turn/... -run 'TestPrepareTurnKeepsExplicitSkillRefsMetadataOnly|TestPrepareTurnRejectsCandidateSkillsAsProviderNativeUnsupported|TestPrepareTurnRejectsEmptySelectedSkillMetadata|TestStartTurnAppliesProviderNativeSkillGate|TestStartTurnAllowsIdentityOnlyProviderNativeSkillMetadata' -count=1"

# --------------------------------------------------------------------
# RT-7  旧 skill tool surface 必须从 provider MCP 图移除并 fail-closed
# provider-native discovery 是唯一 skill 路径；旧 skill__* 名称不得转发到 MCP。
# --------------------------------------------------------------------
ASSERT RT-7 "旧 skill tool surface 不得进入 provider MCP" \
  "go test ./internal/platform/toolbridge/... -run 'TestPrepareCodexToolSurfaceRejectsLegacySkillSurface|TestCodexToolSurfaceLegacyNamesFailClosedWhenSurfaceMissing' -count=1"

# --------------------------------------------------------------------
# RT-8  已移除的旧 trajectory/skill 管线不得回归（架构红线）
# 旧 collector、extractor、evaluator 和 expanded state 已不再属于生产图；
# 这里锁定物理删除及 Fx 入口清理，避免只删测试或重新接回无界订阅。
# --------------------------------------------------------------------
ASSERT RT-8 "旧 trajectory/skill 管线物理残留已移除" \
  "test ! -e internal/module/turn/trajectory_collector.go \
   && test ! -e internal/module/turn/skill_extractor.go \
   && test ! -e internal/module/turn/skill_evaluator.go \
   && test ! -e internal/module/turn/expanded_state.go \
   && ! grep -Eq 'NewTrajectoryCollector|NewTrajectorySubscribers|NewDefaultExtractor|NewExtractorRunner' internal/module/turn/module.go"

# --------------------------------------------------------------------
# RT-9  webhook SSRF + redirect 重校验
# 文档：fix01.v2.md T-17 / P2
# --------------------------------------------------------------------
ASSERT RT-9 "webhook 拒绝私网/loopback、redirect 重跑 LookupIP、禁 env proxy" \
  "go test ./internal/platform/notify/... -run 'TestPostRejectsLoopbackAddress|TestRedirectTargetRejectsLoopbackBeforeDial|TestRedirectTargetRejectsIPv6ZoneID|TestWebhookTransportDisablesEnvProxy|TestCheckRedirectEnforcesHTTPSAgain' -count=1"

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
