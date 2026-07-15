#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
smoke_date="$(date +%Y-%m-%d)"
log_dir="${SMOKE_LOG_DIR:-$root/docs/reviews/smoke-logs/$smoke_date}"
app_path="${APP_PATH:-$root/dist/package/macos/Super Dolphin.app}"
dmg_path="${DMG_PATH:-$root/dist/package/macos/Super Dolphin.dmg}"
latest_json_path="${LATEST_JSON_PATH:-$root/dist/package/macos/latest.json}"
dmg_sha256_path="${DMG_SHA256_PATH:-$dmg_path.sha256}"
mode="${1:-local}"

usage() {
  cat >&2 <<'USAGE'
usage: docs/scripts/macos_release_smoke.sh <local|startup|blockers|notarized-dmg|relay-turn|manifest|update-loop>

Modes:
  local          Verify the locally built macOS app/DMG structure, bundled relay env,
                 mounted DMG contents, and bundled Codex help with external Codex paths hidden.
  startup        Launch the packaged app binary with a temporary HOME and external Codex
                 paths hidden; observe that it stays alive for STARTUP_WINDOW_SECONDS.
  blockers       Fail-fast preflight for release-only blockers that cannot be inferred
                 from local package structure: clean offline VM, production relay, GUI turn,
                 and real update-loop installation.
  notarized-dmg  Validate that the DMG is notarized/stapled, then mount and verify it.
  relay-turn     Run a non-GUI Codex CLI turn against the configured relay using the bundled Codex.
  manifest       Verify gray update manifest inputs and that latest.json matches a freshly
                 generated manifest for the local DMG. Does not publish or install updates.
  update-loop    Run manifest smoke, then require explicit evidence that the real app update
                 install loop was executed outside this script.

All logs are written under docs/reviews/smoke-logs/2026-05-28 by default.
USAGE
  exit 2
}

timestamp() {
  date -u +%Y-%m-%dT%H:%M:%SZ
}

log_run() {
  local log="$1"
  shift
  mkdir -p "$log_dir"
  set +e
  (
    set -euo pipefail
    echo "command: docs/scripts/macos_release_smoke.sh $mode"
    echo "started_at: $(timestamp)"
    "$@"
    echo "exit_code: 0"
    echo "finished_at: $(timestamp)"
  ) >"$log" 2>&1
  local status=$?
  if [[ "$status" != "0" ]]; then
    {
      echo "exit_code: $status"
      echo "finished_at: $(timestamp)"
    } >>"$log"
    tail -n 80 "$log" >&2
  fi
  set -e
  echo "$log"
  return "$status"
}

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

blocker() {
  echo "BLOCKER: $*" >&2
  exit 1
}

record_blocker() {
  echo "BLOCKER: $*" >&2
  blocker_count=$((blocker_count + 1))
}

require_darwin() {
  local os_name
  os_name="$(uname -s)"
  if [[ "$os_name" != "Darwin" ]]; then
    fail "macOS smoke must run on Darwin; current uname=$os_name"
  fi
}

require_file() {
  local path="$1"
  if [[ ! -f "$path" ]]; then
    fail "missing file: $path"
  fi
}

require_dir() {
  local path="$1"
  if [[ ! -d "$path" ]]; then
    fail "missing directory: $path"
  fi
}

require_exec() {
  local path="$1"
  if [[ ! -x "$path" ]]; then
    fail "missing executable: $path"
  fi
}

require_env_blocker() {
  local name="$1"
  local value="${!name:-}"
  if [[ ! "$value" =~ [^[:space:]] ]]; then
    blocker "$name is required for macOS gray update manifest smoke"
  fi
  if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
    blocker "$name must not contain newline characters"
  fi
}

redacted_env_state() {
  local key="$1"
  if [[ -n "${!key:-}" ]]; then
    echo "$key=<set>"
  else
    echo "$key=<unset>"
  fi
}

resource_dir() {
  printf '%s\n' "$app_path/Contents/Resources"
}

sha256_file() {
  local path="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
    return
  fi
  fail "missing SHA-256 tool; install shasum or sha256sum"
}

verify_sha256_hex() {
  local label="$1"
  local value="$2"
  if [[ ! "$value" =~ ^[[:xdigit:]]{64}$ ]]; then
    fail "$label must be a 64-character hex SHA-256"
  fi
}

