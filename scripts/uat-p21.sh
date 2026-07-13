#!/usr/bin/env bash
# P21 用户验收测试（UAT）脚本
#
# 目标：把 P21 五块用户视角功能（自学习 skill / 多 codex 实例 / cron / 通知 / insights）
# 串成一组 RPC 调用，每一步都做 JSON-RPC 响应断言，最终按计数决定退出码。
#
# 用法：
#   HOST=http://127.0.0.1:7777 \
#   CWD_A=/tmp/p21-uat-repoA \
#   CWD_B=/tmp/p21-uat-repoB \
#   CODEX_HOME_QWEN=$HOME/.codex-qwen \
#   CODEX_HOME_GLM=$HOME/.codex-glm \
#   bash scripts/uat-p21.sh [feature]
#
#   feature ∈ {all, skill, codex, cron, notify, insights}
#
# 退出码：
#   0 = 全部 PASS（含 MANUAL 步骤）
#   1 = 至少一条 FAIL
#   2 = 用法错误（feature 名不识别）
#
# 通过判定：
#   * 每条断言要求 HTTP 200 + JSON-RPC 形式合法 + 期望字段命中。
#   * "故意失败"用例（缺字段、非法 id 等）期望 RPC 返回 error，断言反过来 must_error。
#   * notify 一节默认 MANUAL（不计 FAIL，但需要人工核对 webhook 收到 payload）。
#
# 依赖：bash + curl + jq（jq 必装，断言需要它）。

set -u
set -o pipefail

HOST="${HOST:-http://127.0.0.1:7777}"
RPC_PATH="${RPC_PATH:-/jrpc}"
CWD_A="${CWD_A:-/tmp/p21-uat-repoA}"
CWD_B="${CWD_B:-/tmp/p21-uat-repoB}"
CODEX_HOME_QWEN="${CODEX_HOME_QWEN:-$HOME/.codex-qwen}"
CODEX_HOME_GLM="${CODEX_HOME_GLM:-$HOME/.codex-glm}"

FEATURE="${1:-all}"

if ! command -v jq >/dev/null 2>&1; then
  echo "FATAL: jq is required. brew install jq / apt install jq"
  exit 2
fi

mkdir -p "$CWD_A" "$CWD_B"

PASS=0
FAIL=0
MANUAL=0
FAILED_STEPS=()

color() {
  local code="$1" text="$2"
  if [ -t 1 ]; then printf "\033[%sm%s\033[0m" "$code" "$text"; else printf "%s" "$text"; fi
}

# rpc_call <method> <params-json> -> echoes response body, sets RC=curl exit code
rpc_call() {
  local method="$1" params="$2"
  curl -sS -m 15 -X POST "$HOST$RPC_PATH" \
    -H 'Content-Type: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$method\",\"params\":$params}"
}

# expect_ok <step-id> <method> <params> [<jq filter that must yield non-null/non-empty>]
# PASS：HTTP 200 + 响应里有 result.* 命中 jq filter
expect_ok() {
  local id="$1" method="$2" params="$3" filter="${4:-.result}"
  printf "  [%s] %-44s ... " "$id" "$method"
  local resp rc
  resp=$(rpc_call "$method" "$params") || rc=$?
  if [ -z "${resp:-}" ]; then
    color 31 "FAIL"; printf "  (no response / network)\n"
    FAIL=$((FAIL+1)); FAILED_STEPS+=("$id $method: no response")
    return
  fi
  # 必须是合法 JSON
  if ! echo "$resp" | jq -e . >/dev/null 2>&1; then
    color 31 "FAIL"; printf "  (non-JSON response)\n    %s\n" "${resp:0:200}"
    FAIL=$((FAIL+1)); FAILED_STEPS+=("$id $method: non-JSON")
    return
  fi
  # 必须没有 .error
  if echo "$resp" | jq -e '.error' >/dev/null 2>&1; then
    local errmsg
    errmsg=$(echo "$resp" | jq -r '.error.message // .error')
    color 31 "FAIL"; printf "  (jrpc error: %s)\n" "$errmsg"
    FAIL=$((FAIL+1)); FAILED_STEPS+=("$id $method: $errmsg")
    return
  fi
  # 命中 result filter
  if ! echo "$resp" | jq -e "$filter" >/dev/null 2>&1; then
    color 31 "FAIL"; printf "  (filter %s 未命中)\n    %s\n" "$filter" "${resp:0:200}"
    FAIL=$((FAIL+1)); FAILED_STEPS+=("$id $method: filter $filter miss")
    return
  fi
  color 32 "PASS"; printf "\n"
  PASS=$((PASS+1))
}

