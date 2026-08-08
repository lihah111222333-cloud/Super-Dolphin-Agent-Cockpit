package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixTestGuardGitTimeout = 5 * time.Second

const commitTitleEnforcementBaselinePath = "scripts/commit_title_enforcement_baseline.txt"

func assertOutputContainsAll(t *testing.T, output string, parts ...string) {
	t.Helper()
	for _, want := range parts {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q\n%s", want, output)
		}
	}
}

func assertOutputOmitsAll(t *testing.T, output string, parts ...string) {
	t.Helper()
	for _, forbidden := range parts {
		if strings.Contains(output, forbidden) {
			t.Fatalf("output unexpectedly contains %q\n%s", forbidden, output)
		}
	}
}

func prepareFixTestGuardRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	source := locateFixTestGuardScript(t)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read guard script: %v", err)
	}
	target := filepath.Join(root, "scripts", "guard_fix_commits_have_tests.sh")
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatalf("copy guard script: %v", err)
	}

	runFixTestGuardGit(t, root, "init", "-q")
	runFixTestGuardGit(t, root, "config", "user.email", "guard@example.test")
	runFixTestGuardGit(t, root, "config", "user.name", "Guard Test")
	writeFixTestGuardFile(t, root, "README.md", "fixture repo\n")
	runFixTestGuardGit(t, root, "add", "README.md")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: init")
	return root
}

func writePreCommitFakeCodeGuardScript(t *testing.T, root string) {
	t.Helper()
	content := "#!/usr/bin/env bash\nset -e\nprintf 'fake code guard %s skip-gosec=%s\\n' \"$*\" \"${SUPER_DOLPHIN_GITHOOK_SKIP_GOSEC:-}\"\nif [ \"$*\" != \"--guard-only\" ]; then\n  echo \"unexpected guard args: $*\" >&2\n  exit 1\nfi\n"
	path := filepath.Join(root, "scripts", "test_with_guard.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake test_with_guard.sh: %v", err)
	}
}

func preparePreCommitGateFixture(t *testing.T) string {
	t.Helper()
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/pre-commit", 0o755)
	copyFixTestGuardRepoFile(t, root, ".githooks/trusted-gate-launcher.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/configure_hook_node_runtime.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/refresh_generated_artifacts.sh", 0o755)
	writePreCommitFixtureGateCLI(t, root)
	remoteConfig := filepath.Join(root, ".pre-commit-fixture-remote", "config.yaml")
	remoteLedger := filepath.Join(root, ".pre-commit-fixture-remote", "duration-ledger.sqlite")
	writeFixTestGuardFile(t, root, ".pre-commit-fixture-remote/config.yaml", "fixture: remote-config\n")
	writeFixTestGuardFile(t, root, ".pre-commit-fixture-remote/duration-ledger.sqlite", "fixture remote duration ledger\n")
	runFixTestGuardGit(t, root, "config", "--local", "super-dolphin.remote.config", remoteConfig)
	runFixTestGuardGit(t, root, "config", "--local", "super-dolphin.remote.ledger", remoteLedger)
	writePreCommitFakeCodeGuardScript(t, root)
	writeFakeAIMaintenanceGateScript(t, root)
	writePreCommitFakeAIMaintenancePlanner(t, root)
	writePreCommitFakeCodemapMakefile(t, root)
	writeFixTestGuardFile(t, root, ".gitignore", ".build-cache/\n")
	runFixTestGuardGit(t, root, "add", ".githooks/pre-commit", ".githooks/trusted-gate-launcher.sh", ".gitignore", "scripts/configure_hook_node_runtime.sh", "scripts/refresh_generated_artifacts.sh", "scripts/test_with_guard.sh", "scripts/guard_fix_commits_have_tests.sh", "scripts/ai_maintenance_gates.sh", "scripts/ai_maintenance/main.go", "go.mod", "Makefile")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 安装 precommit fixture")
	installPreCommitFixtureLauncher(t, root)
	return root
}

