#!/usr/bin/env bash
set -euo pipefail

release_rollout_fail() {
  echo "$1" >&2
  return 1
}

release_rollout_run_id() {
  local evidence_url="$1"
  local repository="$2"
  local prefix="https://github.com/$repository/actions/runs/"
  if [[ "$evidence_url" != "$prefix"* ]]; then
    release_rollout_fail "evidence URL must belong to this repository"
    return 1
  fi
  local run_id="${evidence_url#"$prefix"}"
  if [[ ! "$run_id" =~ ^[1-9][0-9]*$ ]]; then
    release_rollout_fail "evidence URL must end in one positive run ID without a suffix"
    return 1
  fi
  printf '%s\n' "$run_id"
}

release_rollout_distinct_run_ids() {
  local macos_run_id windows_run_id
  if ! macos_run_id="$(release_rollout_run_id "$1" "$3")"; then
    return 1
  fi
  if ! windows_run_id="$(release_rollout_run_id "$2" "$3")"; then
    return 1
  fi
  if [[ "$macos_run_id" == "$windows_run_id" ]]; then
    release_rollout_fail "macOS and Windows upgrade matrix evidence must use different run IDs"
    return 1
  fi
}

release_rollout_api_get() {
  local url="$1"
  local output="$2"
  if ! curl --fail --show-error --silent --location \
    -H "Accept: application/vnd.github+json" \
    -H "Authorization: Bearer $GITHUB_TOKEN" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "$url" > "$output"; then
    release_rollout_fail "GitHub API request failed: $url"
    return 1
  fi
}

release_rollout_verify_repository_owner() {
  local output="$1/repository.json"
  if ! release_rollout_api_get "$GITHUB_API_URL/repos/$GITHUB_REPOSITORY" "$output"; then
    return 1
  fi
  if ! jq -e '.owner.type == "Organization"' "$output" >/dev/null; then
    release_rollout_fail "release requires an Organization-owned repository"
    return 1
  fi
}

release_rollout_verify_run() {
  local run_id="$1"
  local workflow_path="$2"
  local output="$3/run-$run_id.json"
  if ! release_rollout_api_get "$GITHUB_API_URL/repos/$GITHUB_REPOSITORY/actions/runs/$run_id" "$output"; then
    return 1
  fi
  if ! jq -e --arg build_commit "$BUILD_COMMIT" --arg workflow_path "$workflow_path" \
    '.conclusion == "success" and .head_sha == $build_commit and .path == $workflow_path' "$output" >/dev/null; then
    release_rollout_fail "run $run_id must be successful and bind the required commit and workflow path"
    return 1
  fi
}

release_rollout_download_attestation() {
  local run_id="$1"
  local artifact_name="$2"
  local output_dir="$3"
  local list_file="$output_dir/artifacts-$run_id.json"
  local archive_file="$output_dir/$artifact_name.zip"
  local attestation_file="$output_dir/$artifact_name.json"
  if ! release_rollout_api_get "$GITHUB_API_URL/repos/$GITHUB_REPOSITORY/actions/runs/$run_id/artifacts?per_page=100" "$list_file"; then
    return 1
  fi
  local archive_download_url
  if ! archive_download_url="$(jq -er --arg artifact_name "$artifact_name" '
    [.artifacts[] | select(.name == $artifact_name and .expired == false)] as $matches
    | if ($matches | length) == 1 then $matches[0].archive_download_url else error("required exact artifact is missing, duplicate, or expired") end
  ' "$list_file")"; then
    release_rollout_fail "required exact artifact is missing, duplicate, or expired: $artifact_name"
    return 1
  fi
  if ! release_rollout_api_get "$archive_download_url" "$archive_file"; then
    return 1
  fi
  local entry_count
  if ! entry_count="$(unzip -Z1 "$archive_file" | grep -Fxc 'attestation.json')"; then
    release_rollout_fail "artifact $artifact_name does not contain a root attestation.json"
    return 1
  fi
  if [[ "$entry_count" != "1" ]]; then
    release_rollout_fail "artifact $artifact_name must contain exactly one root attestation.json"
    return 1
  fi
  if ! unzip -p "$archive_file" attestation.json > "$attestation_file"; then
    release_rollout_fail "artifact $artifact_name attestation.json cannot be extracted"
    return 1
  fi
  printf '%s\n' "$attestation_file"
}

release_rollout_verify_tuple() {
  local attestation_file="$1"
  if ! jq -e \
    --arg version "$VERSION" \
    --arg build_commit "$BUILD_COMMIT" \
    --arg signing_public_key_fingerprint "$SIGNING_PUBLIC_KEY_FINGERPRINT" \
    --arg previous_version "$PREVIOUS_VERSION" \
    --arg monitoring_window_hours "$MONITORING_WINDOW_HOURS" \
    '.version == $version
      and .build_commit == $build_commit
      and .signing_public_key_fingerprint == $signing_public_key_fingerprint
      and .previous_version == $previous_version
      and .monitoring_window_hours == $monitoring_window_hours' \
    "$attestation_file" >/dev/null; then
    release_rollout_fail "attestation does not bind the complete release tuple"
    return 1
  fi
}

