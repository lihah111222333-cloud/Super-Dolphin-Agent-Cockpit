#!/usr/bin/env bash
set -euo pipefail

readonly registry_object='source-bundles/baseline-refresh/tools/registry_3.0.0_linux_amd64.tar.gz'
readonly registry_sha256='61c9a2c0d5981a78482025b6b69728521fbc78506d68b223d4a2eb825de5ca3d'
readonly crane_object='source-bundles/baseline-refresh/tools/go-containerregistry_v0.21.0_linux_x86_64.tar.gz'
readonly crane_sha256='f76db5d7544a3c691a90fea7c87561342b2f03f1090f0299f029eafa1da3de41'
readonly accepted_base_digest='502e70fdbbae19d29722810179649e37b4bf38894a7e3d9bcdcd858372435aa7'
readonly accepted_base_archive_sha256='0387001cad8918d779fb78c5d2e5234ba3725a677c3f5f6ebadb9744a40aeaa5'
readonly receipt_schema='remote-ci-imagecache-refresh-receipt/v2'
readonly image_cache_size_gib=30

config='config/remote-ci/aliyun.json'
source_ref='HEAD'
retention_days=7
poll_seconds=5
timeout_seconds=1800
if_older_than_hours=0
receipt_object=
dry_run=false
skip_refresh=false

usage() {
  cat <<'EOF'
usage: scripts/refresh_remote_ci_imagecache.sh [options]

Build a finite-lived remote-CI ImageCache candidate from the accepted ECI
snapshot. The command never mutates the accepted SQLite authority.

To continue from the latest finite-lived candidate, set both
SUPER_DOLPHIN_CI_REFRESH_BASE_IMAGE and SUPER_DOLPHIN_CI_REFRESH_BASE_SNAPSHOT
from its previous receipt. The immutable OCI base archive remains the pinned
generation-one image unless explicitly overridden with matching archive SHA.

Options:
  --config PATH          remote-CI aliyun JSON (default config/remote-ci/aliyun.json)
  --source-ref REF       exact commit to warm (default HEAD)
  --retention-days N     ImageCache retention, 1..30 (default 7)
  --timeout-seconds N    cloud phase timeout (default 1800)
  --if-older-than-hours N
                         skip when the OSS scheduling receipt is at most N hours old
  --receipt-object KEY   OSS scheduling receipt key (default under source_prefix)
  --dry-run              validate inputs and print the non-secret plan only
EOF
}

die() {
  printf 'remote ImageCache refresh: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command is missing: $1"
}

require_uint() {
  [[ "$2" =~ ^[0-9]+$ ]] || die "$1 must be an unsigned integer"
}

