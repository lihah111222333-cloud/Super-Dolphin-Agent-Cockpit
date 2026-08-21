#!/usr/bin/env bash
set -Eeuo pipefail

export LOCAL_NON_AUTHORITATIVE=1
export NOT_VERIFIED=1
export SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE
export SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED

die() { printf 'linux_runner_daemon: %s LOCAL_NON_AUTHORITATIVE=1 NOT_VERIFIED=1\n' "$*" >&2; exit 2; }

repo_path=''
runner_id='linux-host'
mode='native'
timeout_seconds=120
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
runtime=''
runtime_seen=0
docker_runtime_marker=''
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

require_safe_namespace_id() {
  local value=$1 label=$2
  [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || die "$label must be a safe repository/node identity"
}

require_canonical_sha256() {
  local value=$1 label=$2
  [[ "$value" =~ ^sha256:[a-f0-9]{64}$ ]] || die "$label must be canonical sha256:<64 lowercase hex>"
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
    [[ "${actual%% *}" == "$digest" ]] || die "$label destination cache collision detected"
    return 0
  fi
  temporary="$parent/.${target##*/}.$$.tmp"
  [[ ! -e "$temporary" ]] || die "$label staging file already exists"
  cp -p -- "$blob" "$temporary" || die "$label staging file cannot be copied"
  mv -- "$temporary" "$target" || die "$label cache file cannot be installed atomically"
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
    "module_identity=$module_identity" >"$expected" || die 'cache identity manifest cannot be written'
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
  local stage blobs node_records current_records combined_records sorted_records final_manifest manifest_digest manifest_target ready_stage
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
  current_records="$stage/current-records"
  : >"$current_records"
  if [[ -n "$current_manifest" ]]; then
    validate_cache_generation "$current_manifest" "$current_generation" 0
    tail -n +4 -- "$current_manifest" >"$current_records" || die 'current shared cache generation cannot be read'
  fi
  combined_records="$stage/combined-records"
  if ! awk -F '\t' '
    NF != 5 || $1 != "file" { exit 41 }
    { key=$2 "\t" $5; value=$3 "\t" $4; if (key in seen && seen[key] != value) exit 42; seen[key]=value; print }
  ' "$current_records" "$node_records" >"$combined_records"; then
    die 'shared cache generation path collision detected'
  fi
  sorted_records="$stage/sorted-records"
  LC_ALL=C sort -u "$combined_records" >"$sorted_records" || die 'shared cache generation records cannot be sorted'
  if [[ ! -s "$sorted_records" ]]; then
    release_shared_cache_lock
    rm -rf -- "$stage"
    printf 'cache_scope=ubuntu_shared repository_id=%s node_id=%s cache_publish=empty cache_identity=%s authority=LOCAL_NON_AUTHORITATIVE status=NOT_VERIFIED\n' \
      "$repository_id" "$node_id" "$cache_identity"
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
  printf 'cache_scope=ubuntu_shared repository_id=%s node_id=%s cache_publish=complete cache_identity=%s cache_generation=%s authority=LOCAL_NON_AUTHORITATIVE status=NOT_VERIFIED\n' \
    "$repository_id" "$node_id" "$cache_identity" "$cache_generation"
}

# shellcheck disable=SC2329 # trap 通过函数名在共享缓存模式下调用该清理边界。
cleanup_shared_cache_state() {
  local status=$?
  if [[ -n "$run_tmp_root" && -d "$run_tmp_root" ]]; then
    rm -rf -- "$run_tmp_root" || status=2
  fi
  if ((shared_cache_lock_held != 0)); then
    flock -u 9 2>/dev/null || status=2
    exec 9>&-
    shared_cache_lock_held=0
  fi
  exit "$status"
}

setup_ubuntu_shared_cache() {
  local target_top_level target_object_format target_commit target_tree target_status shared_tree missing_object identity_material identity_digest
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
  target_commit=$(git -C "$repo_path" rev-parse HEAD 2>/dev/null) || die 'unable to resolve target commit'
  [[ "$target_commit" == "$source_commit" ]] || die 'target HEAD does not match --source-commit'
  target_tree=$(git -C "$repo_path" rev-parse "${source_commit}^{tree}" 2>/dev/null) || die 'unable to resolve target tree'
  [[ "$target_tree" == "$source_tree" ]] || die 'target tree does not match --source-tree'
  target_status=$(git -C "$repo_path" status --porcelain=v1 --untracked-files=all --ignored=matching 2>/dev/null) || die 'unable to verify target worktree cleanliness'
  [[ -z "$target_status" ]] || die 'target worktree must exactly match the committed source tree'
  path_is_within "$repo_path" "$shared_root" && die 'target worktree must not be inside --shared-root'
  path_is_within "$repo_path" "$cache_root" && die 'target worktree must not be inside --cache-root'
  path_is_within "$shared_root" "$repo_path" && die '--shared-root must not be inside target worktree'
  path_is_within "$cache_root" "$repo_path" && die '--cache-root must not be inside target worktree'

  shared_namespace="$shared_root/$repository_id"
  identity_material=$(printf '%s\n%s\n%s\n%s\n%s\n%s\n' \
    "$repository_id" "$toolchain_identity" "$goos" "$goarch" "$cgo_enabled" "$module_identity" | sha256sum) || die 'shared cache identity cannot be generated'
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
  trap cleanup_shared_cache_state EXIT
  node_gomodcache="$node_overlay_root/gomodcache"
  node_gocache="$node_overlay_root/gocache"
  node_tmp_root="$node_overlay_root/tmp"
  node_write_overlay="$node_overlay_root/write"
  [[ ! -e "$node_tmp_root" || -d "$node_tmp_root" ]] || die 'node temporary root is not a directory'
  [[ ! -e "$node_write_overlay" || -d "$node_write_overlay" ]] || die 'node write overlay is not a directory'
  mkdir -p -- "$node_tmp_root" "$node_write_overlay" || die 'node-local overlay directories cannot be created'
  run_tmp_root=$(mktemp -d "$node_tmp_root/run.XXXXXX") || die 'node-local temporary directory cannot be created'
  mkdir "$run_tmp_root/go" || die 'node-local Go temporary directory cannot be created'
  mkdir -p -- "$node_gomodcache" "$node_gocache" || die 'node-local cache overlays cannot be created'
  validate_cache_tree "$node_gomodcache" 'node GOMODCACHE overlay'
  validate_cache_tree "$node_gocache" 'node GOCACHE overlay'
  sync_shared_cache_to_node
  export GIT_OBJECT_DIRECTORY="$shared_objects"
  export GIT_NO_REPLACE_OBJECTS=1
  unset GIT_ALTERNATE_OBJECT_DIRECTORIES
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
  printf 'cache_scope=ubuntu_shared repository_id=%s node_id=%s shared_source=readonly cache_seed_state=%s cache_identity=%s cache_generation=%s cache_publish=pending authority=LOCAL_NON_AUTHORITATIVE status=NOT_VERIFIED\n' \
    "$repository_id" "$node_id" "$cache_seed_state" "$cache_identity" "${cache_generation:-none}"
}
require_bounded_positive_integer() {
  local value=$1 label=$2
  [[ "$value" =~ ^[1-9][0-9]*$ ]] || die "$label must be a positive decimal integer"
  if (( ${#value} > ${#max_int64} )); then
    die "$label overflows signed integer"
  fi
  if (( ${#value} == ${#max_int64} )) && (( 10#$value > max_int64 )); then
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
    --runner-id)
      (($# >= 2)) || die '--runner-id requires a value'
      runner_id=$2
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
      mode=$2
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
    --timeout-seconds)
      (($# >= 2)) || die '--timeout-seconds requires a value'
      timeout_seconds=$2
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
      printf '%s\n' 'linux_runner_daemon.sh --repo PATH [--mode native|docker] [--runtime ubuntu|darling-compat] [--docker-image sha256:ID|REPOSITORY@sha256:DIGEST] [--alternates OBJECTS_DIR] [--shared-root PATH --cache-root PATH --repository-id ID --node-id ID --object-format sha1|sha256 --source-commit OID --source-tree OID --toolchain-identity SHA256 --goos GOOS --goarch GOARCH --cgo-enabled 0|1 --module-identity SHA256 --workload-identity SHA256] [-- COMMAND...]'
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

[[ -n "$repo_path" && "$repo_path" = /* && -d "$repo_path" ]] || die 'an absolute existing --repo is required'
if ((shared_root_seen != cache_root_seen)); then
  die '--shared-root and --cache-root must be provided together'
fi
[[ "$mode" == native || "$mode" == docker ]] || die '--mode must be native or docker'
if [[ "$mode" == docker ]]; then
  ((runtime_seen == 1)) || die '--runtime is required for docker mode'
  ((docker_image_seen == 1)) || die '--docker-image is required for docker mode'
  require_immutable_docker_image "$docker_image"
  case "$runtime" in
	ubuntu)
	  docker_runtime_marker=ubuntu_container
      ;;
    darling-compat)
      docker_runtime_marker=darling_compat_runtime
      ;;
    *)
	  die '--runtime must be ubuntu or darling-compat'
	  ;;
	esac
	export SUPER_DOLPHIN_RUNTIME="$docker_runtime_marker"
else
	((runtime_seen == 0)) || die '--runtime is forbidden in native mode'
	((docker_image_seen == 0)) || die '--docker-image is forbidden in native mode'
	unset SUPER_DOLPHIN_RUNTIME
fi
require_bounded_positive_integer "$timeout_seconds" '--timeout-seconds'
[[ "$timeout_seconds" -le 120 ]] || die '--timeout-seconds must be in 1..120'
[[ "$(uname -s)" == Linux ]] || die 'daemon only runs on Linux'
[[ ${#command_args[@]} -gt 0 ]] || die 'a command is required after --'
command -v git >/dev/null 2>&1 || die 'git is required'
cgroup_root='/sys/fs/cgroup'
if [[ "$cgroup_root" == /sys/fs/cgroup ]]; then
  cgroup_relative=$(cut -d: -f3 /proc/self/cgroup) || die 'current cgroup path cannot be read'
  [[ -n "$cgroup_relative" && "$cgroup_relative" = /* && ! "$cgroup_relative" =~ [[:cntrl:]] ]] || die 'current cgroup path is invalid'
  cgroup_root="$cgroup_root$cgroup_relative"
fi
[[ -r "$cgroup_root/cgroup.controllers" ]] || die 'cgroup v2 is required'
git -C "$repo_path" rev-parse --git-dir >/dev/null 2>&1 || die 'unable to materialize Git repository identity'
if ((shared_root_seen == 1)); then
  ((repository_id_seen == 1)) || die '--repository-id is required with shared Ubuntu cache'
  ((node_id_seen == 1)) || die '--node-id is required with shared Ubuntu cache'
  ((toolchain_identity_seen == 1)) || die '--toolchain-identity is required with shared Ubuntu cache'
  ((goos_seen == 1)) || die '--goos is required with shared Ubuntu cache'
  ((goarch_seen == 1)) || die '--goarch is required with shared Ubuntu cache'
  ((cgo_enabled_seen == 1)) || die '--cgo-enabled is required with shared Ubuntu cache'
  ((module_identity_seen == 1)) || die '--module-identity is required with shared Ubuntu cache'
  ((workload_identity_seen == 1)) || die '--workload-identity is required with shared Ubuntu cache'
  ((object_format_seen == 1)) || die '--object-format is required with shared Ubuntu source objects'
  ((source_commit_seen == 1)) || die '--source-commit is required with shared Ubuntu source objects'
  ((source_tree_seen == 1)) || die '--source-tree is required with shared Ubuntu source objects'
  setup_ubuntu_shared_cache
elif ((repository_id_seen != 0 || node_id_seen != 0 || toolchain_identity_seen != 0 || goos_seen != 0 || goarch_seen != 0 || cgo_enabled_seen != 0 || module_identity_seen != 0 || workload_identity_seen != 0 || object_format_seen != 0 || source_commit_seen != 0 || source_tree_seen != 0)); then
  die 'Ubuntu cache identity fields require shared Ubuntu cache roots'
fi
read_cgroup_file() {
  local path=$1 label=$2 status
  [[ -r "$path" ]] || die "$label is missing or unreadable"
  cgroup_file_value=''
  if IFS= read -r -d '' cgroup_file_value < "$path"; then
    die "$label contains a NUL byte"
  else
    status=$?
    (( status == 1 )) || die "$label cannot be read"
  fi
}
read_cgroup_memory_bytes() {
  local value
  read_cgroup_file "$cgroup_root/memory.max" 'cgroup memory.max'
  value=$cgroup_file_value
  if [[ "$value" == *$'\n' ]]; then
    value=${value%$'\n'}
  fi
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || die 'cgroup memory.max has extra lines'
  require_bounded_positive_integer "$value" 'cgroup memory.max'
  printf '%s\n' "$value"
}
read_cgroup_cpu_milli() {
	local raw quota period milli
	read_cgroup_file "$cgroup_root/cpu.max" 'cgroup cpu.max'
  raw=$cgroup_file_value
  if [[ "$raw" == *$'\n' ]]; then
    raw=${raw%$'\n'}
  fi
  [[ "$raw" != *$'\n'* && "$raw" != *$'\r'* ]] || die 'cgroup cpu.max has extra lines'
  [[ "$raw" =~ ^([1-9][0-9]*)[[:blank:]]+([1-9][0-9]*)$ ]] || die 'cgroup cpu.max must contain exactly positive quota and period'
  quota=${BASH_REMATCH[1]}
  period=${BASH_REMATCH[2]}
	require_bounded_positive_integer "$quota" 'cgroup cpu.max quota'
	require_bounded_positive_integer "$period" 'cgroup cpu.max period'
	((quota <= max_int64 / 1000)) || die 'cgroup cpu.max quota overflows millicore precision'
	milli=$(( quota * 1000 / period ))
	[[ "$milli" =~ ^[1-9][0-9]*$ ]] || die 'cgroup CPU probe returned less than one millicore'
	printf '%s\n' "$milli"
}

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

cpu_milli=$(read_cgroup_cpu_milli)
docker_cpu=$(format_cpu_milli "$cpu_milli")
logical_cpu=$(( cpu_milli / 1000 ))
if ((cpu_milli % 1000 != 0)); then
  logical_cpu=$(( logical_cpu + 1 ))
fi
memory_bytes=$(read_cgroup_memory_bytes)
[[ "$memory_bytes" =~ ^[1-9][0-9]*$ ]] || die 'dynamic cgroup memory probe returned zero'

if [[ -n "$alternates" && "$shared_mode" == 0 ]]; then
  [[ -d "$alternates" ]] || die '--alternates must name an existing directory'
  export GIT_ALTERNATE_OBJECT_DIRECTORIES="$alternates"
fi

if [[ "$mode" == docker ]]; then
  command -v docker >/dev/null 2>&1 || die 'docker is required for docker mode'
  docker info >/dev/null 2>&1 || die 'docker daemon is unavailable'
  image_runtime=$(docker image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$docker_image" 2>/dev/null) || die 'immutable Docker image cannot be inspected'
  grep -Fxq "SUPER_DOLPHIN_RUNTIME=$docker_runtime_marker" <<<"$image_runtime" || die 'immutable Docker image runtime marker does not match --runtime'
fi

if [[ "$mode" == docker ]]; then
  printf 'runner_id=%s mode=%s runtime=%s cgroup_v2=1 cpu_milli=%s logical_cpu_ceiling=%s memory_bytes=%s authority=LOCAL_NON_AUTHORITATIVE status=NOT_VERIFIED LOCAL_NON_AUTHORITATIVE=1 NOT_VERIFIED=1\n' \
    "$runner_id" "$mode" "$docker_runtime_marker" "$cpu_milli" "$logical_cpu" "$memory_bytes"
else
  printf 'runner_id=%s mode=%s cgroup_v2=1 cpu_milli=%s logical_cpu_ceiling=%s memory_bytes=%s authority=LOCAL_NON_AUTHORITATIVE status=NOT_VERIFIED LOCAL_NON_AUTHORITATIVE=1 NOT_VERIFIED=1\n' \
    "$runner_id" "$mode" "$cpu_milli" "$logical_cpu" "$memory_bytes"
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
  if [[ "$mode" == native ]]; then
    argv=("${command_args[@]}")
  else
	argv=(docker run --rm --platform linux/amd64 --cpus "$docker_cpu" --memory "$memory_bytes" -v "$repo_path:/workspace:rw" -w /workspace)
	if ((shared_mode != 0)); then
	  argv+=(
	    -v "$shared_objects:/cache/git-objects:ro"
	    -v "$node_gomodcache:/cache/gomodcache:rw"
	    -v "$node_gocache:/cache/gocache:rw"
	    -v "$run_tmp_root:/tmp:rw"
	    -v "$node_write_overlay:/cache/write:rw"
	  )
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
	    -e GIT_OBJECT_DIRECTORY=/cache/git-objects
	    -e GIT_NO_REPLACE_OBJECTS=1
	    -e GOMODCACHE=/cache/gomodcache
	    -e GOCACHE=/cache/gocache
	    -e TMPDIR=/tmp
	    -e GOTMPDIR=/tmp/go
	    -e "GOOS=$goos"
	    -e "GOARCH=$goarch"
	    -e "CGO_ENABLED=$cgo_enabled"
	  )
	fi
	argv+=("$docker_image" "${command_args[@]}")
  fi
  command -v setsid >/dev/null 2>&1 || die 'setsid is required for process-group ownership'
  (
    cd -- "$repo_path" || exit 125
    exec setsid "${argv[@]}"
  ) &
  child_pid=$!
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
  if ((cleanup_failed != 0)); then
    die 'Capacity/ownership blocker: process-group cleanup failed'
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