# expect_error <step-id> <method> <params> [<message regex that error must contain, case-insensitive>]
# PASS：响应里必须有 .error，且（可选）message 命中 regex
expect_error() {
  local id="$1" method="$2" params="$3" want_re="${4:-}"
  printf "  [%s] %-44s ... " "$id" "$method (must error)"
  local resp
  resp=$(rpc_call "$method" "$params") || true
  if [ -z "${resp:-}" ]; then
    color 31 "FAIL"; printf "  (no response)\n"
    FAIL=$((FAIL+1)); FAILED_STEPS+=("$id $method: no response")
    return
  fi
  if ! echo "$resp" | jq -e '.error' >/dev/null 2>&1; then
    color 31 "FAIL"; printf "  (期望 error，但 RPC 成功)\n    %s\n" "${resp:0:200}"
    FAIL=$((FAIL+1)); FAILED_STEPS+=("$id $method: expected error, got success")
    return
  fi
  if [ -n "$want_re" ]; then
    local errmsg
    errmsg=$(echo "$resp" | jq -r '.error.message // .error | tostring')
    if ! echo "$errmsg" | grep -iE "$want_re" >/dev/null; then
      color 31 "FAIL"; printf "  (error msg %q 不含 /%s/i)\n" "$errmsg" "$want_re"
      FAIL=$((FAIL+1)); FAILED_STEPS+=("$id $method: bad error msg")
      return
    fi
  fi
  color 32 "PASS"; printf "\n"
  PASS=$((PASS+1))
}

manual() {
  local id="$1" desc="$2"
  printf "  [%s] " "$id"; color 33 "MANUAL"; printf "  %s\n" "$desc"
  MANUAL=$((MANUAL+1))
}

heading() {
  echo
  color 36 "── $* ──"
  echo
}

# ───────────────────────── F1 自学习 Skill ─────────────────────────
run_skill() {
  heading "F1 自学习 Skill (P0a + P0b)"
  expect_ok    F1.1 skills/candidate/list/pending "{\"cwd\":\"$CWD_A\"}"  '.result'
  expect_ok    F1.2 skills/local/listFiles       "{\"cwd\":\"$CWD_A\"}"  '.result'
  expect_ok    F1.3 skills/candidate/list/pending "{\"cwd\":\"$CWD_B\"}"  '.result'
  expect_error F1.4 skills/candidate/approve \
               '{"id":"00000000-0000-0000-0000-000000000000","cwd":"","approved_by":""}' \
               'cwd|approved_by|fingerprint|invalid'
}

# ───────────────────────── F2 多 Codex 实例 ─────────────────────────
run_codex() {
  heading "F2 多 Codex 实例 (P1a)"
  expect_ok F2.1 thread/start "{
    \"provider\":\"codex\",
    \"config\":{
      \"codexHome\":\"$CODEX_HOME_QWEN\",
      \"codexInstanceKey\":\"qwen-uat\",
      \"codexModelProvider\":\"qwen\"
    }
  }" '.result'
  expect_ok F2.2 thread/start "{
    \"provider\":\"codex\",
    \"config\":{
      \"codexHome\":\"$CODEX_HOME_GLM\",
      \"codexInstanceKey\":\"glm-uat\",
      \"codexModelProvider\":\"glm\"
    }
  }" '.result'
  expect_error F2.3 thread/start "{
    \"provider\":\"codex\",
    \"config\":{
      \"codexHome\":\"$CODEX_HOME_QWEN\",
      \"codexModelProvider\":\"qwen\"
    }
  }" 'instance|required|missing'
}

