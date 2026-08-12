#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/trusted-launcher-contract.XXXXXX")
fixture_root=$(cd "$fixture_root" && pwd -P)
trap 'rm -rf -- "$fixture_root"' EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

git_repo="$fixture_root/repository"
git init -q "$git_repo"
git -C "$git_repo" config user.name 'Trusted Launcher Fixture'
git -C "$git_repo" config user.email 'trusted-launcher@example.invalid'
printf 'fixture-%s\n' "$$-$RANDOM" >"$git_repo/tracked.txt"
git -C "$git_repo" add tracked.txt
git -C "$git_repo" commit -qm 'fixture base'
tree=$(git -C "$git_repo" write-tree)

candidate_source="$fixture_root/super-dolphin-gate"
cat >"$candidate_source" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$#" -eq 8 && "$1" == launcher && "$2" == verify ]]
[[ "$3" == --repository && -d "$4/.git" ]]
[[ "$5" == --tree && "$6" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]]
[[ "$6" == "$(git -C "$4" write-tree)" ]]
[[ "$7" == --receipt && "$8" == "$(dirname "$0")/receipt.json" && -f "$8" ]]
[[ "$(cat "$8")" == '{"fixture":"valid"}' ]]
EOF
chmod 500 "$candidate_source"
digest=$(sha256_file "$candidate_source")
source "$repo_root/.githooks/trusted-gate-launcher.sh"
real_os_home=$(trusted_launcher_os_home) || fail 'operating-system home directory could not be resolved'
test_home=$(mktemp -d "$real_os_home/.trusted-launcher-contract-home.XXXXXX")
chmod 700 "$test_home"
fake_bin="$fixture_root/bin"
mkdir -m 700 "$fake_bin"
if [[ "$(uname -s)" == "Darwin" ]]; then
  printf '#!/usr/bin/env bash\nprintf '\''NFSHomeDirectory: %s\\n'\''\n' "$test_home" >"$fake_bin/dscl"
else
  # fixture 必须在运行时读取位置参数。
  # shellcheck disable=SC2016
  printf '#!/usr/bin/env bash\nif [[ "${1:-}" == passwd ]]; then printf '\''fixture:x:0:0::%s:/bin/sh\\n'\''\nfi\n' "$test_home" >"$fake_bin/getent"