const preCommitFixtureGateCLIScript = `#!/usr/bin/env bash
set -euo pipefail

repository_root=$(git rev-parse --show-toplevel)
fixture_tmp=
fixture_worktree=
fixture_job=job-0123456789abcdef0123456789abcdef

require_current_staged_tree() {
  local command=$1
  local tree=$2
  if ! git -C "$repository_root" rev-parse --verify "$tree^{tree}" >/dev/null; then
    printf 'fixture gate: %s tree is invalid: %s\n' "$command" "$tree" >&2
    return 1
  fi
  local staged_tree
  staged_tree=$(git -C "$repository_root" write-tree)
  if [ "$tree" != "$staged_tree" ]; then
    printf 'fixture gate: %s tree does not match staged tree (%s != %s)\n' "$command" "$tree" "$staged_tree" >&2
    return 1
  fi
}

cleanup() {
  local original_status=${1:-0}
  local cleanup_status=0
  if [ -n "$fixture_worktree" ]; then
    if ! git -C "$repository_root" worktree remove --force "$fixture_worktree"; then
      printf '%s\n' 'pre-commit cleanup failed: fixture staged worktree' >&2
      cleanup_status=1
    fi
    if [ -e "$fixture_worktree" ] || git -C "$repository_root" worktree list --porcelain | grep -Fqx "worktree $fixture_worktree"; then
      printf '%s\n' 'pre-commit cleanup verification failed: fixture staged worktree remains' >&2
      cleanup_status=1
    fi
  fi
  if [ -n "$fixture_tmp" ]; then
    if ! rmdir "$fixture_tmp"; then
      printf '%s\n' 'pre-commit cleanup failed: fixture temporary directory' >&2
      cleanup_status=1
    fi
    if [ -e "$fixture_tmp" ]; then
      printf '%s\n' 'pre-commit cleanup verification failed: fixture temporary directory remains' >&2
      cleanup_status=1
    fi
  fi
  if [ "$original_status" -ne 0 ]; then
    return "$original_status"
  fi
  return "$cleanup_status"
}

finish_cleanup() {
  local status=$1
  trap - EXIT INT TERM HUP
  cleanup "$status"
}

trap 'status=$?; finish_cleanup "$status"; exit $?' EXIT
trap 'finish_cleanup 130; exit $?' INT
trap 'finish_cleanup 143; exit $?' TERM
trap 'finish_cleanup 129; exit $?' HUP

case "${1:-}" in
  launcher)
    if [ "$#" -eq 8 ] && [ "$2" = "verify" ] && [ "$3" = "--repository" ] && [ "$5" = "--tree" ] && [ "$7" = "--receipt" ]; then
      exit 0
    fi
    printf '%s\n' 'fixture gate: launcher requires verify arguments' >&2
    exit 64
    ;;
  closure)
	if [ "$#" -eq 4 ] && [ "$2" = "provenance" ] && [ "$3" = "--tree" ]; then
	  tree=$4
	  if ! require_current_staged_tree closure "$tree"; then
		exit 1
	  fi
	  printf '%s %s\n' 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
	  exit 0
	fi
    if [ "$#" -ne 4 ] || [ "$3" != "--tree" ] || [[ "$2" != "check" && "$2" != "refresh" && "$2" != "refresh-dependencies" ]]; then
      printf 'fixture gate: closure requires check, refresh, or refresh-dependencies --tree <tree>\n' >&2
      exit 64
    fi
    tree=$4
    if ! require_current_staged_tree closure "$tree"; then
      exit 1
    fi
    if [ "$2" = "check" ]; then
      printf 'fixture closure verified staged tree %s\n' "$tree"
    else
      printf 'fixture closure %s verified staged tree %s\n' "$2" "$tree"
    fi
    ;;
  project-map)
    if [ "$#" -ne 4 ] || [ "$3" != "--tree" ] || [[ "$2" != "check" && "$2" != "refresh" ]]; then
      printf 'fixture gate: project-map requires check or refresh --tree <tree>\n' >&2
      exit 64
    fi
    tree=$4
    if ! require_current_staged_tree project-map "$tree"; then
      exit 1
    fi
    printf 'fixture project-map %s verified staged tree %s\n' "$2" "$tree"
    ;;
  codemap)
    if [ "$#" -ne 4 ] || [ "$3" != "--tree" ] || [[ "$2" != "check" && "$2" != "refresh" ]]; then
      printf 'fixture gate: codemap requires check or refresh --tree <tree>\n' >&2
      exit 64
    fi
    tree=$4
    if ! require_current_staged_tree codemap "$tree"; then
      exit 1
    fi
    printf 'fixture codemap %s verified staged tree %s\n' "$2" "$tree"
    ;;
  capability-contract)
    if [ "$#" -ne 4 ] || [ "$3" != "--tree" ] || [[ "$2" != "check" && "$2" != "refresh" ]]; then
      printf 'fixture gate: capability-contract requires check or refresh --tree <tree>\n' >&2
      exit 64
    fi
    tree=$4
    if ! require_current_staged_tree capability-contract "$tree"; then
      exit 1
    fi
    if [ "$2" = "refresh" ]; then
      mkdir -p "$repository_root/docs/doc/codemap/capability-contract"
      printf '{"capability":"refreshed"}\n' >"$repository_root/docs/doc/codemap/capability-contract/capability_manifest.json"
    fi
    printf 'fixture capability-contract %s verified staged tree %s\n' "$2" "$tree"
    ;;
  frontend-code-size)
    if [ "$#" -ne 6 ] || [ "$2" != "check" ] || [ "$3" != "--tree" ] || [ "$5" != "--accepted-tree" ]; then
      printf 'fixture gate: frontend-code-size requires check --tree <tree> --accepted-tree <tree>\n' >&2
      exit 64
    fi
    tree=$4
    if ! require_current_staged_tree frontend-code-size "$tree"; then
      exit 1
    fi
    accepted_tree=$6
    head_tree=$(git -C "$repository_root" rev-parse --verify 'HEAD^{tree}')
    if [ "$accepted_tree" != "$head_tree" ]; then
      printf 'fixture gate: frontend-code-size accepted tree does not match HEAD (%s != %s)\n' "$accepted_tree" "$head_tree" >&2
      exit 1
    fi
    printf 'fixture frontend-code-size verified staged tree %s accepted tree %s\n' "$tree" "$accepted_tree"
    ;;
  remote)
    if [ "$#" -ne 13 ] || [ "$2" != "hook" ] || [ "$3" != "pre-commit" ] || [ "$4" != "--config" ] || [ "$6" != "--ledger" ] || [ "$8" != "--repository" ] || [ "${10}" != "--tree" ] || [ "${12}" != "--parent" ]; then
      printf 'fixture gate: remote requires hook pre-commit --config <path> --ledger <path> --repository <path> --tree <tree> --parent <commit>\n' >&2
      exit 64
    fi
    if [ -n "${GIT_DIR:-}" ] || [ -n "${GIT_WORK_TREE:-}" ]; then
      printf 'fixture gate: remote hook inherited repository-local Git environment\n' >&2
      exit 1
    fi
    remote_config=$5
    remote_ledger=$7
    remote_repository=$9
    tree=${11}
    parent_commit=${13}
    if [ ! -f "$remote_config" ] || [ ! -f "$remote_ledger" ] || [ "$remote_repository" != "$repository_root" ]; then
      printf 'fixture gate: remote hook config, ledger, or repository is invalid\n' >&2
      exit 1
    fi
    if [ "$parent_commit" != "$(git -C "$repository_root" rev-parse --verify 'HEAD^{commit}')" ]; then
      printf 'fixture gate: remote hook parent does not match HEAD\n' >&2
      exit 1
    fi
    if ! require_current_staged_tree remote-hook "$tree"; then
      exit 1
    fi
    printf 'fixture hook queued staged tree %s job=%s\n' "$tree" "$fixture_job"
    printf 'fixture wait verified staged tree %s job=%s\n' "$tree" "$fixture_job"
    if [ -n "${GATE_WAIT_READY_FILE:-}" ]; then
      : >"$GATE_WAIT_READY_FILE"
    fi
    if [ -n "${GATE_WAIT_FOR_INTERRUPT:-}" ]; then
      sleep 2
      exit 130
    fi
    if [ -n "${GATE_WAIT_FORCE_FAILURE:-}" ]; then
      printf '%s\n' 'forced wait failure' >&2
      exit 42
    fi
    fixture_tmp=$(mktemp -d "${TMPDIR:-/tmp}/pre-commit-fixture.XXXXXX")
    fixture_worktree="$fixture_tmp/worktree"
    git -C "$repository_root" worktree add --quiet --detach --no-checkout "$fixture_worktree" HEAD
    git -C "$fixture_worktree" read-tree "$tree"
    git -C "$fixture_worktree" checkout-index -a
    cd "$fixture_worktree"
    if grep -qs 'go:embed all:web-dist' cmd/agent-terminal/*.go 2>/dev/null && [ ! -f cmd/agent-terminal/web-dist/index.html ]; then
      mkdir -p cmd/agent-terminal/web-dist
      printf '%s\n' '<!doctype html><title>staged snapshot</title>' >cmd/agent-terminal/web-dist/index.html
    fi
    if [ -n "${GATE_ASSERT_RELATIVE_PATH:-}" ]; then
      grep -Fq "${GATE_ASSERT_CONTENT:?}" "$GATE_ASSERT_RELATIVE_PATH"
    fi
    if [ -d cmd/agent-terminal ]; then
      printf '%s\n' '[pre-commit] go vet (staged snapshot)...'
      go vet ./cmd/agent-terminal
    fi
    printf '%s\n' 'pre-commit OK'
    if [ -n "${GATE_FORCE_CLEANUP_FAILURE:-}" ]; then
      chmod 0500 "${TMPDIR:-/tmp}"
    fi
    ;;
  *)
    printf 'fixture gate: unsupported command %s\n' "${1:-}" >&2
    exit 64
    ;;
esac
`

