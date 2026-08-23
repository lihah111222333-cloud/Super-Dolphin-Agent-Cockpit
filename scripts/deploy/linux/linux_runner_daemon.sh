#!/usr/bin/env bash
set -Eeuo pipefail

export LOCAL_NON_AUTHORITATIVE=1
export NOT_VERIFIED=1
export SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE
export SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED

emit_event() {
  local stage=$1 state=$2 code=$3 runtime_value=${4:-} cache_scope=${5:-none}
  printf '{"schema_version":"remote-ci-helper-log/v1","authority":"LOCAL_NON_AUTHORITATIVE","stage":"%s","state":"%s","code":"%s","runtime":"%s","cache_scope":"%s"}\n' \
    "$stage" "$state" "$code" "$runtime_value" "$cache_scope" >&2
}

die() { emit_event runner failed runner_contract_error "${docker_runtime_marker:-none}" "${SUPER_DOLPHIN_CACHE_SCOPE:-none}"; exit 2; }

cd / || die 'unable to enter the fixed runner working directory'

repo_path=''
execution_repo_path=''
private_worktree_root=''
seed_head_before=''
seed_status_before=''
mode='docker'
mode_seen=0
timeout_seconds=120
cpu_milli=''
cpu_milli_seen=0
memory_bytes=''
memory_bytes_seen=0
alternates=''
alternates_seen=0
shared_root=''
shared_root_seen=0
cache_root=''
cache_root_seen=0
repository_id=''
repository_id_seen=0
node_id=''
node_id_seen=0
toolchain_identity=''
toolchain_identity_seen=0
goos=''
goos_seen=0
goarch=''
goarch_seen=0
cgo_enabled=''
cgo_enabled_seen=0
module_identity=''
module_identity_seen=0
workload_identity=''
workload_identity_seen=0
object_format=''
object_format_seen=0
source_commit=''
source_commit_seen=0
source_tree=''
source_tree_seen=0
shared_mode=0
shared_namespace=''
cache_namespace=''
shared_objects=''
shared_cache_cas=''
shared_cache_manifests=''
node_overlay_root=''
node_gomodcache=''
node_gocache=''
node_tmp_root=''
node_write_overlay=''
operation_overlay_root=''
run_tmp_root=''
shared_cache_lock=''
shared_cache_lock_held=0
shared_cache_ready=''
cache_seed_state='cold'
cache_identity=''
cache_generation=''
command_args=()
docker_image=''
docker_image_seen=0
docker_executable=''
docker_executable_seen=0
docker_host=''
docker_host_seen=0
docker_server_id=''
docker_server_id_seen=0
runner_identity=''
runner_identity_seen=0
docker_image_identity=''
docker_image_identity_seen=0
runtime=''
runtime_seen=0
docker_runtime_marker=''
worktree_root=''
worktree_root_seen=0
private_overlay_root=''
private_overlay_root_seen=0
temp_root=''
temp_root_seen=0
wine_executable=''
wine_executable_seen=0
wine_executable_identity=''
wine_executable_identity_seen=0
wine_server_executable=''
wine_server_executable_seen=0
wine_server_executable_identity=''
wine_server_executable_identity_seen=0
wine_prefix_root=''
wine_prefix_root_seen=0
wine_run_prefix_root=''
docker_container_name=''
docker_container_started=0
child_stderr_log=''
worker_binary=''
worker_binary_seen=0
worker_identity=''
worker_identity_seen=0
readonly max_int64=9223372036854775807