fi
chmod 700 "$fake_bin"/*
export PATH="$fake_bin:$PATH"
install_root="$test_home/.super-dolphin-gate-launchers"
root_created=0
if [[ ! -d "$install_root" ]]; then
  mkdir -m 700 "$install_root"
  root_created=1
fi
cleanup_contract() {
  if [[ -n ${install_root:-} && -n ${tree:-} ]]; then
    rm -rf -- "$install_root/v1/$tree"
  fi
  if [[ ${root_created:-0} -eq 1 ]]; then
    rmdir "$install_root" 2>/dev/null || true
  fi
  rm -rf -- "$test_home"
  rm -rf -- "$fixture_root"
}
trap cleanup_contract EXIT
launcher="$install_root/v1/$tree/$digest/super-dolphin-gate"
mkdir -p "$(dirname "$launcher")"
chmod 700 "$install_root" "$install_root/v1" "$install_root/v1/$tree" "$(dirname "$launcher")"
cp "$candidate_source" "$launcher"
chmod 500 "$launcher"
printf '%s\n' '{"fixture":"valid"}' >"$(dirname "$launcher")/receipt.json"
chmod 400 "$(dirname "$launcher")/receipt.json"

git -C "$git_repo" config --local superdolphin.gateLauncher "$launcher"
git -C "$git_repo" config --local superdolphin.gateLauncherRoot "$fixture_root/wrong-launcher-root"

run_direct_source_tree_test() {
  local shell_name=$1
  local shell_flags=()
  command -v "$shell_name" >/dev/null 2>&1 || fail "$shell_name is required for the trusted launcher shell contract"
  if [[ "$shell_name" == zsh ]]; then
    shell_flags=(-f)
  else
    shell_flags=(--noprofile --norc)
  fi
  # 子 shell 脚本必须在目标 shell 内展开参数。
  # shellcheck disable=SC2016
  "$shell_name" "${shell_flags[@]}" -c '
set -euo pipefail
repo_root=$1
git_repo=$2
launcher=$3
tree=$4
source "$repo_root/.githooks/trusted-gate-launcher.sh"
before_path=$PATH
actual=$(trusted_gate_launcher_for_tree "$git_repo" "$tree")
[[ "$actual" == "$launcher" ]]
[[ "$PATH" == "$before_path" ]]
' -- "$repo_root" "$git_repo" "$launcher" "$tree" || fail "$shell_name could not resolve the verified launcher for the current tree after sourcing the helper"
}

validate_trusted_gate_launcher "$git_repo" "$launcher" || fail 'valid content-addressed launcher was rejected'
[[ "$(trusted_gate_launcher "$git_repo")" == "$launcher" ]] || fail 'valid launcher receipt was rejected'
run_direct_source_tree_test bash
run_direct_source_tree_test zsh
chmod 600 "$(dirname "$launcher")/receipt.json"
printf '%s\n' '{"fixture":"tampered"}' >"$(dirname "$launcher")/receipt.json"
chmod 400 "$(dirname "$launcher")/receipt.json"
if trusted_gate_launcher "$git_repo" >/dev/null 2>&1; then
  fail 'tampered launcher receipt passed verification'
fi
rm "$(dirname "$launcher")/receipt.json"
if trusted_gate_launcher "$git_repo" >/dev/null 2>&1; then
  fail 'missing launcher receipt passed verification'
fi
missing_tree=$(printf '%040d' 1)
if trusted_gate_launcher_for_tree "$git_repo" "$missing_tree" >/dev/null 2>&1; then
  fail 'version-incompatible candidate tree passed launcher verification'
fi
printf '%s\n' '{"fixture":"valid"}' >"$(dirname "$launcher")/receipt.json"
chmod 400 "$(dirname "$launcher")/receipt.json"

chmod 700 "$launcher"
printf '%s\n' tamper >>"$launcher"
if validate_trusted_gate_launcher "$git_repo" "$launcher" >/dev/null 2>&1; then
  fail 'tampered binary passed the content-address digest check'
fi
cp "$candidate_source" "$launcher"
chmod 500 "$launcher"

rm -rf "$install_root/v1/$tree/$digest"
ln -s "$fixture_root" "$install_root/v1/$tree/$digest"
if validate_trusted_gate_launcher "$git_repo" "$launcher" >/dev/null 2>&1; then
  fail 'symlinked content-address directory was accepted'
fi
rm "$install_root/v1/$tree/$digest"
mkdir "$install_root/v1/$tree/$digest"
chmod 700 "$install_root/v1/$tree/$digest"
cp "$candidate_source" "$launcher"
chmod 500 "$launcher"
printf '%s\n' '{"fixture":"valid"}' >"$(dirname "$launcher")/receipt.json"
chmod 400 "$(dirname "$launcher")/receipt.json"

chmod 770 "$install_root"
if validate_trusted_gate_launcher "$git_repo" "$launcher" >/dev/null 2>&1; then
  fail 'group-writable launcher install root was accepted'
fi
chmod 700 "$install_root"

original_repo_mode=$(trusted_launcher_stat '%Lp' "$git_repo")
chmod 770 "$git_repo"
validate_trusted_gate_launcher "$git_repo" "$launcher" || {
  chmod "$original_repo_mode" "$git_repo"
  fail 'secure user-private launcher root was coupled to a group-writable repository ancestor'
}
chmod "$original_repo_mode" "$git_repo"

git -C "$git_repo" config --local superdolphin.gateLauncherRoot "$fixture_root/wrong-launcher-root"
validate_trusted_gate_launcher "$git_repo" "$launcher" || fail 'Git local launcher root override changed the OS-derived trust root'
(
  cd "$git_repo"
  validate_trusted_gate_launcher "$launcher"
) || fail 'single-argument launcher validation accepted a Git local root override'
[[ "$(trusted_launcher_root "$git_repo")" == "$install_root" ]] || fail 'trusted launcher root did not remain OS-derived'

printf 'version-compatible candidate\n' >>"$git_repo/tracked.txt"
git -C "$git_repo" add tracked.txt
candidate_tree=$(git -C "$git_repo" write-tree)
[[ "$candidate_tree" != "$tree" ]] || fail 'version compatibility fixture did not create a different candidate tree'
[[ "$(trusted_gate_launcher_for_tree "$git_repo" "$candidate_tree")" == "$launcher" ]] \
  || fail 'version-compatible launcher was incorrectly bound to its installation tree'

printf '%s\n' 'trusted launcher shell contract: PASS'