// writePreCommitFixtureGateCLI provides only the strict command surface needed
// by the staged-worktree regression fixture, without relying on a host CLI.
func writePreCommitFixtureGateCLI(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".pre-commit-fixture-bin", "super-dolphin-gate")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture gate CLI directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(preCommitFixtureGateCLIScript), 0o755); err != nil {
		t.Fatalf("write fixture gate CLI: %v", err)
	}
}

func installPreCommitFixtureLauncher(t *testing.T, root string) string {
	t.Helper()
	source := filepath.Join(root, ".pre-commit-fixture-bin", "super-dolphin-gate")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read fixture gate CLI: %v", err)
	}
	digest := sha256.Sum256(data)
	tree := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree"))
	installRoot := secureTrustedLauncherTestRoot(t)
	launcher := filepath.Join(installRoot, "v1", tree, fmt.Sprintf("%x", digest[:]), "super-dolphin-gate")
	if _, err := os.Lstat(launcher); err == nil {
		t.Fatalf("refusing to overwrite existing launcher fixture: %s", launcher)
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect launcher fixture path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatalf("create fixture launcher directory: %v", err)
	}
	if err := os.WriteFile(launcher, data, 0o500); err != nil {
		t.Fatalf("write fixture launcher: %v", err)
	}
	runFixTestGuardGit(t, root, "config", "--local", "superdolphin.gateLauncher", launcher)
	runFixTestGuardGit(t, root, "config", "--local", "superdolphin.gateLauncherRoot", installRoot)
	return launcher
}