# ───────────────────────── F3 Cron ─────────────────────────
run_cron() {
  heading "F3 Cron 定时任务 (P1b)"
  expect_ok    F3.1 cronjob/list '{"limit":25,"cursor":""}' '.result | ((keys | sort) == ["has_more","jobs","next_cursor"]) and (.jobs | type == "array") and (.next_cursor | type == "string") and (.has_more | type == "boolean")'
  expect_ok    F3.2 cronjob/create "{
    \"name\":\"p21-uat-job\",
    \"schedule\":\"*/1 * * * *\",
    \"cwd\":\"$CWD_A\",
    \"config\":{
      \"codexHome\":\"$CODEX_HOME_QWEN\",
      \"codexInstanceKey\":\"qwen-uat\",
      \"codexModelProvider\":\"qwen\"
    },
    \"prompt\":\"P21 UAT smoke\",
    \"notify_channel\":\"team-uat\"
  }" '.result'
  expect_error F3.3 cronjob/create "{
    \"name\":\"p21-uat-job-bad\",
    \"schedule\":\"*/1 * * * *\",
    \"cwd\":\"\",
    \"prompt\":\"missing cwd\"
  }" 'cwd|missing'
  manual F3.4 "调 cronjob/listRuns 验证排程，job_id 用 F3.2 返回的 id"
}

# ───────────────────────── F4 通知 ─────────────────────────
run_notify() {
  heading "F4 多平台通知 (P2)"
  manual F4.1 "启动 mock： docker run --rm -p 18080:80 kennethreitz/httpbin"
  manual F4.2 "在 host 配置中加 channel：team-slack/team-ding/team-feishu，URL 全部指向 127.0.0.1:18080/post"
  manual F4.3 "把 F3.2 的 cron job notify_channel 改成 team-feishu，等下次 tick"
  manual F4.4 "验收：mock 收到带签名的 payload；host 日志中 access_token / SECRET 段被 [REDACTED:*] 替换"
  manual F4.5 "验收：alias 不存在 / redirect 到 127.0.0.1 私网时拒绝（参考 docs/security/p21-redteam.sh RT-9）"
}

# ───────────────────────── F5 Insights ─────────────────────────
run_insights() {
  heading "F5 Session Insights (P3)"
  expect_ok F5.1 dashboard/insights/list      '{"limit":5}' '.result'
  expect_ok F5.2 dashboard/insights/approvals '{"limit":5}' '.result'
  manual    F5.3 "拿真实 thread_id 调 dashboard/insights/list 验证字段：skills_selected / tokens / terminal status"
}

# ───────────────────────── 入口 ─────────────────────────
case "$FEATURE" in
  all)      run_skill; run_codex; run_cron; run_notify; run_insights ;;
  skill)    run_skill ;;
  codex)    run_codex ;;
  cron)     run_cron ;;
  notify)   run_notify ;;
  insights) run_insights ;;
  *)
    echo "Unknown feature: $FEATURE"
    echo "Valid: all skill codex cron notify insights"
    exit 2
    ;;
esac

echo
color 36 "=== P21 UAT Summary ==="; echo
printf "PASS=%d  FAIL=%d  MANUAL=%d\n" "$PASS" "$FAIL" "$MANUAL"

if [ "${#FAILED_STEPS[@]}" -gt 0 ]; then
  echo
  echo "Failed steps:"
  for s in "${FAILED_STEPS[@]}"; do echo "  - $s"; done
fi

if [ "$MANUAL" -gt 0 ]; then
  echo
  echo "Manual checklist 必须人工核对，否则不能算整轮通过。"
fi

if [ "$FAIL" -gt 0 ]; then
  echo
  color 31 "P21 UAT FAILED"; echo
  exit 1
fi

echo
color 32 "P21 UAT PASSED (auto). 别忘了核对 MANUAL 步骤。"; echo
exit 0