verify_dmg_checksum() {
  local dmg="$1"
  local checksum_file="${2:-$dmg.sha256}"
  require_file "$dmg"
  require_file "$checksum_file"
  local expected actual
  expected="$(awk 'NF { print $1; exit }' "$checksum_file" | tr 'A-F' 'a-f')"
  verify_sha256_hex "$checksum_file" "$expected"
  actual="$(sha256_file "$dmg")"
  if [[ "$actual" != "$expected" ]]; then
    fail "DMG sha256 mismatch for $dmg: expected $expected, actual $actual"
  fi
  echo "DMG sha256 verified: $checksum_file"
}

base64_decode_to_file() {
  local value="$1"
  local dest="$2"
  if printf '%s' "$value" | base64 --decode >"$dest" 2>/dev/null; then
    return
  fi
  if printf '%s' "$value" | base64 -D >"$dest" 2>/dev/null; then
    return
  fi
  blocker "SUPER_DOLPHIN_UPDATE_PUBLIC_KEY must be valid base64"
}

validate_update_public_key() {
  local decoded_key byte_count
  decoded_key="$(mktemp)"
  base64_decode_to_file "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" "$decoded_key"
  byte_count="$(wc -c <"$decoded_key" | tr -d '[:space:]')"
  rm -f "$decoded_key"
  if [[ "$byte_count" != "32" ]]; then
    blocker "decoded SUPER_DOLPHIN_UPDATE_PUBLIC_KEY must be 32 bytes"
  fi
}