func writeFakeAIMaintenanceGateScript(t *testing.T, root string) {
	t.Helper()
	content := `#!/usr/bin/env bash
set -e
printf 'fake ai maintenance gate %s\n' "$*"
printf 'gate-worktree=%s\n' "$PWD"
printf 'gate-head=%s\n' "$(git rev-parse HEAD)"
printf 'gate-tree=%s\n' "$(git rev-parse 'HEAD^{tree}')"
printf 'gate-head-parents=%s\n' "$(git rev-list --parents -n 1 HEAD)"
gate_status=$(git status --porcelain=v1)
printf 'gate-status=%s\n' "$gate_status"
if [ -n "${GATE_ASSERT_CLEAN:-}" ] && [ -n "$gate_status" ]; then
  echo "gate worktree is not clean: $gate_status" >&2
  exit 1
fi
if [ -n "${GATE_MUTATE_ORIGINAL_PATH:-}" ]; then
  printf 'mutated during gate\n' >"$GATE_MUTATE_ORIGINAL_PATH"
fi
if [ -n "${GATE_ASSERT_RELATIVE_PATH:-}" ]; then
  grep -Fq "${GATE_ASSERT_CONTENT:?}" "$GATE_ASSERT_RELATIVE_PATH"
fi
if [ -n "${GIT_INDEX_FILE:-}" ]; then
  printf 'gate-index=%s gate-index-tree=%s\n' "$GIT_INDEX_FILE" "$(git write-tree)"
fi
if [ -n "${GATE_ASSERT_WORKTREE_INDEX:-}" ]; then
  cache_tree=$(git write-tree)
  worktree_tree=$(unset GIT_INDEX_FILE; git write-tree)
  printf 'cache-tree=%s worktree-tree=%s\n' "$cache_tree" "$worktree_tree"
  [ "$cache_tree" = "$worktree_tree" ]
fi
if [ -n "${GATE_ASSERT_NODE_MODULES_COPY:-}" ]; then
  [ -d frontend-app/node_modules ]
  [ ! -L frontend-app/node_modules ]
  [ -x frontend-app/node_modules/.bin/vite ]
fi
if [ -n "${GATE_READY_FILE:-}" ]; then
  : >"$GATE_READY_FILE"
fi
if [ -n "${GATE_WAIT_FOR_INTERRUPT:-}" ]; then
  sleep 2
fi
if [ -n "${GATE_FORCE_FAILURE:-}" ]; then
  echo "forced gate failure" >&2
  exit 42
fi
if [ -n "${GATE_FORCE_CLEANUP_FAILURE:-}" ]; then
  chmod 0500 "$TMPDIR"
fi
if [ -n "${HOOK_SCOPE_LOG:-}" ]; then
  printf 'soft-generated=%s ai-maintenance %s\n' "${SUPER_DOLPHIN_PRE_PUSH_SOFT_GENERATED_DRIFT:-}" "$*" >>"$HOOK_SCOPE_LOG"
fi
`
	path := filepath.Join(root, "scripts", "ai_maintenance_gates.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fake ai maintenance gate dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake ai_maintenance_gates.sh: %v", err)
	}
}

