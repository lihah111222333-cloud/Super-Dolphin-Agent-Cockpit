#!/bin/sh
set -eu

repository_root=$(git rev-parse --show-toplevel)
common_git_directory=$(git rev-parse --path-format=absolute --git-common-dir)
tracked_config="$repository_root/config/remote-ci/aliyun.json"
runtime_directory="$common_git_directory/super-dolphin-remote"
runtime_config="$runtime_directory/config.json"
ledger_path="$runtime_directory/config.baseline-state.sqlite"

if [ ! -f "$tracked_config" ]; then
  echo "remote CI tracked config is missing: $tracked_config" >&2
  exit 1
fi
if [ -e "$ledger_path" ] && [ ! -f "$ledger_path" ]; then
  echo "remote CI ledger path is not a regular file: $ledger_path" >&2
  exit 1
fi

umask 077
mkdir -p "$runtime_directory"
chmod 700 "$runtime_directory"

temporary_config="$runtime_directory/.config.json.tmp.$$"
cleanup_temporary_config() {
  rm -f "$temporary_config"
}
trap cleanup_temporary_config EXIT HUP INT TERM
if ! cmp -s "$tracked_config" "$runtime_config"; then
  cp "$tracked_config" "$temporary_config"
  chmod 600 "$temporary_config"
  mv "$temporary_config" "$runtime_config"
fi
chmod 600 "$runtime_config"

if [ ! -s "$ledger_path" ]; then
  (
    cd "$repository_root"
    ./scripts/go run ./cmd/super-dolphin-gate remote init-ledger --ledger "$ledger_path"
  )
fi

git config --local super-dolphin.remote.config "$runtime_config"
git config --local super-dolphin.remote.ledger "$ledger_path"

printf 'remote CI local config: %s\n' "$runtime_config"
printf 'remote CI local SQLite authority: %s\n' "$ledger_path"