validate_https_url_env() {
  local name="$1"
  local value="${!name:-}"
  if [[ ! "$value" =~ ^https://[^/?#]+($|[/?#]) ]]; then
    blocker "$name must be an HTTPS URL with a host"
  fi
}

require_update_manifest_env() {
  require_env_blocker SUPER_DOLPHIN_UPDATE_MANIFEST_URL
  require_env_blocker SUPER_DOLPHIN_UPDATE_PUBLIC_KEY
  require_env_blocker SUPER_DOLPHIN_UPDATE_SIGNING_KEY
  require_env_blocker SUPER_DOLPHIN_UPDATE_ARTIFACT_URL
  require_env_blocker VERSION
  require_env_blocker SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION
  validate_https_url_env SUPER_DOLPHIN_UPDATE_MANIFEST_URL
  validate_https_url_env SUPER_DOLPHIN_UPDATE_ARTIFACT_URL
  validate_update_public_key
  if [[ ! -f "$SUPER_DOLPHIN_UPDATE_SIGNING_KEY" ]]; then
    blocker "SUPER_DOLPHIN_UPDATE_SIGNING_KEY must point to an existing Ed25519 private key file"
  fi
}

verify_packaged_relay_env() {
  local env_file="$1/.env"
  require_file "$env_file"
  local mode_bits
  mode_bits="$(stat -f %Lp "$env_file" 2>/dev/null || stat -c %a "$env_file")"
  if [[ "$mode_bits" != "600" ]]; then
    fail "packaged relay .env must be mode 600; got $mode_bits at $env_file"
  fi
  if ! grep -q '^SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=' "$env_file"; then
    fail "packaged relay .env missing SUPER_DOLPHIN_CODEX_RELAY_BASE_URL"
  fi
  if ! grep -q '^SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=' "$env_file"; then
    fail "packaged relay .env missing SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"
  fi
  if grep -Eq '^SUPER_DOLPHIN_CODEX_RELAY_(BASE_URL|BOOTSTRAP_TOKEN)=$' "$env_file"; then
    fail "packaged relay .env contains an empty required value"
  fi
  echo "packaged relay .env keys present with values redacted: $env_file"
}

verify_packaged_update_env() {
  local env_file="$1/.env"
  require_file "$env_file"
  for expected_line in \
    "SUPER_DOLPHIN_UPDATE_ENABLED=1" \
    "SUPER_DOLPHIN_UPDATE_MANIFEST_URL=$SUPER_DOLPHIN_UPDATE_MANIFEST_URL" \
    "SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" \
    "SUPER_DOLPHIN_UPDATE_CHANNEL=${SUPER_DOLPHIN_UPDATE_CHANNEL:-gray}" \
    "SUPER_DOLPHIN_UPDATE_VERSION=$VERSION"; do
    if ! grep -Fqx "$expected_line" "$env_file"; then
      fail "packaged update .env missing expected line: ${expected_line%%=*}=<redacted>"
    fi
  done
  echo "packaged update .env keys match release manifest env with values redacted: $env_file"
}

verify_runtime_manifest_contract() {
  local resources="$1"
  local manifest="$resources/runtime-manifest.json"
  require_file "$manifest"
  grep -q '"bundled_codex_path"' "$manifest" || fail "runtime manifest missing bundled_codex_path"
  grep -q '"model_registry_path"' "$manifest" || fail "runtime manifest missing model_registry_path"
  require_exec "$resources/bin/codex"
  require_file "$resources/models.yaml"
  echo "runtime manifest contract present: $manifest"
}

verify_bundled_codex_help_without_external_path() {
  local resources="$1"
  local tmp_home status
  tmp_home="$(mktemp -d)"
  set +e
  env -i \
    HOME="$tmp_home" \
    CODEX_HOME="$tmp_home" \
    PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
    TMPDIR="${TMPDIR:-/tmp}" \
    "$resources/bin/codex" app-server --help >/dev/null
  status=$?
  set -e
  rm -rf "$tmp_home"
  if [[ "$status" != "0" ]]; then
    fail "bundled Codex app-server --help failed with external Codex paths hidden"
  fi
  echo "bundled Codex app-server --help passed with /opt/homebrew/bin and /usr/local/bin hidden from PATH"
}

verify_dmg_mount_contents() {
  local dmg="$1"
  local check_update_env="${2:-0}"
  require_file "$dmg"
  local mount_dir attached
  mount_dir="$(mktemp -d)"
  attached=0
  cleanup_dmg_mount() {
    if [[ "$attached" == "1" ]]; then
      hdiutil detach "$mount_dir" >/dev/null 2>&1 || true
    fi
    rm -rf "$mount_dir"
  }
  trap cleanup_dmg_mount EXIT
  hdiutil attach "$dmg" -nobrowse -readonly -mountpoint "$mount_dir" >/dev/null
  attached=1
  require_dir "$mount_dir/Super Dolphin.app/Contents"
  "$root/scripts/verify_packaged_app_macos.sh" "$mount_dir/Super Dolphin.app"
  if [[ "$check_update_env" == "1" ]]; then
    verify_packaged_update_env "$mount_dir/Super Dolphin.app/Contents/Resources"
  fi
  hdiutil detach "$mount_dir" >/dev/null
  attached=0
  rm -rf "$mount_dir"
  trap - EXIT
  echo "DMG mounted and packaged app verified: $dmg"
}

local_smoke() {
  require_darwin
  require_dir "$app_path/Contents"
  require_file "$dmg_path"
  echo "app_path: $app_path"
  echo "dmg_path: $dmg_path"
  echo "go_platform: $(go env GOOS)-$(go env GOARCH)"
  "$root/scripts/verify_packaged_app_macos.sh" "$app_path"
  local resources
  resources="$(resource_dir)"
  verify_packaged_relay_env "$resources"
  verify_runtime_manifest_contract "$resources"
  verify_bundled_codex_help_without_external_path "$resources"
  verify_dmg_mount_contents "$dmg_path"
}

is_running_in_vm() {
  [[ "$(sysctl -n kern.hv_vmm_present 2>/dev/null || echo 0)" == "1" ]]
}

has_default_route() {
  route -n get default >/dev/null 2>&1
}

blocker_preflight() {
  require_darwin
  local blocker_count=0
  echo "clean_vm_marker: $(redacted_env_state SUPER_DOLPHIN_CLEAN_VM_SMOKE)"
  echo "production_relay_marker: $(redacted_env_state SUPER_DOLPHIN_PRODUCTION_RELAY_SMOKE)"
  echo "gui_turn_marker: $(redacted_env_state SUPER_DOLPHIN_GUI_CODEX_TURN_SMOKE)"
  echo "relay_base_url: $(redacted_env_state SUPER_DOLPHIN_CODEX_RELAY_BASE_URL)"
  echo "relay_bootstrap_token: $(redacted_env_state SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN)"
  echo "update_loop_marker: $(redacted_env_state SUPER_DOLPHIN_UPDATE_LOOP_SMOKE)"

  if [[ "${SUPER_DOLPHIN_CLEAN_VM_SMOKE:-}" != "1" ]]; then
    record_blocker "clean VM acceptance not executed: set SUPER_DOLPHIN_CLEAN_VM_SMOKE=1 only inside the target clean macOS VM after installing the app"
  fi
  is_running_in_vm || record_blocker "clean VM acceptance requires a macOS VM; sysctl kern.hv_vmm_present is not 1"
  has_default_route && record_blocker "offline acceptance requires no default network route; route -n get default succeeded"
  [[ ! -d "$HOME/.codex" ]] || record_blocker "clean VM acceptance requires no preexisting ~/.codex directory"
  [[ ! -d "$HOME/Library/Application Support/Super Dolphin" ]] || record_blocker "clean VM acceptance requires no preexisting Super Dolphin app data directory"
  [[ -d "/Applications/Super Dolphin.app/Contents" ]] || record_blocker "clean VM acceptance requires /Applications/Super Dolphin.app installed from the DMG"

  if [[ "${SUPER_DOLPHIN_PRODUCTION_RELAY_SMOKE:-}" != "1" ]]; then
    record_blocker "production relay turn not executed: set SUPER_DOLPHIN_PRODUCTION_RELAY_SMOKE=1 only for a real production relay run"
  fi
  local base_url="${SUPER_DOLPHIN_CODEX_RELAY_BASE_URL:-}"
  local bootstrap_token="${SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN:-}"
  if [[ -z "$base_url" ]]; then
    record_blocker "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL is required for production relay smoke"
  fi
  if [[ -z "$bootstrap_token" ]]; then
    record_blocker "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN is required for production relay smoke"
  fi
  case "$base_url" in
    http://127.*|http://localhost*|https://relay.example.*|http://relay.example.*)
      record_blocker "relay base URL is a local/example value, not production: $base_url"
      ;;
  esac

  if [[ "${SUPER_DOLPHIN_GUI_CODEX_TURN_SMOKE:-}" != "1" ]]; then
    record_blocker "complete GUI Codex turn was not executed: set SUPER_DOLPHIN_GUI_CODEX_TURN_SMOKE=1 only after recording GUI send/response evidence"
  fi
  if [[ "${SUPER_DOLPHIN_UPDATE_LOOP_SMOKE:-}" != "1" ]]; then
    record_blocker "real app update install loop was not executed: set SUPER_DOLPHIN_UPDATE_LOOP_SMOKE=1 only after recording update download, verify, install, and relaunch evidence"
  fi
  if ((blocker_count > 0)); then
    exit 1
  fi
}

require_production_relay_env() {
  local base_url="${SUPER_DOLPHIN_CODEX_RELAY_BASE_URL:-}"
  local bootstrap_token="${SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN:-}"
  if [[ -z "$base_url" ]]; then
    blocker "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL is required for production relay smoke"
  fi
  if [[ -z "$bootstrap_token" ]]; then
    blocker "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN is required for production relay smoke"
  fi
  case "$base_url" in
    http://127.*|http://localhost*|https://relay.example.*|http://relay.example.*)
      blocker "relay base URL is a local/example value, not production: $base_url"
      ;;
  esac
}

notarized_dmg_smoke() {
  require_darwin
  require_file "$dmg_path"
  echo "notary_profile: $(redacted_env_state NOTARY_PROFILE)"
  xcrun stapler validate "$dmg_path"
  spctl -a -vv -t open "$dmg_path"
  verify_dmg_mount_contents "$dmg_path"
}

startup_smoke() {
  require_darwin
  local binary="$app_path/Contents/MacOS/agent-terminal"
  require_exec "$binary"
  local tmp_home tmp_dir window pid stat status
  tmp_home="$(mktemp -d)"
  tmp_dir="$(mktemp -d)"
  window="${STARTUP_WINDOW_SECONDS:-20}"
  echo "startup command: env -i HOME=<temp> PATH=/usr/bin:/bin:/usr/sbin:/sbin TMPDIR=<temp> SUPER_DOLPHIN_CODEX_INSTALL_ROOT=<temp> SUPER_DOLPHIN_CODEX_RELEASE_API_URL=http://127.0.0.1:9/latest LOG_LEVEL=debug $binary"
  echo "external_codex_path_hidden: /opt/homebrew/bin and /usr/local/bin omitted from initial PATH"
  set +e
  env -i \
    HOME="$tmp_home" \
    PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
    TMPDIR="$tmp_dir" \
    SUPER_DOLPHIN_CODEX_INSTALL_ROOT="$tmp_dir/codex-install" \
    SUPER_DOLPHIN_CODEX_RELEASE_API_URL="http://127.0.0.1:9/latest" \
    LOG_LEVEL="debug" \
    "$binary" &
  pid=$!
  set -e
  sleep "$window"
  stat="$(ps -p "$pid" -o stat= 2>/dev/null || true)"
  if [[ -z "$stat" || "$stat" == *Z* ]]; then
    set +e
    wait "$pid"
    status=$?
    set -e
    rm -rf "$tmp_home" "$tmp_dir"
    fail "packaged app exited before startup window; exit_code=$status stat=${stat:-missing}"
  fi
  echo "startup_window_observed_seconds: $window"
  kill -TERM "$pid" 2>/dev/null || true
  sleep 5
  kill -KILL "$pid" 2>/dev/null || true
  set +e
  wait "$pid"
  status=$?
  set -e
  echo "app_exit_after_termination: $status"
  rm -rf "$tmp_home" "$tmp_dir"
}

relay_turn_smoke() {
  require_darwin
  require_production_relay_env
  local resources
  resources="$(resource_dir)"
  require_exec "$resources/bin/codex"
  local tmp_home status
  tmp_home="$(mktemp -d)"
  cat >"$tmp_home/config.toml" <<EOF
model_provider = "super-dolphin-relay"

[model_providers.super-dolphin-relay]
name = "Super Dolphin Relay"
base_url = "${SUPER_DOLPHIN_CODEX_RELAY_BASE_URL}"
env_key = "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"
wire_api = "responses"
EOF
  chmod 600 "$tmp_home/config.toml"
  echo "production relay turn command uses bundled Codex; API key redacted"
  set +e
  env \
    CODEX_HOME="$tmp_home" \
    PATH="$resources/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
    SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="$SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN" \
    "$resources/bin/codex" exec --json --skip-git-repo-check "Say hello from packaged Super Dolphin release smoke."
  status=$?
  set -e
  rm -rf "$tmp_home"
  return "$status"
}

manifest_smoke() {
  require_darwin
  require_update_manifest_env
  require_file "$latest_json_path"
  echo "dmg_path: $dmg_path"
  echo "dmg_sha256_path: $dmg_sha256_path"
  echo "latest_json_path: $latest_json_path"
  echo "update_manifest_url: ${SUPER_DOLPHIN_UPDATE_MANIFEST_URL}"
  echo "update_artifact_url: ${SUPER_DOLPHIN_UPDATE_ARTIFACT_URL}"
  echo "update_channel: ${SUPER_DOLPHIN_UPDATE_CHANNEL:-gray}"
  echo "update_version: $VERSION"
  verify_dmg_checksum "$dmg_path" "$dmg_sha256_path"
  verify_dmg_mount_contents "$dmg_path" 1

  local tmp_dir generated_manifest
  tmp_dir="$(mktemp -d)"
  generated_manifest="$tmp_dir/latest.json"
  (
    cd "$root"
    go run ./cmd/super-dolphin-release-manifest \
      -artifact "$dmg_path" \
      -artifact-url "$SUPER_DOLPHIN_UPDATE_ARTIFACT_URL" \
      -app-id "${SUPER_DOLPHIN_UPDATE_APP_ID:-super-dolphin}" \
      -channel "${SUPER_DOLPHIN_UPDATE_CHANNEL:-gray}" \
      -version "$VERSION" \
      -minimum-version "$SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION" \
      -platform "${SUPER_DOLPHIN_UPDATE_PLATFORM:-$(go env GOOS)-$(go env GOARCH)}" \
      -signing-key "$SUPER_DOLPHIN_UPDATE_SIGNING_KEY" \
      -out "$generated_manifest"
  )
  if ! cmp -s "$generated_manifest" "$latest_json_path"; then
    echo "generated_manifest: $generated_manifest" >&2
    echo "latest_json_path: $latest_json_path" >&2
    fail "latest.json does not match fresh output from cmd/super-dolphin-release-manifest"
  fi
  rm -rf "$tmp_dir"
  echo "update manifest smoke verified latest.json against local DMG; does not publish, install, or execute a real update loop"
}

update_loop_smoke() {
  manifest_smoke
  echo "update-loop smoke does not install or execute a real update loop"
  if [[ "${SUPER_DOLPHIN_UPDATE_LOOP_SMOKE:-}" != "1" ]]; then
    blocker "real app update install loop not executed: set SUPER_DOLPHIN_UPDATE_LOOP_SMOKE=1 only after recording update download, verify, install, and relaunch evidence"
  fi
  echo "manual update-loop evidence marker present; this script did not perform the install"
}

case "$mode" in
  local)
    log_run "$log_dir/macos-release-local-smoke-sixth.log" local_smoke
    ;;
  startup)
    log_run "$log_dir/macos-packaged-app-startup-sixth.log" startup_smoke
    ;;
  blockers)
    log_run "$log_dir/macos-release-blockers-sixth.log" blocker_preflight
    ;;
  notarized-dmg)
    log_run "$log_dir/macos-notarized-dmg-smoke-sixth.log" notarized_dmg_smoke
    ;;
  relay-turn)
    log_run "$log_dir/macos-production-relay-turn-sixth.log" relay_turn_smoke
    ;;
  manifest)
    log_run "$log_dir/macos-update-manifest-smoke-sixth.log" manifest_smoke
    ;;
  update-loop)
    log_run "$log_dir/macos-update-loop-smoke-sixth.log" update_loop_smoke
    ;;
  *)
    usage
    ;;
esac