func writePreCommitFakeAIMaintenancePlanner(t *testing.T, root string) {
	t.Helper()
	writeFixTestGuardFile(t, root, "go.mod", "module example.com/precommitfixture\n\ngo 1.24\n")
	content := `package main

import (
	"encoding/json"
	"os"
)

const manifest = "docs/doc/codemap/capability-contract/capability_manifest.json"

func main() {
	generated := []string{}
	for i := 0; i+1 < len(os.Args); i++ {
		if os.Args[i] != "--changed-file" {
			continue
		}
		if os.Args[i+1] == "internal/provider/producer.go" {
			generated = []string{manifest}
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"generated_files": generated,
		"required_gates":  []string{"codemap:check", "project-map:check"},
	}); err != nil {
		panic(err)
	}
}
`
	writeFixTestGuardFile(t, root, "scripts/ai_maintenance/main.go", content)
}

func writePreCommitFakeCodemapMakefile(t *testing.T, root string) {
	t.Helper()
	content := ".PHONY: codemap-refresh project-map-refresh capcontract-refresh\n\n" +
		"codemap-refresh:\n" +
		"\t@mkdir -p docs/doc/codemap\n" +
		"\t@printf 'root readme refreshed\\n' > README.md\n" +
		"\t@printf 'archtest map refreshed\\n' > docs/doc/codemap/13-archtest-boundaries.md\n" +
		"\t@printf 'readme refreshed\\n' > docs/doc/codemap/README.md\n" +
		"\t@printf '{\"generated\":true}\\n' > docs/doc/codemap/ai-index.json\n\n" +
		"project-map-refresh:\n" +
		"\t@mkdir -p docs/doc/codemap/project-map/index\n" +
		"\t@printf 'project map refreshed\\n' > docs/doc/codemap/project-map/AI_PROJECT_MAP.md\n" +
		"\t@printf 'drift refreshed\\n' > docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md\n" +
		"\t@printf '{\"generated\":true}\\n' > docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json\n" +
		"\t@printf 'path\\tmodule\\n' > docs/doc/codemap/project-map/index/other.tsv\n\n" +
		"capcontract-refresh:\n" +
		"\t@mkdir -p docs/doc/codemap/capability-contract\n" +
		"\t@printf '{\"capability\":\"refreshed\"}\\n' > docs/doc/codemap/capability-contract/capability_manifest.json\n"
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fake Makefile: %v", err)
	}
}

