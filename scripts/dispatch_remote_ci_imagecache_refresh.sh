#!/usr/bin/env bash
set -euo pipefail

repository=
source_ref=
config=
refresh_script=
if_older_than_hours=24

die() {
  printf 'remote ImageCache refresh dispatcher: %s\n' "$*" >&2
  exit 1
}

while (($#)); do
  case "$1" in
    --repository) (($# >= 2)) || die '--repository requires a value'; repository=$2; shift 2 ;;
    --source-ref) (($# >= 2)) || die '--source-ref requires a value'; source_ref=$2; shift 2 ;;
    --config) (($# >= 2)) || die '--config requires a value'; config=$2; shift 2 ;;
    --refresh-script) (($# >= 2)) || die '--refresh-script requires a value'; refresh_script=$2; shift 2 ;;
    --if-older-than-hours) (($# >= 2)) || die '--if-older-than-hours requires a value'; if_older_than_hours=$2; shift 2 ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ -n "$repository" && -d "$repository" ]] || die 'repository is unavailable'
[[ -n "$config" && -f "$config" ]] || die 'remote CI config is unavailable'
[[ -n "$refresh_script" && -x "$refresh_script" ]] || die 'refresh script is unavailable'
[[ "$if_older_than_hours" =~ ^[0-9]+$ && "$if_older_than_hours" -gt 0 ]] || die 'refresh age must be a positive integer'
((if_older_than_hours <= 8760)) || die 'refresh age must not exceed one year'
source_commit=$(git -C "$repository" rev-parse --verify "${source_ref}^{commit}") || die 'source commit is unavailable'
[[ "$source_commit" == "$source_ref" ]] || die 'source ref must be a canonical commit'
common_dir=$(git -C "$repository" rev-parse --path-format=absolute --git-common-dir) || die 'Git common directory is unavailable'
state_dir="$common_dir/super-dolphin/imagecache-refresh"
mkdir -p "$state_dir"
chmod 0700 "$state_dir"
lock_file="$state_dir/refresh.lock"
command -v shlock >/dev/null 2>&1 || die 'shlock is unavailable'
if ! shlock -p $$ -f "$lock_file"; then
  exit 0
fi
cleanup_lock() {
  rm -f -- "$lock_file"
}
trap cleanup_lock EXIT HUP INT TERM

cd "$repository"
"$refresh_script" \
  --config "$config" \
  --source-ref "$source_commit" \
  --if-older-than-hours "$if_older_than_hours"