parse_args() {
  while (($#)); do
    case "$1" in
      --config) (($# >= 2)) || die '--config requires a value'; config=$2; shift 2 ;;
      --source-ref) (($# >= 2)) || die '--source-ref requires a value'; source_ref=$2; shift 2 ;;
      --retention-days) (($# >= 2)) || die '--retention-days requires a value'; retention_days=$2; shift 2 ;;
      --timeout-seconds) (($# >= 2)) || die '--timeout-seconds requires a value'; timeout_seconds=$2; shift 2 ;;
      --if-older-than-hours) (($# >= 2)) || die '--if-older-than-hours requires a value'; if_older_than_hours=$2; shift 2 ;;
      --receipt-object) (($# >= 2)) || die '--receipt-object requires a value'; receipt_object=$2; shift 2 ;;
      --dry-run) dry_run=true; shift ;;
      -h|--help) usage; exit 0 ;;
      *) die "unknown argument: $1" ;;
    esac
  done
}

json_required() {
  local expression=$1 value
  value=$(jq -er "$expression | select(type == \"string\" and length > 0)" "$config") || die "missing config field: $expression"
  printf '%s' "$value"
}

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

oss_endpoint_host() {
  printf '%s' "$1" | sed -E 's#^https?://##; s#/$##'
}

redact_cloud_error() {
  sed -E 's/((AccessKeyId|Signature|SignatureNonce|SecurityToken|security-token)=)[^&" ]+/\1REDACTED/g'
}

oss_object_exists() {
  local object=$1 output attempt=1
  while ((attempt <= 3)); do
    if output=$(aliyun oss stat "oss://${bucket}/${object}" --profile "$profile" --endpoint "$oss_public_host" 2>&1); then
      return 0
    fi
    [[ "$output" != *'NoSuchKey'* ]] || return 1
    ((attempt == 3)) && break
    sleep 2
    attempt=$((attempt + 1))
  done
  die "OSS stat failed for ${object}: $(printf '%s' "$output" | redact_cloud_error)"
}

upload_if_missing() {
  local file=$1 object=$2
  if oss_object_exists "$object"; then
    printf 'reuse OSS object %s\n' "$object" >&2
    return
  fi
  local output
  if ! output=$(aliyun oss cp "$file" "oss://${bucket}/${object}" --profile "$profile" --endpoint "$oss_public_host" --force 2>&1); then
    die "OSS upload failed for ${object}: $(printf '%s' "$output" | redact_cloud_error)"
  fi
}

download_oss_object() {
  local object=$1 destination=$2 output attempt=1
  while ((attempt <= 3)); do
    if output=$(aliyun oss cp "oss://${bucket}/${object}" "$destination" --profile "$profile" --endpoint "$oss_public_host" --force 2>&1); then
      return 0
    fi
    ((attempt == 3)) && break
    sleep 2
    attempt=$((attempt + 1))
  done
  die "OSS download failed for ${object}: $(printf '%s' "$output" | redact_cloud_error)"
}

replace_oss_object() {
  local file=$1 object=$2 output attempt=1
  while ((attempt <= 3)); do
    if output=$(aliyun oss cp "$file" "oss://${bucket}/${object}" --profile "$profile" --endpoint "$oss_public_host" --force 2>&1); then
      return 0
    fi
    ((attempt == 3)) && break
    sleep 2
    attempt=$((attempt + 1))
  done
  die "OSS replace failed for ${object}: $(printf '%s' "$output" | redact_cloud_error)"
}

sign_internal_object() {
  local object=$1 output url attempt=1
  while ((attempt <= 3)); do
    if output=$(aliyun oss sign "oss://${bucket}/${object}" --profile "$profile" --endpoint "$oss_internal_host" --timeout "$timeout_seconds" 2>&1); then
      break
    fi
    ((attempt == 3)) && die "OSS sign failed for ${object}: $(printf '%s' "$output" | redact_cloud_error)"
    sleep 2
    attempt=$((attempt + 1))
  done
  url=$(printf '%s\n' "$output" | head -1)
  [[ "$url" == https://* ]] || die "OSS sign did not return an HTTPS URL for ${object}"
  printf '%s' "$url"
}

eci_json() {
  local action=$1 attempts=1 attempt=1 output
  [[ "$action" == Describe* ]] && attempts=3
  while ((attempt <= attempts)); do
    if output=$(aliyun eci "$@" --profile "$profile" --RegionId "$region" 2>&1); then
      printf '%s' "$output"
      return 0
    fi
    ((attempt == attempts)) && break
    sleep 2
    attempt=$((attempt + 1))
  done
  printf 'Aliyun ECI %s failed: %s\n' "$action" "$(printf '%s' "$output" | redact_cloud_error)" >&2
  return 1
}

delete_group() {
  local group_id=$1
  [[ -n "$group_id" ]] || return 0
  eci_json DeleteContainerGroup --ContainerGroupId "$group_id" >/dev/null 2>&1 || true
}

delete_cache() {
  local candidate_id=$1
  [[ -n "$candidate_id" ]] || return 0
  eci_json DeleteImageCache --ImageCacheId "$candidate_id" >/dev/null 2>&1 || true
}

retire_builder() {
  local retired_id=$builder_group_id deadline=$((SECONDS + timeout_seconds)) count
  delete_group "$retired_id"
  while ((SECONDS < deadline)); do
    count=$(eci_json DescribeContainerGroups --ContainerGroupIds "[\"${retired_id}\"]" | jq -er '.ContainerGroups | length')
    if [[ "$count" == 0 ]]; then
      builder_group_id=
      return 0
    fi
    sleep "$poll_seconds"
  done
  die 'temporary builder registry did not disappear before ImageCache verification'
}

cleanup() {
  local exit_code=$?
  delete_group "${verify_group_id:-}"
  delete_group "${builder_group_id:-}"
  if ((exit_code != 0)); then
    delete_cache "${image_cache_id:-}"
  fi
  if [[ -n "${temp_root:-}" && -d "$temp_root" ]]; then
    rm -rf -- "$temp_root"
  fi
  exit "$exit_code"
}

create_source_archive() {
  source_commit=$(git rev-parse "${source_ref}^{commit}")
  source_tree=$(git rev-parse "${source_ref}^{tree}")
  source_archive="$temp_root/source.tar.gz"
  git archive --format=tar "$source_commit" | gzip -n >"$source_archive"
  source_sha256=$(sha256_file "$source_archive")
  source_object="${source_prefix}baseline-refresh/sources/${source_tree}/${source_sha256}.tar.gz"
}

validate_refresh_receipt() {
  local receipt_file=$1
  jq -e --arg schema "$receipt_schema" '
    type == "object" and
    keys == ["action","authoritative","base_image","base_snapshot_id","builder_compile_seconds","execution_provider","gate_binary_sha256","image","image_cache_id","image_cache_name","image_cache_snapshot_id","image_cache_status","image_digest","mutates_sqlite","oci_base_image","refreshed_at_unix_sec","refreshed_at_utc","region_id","retention_days","schema_version","source_commit","source_tree","verification_compile_seconds"] and
    .schema_version == $schema and .authoritative == false and
    .action == "candidate_created_not_accepted" and
    .execution_provider == "aliyun-eci/v1" and (.region_id | type == "string" and length > 0) and .mutates_sqlite == false and
    (.source_commit | type == "string" and test("^[0-9a-f]{40}([0-9a-f]{24})?$")) and
    (.source_tree | type == "string" and test("^[0-9a-f]{40}([0-9a-f]{24})?$")) and
    (.base_image | type == "string" and test("@sha256:[0-9a-f]{64}$")) and
    (.base_snapshot_id | type == "string" and length > 0) and
    (.oci_base_image | type == "string" and test("@sha256:[0-9a-f]{64}$")) and
    (.image | type == "string" and test("@sha256:[0-9a-f]{64}$")) and
    (.image_digest | type == "string" and test("^sha256:[0-9a-f]{64}$")) and
    (.image_cache_id | type == "string" and length > 0) and
    (.image_cache_name | type == "string" and length > 0) and
    (.image_cache_snapshot_id | type == "string" and length > 0) and
    .image_cache_status == "Ready" and
    (.gate_binary_sha256 | type == "string" and test("^sha256:[0-9a-f]{64}$")) and
    (.builder_compile_seconds | type == "number" and . >= 0 and floor == .) and
    (.verification_compile_seconds | type == "number" and . >= 0 and floor == .) and
    (.retention_days | type == "number" and . >= 1 and . <= 30 and floor == .) and
    (.refreshed_at_unix_sec | type == "number" and . > 0 and floor == .) and
    (.refreshed_at_utc | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
  ' "$receipt_file" >/dev/null || die "refresh receipt is invalid: $receipt_file"
}

load_refresh_schedule() {
  local current_receipt="$temp_root/current-receipt.json" now refreshed_at age_seconds threshold_seconds
  ((if_older_than_hours > 0)) || return 0
  if ! oss_object_exists "$receipt_object"; then
    return 0
  fi
  download_oss_object "$receipt_object" "$current_receipt"
  validate_refresh_receipt "$current_receipt"
  now=$(date +%s)
  refreshed_at=$(jq -er '.refreshed_at_unix_sec' "$current_receipt")
  ((refreshed_at <= now + 300)) || die 'refresh receipt timestamp is in the future'
  age_seconds=$((now - refreshed_at))
  threshold_seconds=$((if_older_than_hours * 3600))
  previous_base_image=$(jq -er '.image' "$current_receipt")
  previous_base_snapshot=$(jq -er '.image_cache_snapshot_id' "$current_receipt")
  if ((age_seconds <= threshold_seconds)); then
    skip_refresh=true
    jq -n --argjson age_seconds "$age_seconds" --argjson threshold_seconds "$threshold_seconds" \
      '{authoritative:false,action:"skip_fresh_imagecache",age_seconds:$age_seconds,threshold_seconds:$threshold_seconds,mutates_sqlite:false}'
  fi
}

create_module_download_cache() {
  local go_mod gomodcache manifest manifest_json module_dir module_root
  gomodcache=$(go env GOMODCACHE)
  module_root="$temp_root/module-root"
  mkdir -p "$module_root"
  tar -xzf "$source_archive" -C "$module_root"
  manifest_json="$temp_root/module-downloads.jsonl"
  : >"$manifest_json"
  while IFS= read -r go_mod; do
    module_dir=${go_mod%/go.mod}
    [[ "$module_dir" == "$module_root" || "$module_dir" == "$module_root"/* ]] || die "nested module escaped source archive: $module_dir"
    (
      cd "$module_dir"
      GOTOOLCHAIN=local GOWORK=off go mod download
      GOTOOLCHAIN=local GOWORK=off go mod download -json all
    ) >>"$manifest_json"
  done < <(find "$module_root" -type f -name go.mod | LC_ALL=C sort)
  event_version=$(awk '$1 == "github.com/kelindar/event" {print $2; exit}' "$module_root/go.mod")
  [[ "$event_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'github.com/kelindar/event version is missing from go.mod'
  event_download_json=$(GOTOOLCHAIN=local GOWORK=off go mod download -json "github.com/kelindar/event@${event_version}")
  event_sum=$(printf '%s' "$event_download_json" | jq -er '.Sum | select(type == "string" and startswith("h1:"))')
  event_gomod_sum=$(printf '%s' "$event_download_json" | jq -er '.GoModSum | select(type == "string" and startswith("h1:"))')
  printf '%s\n' "$event_download_json" >>"$manifest_json"
  manifest="$temp_root/module-download-files.txt"
  jq -s '.[]' "$manifest_json" |
    jq -r '[.Info, .GoMod, .Zip, (if .Zip then (.Zip + "hash") else empty end)][] | select(. != null)' |
    awk -v prefix="${gomodcache}/" 'index($0,prefix)==1 {print substr($0,length(prefix)+1)}' |
    LC_ALL=C sort -u >"$manifest"
  [[ -s "$manifest" ]] || die 'Go module download cache manifest is empty'
  module_archive="$temp_root/go-module-download-cache.tar.gz"
  COPYFILE_DISABLE=1 tar -C "$gomodcache" -cf - -T "$manifest" | gzip -n >"$module_archive"
  module_sha256=$(sha256_file "$module_archive")
  module_object="${source_prefix}baseline-refresh/dependencies/go-module-download-cache/${module_sha256}.tar.gz"
}

print_plan() {
  jq -n \
    --arg schema "$receipt_schema" \
    --arg commit "$source_commit" \
    --arg tree "$source_tree" \
    --arg base_image "$base_image" \
    --arg base_snapshot "$base_snapshot" \
    --arg oci_base_image "$oci_base_image" \
    --arg source_object "$source_object" \
    --arg source_sha256 "sha256:${source_sha256}" \
    --arg module_object "$module_object" \
    --arg module_sha256 "sha256:${module_sha256}" \
    --argjson retention_days "$retention_days" \
    '{schema_version:$schema, authoritative:false, action:"create_candidate_only", source_commit:$commit, source_tree:$tree, base_image:$base_image, base_snapshot_id:$base_snapshot, oci_base_image:$oci_base_image, source_object:$source_object, source_sha256:$source_sha256, module_object:$module_object, module_sha256:$module_sha256, retention_days:$retention_days, mutates_sqlite:false}'
}

builder_script() {
  cat <<'EOF'
set -eu
mkdir -p /tmp/bin /tmp/registry-store /tmp/src /tmp/gomod /tmp/go-build /tmp/overlay
fetch() { curl -fsSL --retry 3 "$1" -o "$2"; printf "%s  %s\n" "$3" "$2" | sha256sum -c -; }
fetch "$REGISTRY_URL" /tmp/registry.tar.gz "$REGISTRY_SHA256"
fetch "$CRANE_URL" /tmp/crane.tar.gz "$CRANE_SHA256"
fetch "$SOURCE_URL" /tmp/source.tar.gz "$SOURCE_SHA256"
fetch "$MODULE_URL" /tmp/module.tar.gz "$MODULE_SHA256"
fetch "$BASE_ARCHIVE_URL" /tmp/base-image.tar "$BASE_ARCHIVE_SHA256"
tar -xzf /tmp/registry.tar.gz -C /tmp/bin registry
tar -xzf /tmp/crane.tar.gz -C /tmp/bin crane
tar -xzf /tmp/source.tar.gz -C /tmp/src
tar --delay-directory-restore --no-same-permissions -xzf /tmp/module.tar.gz -C /tmp/gomod
cp -a /opt/super-dolphin/cache/go-build/. /tmp/go-build/
test -s /opt/super-dolphin-gate/frontend-embed/index.html
mkdir -p /tmp/src/cmd/agent-terminal
cp -a /opt/super-dolphin-gate/frontend-embed /tmp/src/cmd/agent-terminal/web-dist
chmod -R u+w /tmp/go-build
cd /tmp/src
started=$(date +%s)
mkdir -p /tmp/event-probe
printf 'module cacheprobe\n\ngo 1.26.5\n\nrequire github.com/kelindar/event %s\n' "$EVENT_VERSION" >/tmp/event-probe/go.mod
printf 'github.com/kelindar/event %s %s\ngithub.com/kelindar/event %s/go.mod %s\n' "$EVENT_VERSION" "$EVENT_SUM" "$EVENT_VERSION" "$EVENT_GOMOD_SUM" >/tmp/event-probe/go.sum
(cd /tmp/event-probe && GOTOOLCHAIN=local GOPROXY=off GOMODCACHE=/tmp/gomod GOCACHE=/tmp/go-build go list -deps github.com/kelindar/event >/dev/null)
for nested_module in build/gate/runtime-proxy build/gate/runtime-tools third_party/kelindar-event; do
  (cd "/tmp/src/${nested_module}" && GOTOOLCHAIN=local GOPROXY=off GOMODCACHE=/tmp/gomod GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go list -deps -test ./... >/dev/null)
done
GOTOOLCHAIN=local GOPROXY=off GOMODCACHE=/tmp/gomod GOCACHE=/tmp/go-build CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go list -deps -test ./internal/devtools/gate >/dev/null
GOTOOLCHAIN=local GOPROXY=off GOMODCACHE=/tmp/gomod GOCACHE=/tmp/go-build CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go list -f '{{.Dir}}' ./... >/tmp/package-dirs.txt
while IFS= read -r package_dir; do
  case "$package_dir" in
    "$PWD"/*) package="./${package_dir#"$PWD"/}" ;;
    *) printf 'refresh-builder-package-directory-invalid directory=%s\n' "$package_dir" >&2; exit 22 ;;
  esac
  if ! SUPER_DOLPHIN_TEST_BACKEND=remote-worker GOTOOLCHAIN=local GOPROXY=off GOMODCACHE=/tmp/gomod GOCACHE=/tmp/go-build CGO_ENABLED=1 GOOS=linux GOARCH=amd64 ./scripts/test_with_guard.sh --ci-compile-package "$package"; then
    printf 'refresh-builder-package-failed package=%s\n' "$package" >&2
    exit 23
  fi
done </tmp/package-dirs.txt
GOTOOLCHAIN=local GOPROXY=off GOMODCACHE=/tmp/gomod GOCACHE=/tmp/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/candidate-gate ./cmd/super-dolphin-gate
completed=$(date +%s)
gate_sha=$(sha256sum /tmp/candidate-gate | cut -d ' ' -f1)
mkdir -p /tmp/overlay/opt/super-dolphin/cache/go-build
mkdir -p /tmp/overlay/opt/super-dolphin-gate/runtime/go-mod-cache
: >/tmp/overlay/opt/super-dolphin/cache/go-build/.wh..wh..opq
: >/tmp/overlay/opt/super-dolphin-gate/runtime/go-mod-cache/.wh..wh..opq
cp -a /tmp/go-build/. /tmp/overlay/opt/super-dolphin/cache/go-build/
cp -a /tmp/gomod/. /tmp/overlay/opt/super-dolphin-gate/runtime/go-mod-cache/
chmod -R a-w /tmp/overlay/opt/super-dolphin/cache/go-build /tmp/overlay/opt/super-dolphin-gate/runtime/go-mod-cache
tar -C /tmp/overlay -cf /tmp/cache-layer.tar .
cat >/tmp/registry.yml <<'REGISTRY'
version: 0.1
log:
  level: warn
storage:
  filesystem:
    rootdirectory: /tmp/registry-store
http:
  addr: 0.0.0.0:5000
REGISTRY
/tmp/bin/registry serve /tmp/registry.yml &
registry_pid=$!
for unused in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:5000/v2/ >/dev/null; then break; fi
  kill -0 "$registry_pid" 2>/dev/null || exit 21
  sleep 1
done
curl -fsS http://127.0.0.1:5000/v2/ >/dev/null
/tmp/bin/crane push --insecure /tmp/base-image.tar "127.0.0.1:5000/sdci/accepted-base:${BASE_TAG}"
/tmp/bin/crane append --insecure -b "127.0.0.1:5000/sdci/accepted-base:${BASE_TAG}" -f /tmp/cache-layer.tar -t "127.0.0.1:5000/sdci/successor:${SOURCE_TREE}"
successor_digest=$(/tmp/bin/crane digest --insecure "127.0.0.1:5000/sdci/successor:${SOURCE_TREE}")
printf 'refresh-builder-ready tree=%s digest=%s compile_seconds=%s gate_sha256=%s\n' "$SOURCE_TREE" "$successor_digest" "$((completed-started))" "$gate_sha"
wait "$registry_pid"
EOF
}

wait_for_builder() {
  local deadline=$((SECONDS + timeout_seconds)) status log
  while ((SECONDS < deadline)); do
    status=$(eci_json DescribeContainerGroups --ContainerGroupIds "[\"${builder_group_id}\"]" | jq -r '.ContainerGroups[0].Status // "missing"')
    log=$(eci_json DescribeContainerLog --ContainerGroupId "$builder_group_id" --ContainerName builder 2>/dev/null | jq -r '.Content // empty')
    builder_ready=$(printf '%s\n' "$log" | sed -nE 's/.*refresh-builder-ready tree=([^ ]+) digest=(sha256:[0-9a-f]{64}) compile_seconds=([0-9]+) gate_sha256=([0-9a-f]{64}).*/\1 \2 \3 \4/p' | tail -1)
    [[ -z "$builder_ready" ]] || return 0
    [[ "$status" != Failed && "$status" != Succeeded ]] || die "builder terminated before readiness: ${status}: $(printf '%s' "$log" | tail -20)"
    sleep "$poll_seconds"
  done
  die 'builder readiness timed out'
}

start_builder() {
  local script result
  script=$(builder_script)
  result=$(eci_json CreateContainerGroup \
    --ContainerGroupName "sdci-imagecache-refresh-${source_tree:0:8}-$(date -u +%Y%m%d%H%M%S)" \
    --SecurityGroupId "$security_group" --VSwitchId "$vswitch_csv" --ScheduleStrategy VSwitchRandom \
    --RestartPolicy Never --Cpu 8 --Memory 16 --EphemeralStorage 100 --RamRoleName "$worker_role" \
    --Container.1.Name builder --Container.1.Image "$base_image" --Container.1.ImagePullPolicy IfNotPresent \
    --Container.1.Cpu 8 --Container.1.Memory 16 --Container.1.Command.1 /bin/sh --Container.1.Arg.1=-c --Container.1.Arg.2 "$script" \
    --Container.1.EnvironmentVar.1.Key REGISTRY_URL --Container.1.EnvironmentVar.1.Value "$(sign_internal_object "$registry_object")" \
    --Container.1.EnvironmentVar.2.Key REGISTRY_SHA256 --Container.1.EnvironmentVar.2.Value "$registry_sha256" \
    --Container.1.EnvironmentVar.3.Key CRANE_URL --Container.1.EnvironmentVar.3.Value "$(sign_internal_object "$crane_object")" \
    --Container.1.EnvironmentVar.4.Key CRANE_SHA256 --Container.1.EnvironmentVar.4.Value "$crane_sha256" \
    --Container.1.EnvironmentVar.5.Key SOURCE_URL --Container.1.EnvironmentVar.5.Value "$(sign_internal_object "$source_object")" \
    --Container.1.EnvironmentVar.6.Key SOURCE_SHA256 --Container.1.EnvironmentVar.6.Value "$source_sha256" \
    --Container.1.EnvironmentVar.7.Key MODULE_URL --Container.1.EnvironmentVar.7.Value "$(sign_internal_object "$module_object")" \
    --Container.1.EnvironmentVar.8.Key MODULE_SHA256 --Container.1.EnvironmentVar.8.Value "$module_sha256" \
    --Container.1.EnvironmentVar.9.Key BASE_ARCHIVE_URL --Container.1.EnvironmentVar.9.Value "$(sign_internal_object "$base_archive_object")" \
    --Container.1.EnvironmentVar.10.Key BASE_ARCHIVE_SHA256 --Container.1.EnvironmentVar.10.Value "$base_archive_sha256" \
    --Container.1.EnvironmentVar.11.Key SOURCE_TREE --Container.1.EnvironmentVar.11.Value "$source_tree" \
    --Container.1.EnvironmentVar.12.Key BASE_TAG --Container.1.EnvironmentVar.12.Value "${oci_base_digest:0:12}" \
    --Container.1.EnvironmentVar.13.Key EVENT_VERSION --Container.1.EnvironmentVar.13.Value "$event_version" \
    --Container.1.EnvironmentVar.14.Key EVENT_SUM --Container.1.EnvironmentVar.14.Value "$event_sum" \
    --Container.1.EnvironmentVar.15.Key EVENT_GOMOD_SUM --Container.1.EnvironmentVar.15.Value "$event_gomod_sum" \
    --ImageSnapshotId "$base_snapshot")
  builder_group_id=$(printf '%s' "$result" | jq -er '.ContainerGroupId')
  wait_for_builder
  read -r ready_tree successor_digest compile_seconds gate_sha256 <<<"$builder_ready"
  [[ "$ready_tree" == "$source_tree" ]] || die 'builder returned the wrong source tree'
  builder_ip=$(eci_json DescribeContainerGroups --ContainerGroupIds "[\"${builder_group_id}\"]" | jq -er '.ContainerGroups[0].IntranetIp')
}

create_image_cache() {
  local result name client_token
  name="sdci-refresh-${source_tree:0:8}-$(date -u +%Y%m%d%H%M%S)"
  image_cache_name=$name
  client_token=$(printf '%s\n' "standard:${image_cache_size_gib}:${source_tree}:${successor_digest}:${base_snapshot}:${retention_days}" | shasum -a 256 | awk '{print $1}')
  result=$(eci_json CreateImageCache --ImageCacheName "$name" \
    --Image.1 "${builder_ip}:5000/sdci/successor@${successor_digest}" \
    --SecurityGroupId "$security_group" --VSwitchId "$vswitch_csv" --ImageCacheSize "$image_cache_size_gib" \
    --RetentionDays "$retention_days" --AutoMatchImageCache true --EliminationStrategy LRU \
    --ClientToken "$client_token" --PlainHttpRegistry "${builder_ip}:5000")
  image_cache_id=$(printf '%s' "$result" | jq -er '.ImageCacheId')
  local deadline=$((SECONDS + timeout_seconds)) status
  while ((SECONDS < deadline)); do
    cache_json=$(eci_json DescribeImageCaches --ImageCacheId "$image_cache_id")
    status=$(printf '%s' "$cache_json" | jq -r '(.ImageCaches[0] // .ImageCaches.ImageCache[0]).Status // "missing"')
    if [[ "$status" == Ready ]]; then
      image_cache_snapshot=$(printf '%s' "$cache_json" | jq -er '(.ImageCaches[0] // .ImageCaches.ImageCache[0]).SnapshotId')
      return 0
    fi
    [[ "$status" != Failed ]] || die "ImageCache failed: $(printf '%s' "$cache_json" | jq -c '(.ImageCaches[0] // .ImageCaches.ImageCache[0]).Events // []')"
    sleep "$poll_seconds"
  done
  die 'ImageCache creation timed out'
}

verify_image_cache() {
  local verify_script result status log deadline
  # 该脚本由远端 ECI shell 展开，本地必须保留其中的环境变量与命令替换。
  # shellcheck disable=SC2016
  verify_script='set -eu; test -z "$(find /opt/super-dolphin/cache/go-build /opt/super-dolphin-gate/runtime/go-mod-cache -perm /222 -print -quit)"; mkdir -p /tmp/src /tmp/go-build /tmp/gomod; curl -fsSL --retry 3 "$SOURCE_URL" -o /tmp/source.tar.gz; printf "%s  %s\n" "$SOURCE_SHA256" /tmp/source.tar.gz | sha256sum -c -; tar -xzf /tmp/source.tar.gz -C /tmp/src; test -s /opt/super-dolphin-gate/frontend-embed/index.html; mkdir -p /tmp/src/cmd/agent-terminal; cp -a /opt/super-dolphin-gate/frontend-embed /tmp/src/cmd/agent-terminal/web-dist; cp -a /opt/super-dolphin/cache/go-build/. /tmp/go-build/; chmod -R u+w /tmp/go-build; chmod 0700 /tmp/gomod; /super-dolphin-gate worker go-module-overlay /opt/super-dolphin-gate/runtime/go-mod-cache /tmp/gomod; cd /tmp/src; GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/go-build GOMODCACHE=/tmp/gomod CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go list -deps -test ./... >/dev/null; started=$(date +%s); GOTOOLCHAIN=local GOPROXY=off GOCACHE=/tmp/go-build GOMODCACHE=/tmp/gomod CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/verified-gate ./cmd/super-dolphin-gate; completed=$(date +%s); printf "refresh-verify-ready tree=%s compile_seconds=%s gate_sha256=%s\n" "$SOURCE_TREE" "$((completed-started))" "$(sha256sum /tmp/verified-gate | cut -d " " -f1)"'
  result=$(eci_json CreateContainerGroup --ContainerGroupName "sdci-imagecache-verify-${source_tree:0:8}-$(date -u +%Y%m%d%H%M%S)" \
    --SecurityGroupId "$security_group" --VSwitchId "$vswitch_csv" --ScheduleStrategy VSwitchRandom \
    --RestartPolicy Never --Cpu 8 --Memory 16 --EphemeralStorage 100 \
    --Container.1.Name verify --Container.1.Image "${builder_ip}:5000/sdci/successor@${successor_digest}" --Container.1.ImagePullPolicy Never \
    --Container.1.Cpu 8 --Container.1.Memory 16 --Container.1.Command.1 /bin/sh --Container.1.Arg.1=-c --Container.1.Arg.2 "$verify_script" \
    --Container.1.EnvironmentVar.1.Key SOURCE_URL --Container.1.EnvironmentVar.1.Value "$(sign_internal_object "$source_object")" \
    --Container.1.EnvironmentVar.2.Key SOURCE_SHA256 --Container.1.EnvironmentVar.2.Value "$source_sha256" \
    --Container.1.EnvironmentVar.3.Key SOURCE_TREE --Container.1.EnvironmentVar.3.Value "$source_tree" \
    --ImageSnapshotId "$image_cache_snapshot")
  verify_group_id=$(printf '%s' "$result" | jq -er '.ContainerGroupId')
  deadline=$((SECONDS + timeout_seconds))
  while ((SECONDS < deadline)); do
    status=$(eci_json DescribeContainerGroups --ContainerGroupIds "[\"${verify_group_id}\"]" | jq -r '.ContainerGroups[0].Status // "missing"')
    log=$(eci_json DescribeContainerLog --ContainerGroupId "$verify_group_id" --ContainerName verify 2>/dev/null | jq -r '.Content // empty')
    if [[ "$log" == *ErrImageNeverPull* || "$log" == *ImagePullBackOff* || "$log" == *InvalidImageName* ]]; then
      die "ImageCache verification cannot start from the cached image: $(printf '%s' "$log" | tail -5)"
    fi
    verify_ready=$(printf '%s\n' "$log" | sed -nE 's/.*refresh-verify-ready tree=([^ ]+) compile_seconds=([0-9]+) gate_sha256=([0-9a-f]{64}).*/\1 \2 \3/p' | tail -1)
    if [[ -n "$verify_ready" ]]; then
      read -r verify_tree verify_compile_seconds verify_gate_sha256 <<<"$verify_ready"
      [[ "$verify_tree" == "$source_tree" && "$verify_gate_sha256" == "$gate_sha256" ]] || die 'verification identity drifted'
      return 0
    fi
    [[ "$status" != Failed && "$status" != Succeeded ]] || die "verification terminated without receipt: ${status}: $(printf '%s' "$log" | tail -20)"
    sleep "$poll_seconds"
  done
  die 'ImageCache verification timed out'
}

print_receipt() {
  jq -n \
    --arg schema "$receipt_schema" --arg commit "$source_commit" --arg tree "$source_tree" \
    --arg region_id "$region" \
    --arg base_image "$base_image" --arg base_snapshot "$base_snapshot" \
    --arg oci_base_image "$oci_base_image" \
    --arg image "${builder_ip}:5000/sdci/successor@${successor_digest}" \
    --arg image_digest "$successor_digest" --arg cache_id "$image_cache_id" --arg cache_name "$image_cache_name" --arg snapshot_id "$image_cache_snapshot" \
    --arg gate_sha256 "sha256:${gate_sha256}" --argjson build_seconds "$compile_seconds" \
    --argjson verify_seconds "$verify_compile_seconds" --argjson retention_days "$retention_days" \
    --arg refreshed_at_utc "$refreshed_at_utc" --argjson refreshed_at_unix_sec "$refreshed_at_unix_sec" \
    '{schema_version:$schema, authoritative:false, action:"candidate_created_not_accepted", execution_provider:"aliyun-eci/v1", region_id:$region_id, source_commit:$commit, source_tree:$tree, base_image:$base_image, base_snapshot_id:$base_snapshot, oci_base_image:$oci_base_image, image:$image, image_digest:$image_digest, image_cache_id:$cache_id, image_cache_name:$cache_name, image_cache_snapshot_id:$snapshot_id, image_cache_status:"Ready", gate_binary_sha256:$gate_sha256, builder_compile_seconds:$build_seconds, verification_compile_seconds:$verify_seconds, retention_days:$retention_days, refreshed_at_unix_sec:$refreshed_at_unix_sec, refreshed_at_utc:$refreshed_at_utc, mutates_sqlite:false}'
}

persist_receipt() {
  local receipt_file="$temp_root/refresh-receipt.json" receipt_sha receipt_archive_object
  refreshed_at_unix_sec=$(date +%s)
  refreshed_at_utc=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  print_receipt >"$receipt_file"
  validate_refresh_receipt "$receipt_file"
  receipt_sha=$(sha256_file "$receipt_file")
  receipt_archive_object="${source_prefix}baseline-refresh/receipts/${source_tree}/${receipt_sha}.json"
  upload_if_missing "$receipt_file" "$receipt_archive_object"
  replace_oss_object "$receipt_file" "$receipt_object"
  cat "$receipt_file"
}

main() {
  parse_args "$@"
  require_uint retention_days "$retention_days"
  require_uint timeout_seconds "$timeout_seconds"
  require_uint if_older_than_hours "$if_older_than_hours"
  ((retention_days >= 1 && retention_days <= 30)) || die 'retention days must be between 1 and 30'
  ((if_older_than_hours <= 8760)) || die 'refresh age must not exceed one year'
  for command in aliyun awk curl date find git go gzip jq sed shasum sort tar; do require_command "$command"; done
  [[ -f "$config" ]] || die "config is not a regular file: $config"

  profile=$(json_required '.credential_profile')
  region=$(json_required '.region_id')
  security_group=$(json_required '.security_group_id')
  worker_role=$(json_required '.worker_role_name')
  bucket=$(json_required '.oss.bucket')
  oss_public_host=$(oss_endpoint_host "$(json_required '.oss.endpoint')")
  oss_internal_host=$(oss_endpoint_host "$(json_required '.oss.internal_endpoint')")
  source_prefix=$(json_required '.oss.source_prefix')
  receipt_object=${receipt_object:-${source_prefix}baseline-refresh/receipts/current.json}
  [[ "$receipt_object" != /* && "$receipt_object" != *'..'* && "$receipt_object" != *[[:space:]]* ]] || die 'receipt object key is invalid'
  temp_root=$(mktemp -d)
  trap cleanup EXIT INT TERM
  load_refresh_schedule
  [[ "$skip_refresh" != true ]] || return 0
  if [[ -n "${SUPER_DOLPHIN_CI_REFRESH_BASE_IMAGE:-}" || -n "${SUPER_DOLPHIN_CI_REFRESH_BASE_SNAPSHOT:-}" ]]; then
    [[ -n "${SUPER_DOLPHIN_CI_REFRESH_BASE_IMAGE:-}" && -n "${SUPER_DOLPHIN_CI_REFRESH_BASE_SNAPSHOT:-}" ]] || die 'base image and snapshot overrides must be provided together'
  fi
  base_image=${SUPER_DOLPHIN_CI_REFRESH_BASE_IMAGE:-${previous_base_image:-$(json_required '.generation_one_provision.image')}}
  base_snapshot=${SUPER_DOLPHIN_CI_REFRESH_BASE_SNAPSHOT:-${previous_base_snapshot:-$(json_required '.generation_one_provision.image_cache_snapshot_id')}}
  [[ "$base_image" =~ @sha256:([0-9a-f]{64})$ ]] || die 'base image must use an immutable sha256 digest'
  oci_base_image=${SUPER_DOLPHIN_CI_REFRESH_OCI_BASE_IMAGE:-$(json_required '.generation_one_provision.image')}
  [[ "$oci_base_image" =~ @sha256:([0-9a-f]{64})$ ]] || die 'OCI base image must use an immutable sha256 digest'
  oci_base_digest=${BASH_REMATCH[1]}
  if [[ "$oci_base_digest" == "$accepted_base_digest" ]]; then
    base_archive_sha256=${SUPER_DOLPHIN_CI_REFRESH_BASE_ARCHIVE_SHA256:-$accepted_base_archive_sha256}
  else
    base_archive_sha256=${SUPER_DOLPHIN_CI_REFRESH_BASE_ARCHIVE_SHA256:-}
  fi
  [[ "$base_archive_sha256" =~ ^[0-9a-f]{64}$ ]] || die 'a non-default base image requires SUPER_DOLPHIN_CI_REFRESH_BASE_ARCHIVE_SHA256'
  base_archive_object="${source_prefix}baseline-refresh/images/sha256-${oci_base_digest}.tar"
  vswitches=()
  while IFS= read -r vswitch; do
    vswitches[${#vswitches[@]}]=$vswitch
  done < <(jq -er '.vswitches | select(length >= 2 and length <= 10) | .[].id' "$config")
  ((${#vswitches[@]} >= 2)) || die 'config must contain at least two vSwitches'
  vswitch_csv=$(IFS=,; printf '%s' "${vswitches[*]}")

  create_source_archive
  create_module_download_cache
  if [[ "$dry_run" == true ]]; then print_plan; return; fi
  upload_if_missing "$source_archive" "$source_object"
  upload_if_missing "$module_archive" "$module_object"
  oss_object_exists "$registry_object" || die "missing pinned registry tool: $registry_object"
  oss_object_exists "$crane_object" || die "missing pinned crane tool: $crane_object"
  oss_object_exists "$base_archive_object" || die "missing accepted base image archive: $base_archive_object"
  start_builder
  create_image_cache
  retire_builder
  verify_image_cache
  persist_receipt
}

main "$@"