func TestPreCommitCreatesDeterministicEmbedPlaceholderFromStagedSnapshot(t *testing.T) {
	for _, mutableIgnoredArtifact := range []bool{false, true} {
		name := "without ignored artifact"
		if mutableIgnoredArtifact {
			name = "with mutable ignored artifact"
		}
		t.Run(name, func(t *testing.T) {
			root := preparePreCommitGateFixture(t)
			writeFixTestGuardFile(t, root, ".gitignore", ".build-cache/\ncmd/agent-terminal/web-dist/\n")
			runFixTestGuardGit(t, root, "add", ".gitignore", "scripts/guard_fix_commits_have_tests.sh")
			runFixTestGuardGit(t, root, "commit", "-m", "chore: 安装 embed fixture")
			writeFixTestGuardFile(t, root, "cmd/agent-terminal/main.go", "package main\n\nimport \"embed\"\n\n//go:embed all:web-dist\nvar frontend embed.FS\n\nfunc main() { _ = frontend }\n")
			if mutableIgnoredArtifact {
				writeFixTestGuardFile(t, root, "cmd/agent-terminal/web-dist/index.html", "mutable ignored artifact\n")
			}
			runFixTestGuardGit(t, root, "add", "cmd/agent-terminal/main.go")
			stagedTree := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree"))
			out, err := runPreCommitHookWithEnv(t, root, map[string]string{
				"GATE_ASSERT_RELATIVE_PATH": "cmd/agent-terminal/web-dist/index.html",
				"GATE_ASSERT_CONTENT":       "staged snapshot",
			})
			if err != nil {
				t.Fatalf("pre-commit embed placeholder failed: %v\n%s", err, out)
			}
			assertOutputContainsAll(t, out,
				"fixture closure verified staged tree "+stagedTree,
				"fixture hook queued staged tree "+stagedTree+" job=job-0123456789abcdef0123456789abcdef",
				"fixture wait verified staged tree "+stagedTree+" job=job-0123456789abcdef0123456789abcdef",
				"go vet (staged snapshot)",
				"pre-commit OK",
			)
		})
	}
}

func TestPreCommitFixtureGateRejectsUnsupportedClosureCommand(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	gate := filepath.Join(root, ".pre-commit-fixture-bin", "super-dolphin-gate")
	cmd := exec.Command(gate, "closure", "verify")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("fixture gate accepted unsupported closure command:\n%s", out)
	}
	assertOutputContainsAll(t, string(out), "fixture gate: closure requires check, refresh, or refresh-dependencies --tree <tree>")
}

func runPreCommitHookWithEnv(t *testing.T, root string, extra map[string]string) (string, error) {
	t.Helper()
	cmd := preCommitCommand(t, root, extra)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func preCommitCommand(t *testing.T, root string, extra map[string]string) *exec.Cmd {
	t.Helper()
	installPreCommitFixtureLauncher(t, root)
	cmd := exec.Command("bash", bashPath(".githooks", "pre-commit"))
	cmd.Dir = root
	cmd.Env = preCommitFixtureEnvironment(t, root, extra)
	return cmd
}

func preCommitFixtureEnvironment(t *testing.T, root string, extra map[string]string) []string {
	t.Helper()
	env, pathValue := preCommitBaseEnvironment(extra)
	fixtureBin := filepath.Join(root, ".pre-commit-fixture-bin")
	if info, err := os.Stat(filepath.Join(fixtureBin, "super-dolphin-gate")); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatalf("pre-commit fixture gate CLI is unavailable: %v", err)
	}
	env = append(env, "PATH="+bashArg("", fixtureBin)+":"+pathValue)
	keys := []string{"PATH"}
	if _, explicit := extra["SUPER_DOLPHIN_CI_AGENT_TOKEN"]; !explicit {
		env = append(env, "SUPER_DOLPHIN_CI_AGENT_TOKEN=fixture-agent-token")
		keys = append(keys, "SUPER_DOLPHIN_CI_AGENT_TOKEN")
	}
	for key, value := range extra {
		if key != "PATH" {
			env = append(env, key+"="+value)
			keys = append(keys, key)
		}
	}
	return appendWSLEnvKeysWithGitPath(t, env, keys...)
}

func preCommitBaseEnvironment(extra map[string]string) ([]string, string) {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if key == "PATH" || key == "SUPER_DOLPHIN_CI_AGENT_TOKEN" {
			continue
		}
		if _, replaced := extra[key]; !replaced {
			env = append(env, item)
		}
	}
	pathValue := os.Getenv("PATH")
	if value, ok := extra["PATH"]; ok {
		pathValue = value
	}
	return env, pathValue
}

func assertPreCommitFixtureClean(t *testing.T, root, tmpRoot string) {
	t.Helper()
	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		t.Fatalf("read controlled TMPDIR: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("pre-commit leaked controlled TMPDIR entries: %v", entries)
	}
	worktrees := runFixTestGuardGitOutput(t, root, "worktree", "list", "--porcelain")
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize fixture root: %v", err)
	}
	if strings.Count(worktrees, "worktree ") != 1 || !strings.Contains(worktrees, "worktree "+canonicalRoot) {
		t.Fatalf("fixture worktrees after hook = %q, want only %s", worktrees, root)
	}
}