release_rollout_verify_upgrade_attestation() {
  local run_id="$1"
  local platform="$2"
  local artifact_name="update-recovery-upgrade-attestation-$platform"
  local output_dir="$3"
  local attestation_file
  if ! attestation_file="$(release_rollout_download_attestation "$run_id" "$artifact_name" "$output_dir")"; then
    return 1
  fi
  if ! release_rollout_verify_tuple "$attestation_file"; then
    return 1
  fi
  if ! jq -e --arg platform "$platform" '
    .schema == "super-dolphin/update-recovery-attestation/v1"
    and .kind == "upgrade-matrix"
    and .platform == $platform
    and .directions == ["previous-to-current", "current-to-previous"]
  ' "$attestation_file" >/dev/null; then
    release_rollout_fail "upgrade attestation has the wrong platform or direction contract"
    return 1
  fi
}

release_rollout_expected_predecessor_stage() {
  case "$1" in
    10-percent) printf '%s\n' "internal-20" ;;
    30-percent) printf '%s\n' "10-percent" ;;
    100-percent) printf '%s\n' "30-percent" ;;
    *)
      release_rollout_fail "internal stage has no external predecessor stage"
      return 1
      ;;
  esac
}

release_rollout_verify_predecessor() {
  local run_id="$1"
  local expected_stage="$2"
  local output_dir="$3"
  if ! release_rollout_verify_run "$run_id" "$RELEASE_WORKFLOW_PATH" "$output_dir"; then
    return 1
  fi
  local artifact_name="update-recovery-stage-attestation-$expected_stage"
  local attestation_file
  if ! attestation_file="$(release_rollout_download_attestation "$run_id" "$artifact_name" "$output_dir")"; then
    return 1
  fi
  if ! release_rollout_verify_tuple "$attestation_file"; then
    return 1
  fi
  if ! jq -e --arg stage "$expected_stage" '
    .schema == "super-dolphin/update-recovery-attestation/v1"
    and .kind == "rollout-stage"
    and .stage == $stage
  ' "$attestation_file" >/dev/null; then
    release_rollout_fail "predecessor attestation does not prove the expected previous stage"
    return 1
  fi
}

release_rollout_require_env() {
  local name
  for name in STAGE VERSION BUILD_COMMIT SIGNING_PUBLIC_KEY_FINGERPRINT PREVIOUS_VERSION MONITORING_WINDOW_HOURS PREDECESSOR_EVIDENCE MACOS_UPGRADE_MATRIX_EVIDENCE WINDOWS_UPGRADE_MATRIX_EVIDENCE DEFAULT_BRANCH GITHUB_REPOSITORY GITHUB_API_URL GITHUB_TOKEN RELEASE_WORKFLOW_PATH UPGRADE_EVIDENCE_WORKFLOW_PATH; do
    if [[ -z "${!name:-}" ]]; then
      release_rollout_fail "$name is required"
      return 1
    fi
  done
}

release_rollout_main() (
  if ! release_rollout_require_env; then
    return 1
  fi
  local output_dir
  if ! output_dir="$(mktemp -d)"; then
    release_rollout_fail "cannot create release validation temporary directory"
    return 1
  fi
  trap 'rm -rf -- "$output_dir"' EXIT
  if ! release_rollout_verify_repository_owner "$output_dir"; then
    return 1
  fi
  if [[ ! -f "$UPGRADE_EVIDENCE_WORKFLOW_PATH" ]]; then
    release_rollout_fail "trusted upgrade evidence producer workflow is not deployed; release remains fail-closed"
    return 1
  fi
  local macos_run_id windows_run_id predecessor_run_id expected_stage
  if ! macos_run_id="$(release_rollout_run_id "$MACOS_UPGRADE_MATRIX_EVIDENCE" "$GITHUB_REPOSITORY")"; then
    return 1
  fi
  if ! windows_run_id="$(release_rollout_run_id "$WINDOWS_UPGRADE_MATRIX_EVIDENCE" "$GITHUB_REPOSITORY")"; then
    return 1
  fi
  if ! release_rollout_distinct_run_ids "$MACOS_UPGRADE_MATRIX_EVIDENCE" "$WINDOWS_UPGRADE_MATRIX_EVIDENCE" "$GITHUB_REPOSITORY"; then
    return 1
  fi
  if ! release_rollout_verify_run "$macos_run_id" "$UPGRADE_EVIDENCE_WORKFLOW_PATH" "$output_dir"; then
    return 1
  fi
  if ! release_rollout_verify_run "$windows_run_id" "$UPGRADE_EVIDENCE_WORKFLOW_PATH" "$output_dir"; then
    return 1
  fi
  if ! release_rollout_verify_upgrade_attestation "$macos_run_id" "macos-arm64" "$output_dir"; then
    return 1
  fi
  if ! release_rollout_verify_upgrade_attestation "$windows_run_id" "windows-arm64" "$output_dir"; then
    return 1
  fi
  if [[ "$STAGE" != "internal-20" ]]; then
    if ! predecessor_run_id="$(release_rollout_run_id "$PREDECESSOR_EVIDENCE" "$GITHUB_REPOSITORY")"; then
      return 1
    fi
    if ! expected_stage="$(release_rollout_expected_predecessor_stage "$STAGE")"; then
      return 1
    fi
    if ! release_rollout_verify_predecessor "$predecessor_run_id" "$expected_stage" "$output_dir"; then
      return 1
    fi
  fi
)

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  release_rollout_main "$@"
fi
