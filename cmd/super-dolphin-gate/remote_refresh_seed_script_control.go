package main

// remoteBaselineSeedScriptGateHelpers 验证重放层中的 gate 二进制及候选身份。
const remoteBaselineSeedScriptGateHelpers = `
previous_gate_source_sha256=
previous_gate_platform=
previous_gate_toolchain_digest=
load_verified_gate() {
  manifest=$1
  binary=$2
  test -f "$manifest"
  test -x "$binary"
  candidate_gate_source=$(sed -n 's/.*"gate_source_sha256":"\([^"]*\)".*/\1/p' "$manifest")
  candidate_gate_digest=$(sed -n 's/.*"gate_binary_sha256":"\([^"]*\)".*/\1/p' "$manifest")
  candidate_gate_size=$(sed -n 's/.*"gate_binary_size":\([0-9][0-9]*\).*/\1/p' "$manifest")
  candidate_gate_platform=$(sed -n 's/.*"platform":"\([^"]*\)".*/\1/p' "$manifest")
  candidate_gate_toolchain=$(sed -n 's/.*"toolchain_digest":"\([^"]*\)".*/\1/p' "$manifest")
  test -n "$candidate_gate_digest"
  test -n "$candidate_gate_size"
  test -n "$candidate_gate_platform"
  test -n "$candidate_gate_toolchain"
  test "$(digest_file "$binary")" = "$candidate_gate_digest"
  test "$(stat -c '%s' "$binary")" = "$candidate_gate_size"
  install -d -m 0755 "$payload_root/bin"
  cp "$binary" "$payload_root/bin/super-dolphin-gate"
  chmod 0755 "$payload_root/bin/super-dolphin-gate"
  previous_gate_source_sha256=$candidate_gate_source
  previous_gate_platform=$candidate_gate_platform
  previous_gate_toolchain_digest=$candidate_gate_toolchain
}

verify_gate_cli_identity() (
  binary=$1
  if ! grep -Fq 'case "cli-identity":' "$source_root/cmd/super-dolphin-gate/main.go"; then
    "$binary" plan local-fast >/dev/null
    printf 'gate CLI identity mode: source-bound legacy probe\n'
    exit 0
  fi
  identity=$("$binary" worker cli-identity)
  expected=$(printf 'gate_source_sha256=%s\nplatform=%s\ntoolchain_digest=%s' \
    "$BASELINE_GATE_SOURCE_SHA256" "$BASELINE_PLATFORM" "$BASELINE_TOOLCHAIN_DIGEST")
  test "$identity" = "$expected"
)
`