type preCommitRepositoryState struct {
	headRef    string
	headCommit string
	indexTree  string
	stagedDiff string
	refs       string
	mergeHeads string
}

func capturePreCommitRepositoryState(t *testing.T, root string) preCommitRepositoryState {
	t.Helper()
	headCommit := "<unborn>"
	cmd := exec.Command("git", "-c", "gc.auto=0", "rev-parse", "--verify", "HEAD")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err == nil {
		headCommit = strings.TrimSpace(string(out))
	}
	mergeHeads := ""
	cmd = exec.Command("git", "-c", "gc.auto=0", "rev-parse", "--verify", "MERGE_HEAD")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err == nil {
		mergeHeads = strings.TrimSpace(string(out))
	}
	return preCommitRepositoryState{
		headRef:    strings.TrimSpace(runFixTestGuardGitOutput(t, root, "symbolic-ref", "-q", "HEAD")),
		headCommit: headCommit,
		indexTree:  strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree")),
		stagedDiff: runFixTestGuardGitOutput(t, root, "diff", "--cached", "--binary"),
		refs:       runFixTestGuardGitOutput(t, root, "show-ref"),
		mergeHeads: mergeHeads,
	}
}

func assertPreCommitRepositoryState(t *testing.T, root string, want preCommitRepositoryState) {
	t.Helper()
	if got := capturePreCommitRepositoryState(t, root); got != want {
		t.Fatalf("repository state changed by staged gate\ngot:  %#v\nwant: %#v", got, want)
	}
}

func runCICommitGuard(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{bashPath("scripts", "ci_commit_guard.sh")}, bashArgs(root, args)...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = root
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runCommitMsgHook(t *testing.T, root, msgFile string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", bashPath(".githooks", "commit-msg"), bashArg(root, msgFile))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func copyFixTestGuardRepoFile(t *testing.T, root, path string, mode os.FileMode) {
	t.Helper()
	source := locateFixTestGuardRepoFile(t, path)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	target := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, data, mode); err != nil {
		t.Fatalf("copy %s: %v", path, err)
	}
}

func prepareCommitTitleBaselineRepo(t *testing.T) (string, string) {
	t.Helper()
	root := prepareFixTestGuardRepo(t)
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	baseline := commitCommitGuardFixture(t, root, "docs/legacy.md", "legacy\n", "chore: legacy English title")
	copyCommitTitleGuard(t, root, baseline)
	runFixTestGuardGit(t, root, "add", "scripts/guard_commit_titles.sh", commitTitleEnforcementBaselinePath)
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 安装提交标题门禁")
	return root, base
}

func copyCommitTitleGuard(t *testing.T, root, baseline string) {
	t.Helper()
	if baseline == "" {
		baseline = strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	}
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	writeFixTestGuardFile(t, root, commitTitleEnforcementBaselinePath, baseline+"\n")
}

func commitCommitGuardFixture(t *testing.T, root, path, content, subject string) string {
	t.Helper()
	writeFixTestGuardFile(t, root, path, content)
	runFixTestGuardGit(t, root, "add", path)
	runFixTestGuardGit(t, root, "commit", "-m", subject)
	return strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
}

func runCommitTitleGuard(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{bashPath("scripts", "guard_commit_titles.sh")}, bashArgs(root, args)...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func locateFixTestGuardScript(t *testing.T) string {
	t.Helper()
	for _, path := range []string{
		"guard_fix_commits_have_tests.sh",
		filepath.Join("scripts", "guard_fix_commits_have_tests.sh"),
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	t.Fatal("guard_fix_commits_have_tests.sh not found")
	return ""
}

func locateFixTestGuardRepoFile(t *testing.T, path string) string {
	t.Helper()
	for _, candidate := range []string{
		filepath.FromSlash(path),
		filepath.Join("..", filepath.FromSlash(path)),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Fatalf("%s not found", path)
	return ""
}

func writeFixTestGuardFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runFixTestGuard(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{bashPath("scripts", "guard_fix_commits_have_tests.sh")}, bashArgs(root, args)...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runFixTestGuardGit(t *testing.T, root string, args ...string) {
	t.Helper()
	_ = runFixTestGuardGitOutput(t, root, args...)
}

func runFixTestGuardGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
		"-c", "gc.auto=0",
	}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), fixTestGuardGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_PAGER=cat")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("git %s timed out after %s\n%s", strings.Join(args, " "), fixTestGuardGitTimeout, string(out))
	}
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