validate_absolute_clean_path() {
  local label=$1 value=$2 remainder component current=/
  [[ -n "$value" && "$value" = /* ]] || die "$label path must be absolute"
  [[ ! "$value" =~ [[:cntrl:]] ]] || die "$label path contains control characters"
  case "$value" in
    *'//'*) die "$label path must be clean" ;;
    */./*|*/.|*/../*|*/..) die "$label path must be clean" ;;
    */) [[ "$value" == / ]] || die "$label path must be clean" ;;
  esac
  remainder=${value#/}
  while [[ -n "$remainder" ]]; do
    if [[ "$remainder" == */* ]]; then
      component=${remainder%%/*}
      remainder=${remainder#*/}
    else
      component=$remainder
      remainder=''
    fi
    [[ -n "$component" ]] || die "$label path must be clean"
    if [[ "$current" == / ]]; then
      current=/$component
    else
      current=$current/$component
    fi
    [[ ! -L "$current" ]] || die "$label path contains a symlink"
  done
}

validate_docker_host() {
  local value=$1 socket_path
  [[ "$value" == unix:///* ]] || die '--docker-host must use unix://<absolute path>'
  socket_path=${value#unix://}
  validate_absolute_clean_path '--docker-host socket' "$socket_path"
  [[ -S "$socket_path" && ! -L "$socket_path" ]] || die '--docker-host must point to a non-symlink Unix socket'
}

require_safe_namespace_id() {
  local value=$1 label=$2
  [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || die "$label must be a safe repository/node identity"
}

require_safe_docker_server_id() {
  local value=$1
  [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$ ]] || die '--docker-server-id must be a safe opaque ID'
}

require_canonical_sha256() {
  local value=$1 label=$2
  [[ "$value" =~ ^sha256:[a-f0-9]{64}$ ]] || die "$label must be canonical sha256:<64 lowercase hex>"
}

attest_runner_identity() {
  local runner_path=$0 runner_actual
  validate_absolute_clean_path '--runner-executable' "$runner_path"
  [[ -f "$runner_path" && ! -L "$runner_path" && -x "$runner_path" ]] || die '--runner-executable must be a regular non-symlink executable'
  command -v sha256sum >/dev/null 2>&1 || die 'sha256sum is required for runner identity'
  runner_actual=$(sha256sum -- "$runner_path") || die '--runner-executable cannot be hashed'
  [[ "sha256:${runner_actual%% *}" == "$runner_identity" ]] || die '--runner-executable does not match --runner-identity'
}

require_immutable_docker_image() {
  local value=$1
  [[ "$value" =~ ^sha256:[a-f0-9]{64}$ || "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._/:+-]*@sha256:[a-f0-9]{64}$ ]] || \
    die '--docker-image must be an immutable sha256 image ID or repository@sha256 digest'
}

require_git_object_id() {
  local value=$1 format=$2 label=$3
  if [[ "$format" == sha1 ]]; then
    [[ "$value" =~ ^[a-f0-9]{40}$ ]] || die "$label must be a canonical SHA-1 Git object ID"
  elif [[ "$format" == sha256 ]]; then
    [[ "$value" =~ ^[a-f0-9]{64}$ ]] || die "$label must be a canonical SHA-256 Git object ID"
  else
    die '--object-format must be sha1 or sha256'
  fi
}

require_real_directory() {
  local path=$1 label=$2
  [[ -d "$path" && ! -L "$path" && -r "$path" && -x "$path" ]] || die "$label must be a readable directory"
}

require_absolute_executable() {
  local path=$1 label=$2
  validate_absolute_clean_path "$label" "$path"
  [[ -f "$path" && ! -L "$path" && -x "$path" ]] || die "$label must be an executable regular file"
}

emit_runner_status() {
  local state=$1 code=$2 cache_scope=${3:-none}
  emit_event runner "$state" "$code" "${docker_runtime_marker:-none}" "$cache_scope"
}

emit_child_stderr_event() {
  local bytes
  [[ -n "$child_stderr_log" && -f "$child_stderr_log" ]] || return 0
  bytes=$(wc -c <"$child_stderr_log" | tr -d '[:space:]') || return 1
  [[ "$bytes" =~ ^[0-9]+$ ]] || return 1
  emit_event child status child_stderr_capture "$runtime" "${SUPER_DOLPHIN_CACHE_SCOPE:-none}"
  rm -f -- "$child_stderr_log" || return 1
}

prepare_private_source() {
  local target_object_format target_tree private_repo private_git_dir
  validate_absolute_clean_path '--repo' "$repo_path"
  validate_absolute_clean_path '--worktree-root' "$worktree_root"
  require_real_directory "$repo_path" '--repo'
  require_real_directory "$worktree_root" '--worktree-root'
  seed_head_before=$(git -C "$repo_path" rev-parse HEAD 2>/dev/null) || die 'unable to resolve seed repository HEAD'
  seed_status_before=$(git -C "$repo_path" status --porcelain=v1 --untracked-files=all --ignored=matching 2>/dev/null) || die 'unable to read seed repository status'
  target_object_format=$(git -C "$repo_path" rev-parse --show-object-format 2>/dev/null) || die 'unable to resolve seed Git object format'
  [[ "$target_object_format" == "$object_format" ]] || die 'seed Git object format does not match --object-format'
  git -C "$repo_path" cat-file -e "${source_commit}^{commit}" 2>/dev/null || die 'seed repository cannot resolve source commit'
  target_tree=$(git -C "$repo_path" rev-parse "${source_commit}^{tree}" 2>/dev/null) || die 'seed repository cannot resolve source tree'
  [[ "$target_tree" == "$source_tree" ]] || die 'seed repository source tree does not match request'
  private_worktree_root=$(mktemp -d "$worktree_root/runner.XXXXXX") || die 'private worktree directory cannot be created'
  chmod 700 "$private_worktree_root" || die 'private worktree permissions cannot be set'
  private_repo="$private_worktree_root/repository"
  git clone --quiet --shared --no-checkout "$repo_path" "$private_repo" >/dev/null 2>&1 || die 'private source repository cannot be created'
  if ((shared_mode != 0)); then
    private_git_dir="$private_repo/$(git -C "$private_repo" rev-parse --git-dir 2>/dev/null)" || die 'private Git directory cannot be resolved'
    mkdir -p "$private_git_dir/objects/info" || die 'private Git alternates directory cannot be created'
    [[ ! -L "$private_git_dir/objects/info/alternates" ]] || die 'private Git alternates path is unsafe'
    printf '%s\n' "$shared_objects" >"$private_git_dir/objects/info/alternates" || die 'private Git alternates cannot be bound'
    chmod 444 "$private_git_dir/objects/info/alternates" || die 'private Git alternates permissions cannot be set'
  fi
  git -C "$private_repo" -c advice.detachedHead=false checkout --quiet --detach "$source_commit" >/dev/null 2>&1 || die 'private source commit cannot be materialized'
  execution_repo_path=$(git -C "$private_repo" rev-parse --show-toplevel 2>/dev/null) || die 'private source root cannot be resolved'
  [[ "$execution_repo_path" == "$private_repo" ]] || die 'private source root is not canonical'
  [[ "$(git -C "$execution_repo_path" rev-parse HEAD)" == "$source_commit" ]] || die 'private source commit drifted'
  [[ "$(git -C "$execution_repo_path" rev-parse 'HEAD^{tree}')" == "$source_tree" ]] || die 'private source tree drifted'
  [[ -z "$(git -C "$execution_repo_path" status --porcelain=v1 --untracked-files=all --ignored=matching)" ]] || die 'private source tree is not clean'
}

path_is_within() {
  local child=$1 parent=$2
  [[ "$child" == "$parent" || "$child" == "$parent"/* ]]
}

validate_cache_tree() {
  local directory=$1 label=$2 links
  require_real_directory "$directory" "$label"
  links=$(find "$directory" -type l -print -quit 2>/dev/null) || die "$label cannot be inspected"
  [[ -z "$links" ]] || die "$label must not contain symlinks"
}

acquire_shared_cache_lock() {
  [[ ! -L "$shared_cache_lock" && (! -e "$shared_cache_lock" || -f "$shared_cache_lock") ]] || die 'shared Ubuntu cache lock path is unsafe'
  exec 9>>"$shared_cache_lock" || die 'shared Ubuntu cache lock cannot be opened'
  [[ -f "$shared_cache_lock" && ! -L "$shared_cache_lock" ]] || die 'shared Ubuntu cache lock path changed while opening'
  flock -w 30 9 || die 'shared Ubuntu cache synchronization is busy'
  shared_cache_lock_held=1
}

release_shared_cache_lock() {
  if ((shared_cache_lock_held != 0)); then
    flock -u 9 || die 'shared Ubuntu cache synchronization lock cannot be released'
    exec 9>&-
    shared_cache_lock_held=0
  fi
}

cache_file_is_reusable() {
  local kind=$1 relative=$2 base=${2##*/}
  if [[ "$kind" == module ]] && [[ "$base" == *.lock || "$base" == *.tmp || "$base" == *.partial || "$base" == *.tmp.* || "$base" == *.tmp-* ]]; then
    return 1
  fi
  if [[ "$kind" == build ]] && [[ "$relative" == README || "$relative" == trim.txt ]]; then
    return 1
  fi
  return 0
}

stage_cache_tree() {
  local source=$1 kind=$2 manifest=$3 blobs=$4 label=$5
  local list relative digest size staged
  validate_cache_tree "$source" "$label"
  list=$(mktemp "$node_tmp_root/.cache-files.XXXXXX") || die "$label file list cannot be created"
  if ! (cd -- "$source" && find . -type f -print0 >"$list"); then
    rm -f -- "$list"
    die "$label cannot be enumerated completely"
  fi
  while IFS= read -r -d '' relative; do
    relative=${relative#./}
    [[ -n "$relative" && ! "$relative" =~ [[:cntrl:]] && "$relative" != /* && "$relative" != ../* && "$relative" != */../* ]] || die "$label contains an unsafe cache path"
    cache_file_is_reusable "$kind" "$relative" || continue
    digest=$(sha256sum -- "$source/$relative") || die "$label cache file cannot be hashed"
    digest=${digest%% *}
    [[ "$digest" =~ ^[a-f0-9]{64}$ ]] || die "$label cache file digest is invalid"
    size=$(wc -c <"$source/$relative" | tr -d '[:space:]') || die "$label cache file size cannot be read"
    [[ "$size" =~ ^[0-9]+$ ]] || die "$label cache file size is invalid"
    staged="$blobs/$digest"
    if [[ ! -e "$staged" ]]; then
      cp -p -- "$source/$relative" "$staged" || die "$label cache blob cannot be staged"
      [[ "$(sha256sum -- "$staged")" == "$digest "* ]] || die "$label staged cache blob digest drifted"
    fi
    printf 'file\t%s\t%s\t%s\t%s\n' "$kind" "$digest" "$size" "$relative" >>"$manifest" || die "$label manifest cannot be written"
  done <"$list"
  rm -f -- "$list"
}

install_cache_blob() {
  local blob=$1 digest=$2 relative=$3 destination=$4 label=$5 target parent temporary actual
  target="$destination/$relative"
  parent=${target%/*}
  mkdir -p -- "$parent" || die "$label destination directory cannot be created"
  [[ -d "$parent" && ! -L "$parent" ]] || die "$label destination directory is unsafe"
  if [[ -e "$target" ]]; then
    [[ -f "$target" && ! -L "$target" ]] || die "$label destination entry is not a regular file"
    actual=$(sha256sum -- "$target") || die "$label existing entry cannot be hashed"
	if [[ "${actual%% *}" == "$digest" ]]; then
	  return 0
	fi
  fi
  temporary="$parent/.${target##*/}.$$.tmp"
  [[ ! -e "$temporary" ]] || die "$label staging file already exists"
  cp -p -- "$blob" "$temporary" || die "$label staging file cannot be copied"
	mv -f -- "$temporary" "$target" || die "$label cache file cannot be installed atomically"
}

validate_cache_generation() {
  local manifest=$1 expected=$2 install=$3 line_number=0 previous='' record kind digest size relative extra blob actual_size actual_digest destination
  [[ -f "$manifest" && ! -L "$manifest" && ! -w "$manifest" ]] || die 'shared cache generation manifest must be an immutable regular file'
  actual_digest=$(sha256sum -- "$manifest") || die 'shared cache generation manifest cannot be hashed'
  [[ "sha256:${actual_digest%% *}" == "$expected" ]] || die 'shared cache generation manifest digest drifted'
  while IFS=$'\t' read -r record kind digest size relative extra; do
    line_number=$((line_number + 1))
    case "$line_number" in
      1) [[ "$record" == 'schema_version=ubuntu-shared-cache-generation/v1' && -z "$kind$relative$extra" ]] || die 'shared cache generation schema drifted' ;;
      2) [[ "$record" == "repository_id=$repository_id" && -z "$kind$relative$extra" ]] || die 'shared cache generation repository identity drifted' ;;
      3) [[ "$record" == "cache_identity=$cache_identity" && -z "$kind$relative$extra" ]] || die 'shared cache generation identity drifted' ;;
      *)
        [[ "$record" == file && ("$kind" == module || "$kind" == build) && "$digest" =~ ^[a-f0-9]{64}$ && "$size" =~ ^[0-9]+$ && -n "$relative" && -z "$extra" ]] || die 'shared cache generation record is invalid'
        [[ ! "$relative" =~ [[:cntrl:]] && "$relative" != /* && "$relative" != ../* && "$relative" != */../* ]] || die 'shared cache generation path is unsafe'
        [[ -z "$previous" || "$previous" < "$record"$'\t'"$kind"$'\t'"$digest"$'\t'"$size"$'\t'"$relative" ]] || die 'shared cache generation records are not strictly sorted'
        previous="$record"$'\t'"$kind"$'\t'"$digest"$'\t'"$size"$'\t'"$relative"
        blob="$shared_cache_cas/${digest:0:2}/$digest"
        [[ -f "$blob" && ! -L "$blob" && ! -w "$blob" ]] || die 'shared cache generation blob must be an immutable regular file'
        actual_size=$(wc -c <"$blob" | tr -d '[:space:]') || die 'shared cache generation blob size cannot be read'
        [[ "$actual_size" == "$size" ]] || die 'shared cache generation blob size drifted'
        actual_digest=$(sha256sum -- "$blob") || die 'shared cache generation blob cannot be hashed'
        [[ "${actual_digest%% *}" == "$digest" ]] || die 'shared cache generation blob digest drifted'
        if ((install != 0)); then
          if [[ "$kind" == module ]]; then destination="$node_gomodcache/cache/download"; else destination="$node_gocache"; fi
          install_cache_blob "$blob" "$digest" "$relative" "$destination" "shared $kind cache"
        fi
        ;;
    esac
  done <"$manifest"
  ((line_number >= 3)) || die 'shared cache generation manifest is incomplete'
  validated_generation_files=$((line_number - 3))
}

current_cache_generation() {
  local digest manifest
  current_generation=''
  current_manifest=''
  if [[ ! -e "$shared_cache_ready" ]]; then return 0; fi
  [[ -f "$shared_cache_ready" && ! -L "$shared_cache_ready" ]] || die 'shared cache ready pointer must be a regular non-symlink file'
  IFS= read -r digest <"$shared_cache_ready" || die 'shared cache ready pointer cannot be read'
  require_canonical_sha256 "$digest" 'shared cache ready pointer'
  [[ $(wc -l <"$shared_cache_ready" | tr -d '[:space:]') == 1 ]] || die 'shared cache ready pointer has trailing data'
  manifest="$shared_cache_manifests/${digest#sha256:}.manifest"
  current_generation=$digest
  current_manifest=$manifest
}

bind_shared_cache_manifest() {
  local expected temporary manifest="$cache_namespace/cache-identity.txt"
  expected=$(mktemp "$node_tmp_root/.cache-identity.XXXXXX") || die 'cache identity manifest cannot be created'
  printf '%s\n' \
    'schema_version=ubuntu-shared-cache/v1' \
    "repository_id=$repository_id" \
    "cache_identity=$cache_identity" \
    "toolchain_identity=$toolchain_identity" \
    "goos=$goos" \
    "goarch=$goarch" \
    "cgo_enabled=$cgo_enabled" \
    "module_identity=$module_identity" \
    "runtime=$runtime" >"$expected" || die 'cache identity manifest cannot be written'
  if [[ -e "$manifest" ]]; then
    [[ -f "$manifest" && ! -L "$manifest" ]] || die 'shared cache identity manifest must be a regular non-symlink file'
    cmp -s -- "$expected" "$manifest" || die 'shared cache identity manifest drift detected'
  else
    temporary="$cache_namespace/.cache-identity.$$"
    [[ ! -e "$temporary" ]] || die 'cache identity staging file already exists'
    cp -- "$expected" "$temporary" || die 'cache identity staging file cannot be written'
    chmod 600 "$temporary" || die 'cache identity staging permissions cannot be set'
    mv -- "$temporary" "$manifest" || die 'cache identity manifest cannot be installed atomically'
  fi
  rm -f -- "$expected"
}

sync_shared_cache_to_node() {
  acquire_shared_cache_lock
  bind_shared_cache_manifest
  release_shared_cache_lock
  current_cache_generation
  if [[ -n "$current_generation" ]]; then
    validate_cache_generation "$current_manifest" "$current_generation" 1
    ((validated_generation_files > 0)) || die 'shared cache ready pointer references an empty generation'
    cache_seed_state=ready
    cache_generation=$current_generation
  fi
}

publish_node_cache() {
	local stage blobs node_records base_records base_manifest current_records combined_records sorted_records final_manifest manifest_digest manifest_target ready_stage generation_changed
  local blob digest target parent temporary actual
  stage=$(mktemp -d "$node_tmp_root/publish.XXXXXX") || die 'shared cache publication staging directory cannot be created'
  blobs="$stage/blobs"
  mkdir "$blobs" || die 'shared cache publication blob directory cannot be created'
  node_records="$stage/node-records"
  : >"$node_records"
  if [[ -d "$node_gomodcache/cache/download" && ! -L "$node_gomodcache/cache/download" ]]; then
    stage_cache_tree "$node_gomodcache/cache/download" module "$node_records" "$blobs" 'node GOMODCACHE download cache'
  fi
  stage_cache_tree "$node_gocache" build "$node_records" "$blobs" 'node GOCACHE'
  LC_ALL=C sort -u "$node_records" -o "$node_records" || die 'node cache manifest cannot be sorted'
  acquire_shared_cache_lock
  bind_shared_cache_manifest
  current_cache_generation
	base_records="$stage/base-records"
	: >"$base_records"
	if [[ -n "$cache_generation" ]]; then
	  base_manifest="$shared_cache_manifests/${cache_generation#sha256:}.manifest"
	  validate_cache_generation "$base_manifest" "$cache_generation" 0
	  tail -n +4 -- "$base_manifest" >"$base_records" || die 'base shared cache generation cannot be read'
	fi
  current_records="$stage/current-records"
  : >"$current_records"
  if [[ -n "$current_manifest" ]]; then
    validate_cache_generation "$current_manifest" "$current_generation" 0
    tail -n +4 -- "$current_manifest" >"$current_records" || die 'current shared cache generation cannot be read'
  fi
  combined_records="$stage/combined-records"
	if [[ "$cache_generation" == "$current_generation" ]]; then generation_changed=0; else generation_changed=1; fi
  if ! awk -F '\t' -v changed="$generation_changed" '
    NF != 5 || $1 != "file" { exit 41 }
    FILENAME == ARGV[1] { key=$2 "\t" $5; base[key]=$3 "\t" $4; next }
    FILENAME == ARGV[2] { key=$2 "\t" $5; current[key]=$3 "\t" $4; current_line[key]=$0; next }
    FILENAME == ARGV[3] {
      key=$2 "\t" $5; value=$3 "\t" $4
      if (changed && key in current && current[key] != value && (!(key in base) || current[key] != base[key])) exit 42
      node[key]=value; node_line[key]=$0; next
    }
    END {
      for (key in current_line) if (!(key in node_line)) print current_line[key]
      for (key in node_line) print node_line[key]
    }
  ' "$base_records" "$current_records" "$node_records" >"$combined_records"; then
    die 'shared cache generation path collision detected'
  fi
  sorted_records="$stage/sorted-records"
  LC_ALL=C sort -u "$combined_records" >"$sorted_records" || die 'shared cache generation records cannot be sorted'
  if [[ ! -s "$sorted_records" ]]; then
    release_shared_cache_lock
    rm -rf -- "$stage"
    emit_event cache status cache_publish_empty "$runtime" ubuntu_shared
    return 0
  fi
  for blob in "$blobs"/*; do
    [[ -e "$blob" ]] || break
    digest=${blob##*/}
    [[ "$digest" =~ ^[a-f0-9]{64}$ ]] || die 'staged shared cache blob name is invalid'
    parent="$shared_cache_cas/${digest:0:2}"
    mkdir -p -- "$parent" || die 'shared cache CAS shard cannot be created'
    target="$parent/$digest"
    if [[ -e "$target" ]]; then
      [[ -f "$target" && ! -L "$target" && ! -w "$target" ]] || die 'shared cache CAS target must be an immutable regular file'
      actual=$(sha256sum -- "$target") || die 'shared cache CAS target cannot be hashed'
      [[ "${actual%% *}" == "$digest" ]] || die 'shared cache CAS digest collision detected'
    else
      temporary="$parent/.$digest.$$.tmp"
      cp -p -- "$blob" "$temporary" || die 'shared cache CAS blob cannot be copied'
      chmod 444 "$temporary" || die 'shared cache CAS staging blob cannot be made immutable'
      mv -- "$temporary" "$target" || die 'shared cache CAS blob cannot be installed atomically'
    fi
  done
  final_manifest="$stage/final.manifest"
  {
    printf '%s\n' 'schema_version=ubuntu-shared-cache-generation/v1'
    printf 'repository_id=%s\n' "$repository_id"
    printf 'cache_identity=%s\n' "$cache_identity"
    cat -- "$sorted_records"
  } >"$final_manifest" || die 'shared cache generation manifest cannot be assembled'
  manifest_digest=$(sha256sum -- "$final_manifest") || die 'shared cache generation manifest cannot be hashed'
  manifest_digest=${manifest_digest%% *}
  [[ "$manifest_digest" =~ ^[a-f0-9]{64}$ ]] || die 'shared cache generation digest is invalid'
  manifest_target="$shared_cache_manifests/$manifest_digest.manifest"
  if [[ -e "$manifest_target" ]]; then
    [[ -f "$manifest_target" && ! -L "$manifest_target" && ! -w "$manifest_target" ]] || die 'shared cache generation manifest must be immutable'
    cmp -s -- "$final_manifest" "$manifest_target" || die 'shared cache manifest CAS collision detected'
  else
    temporary="$shared_cache_manifests/.$manifest_digest.$$.tmp"
    cp -- "$final_manifest" "$temporary" || die 'shared cache generation manifest cannot be copied'
    chmod 444 "$temporary" || die 'shared cache generation manifest cannot be made immutable'
    mv -- "$temporary" "$manifest_target" || die 'shared cache generation manifest cannot be installed atomically'
  fi
  ready_stage="$cache_namespace/.ready.$$"
  printf 'sha256:%s\n' "$manifest_digest" >"$ready_stage" || die 'shared cache ready pointer cannot be staged'
  chmod 600 "$ready_stage" || die 'shared cache ready pointer permissions cannot be set'
  mv -f -- "$ready_stage" "$shared_cache_ready" || die 'shared cache ready pointer cannot be published atomically'
  cache_generation="sha256:$manifest_digest"
  release_shared_cache_lock
  rm -rf -- "$stage"
  emit_event cache status cache_publish_complete "$runtime" ubuntu_shared
}

# shellcheck disable=SC2329 # trap 通过函数名在共享缓存模式下调用该清理边界。
cleanup_shared_cache_state() {
  local status=$?
  if ((docker_container_started != 0)); then
    cleanup_docker_container || status=2
  fi
  if [[ -n "$run_tmp_root" && -d "$run_tmp_root" ]]; then
    rm -rf -- "$run_tmp_root" || status=2
  fi
  if [[ -n "$wine_run_prefix_root" && -d "$wine_run_prefix_root" ]]; then
    rm -rf -- "$wine_run_prefix_root" || status=2
  fi
  if [[ -n "$operation_overlay_root" && -d "$operation_overlay_root" ]]; then
    rm -rf -- "$operation_overlay_root" || status=2
  fi
  if [[ -n "$private_worktree_root" && -d "$private_worktree_root" ]]; then
    rm -rf -- "$private_worktree_root" || status=2
  fi
  if ((shared_cache_lock_held != 0)); then
    flock -u 9 2>/dev/null || status=2
    exec 9>&-
    shared_cache_lock_held=0
  fi
  if [[ -n "$repo_path" && -n "$seed_head_before" ]]; then
    local head_now status_now
    head_now=$(git -C "$repo_path" rev-parse HEAD 2>/dev/null || true)
    status_now=$(git -C "$repo_path" status --porcelain=v1 --untracked-files=all --ignored=matching 2>/dev/null || true)
    if [[ "$head_now" != "$seed_head_before" || "$status_now" != "$seed_status_before" ]]; then
      emit_event runner failed fixed_repo_mutated "${docker_runtime_marker:-none}" "${SUPER_DOLPHIN_CACHE_SCOPE:-none}"
      status=2
    fi
  fi
  exit "$status"
}

setup_operation_roots() {
  operation_overlay_root=$(mktemp -d "$private_overlay_root/runner.XXXXXX") || die 'private overlay directory cannot be created'
  chmod 700 "$operation_overlay_root" || die 'private overlay permissions cannot be set'
  run_tmp_root=$(mktemp -d "$temp_root/runner.XXXXXX") || die 'private temp directory cannot be created'
  chmod 700 "$run_tmp_root" || die 'private temp permissions cannot be set'
  mkdir "$run_tmp_root/go" || die 'private Go temp directory cannot be created'
  node_tmp_root="$run_tmp_root"
  node_write_overlay="$operation_overlay_root"
  if [[ "$runtime" == ubuntu_wine_container ]]; then
    wine_run_prefix_root=$(mktemp -d "$wine_prefix_root/runner.XXXXXX") || die 'private Wine prefix directory cannot be created'
    chmod 700 "$wine_run_prefix_root" || die 'private Wine prefix permissions cannot be set'
  fi
  trap cleanup_shared_cache_state EXIT
}

chown_operation_owned_root() {
  local path=$1 label=$2
  require_real_directory "$path" "$label"
  command -v chown >/dev/null 2>&1 || die 'chown is required for container-owned private roots'
  chown -R -- 65532:65532 "$path" || die "$label ownership cannot be bound to the container user"
}

prepare_container_owned_roots() {
  chown_operation_owned_root "$private_worktree_root" 'private source worktree'
  chown_operation_owned_root "$operation_overlay_root" 'private overlay root'
  chown_operation_owned_root "$run_tmp_root" 'private temp root'
  if ((shared_mode != 0)); then
    chown_operation_owned_root "$node_gomodcache" 'node GOMODCACHE overlay'
    chown_operation_owned_root "$node_gocache" 'node GOCACHE overlay'
  fi
  if [[ "$runtime" == ubuntu_wine_container ]]; then
    chown_operation_owned_root "$wine_run_prefix_root" 'private Wine prefix root'
  fi
}

cleanup_docker_container() {
  local label
  [[ "$mode" == docker && -n "$docker_container_name" ]] || return 0
  if label=$("$docker_executable" container inspect --format '{{index .Config.Labels "super-dolphin.owner"}}' "$docker_container_name" 2>/dev/null); then
    [[ "$label" == "$workload_identity" ]] || return 1
    "$docker_executable" rm -f "$docker_container_name" >/dev/null 2>&1 || return 1
    ! "$docker_executable" container inspect --format '{{.Id}}' "$docker_container_name" >/dev/null 2>&1 || return 1
  fi
  docker_container_started=0
  return 0
}

setup_ubuntu_shared_cache() {
  local target_top_level target_object_format target_tree shared_tree missing_object identity_material identity_digest
  local object_identity object_identity_expected object_paths object_path writable_object=''
  shared_mode=1
  command -v sha256sum >/dev/null 2>&1 || die 'sha256sum is required for shared cache identity'
  command -v cmp >/dev/null 2>&1 || die 'cmp is required for shared cache identity'
  command -v flock >/dev/null 2>&1 || die 'flock is required for cross-node shared cache promotion'
  validate_absolute_clean_path '--repo' "$repo_path"
  validate_absolute_clean_path '--shared-root' "$shared_root"
  validate_absolute_clean_path '--cache-root' "$cache_root"
  require_real_directory "$shared_root" '--shared-root'
  require_real_directory "$cache_root" '--cache-root'
  if path_is_within "$shared_root" "$cache_root" || path_is_within "$cache_root" "$shared_root"; then
    die '--shared-root and --cache-root must be disjoint'
  fi
  require_safe_namespace_id "$repository_id" '--repository-id'
  require_safe_namespace_id "$node_id" '--node-id'
  require_canonical_sha256 "$toolchain_identity" '--toolchain-identity'
  require_safe_namespace_id "$goos" '--goos'
  require_safe_namespace_id "$goarch" '--goarch'
  [[ "$cgo_enabled" == 0 || "$cgo_enabled" == 1 ]] || die '--cgo-enabled must be 0 or 1'
  require_canonical_sha256 "$module_identity" '--module-identity'
  require_canonical_sha256 "$workload_identity" '--workload-identity'
  require_git_object_id "$source_commit" "$object_format" '--source-commit'
  require_git_object_id "$source_tree" "$object_format" '--source-tree'
  target_top_level=$(git -C "$repo_path" rev-parse --show-toplevel 2>/dev/null) || die 'unable to resolve target worktree'
  [[ "$target_top_level" == "$repo_path" ]] || die 'target worktree must be canonical and node-local'
  target_object_format=$(git -C "$repo_path" rev-parse --show-object-format 2>/dev/null) || die 'unable to resolve target Git object format'
  [[ "$target_object_format" == "$object_format" ]] || die 'target Git object format does not match --object-format'
  target_tree=$(git -C "$repo_path" rev-parse "${source_commit}^{tree}" 2>/dev/null) || die 'unable to resolve target tree'
  [[ "$target_tree" == "$source_tree" ]] || die 'target tree does not match --source-tree'
  path_is_within "$worktree_root" "$shared_root" && die '--worktree-root must not be inside --shared-root'
  path_is_within "$worktree_root" "$cache_root" && die '--worktree-root must not be inside --cache-root'
  path_is_within "$shared_root" "$worktree_root" && die '--shared-root must not be inside --worktree-root'
  path_is_within "$cache_root" "$worktree_root" && die '--cache-root must not be inside --worktree-root'

  shared_namespace="$shared_root/$repository_id"
  identity_material=$(printf '%s\n%s\n%s\n%s\n%s\n%s\n%s\n' \
    "$repository_id" "$toolchain_identity" "$goos" "$goarch" "$cgo_enabled" "$module_identity" "$runtime" | sha256sum) || die 'shared cache identity cannot be generated'
  identity_digest=${identity_material%% *}
  [[ "$identity_digest" =~ ^[a-f0-9]{64}$ ]] || die 'shared cache identity is invalid'
  cache_identity="sha256:$identity_digest"
  cache_namespace="$cache_root/$repository_id/identity-$identity_digest"
  require_real_directory "$shared_namespace" 'shared repository namespace'
  shared_objects="$shared_namespace/objects"
  validate_cache_tree "$shared_objects" 'shared Git object store'
  [[ -d "$shared_objects/info" && -d "$shared_objects/pack" ]] || die 'shared Git object store must be a bare object directory'
  [[ ! -e "$shared_objects/info/alternates" && ! -e "$shared_objects/info/http-alternates" ]] || die 'shared Git object store must not delegate to another object source'
  object_paths=$(mktemp "$cache_root/.object-paths.XXXXXX") || die 'shared Git object-store path list cannot be created'
  if ! find "$shared_objects" \( -type d -o -type f \) -print0 >"$object_paths"; then
    rm -f -- "$object_paths"
    die 'shared Git object store cannot be enumerated completely'
  fi
  while IFS= read -r -d '' object_path; do
    if [[ -w "$object_path" ]]; then writable_object=$object_path; break; fi
  done <"$object_paths"
  rm -f -- "$object_paths"
  [[ -z "$writable_object" ]] || die 'shared Git object store closure must be read-only to the runner'
  object_identity="$shared_namespace/object-store.identity"
  [[ -f "$object_identity" && ! -L "$object_identity" ]] || die 'shared Git object-store identity manifest is missing'
  object_identity_expected=$(printf '%s\n%s\n%s\n' \
    'schema_version=ubuntu-shared-git-object-store/v1' \
    "repository_id=$repository_id" \
    "object_format=$object_format") || die 'shared Git object-store identity cannot be constructed'
  [[ "$(<"$object_identity")" == "$object_identity_expected" ]] || die 'shared Git object-store identity manifest drifted'
  GIT_NO_REPLACE_OBJECTS=1 GIT_OBJECT_DIRECTORY="$shared_objects" GIT_ALTERNATE_OBJECT_DIRECTORIES='' git -C "$repo_path" cat-file -e "${source_commit}^{commit}" 2>/dev/null || die 'shared Git object store cannot resolve the exact source commit'
  shared_tree=$(GIT_NO_REPLACE_OBJECTS=1 GIT_OBJECT_DIRECTORY="$shared_objects" GIT_ALTERNATE_OBJECT_DIRECTORIES='' git -C "$repo_path" rev-parse "${source_commit}^{tree}" 2>/dev/null) || die 'shared Git object store cannot resolve the exact source tree'
  [[ "$shared_tree" == "$source_tree" ]] || die 'shared Git object store source tree drifted'
  missing_object=$(GIT_NO_REPLACE_OBJECTS=1 GIT_OBJECT_DIRECTORY="$shared_objects" GIT_ALTERNATE_OBJECT_DIRECTORIES='' git -C "$repo_path" rev-list --objects --missing=print "$source_commit" 2>/dev/null | awk '/^\?/ { print; exit }') || die 'shared Git object store closure cannot be enumerated'
  [[ -z "$missing_object" ]] || die 'shared Git object store does not contain the exact source closure'
  mkdir -p -- "$cache_namespace/shared" "$cache_namespace/nodes" || die 'cache repository namespace cannot be created'
  require_real_directory "$cache_namespace" 'cache identity namespace'
  require_real_directory "$cache_namespace/shared" 'shared cache namespace'
  require_real_directory "$cache_namespace/nodes" 'cache node namespace'

  if [[ -n "$alternates" ]]; then
    validate_absolute_clean_path '--alternates' "$alternates"
    [[ "$alternates" == "$shared_objects" ]] || die '--alternates conflicts with the repository shared object namespace'
  else
    alternates=$shared_objects
  fi
  shared_cache_cas="$cache_namespace/shared/cas"
  shared_cache_manifests="$cache_namespace/shared/manifests"
  mkdir -p -- "$shared_cache_cas" "$shared_cache_manifests" || die 'shared cache CAS directories cannot be created'
  validate_cache_tree "$shared_cache_cas" 'shared cache CAS root'
  validate_cache_tree "$shared_cache_manifests" 'shared cache manifest root'
  shared_cache_ready="$cache_namespace/ready"
  shared_cache_lock="$cache_namespace/.sync-lock"
  node_overlay_root="$cache_namespace/nodes/$node_id"
  if [[ -e "$node_overlay_root" ]]; then
    require_real_directory "$node_overlay_root" 'node cache overlay'
  else
    mkdir "$node_overlay_root" || die 'node cache overlay cannot be created atomically'
  fi
  node_gomodcache="$node_overlay_root/gomodcache"
  node_gocache="$node_overlay_root/gocache"
  mkdir -p -- "$node_gomodcache" "$node_gocache" || die 'node-local cache overlays cannot be created'
  validate_cache_tree "$node_gomodcache" 'node GOMODCACHE overlay'
  validate_cache_tree "$node_gocache" 'node GOCACHE overlay'
  sync_shared_cache_to_node
  unset GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_NO_REPLACE_OBJECTS
  export GOMODCACHE="$node_gomodcache"
  export GOCACHE="$node_gocache"
  export TMPDIR="$run_tmp_root"
  export GOTMPDIR="$run_tmp_root/go"
  export SUPER_DOLPHIN_CACHE_SCOPE=ubuntu_shared
  export SUPER_DOLPHIN_REPOSITORY_ID="$repository_id"
  export SUPER_DOLPHIN_NODE_ID="$node_id"
  export SUPER_DOLPHIN_CACHE_IDENTITY="$cache_identity"
  export SUPER_DOLPHIN_CACHE_SEED_STATE="$cache_seed_state"
  export SUPER_DOLPHIN_CACHE_GENERATION="$cache_generation"
  export SUPER_DOLPHIN_WORKLOAD_IDENTITY="$workload_identity"
  export SUPER_DOLPHIN_WRITE_OVERLAY="$node_write_overlay"
  export GOOS="$goos"
  export GOARCH="$goarch"
  export CGO_ENABLED="$cgo_enabled"
  emit_event cache status cache_sync_pending "$runtime" ubuntu_shared
}
require_bounded_positive_integer() {
  local value=$1 label=$2
  [[ "$value" =~ ^[1-9][0-9]*$ ]] || die "$label must be a positive decimal integer"
  if (( ${#value} > ${#max_int64} )); then
    die "$label overflows signed integer"
  fi
  # shellcheck disable=SC2071 # [[ > ]] is the required lexical comparison for equal-length decimal strings.
  if (( ${#value} == ${#max_int64} )) && [[ "$value" > "$max_int64" ]]; then
    die "$label overflows signed integer"
  fi
}

while (($#)); do
  case "$1" in
    --repo)
      (($# >= 2)) || die '--repo requires a value'
      repo_path=$2
      shift 2
      ;;
    --shared-root)
      (($# >= 2)) || die '--shared-root requires a value'
      ((shared_root_seen == 0)) || die '--shared-root must be provided once'
      shared_root=$2
      shared_root_seen=1
      shift 2
      ;;
    --cache-root)
      (($# >= 2)) || die '--cache-root requires a value'
      ((cache_root_seen == 0)) || die '--cache-root must be provided once'
      cache_root=$2
      cache_root_seen=1
      shift 2
      ;;
    --repository-id)
      (($# >= 2)) || die '--repository-id requires a value'
      ((repository_id_seen == 0)) || die '--repository-id must be provided once'
      repository_id=$2
      repository_id_seen=1
      shift 2
      ;;
    --node-id)
      (($# >= 2)) || die '--node-id requires a value'
      ((node_id_seen == 0)) || die '--node-id must be provided once'
      node_id=$2
      node_id_seen=1
      shift 2
      ;;
    --toolchain-identity)
      (($# >= 2)) || die '--toolchain-identity requires a value'
      ((toolchain_identity_seen == 0)) || die '--toolchain-identity must be provided once'
      toolchain_identity=$2
      toolchain_identity_seen=1
      shift 2
      ;;
    --goos)
      (($# >= 2)) || die '--goos requires a value'
      ((goos_seen == 0)) || die '--goos must be provided once'
      goos=$2
      goos_seen=1
      shift 2
      ;;
    --goarch)
      (($# >= 2)) || die '--goarch requires a value'
      ((goarch_seen == 0)) || die '--goarch must be provided once'
      goarch=$2
      goarch_seen=1
      shift 2
      ;;
    --cgo-enabled)
      (($# >= 2)) || die '--cgo-enabled requires a value'
      ((cgo_enabled_seen == 0)) || die '--cgo-enabled must be provided once'
      cgo_enabled=$2
      cgo_enabled_seen=1
      shift 2
      ;;
    --module-identity)
      (($# >= 2)) || die '--module-identity requires a value'
      ((module_identity_seen == 0)) || die '--module-identity must be provided once'
      module_identity=$2
      module_identity_seen=1
      shift 2
      ;;
    --workload-identity)
      (($# >= 2)) || die '--workload-identity requires a value'
      ((workload_identity_seen == 0)) || die '--workload-identity must be provided once'
      workload_identity=$2
      workload_identity_seen=1
      shift 2
      ;;
    --object-format)
      (($# >= 2)) || die '--object-format requires a value'
      ((object_format_seen == 0)) || die '--object-format must be provided once'
      object_format=$2
      object_format_seen=1
      shift 2
      ;;
    --source-commit)
      (($# >= 2)) || die '--source-commit requires a value'
      ((source_commit_seen == 0)) || die '--source-commit must be provided once'
      source_commit=$2
      source_commit_seen=1
      shift 2
      ;;
    --source-tree)
      (($# >= 2)) || die '--source-tree requires a value'
      ((source_tree_seen == 0)) || die '--source-tree must be provided once'
      source_tree=$2
      source_tree_seen=1
      shift 2
      ;;
    --mode)
      (($# >= 2)) || die '--mode requires a value'
      ((mode_seen == 0)) || die '--mode must be provided once'
      mode=$2
      mode_seen=1
      shift 2
      ;;
    --runtime)
      (($# >= 2)) || die '--runtime requires a value'
      ((runtime_seen == 0)) || die '--runtime must be provided once'
      runtime=$2
      runtime_seen=1
      shift 2
      ;;
    --docker-image)
      (($# >= 2)) || die '--docker-image requires a value'
      ((docker_image_seen == 0)) || die '--docker-image must be provided once'
      docker_image=$2
      docker_image_seen=1
      shift 2
      ;;
    --docker-executable)
      (($# >= 2)) || die '--docker-executable requires a value'
      ((docker_executable_seen == 0)) || die '--docker-executable must be provided once'
      docker_executable=$2
      docker_executable_seen=1
      shift 2
      ;;
    --docker-host)
      (($# >= 2)) || die '--docker-host requires a value'
      ((docker_host_seen == 0)) || die '--docker-host must be provided once'
      docker_host=$2
      docker_host_seen=1
      shift 2
      ;;
    --docker-server-id)
      (($# >= 2)) || die '--docker-server-id requires a value'
      ((docker_server_id_seen == 0)) || die '--docker-server-id must be provided once'
      docker_server_id=$2
      docker_server_id_seen=1
      shift 2
      ;;
    --runner-identity)
      (($# >= 2)) || die '--runner-identity requires a value'
      ((runner_identity_seen == 0)) || die '--runner-identity must be provided once'
      runner_identity=$2
      runner_identity_seen=1
      shift 2
      ;;
    --docker-image-identity)
      (($# >= 2)) || die '--docker-image-identity requires a value'
      ((docker_image_identity_seen == 0)) || die '--docker-image-identity must be provided once'
      docker_image_identity=$2
      docker_image_identity_seen=1
      shift 2
      ;;
    --worker-binary)
      (($# >= 2)) || die '--worker-binary requires a value'
      ((worker_binary_seen == 0)) || die '--worker-binary must be provided once'
      worker_binary=$2
      worker_binary_seen=1
      shift 2
      ;;
    --worker-identity)
      (($# >= 2)) || die '--worker-identity requires a value'
      ((worker_identity_seen == 0)) || die '--worker-identity must be provided once'
      worker_identity=$2
      worker_identity_seen=1
      shift 2
      ;;
    --worktree-root)
      (($# >= 2)) || die '--worktree-root requires a value'
      ((worktree_root_seen == 0)) || die '--worktree-root must be provided once'
      worktree_root=$2
      worktree_root_seen=1
      shift 2
      ;;
    --private-overlay-root)
      (($# >= 2)) || die '--private-overlay-root requires a value'
      ((private_overlay_root_seen == 0)) || die '--private-overlay-root must be provided once'
      private_overlay_root=$2
      private_overlay_root_seen=1
      shift 2
      ;;
    --temp-root)
      (($# >= 2)) || die '--temp-root requires a value'
      ((temp_root_seen == 0)) || die '--temp-root must be provided once'
      temp_root=$2
      temp_root_seen=1
      shift 2
      ;;
    --wine-executable)
      (($# >= 2)) || die '--wine-executable requires a value'
      ((wine_executable_seen == 0)) || die '--wine-executable must be provided once'
      wine_executable=$2
      wine_executable_seen=1
      shift 2
      ;;
    --wine-executable-identity)
      (($# >= 2)) || die '--wine-executable-identity requires a value'
      ((wine_executable_identity_seen == 0)) || die '--wine-executable-identity must be provided once'
      wine_executable_identity=$2
      wine_executable_identity_seen=1
      shift 2
      ;;
    --wine-server-executable)
      (($# >= 2)) || die '--wine-server-executable requires a value'
      ((wine_server_executable_seen == 0)) || die '--wine-server-executable must be provided once'
      wine_server_executable=$2
      wine_server_executable_seen=1
      shift 2
      ;;
    --wine-server-executable-identity)
      (($# >= 2)) || die '--wine-server-executable-identity requires a value'
      ((wine_server_executable_identity_seen == 0)) || die '--wine-server-executable-identity must be provided once'
      wine_server_executable_identity=$2
      wine_server_executable_identity_seen=1
      shift 2
      ;;
    --wine-prefix-root)
      (($# >= 2)) || die '--wine-prefix-root requires a value'
      ((wine_prefix_root_seen == 0)) || die '--wine-prefix-root must be provided once'
      wine_prefix_root=$2
      wine_prefix_root_seen=1
      shift 2
      ;;
    --timeout-seconds)
      (($# >= 2)) || die '--timeout-seconds requires a value'
      timeout_seconds=$2
      shift 2
      ;;
    --cpu-milli)
      (($# >= 2)) || die '--cpu-milli requires a value'
      ((cpu_milli_seen == 0)) || die '--cpu-milli must be provided once'
      cpu_milli=$2
      cpu_milli_seen=1
      shift 2
      ;;
    --memory-bytes)
      (($# >= 2)) || die '--memory-bytes requires a value'
      ((memory_bytes_seen == 0)) || die '--memory-bytes must be provided once'
      memory_bytes=$2
      memory_bytes_seen=1
      shift 2
      ;;
    --alternates)
      (($# >= 2)) || die '--alternates requires a value'
      ((alternates_seen == 0)) || die '--alternates must be provided once'
      alternates=$2
      alternates_seen=1
      shift 2
      ;;
    --)
      shift
      command_args=("$@")
      break
      ;;
    -h|--help)
      printf '%s\n' 'linux_runner_daemon.sh --repo PATH --mode docker --runtime ubuntu_container|ubuntu_wine_container --cpu-milli POSITIVE --memory-bytes POSITIVE --worktree-root PATH --private-overlay-root PATH --temp-root PATH --docker-executable PATH --docker-host unix://SOCKET --docker-server-id ID --runner-identity SHA256 --docker-image IMAGE --docker-image-identity SHA256 [--wine-executable PATH --wine-executable-identity SHA256 --wine-server-executable PATH --wine-server-executable-identity SHA256 --wine-prefix-root PATH] [--shared-root PATH --cache-root PATH --repository-id ID --node-id ID --object-format sha1|sha256 --source-commit OID --source-tree OID --toolchain-identity SHA256 --goos GOOS --goarch GOARCH --cgo-enabled 0|1 --module-identity SHA256 --workload-identity SHA256] [-- COMMAND...]'
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

[[ -n "$repo_path" && "$repo_path" = /* && -d "$repo_path" ]] || die 'an absolute existing --repo is required'
((worktree_root_seen == 1)) || die '--worktree-root is required'
((private_overlay_root_seen == 1)) || die '--private-overlay-root is required'
((temp_root_seen == 1)) || die '--temp-root is required'
validate_absolute_clean_path '--worktree-root' "$worktree_root"
validate_absolute_clean_path '--private-overlay-root' "$private_overlay_root"
validate_absolute_clean_path '--temp-root' "$temp_root"
require_real_directory "$worktree_root" '--worktree-root'
require_real_directory "$private_overlay_root" '--private-overlay-root'
require_real_directory "$temp_root" '--temp-root'
path_is_within "$repo_path" "$worktree_root" && die '--repo must not be inside --worktree-root'
path_is_within "$repo_path" "$private_overlay_root" && die '--repo must not be inside --private-overlay-root'
path_is_within "$repo_path" "$temp_root" && die '--repo must not be inside --temp-root'
path_is_within "$worktree_root" "$private_overlay_root" && die '--worktree-root and --private-overlay-root must be disjoint'
path_is_within "$worktree_root" "$temp_root" && die '--worktree-root and --temp-root must be disjoint'
path_is_within "$private_overlay_root" "$temp_root" && die '--private-overlay-root and --temp-root must be disjoint'
if [[ "$runtime" == ubuntu_wine_container ]]; then
  path_is_within "$repo_path" "$wine_prefix_root" && die '--repo must not be inside --wine-prefix-root'
  path_is_within "$worktree_root" "$wine_prefix_root" && die '--worktree-root and --wine-prefix-root must be disjoint'
  path_is_within "$private_overlay_root" "$wine_prefix_root" && die '--private-overlay-root and --wine-prefix-root must be disjoint'
  path_is_within "$temp_root" "$wine_prefix_root" && die '--temp-root and --wine-prefix-root must be disjoint'
  path_is_within "$wine_prefix_root" "$repo_path" && die '--wine-prefix-root must not contain --repo'
  path_is_within "$wine_prefix_root" "$worktree_root" && die '--wine-prefix-root must not contain --worktree-root'
  path_is_within "$wine_prefix_root" "$private_overlay_root" && die '--wine-prefix-root must not contain --private-overlay-root'
  path_is_within "$wine_prefix_root" "$temp_root" && die '--wine-prefix-root must not contain --temp-root'
fi
if ((shared_root_seen != cache_root_seen)); then
  die '--shared-root and --cache-root must be provided together'
fi
[[ "$mode" == docker ]] || die '--mode must be docker'
((mode_seen == 1)) || die '--mode is required'
if [[ "$mode" == docker ]]; then
  ((cpu_milli_seen == 1)) || die '--cpu-milli is required'
  ((memory_bytes_seen == 1)) || die '--memory-bytes is required'
  require_bounded_positive_integer "$cpu_milli" '--cpu-milli'
  require_bounded_positive_integer "$memory_bytes" '--memory-bytes'
  ((runtime_seen == 1)) || die '--runtime is required for docker mode'
  ((docker_image_seen == 1)) || die '--docker-image is required for docker mode'
  ((docker_executable_seen == 1)) || die '--docker-executable is required for docker mode'
  ((docker_host_seen == 1)) || die '--docker-host is required for docker mode'
  ((docker_server_id_seen == 1)) || die '--docker-server-id is required for docker mode'
  ((runner_identity_seen == 1)) || die '--runner-identity is required for docker mode'
  ((docker_image_identity_seen == 1)) || die '--docker-image-identity is required for docker mode'
  validate_docker_host "$docker_host"
  require_safe_docker_server_id "$docker_server_id"
  require_canonical_sha256 "$runner_identity" '--runner-identity'
  attest_runner_identity
  require_immutable_docker_image "$docker_image"
  require_absolute_executable "$docker_executable" '--docker-executable'
  require_canonical_sha256 "$docker_image_identity" '--docker-image-identity'
  case "$runtime" in
    ubuntu_container|ubuntu_wine_container)
      docker_runtime_marker=$runtime
      ;;
    *)
	  die '--runtime must be ubuntu_container or ubuntu_wine_container'
	  ;;
	esac
	export SUPER_DOLPHIN_RUNTIME="$docker_runtime_marker"
	if ((worker_binary_seen != worker_identity_seen)); then
	  die '--worker-binary and --worker-identity must be provided together'
	fi
	if ((worker_binary_seen == 1)); then
	  validate_absolute_clean_path '--worker-binary' "$worker_binary"
	  [[ -f "$worker_binary" && ! -L "$worker_binary" ]] || die '--worker-binary must be a regular file'
	  require_canonical_sha256 "$worker_identity" '--worker-identity'
	  command -v sha256sum >/dev/null 2>&1 || die 'sha256sum is required for worker identity'
	  worker_actual=$(sha256sum -- "$worker_binary") || die '--worker-binary cannot be hashed'
	  [[ "sha256:${worker_actual%% *}" == "$worker_identity" ]] || die '--worker-binary does not match --worker-identity'
	fi
  if [[ "$runtime" == ubuntu_wine_container ]]; then
    ((wine_executable_seen == 1 && wine_executable_identity_seen == 1 && wine_server_executable_seen == 1 && wine_server_executable_identity_seen == 1 && wine_prefix_root_seen == 1)) || die 'Wine executable, identity, server, and prefix flags are required once'
    validate_absolute_clean_path '--wine-executable' "$wine_executable"
    validate_absolute_clean_path '--wine-server-executable' "$wine_server_executable"
    require_canonical_sha256 "$wine_executable_identity" '--wine-executable-identity'
    require_canonical_sha256 "$wine_server_executable_identity" '--wine-server-executable-identity'
    validate_absolute_clean_path '--wine-prefix-root' "$wine_prefix_root"
    require_real_directory "$wine_prefix_root" '--wine-prefix-root'
    [[ "$goos" == windows && "$goarch" == amd64 && "$cgo_enabled" == 0 ]] || die 'Wine runtime requires windows/amd64 with cgo_enabled=0'
  else
    ((wine_executable_seen == 0 && wine_executable_identity_seen == 0 && wine_server_executable_seen == 0 && wine_server_executable_identity_seen == 0 && wine_prefix_root_seen == 0)) || die 'Wine flags are forbidden for ubuntu_container'
  fi
	fi
require_bounded_positive_integer "$timeout_seconds" '--timeout-seconds'
[[ "$timeout_seconds" -le 120 ]] || die '--timeout-seconds must be in 1..120'
[[ "$(uname -s)" == Linux ]] || die 'daemon only runs on Linux'
if [[ "$mode" == docker ]]; then
  unset DOCKER_CONTEXT
  export DOCKER_HOST="$docker_host"
fi
[[ ${#command_args[@]} -gt 0 ]] || die 'a command is required after --'
command -v git >/dev/null 2>&1 || die 'git is required'
git -C "$repo_path" rev-parse --git-dir >/dev/null 2>&1 || die 'unable to materialize Git repository identity'
((repository_id_seen == 1)) || die '--repository-id is required'
((node_id_seen == 1)) || die '--node-id is required'
((toolchain_identity_seen == 1)) || die '--toolchain-identity is required'
((goos_seen == 1)) || die '--goos is required'
((goarch_seen == 1)) || die '--goarch is required'
((cgo_enabled_seen == 1)) || die '--cgo-enabled is required'
((module_identity_seen == 1)) || die '--module-identity is required'
((workload_identity_seen == 1)) || die '--workload-identity is required'
((object_format_seen == 1)) || die '--object-format is required'
((source_commit_seen == 1)) || die '--source-commit is required'
((source_tree_seen == 1)) || die '--source-tree is required'
setup_operation_roots
if ((shared_root_seen == 1)); then
  setup_ubuntu_shared_cache
fi
prepare_private_source
prepare_container_owned_roots
format_cpu_milli() {
	local value=$1 whole fraction fraction_text
	whole=$((value / 1000))
	fraction=$((value % 1000))
	if ((fraction == 0)); then
	  printf '%s\n' "$whole"
	  return 0
	fi
	fraction_text=$(printf '%03d' "$fraction")
	while [[ "$fraction_text" == *0 ]]; do
	  fraction_text=${fraction_text%0}
	done
	printf '%s.%s\n' "$whole" "$fraction_text"
}

docker_cpu=$(format_cpu_milli "$cpu_milli")
if [[ -n "$alternates" && "$shared_mode" == 0 ]]; then
  [[ -d "$alternates" ]] || die '--alternates must name an existing directory'
  export GIT_ALTERNATE_OBJECT_DIRECTORIES="$alternates"
fi

if [[ "$mode" == docker ]]; then
  docker_server_actual=$("$docker_executable" info --format '{{.ID}}' 2>/dev/null) || die 'docker daemon is unavailable'
  [[ "$docker_server_actual" == "$docker_server_id" ]] || die 'Docker server ID does not match --docker-server-id'
  image_id=$("$docker_executable" image inspect --format '{{.Id}}' "$docker_image" 2>/dev/null) || die 'immutable Docker image cannot be inspected'
  [[ "$image_id" == "$docker_image_identity" ]] || die 'Docker image ID does not match --docker-image-identity'
  image_runtime=$("$docker_executable" image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$docker_image" 2>/dev/null) || die 'immutable Docker image cannot be inspected'
  grep -Fxq "SUPER_DOLPHIN_RUNTIME=$docker_runtime_marker" <<<"$image_runtime" || die 'immutable Docker image runtime marker does not match --runtime'
fi

if [[ "$mode" == docker ]]; then
  emit_runner_status status runner_ready "${SUPER_DOLPHIN_CACHE_SCOPE:-none}"
else
  emit_runner_status status runner_ready "${SUPER_DOLPHIN_CACHE_SCOPE:-none}"
fi

linux_process_group_exists() {
  local group_id=$1
  kill -0 -- "-$group_id" 2>/dev/null
}

signal_linux_process_group() {
  local group_id=$1 signal_name=$2
  if kill "-$signal_name" -- "-$group_id" 2>/dev/null; then
    return 0
  fi
  ! linux_process_group_exists "$group_id"
}

wait_linux_process_group_gone() {
  local group_id=$1 limit=$2 elapsed=0
  while linux_process_group_exists "$group_id"; do
    ((elapsed < limit)) || return 1
    sleep 1
    elapsed=$((elapsed + 1))
  done
}

terminate_linux_process_group() {
  local group_id=$1
  signal_linux_process_group "$group_id" TERM || return 1
  if wait_linux_process_group_gone "$group_id" 2; then
    return 0
  fi
  signal_linux_process_group "$group_id" KILL || return 1
  wait_linux_process_group_gone "$group_id" 2
}

run_command() {
	local child_pid status elapsed timed_out leader_exited ownership_probe_elapsed cleanup_failed
	local -a argv
	docker_container_name="super-dolphin-${workload_identity#sha256:}-$$"
		docker_container_name=${docker_container_name:0:120}
		argv=("$docker_executable" run --rm --name "$docker_container_name" --label "super-dolphin.owner=$workload_identity" --network none --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges --pids-limit 256 --platform linux/amd64 --cpus "$docker_cpu" --memory "$memory_bytes" -v "$execution_repo_path:/workspace:rw" -w /workspace)
		argv+=(
		  -v "$operation_overlay_root:/cache/write:rw"
		  -v "$run_tmp_root:/tmp:rw"
		)
	if ((shared_mode != 0)); then
	  argv+=(
	    -v "$shared_objects:$shared_objects:ro"
	    -v "$node_gomodcache:/cache/gomodcache:rw"
	    -v "$node_gocache:/cache/gocache:rw"
	  )
	fi
	if ((worker_binary_seen == 1)); then
	  argv+=(-v "$worker_binary:/opt/super-dolphin/bin/super-dolphin-heteroci-node:ro")
	fi
	if [[ "$runtime" == ubuntu_wine_container ]]; then
	  argv+=(-v "$wine_run_prefix_root:$wine_run_prefix_root:rw")
	fi
	argv+=(
	  -e LOCAL_NON_AUTHORITATIVE=1 -e NOT_VERIFIED=1
	  -e SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE -e SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED
		  -e "SUPER_DOLPHIN_RUNTIME=$docker_runtime_marker"
		)
	if ((shared_mode != 0)); then
	  argv+=(
	    -e SUPER_DOLPHIN_CACHE_SCOPE=ubuntu_shared
	    -e "SUPER_DOLPHIN_REPOSITORY_ID=$repository_id"
	    -e "SUPER_DOLPHIN_NODE_ID=$node_id"
	    -e "SUPER_DOLPHIN_CACHE_IDENTITY=$cache_identity"
	    -e "SUPER_DOLPHIN_CACHE_SEED_STATE=$cache_seed_state"
	    -e "SUPER_DOLPHIN_CACHE_GENERATION=$cache_generation"
	    -e "SUPER_DOLPHIN_WORKLOAD_IDENTITY=$workload_identity"
	    -e SUPER_DOLPHIN_WRITE_OVERLAY=/cache/write
	    -e GOMODCACHE=/cache/gomodcache
	    -e GOCACHE=/cache/gocache
	    -e TMPDIR=/tmp
	    -e GOTMPDIR=/tmp/go
	    -e "GOOS=$goos"
		    -e "GOARCH=$goarch"
		    -e "CGO_ENABLED=$cgo_enabled"
		  )
		fi
	if ((shared_mode != 0)); then
	  argv+=(-e "GIT_ALTERNATE_OBJECT_DIRECTORIES=$shared_objects")
	fi
	if [[ "$runtime" == ubuntu_wine_container ]]; then
	  argv+=(-e "WINEPREFIX=$wine_run_prefix_root")
	fi
	argv+=("$docker_image" "${command_args[@]}")
  command -v setsid >/dev/null 2>&1 || die 'setsid is required for process-group ownership'
  (
    cd -- "$execution_repo_path" || exit 125
    exec setsid "${argv[@]}" 2>"$run_tmp_root/child.stderr"
  ) &
  child_pid=$!
  if [[ "$mode" == docker ]]; then
    docker_container_started=1
  fi
  ownership_probe_elapsed=0
  while kill -0 "$child_pid" 2>/dev/null && ! linux_process_group_exists "$child_pid"; do
    if ((ownership_probe_elapsed >= 1)); then
      kill -KILL "$child_pid" 2>/dev/null || true
      wait "$child_pid" 2>/dev/null || true
      die 'Capacity/ownership blocker: command did not acquire an independent process group'
    fi
    sleep 1
    ownership_probe_elapsed=$((ownership_probe_elapsed + 1))
  done

  elapsed=0
  timed_out=0
  leader_exited=0
  cleanup_failed=0
  while linux_process_group_exists "$child_pid"; do
    if ! kill -0 "$child_pid" 2>/dev/null; then
      leader_exited=1
      break
    fi
    if ((elapsed >= timeout_seconds)); then
      timed_out=1
      break
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  if ((timed_out != 0 || leader_exited != 0)); then
    terminate_linux_process_group "$child_pid" || cleanup_failed=1
  fi
  if wait "$child_pid"; then
    status=0
  else
    status=$?
  fi
  child_stderr_log="$run_tmp_root/child.stderr"
  emit_child_stderr_event || cleanup_failed=1
  if ((cleanup_failed != 0)); then
    die 'Capacity/ownership blocker: process-group cleanup failed'
  fi
  if [[ "$mode" == docker ]]; then
    cleanup_docker_container || die 'Capacity/ownership blocker: Docker container cleanup failed'
  fi
  if linux_process_group_exists "$child_pid"; then
    die 'Capacity/ownership blocker: process group remains after cleanup'
  fi
  if ((leader_exited != 0)); then
    die 'Capacity/ownership blocker: command exited with process-group descendants'
  fi
  return "$status"
}

if run_command; then
  command_status=0
else
  command_status=$?
fi
if ((command_status == 0 && shared_mode != 0)); then
  publish_node_cache
fi
exit "$command_status"
