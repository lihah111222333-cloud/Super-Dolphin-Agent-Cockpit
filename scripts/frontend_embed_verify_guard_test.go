package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestFrontendEmbedVerifyScriptContracts(t *testing.T) {
	script := readScript(t, "frontend_embed_verify.sh")

	for _, want := range []string{
		"set -euo pipefail",
		"cmd/agent-terminal/web-dist",
		"frontend-app/dist",
		"git check-ignore",
		"git ls-files -- cmd/agent-terminal/web-dist",
		"SUPER_DOLPHIN_FRONTEND_EMBED_TRACKED_ARTIFACT",
		"tracked frontend embed artifacts are out of scope",
		"frontend embed manifest mismatch",
		"frontend embed smoke hash",
	} {
		assertScriptContains(t, script, want)
	}
}

func TestFrontendEmbedVerifyComparesDistAndEmbeddedManifest(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "frontend-app", "dist")
	dst := filepath.Join(root, "cmd", "agent-terminal", "web-dist")
	writeFixTestGuardFile(t, root, "frontend-app/dist/index.html", "<html>ok</html>\n")
	writeFixTestGuardFile(t, root, "frontend-app/dist/assets/app.js", "console.log('ok')\n")
	writeFixTestGuardFile(t, root, "cmd/agent-terminal/web-dist/index.html", "<html>ok</html>\n")
	writeFixTestGuardFile(t, root, "cmd/agent-terminal/web-dist/assets/app.js", "console.log('ok')\n")

	env := appendWSLEnvKeysWithGitPath(t, append(os.Environ(),
		"SUPER_DOLPHIN_FRONTEND_DIST_DIR="+bashArg("", src),
		"SUPER_DOLPHIN_FRONTEND_EMBED_DIR="+bashArg("", dst),
		"SUPER_DOLPHIN_FRONTEND_EMBED_ASSUME_IGNORED=1",
	), "SUPER_DOLPHIN_FRONTEND_DIST_DIR", "SUPER_DOLPHIN_FRONTEND_EMBED_DIR")
	output, err := runFrontendEmbedVerify(t, env)
	if err != nil {
		t.Fatalf("frontend embed verify failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "frontend embed smoke hash") {
		t.Fatalf("verifier output missing smoke hash:\n%s", output)
	}

	writeFixTestGuardFile(t, root, "cmd/agent-terminal/web-dist/assets/app.js", "console.log('drift')\n")
	output, err = runFrontendEmbedVerify(t, env)
	if err == nil {
		t.Fatalf("frontend embed verify accepted drift:\n%s", output)
	}
	if !strings.Contains(string(output), "frontend embed manifest mismatch") {
		t.Fatalf("verifier drift output missing manifest mismatch:\n%s", output)
	}
}

func runFrontendEmbedVerify(t *testing.T, env []string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("bash", "frontend_embed_verify.sh")
	cmd.Dir = "."
	cmd.Env = env
	return cmd.CombinedOutput()
}

func TestPrePushRunsPathBasedReleaseGates(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		content  string
		wantOut  []string
		wantLog  []string
		omitLog  []string
		makefile string
	}{
		{
			name:     "frontend app runs embed verify",
			path:     "frontend-app/src/App.jsx",
			content:  "export const App = () => null\n",
			wantOut:  []string{"[pre-push] frontend embed verify"},
			wantLog:  []string{"make frontend-embed-verify"},
			makefile: "frontend-embed-verify:\n\t@true\n",
		},
		{
			name:    "sql changes run sqlc verify",
			path:    "sql/queries/example.sql",
			content: "-- query\n",
			wantOut: []string{"[pre-push] sqlc verify"},
			wantLog: []string{"make sqlc-verify"},
			omitLog: []string{"npm run lint"},
		},
		{
			name:    "codemap changes run codemap check",
			path:    "docs/doc/codemap/01-terminal-ui.md",
			content: "codemap\n",
			wantOut: []string{"[pre-push] codemap check", "[pre-push] project map check"},
			wantLog: []string{"make codemap-check", "make project-map-check"},
		},
		{
			name:    "project map generator changes run codemap checks",
			path:    "scripts/generate_ai_project_map.mjs",
			content: "#!/usr/bin/env node\n",
			wantOut: []string{"[pre-push] codemap check", "[pre-push] project map check"},
			wantLog: []string{"make codemap-check", "make project-map-check"},
		},
		{
			name:    "project map overrides changes run codemap checks",
			path:    ".ai-project-map.overrides.json",
			content: "{}\n",
			wantOut: []string{"[pre-push] codemap check", "[pre-push] project map check"},
			wantLog: []string{"make codemap-check", "make project-map-check"},
		},
		{
			name:    "skill changes run skill validation",
			path:    ".agents/skills/demo/SKILL.md",
			content: "---\nname: demo\ndescription: demo\n---\n",
			wantOut: []string{"[pre-push] skill mirror validation"},
			wantLog: []string{"python3 scripts/validate_super_agent_skills.py"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPrePushScopeFixture(t)
			writePrePushFakeMake(t, fixture.binDir)
			writePrePushFakePython3(t, fixture.binDir)
			if tc.makefile != "" {
				writeFixTestGuardFile(t, fixture.root, "Makefile", tc.makefile)
				runFixTestGuardGit(t, fixture.root, "add", "Makefile")
				runFixTestGuardGit(t, fixture.root, "commit", "-m", "chore: 更新 make gate")
				fixture.base = strings.TrimSpace(runFixTestGuardGitOutput(t, fixture.root, "rev-parse", "HEAD"))
			}
			writeFixTestGuardFile(t, fixture.root, tc.path, tc.content)
			runFixTestGuardGit(t, fixture.root, "add", tc.path)
			runFixTestGuardGit(t, fixture.root, "commit", "-m", "chore: 更新 gate path")
			head := strings.TrimSpace(runFixTestGuardGitOutput(t, fixture.root, "rev-parse", "HEAD"))

			out := fixture.run(t, head)
			assertOutputContainsAll(t, out, tc.wantOut...)
			log := fixture.log(t)
			assertOutputContainsAll(t, log, tc.wantLog...)
			assertOutputOmitsAll(t, log, tc.omitLog...)
		})
	}
}

func TestPrePushWarnsButAllowsGeneratedArtifactDrift(t *testing.T) {
	fixture := newPrePushScopeFixture(t)
	writePrePushFakeMake(t, fixture.binDir)
	t.Setenv("HOOK_SCOPE_FAIL_MAKE_TARGETS", "codemap-check project-map-check")

	writeFixTestGuardFile(t, fixture.root, "docs/doc/codemap/01-terminal-ui.md", "codemap\n")
	runFixTestGuardGit(t, fixture.root, "add", "docs/doc/codemap/01-terminal-ui.md")
	runFixTestGuardGit(t, fixture.root, "commit", "-m", "chore: 更新 codemap")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, fixture.root, "rev-parse", "HEAD"))

	out := fixture.run(t, head)
	assertOutputContainsAll(t, out,
		"[pre-push] codemap check",
		"[pre-push] project map check",
		"generated artifact drift; this gate is soft and will not block push",
		"如果 push 随后失败，请查看本 warning 之后的第一条",
		"pre-push OK",
	)
	log := fixture.log(t)
	assertOutputContainsAll(t, log, "make codemap-check", "make project-map-check")
}

func TestCodeSizeGuardDefaultAndStrictEnableFunctionCommentGuard(t *testing.T) {
	guard := readScript(t, "code_size_guard.go")

	assertScriptContains(t, guard, "EnforceFuncComments: true")
	assertScriptOrder(t, guard, "EnforceFuncComments: true", "switch cfg.mode")
	assertScriptContains(t, guard, "runStrict(opts)")
	assertScriptContains(t, guard, "runCheck(opts, freezePath)")
}

func TestValidateRiskEvidenceRejectsMissingExtraAndMisfiledIDs(t *testing.T) {
	plan := filepath.Join("..", "docs", "pians", "2026-06-29-production-risk-remediation-plan.md")
	activeIDs := activeRiskIDsForFixture(t, plan)
	commit := currentShortCommitForTest(t)
	goodEvidence := buildRiskEvidenceFixture(activeIDs, map[string][]string{
		"Adjusted Readiness Dispositions": {"P1-07"},
		"Guard-Only Dispositions":         {"P1-32", "P2-01", "P2-24", "P2-27", "P2-28", "P3-04"},
		"Evidence-Only Dispositions":      {"P3-07"},
	}, commit)

	goodPath := writeTempEvidence(t, goodEvidence)
	output, err := runValidateRiskEvidence(t, plan, goodPath)
	if err != nil {
		t.Fatalf("good evidence rejected: %v\n%s", err, output)
	}

	missingPath := writeTempEvidence(t, strings.Replace(goodEvidence, "| P1-01 |", "| P9-99 |", 1))
	output, err = runValidateRiskEvidence(t, plan, missingPath)
	if err == nil {
		t.Fatalf("missing/extra evidence accepted:\n%s", output)
	}
	assertOutputContainsAll(t, output, "missing active evidence rows", "extra active evidence rows")

	misfiledPath := writeTempEvidence(t, strings.Replace(goodEvidence, "## Adjusted Readiness Dispositions", "| P2-24 | lane | red | green | commit | risk |\n\n## Adjusted Readiness Dispositions", 1))
	output, err = runValidateRiskEvidence(t, plan, misfiledPath)
	if err == nil {
		t.Fatalf("misfiled guard-only evidence accepted:\n%s", output)
	}
	assertOutputContainsAll(t, output, "reserved disposition IDs must not appear in active evidence")

	placeholderPath := writeTempEvidence(t, strings.Replace(goodEvidence, "| "+commit+" |", "| working-tree |", 1))
	output, err = runValidateRiskEvidence(t, plan, placeholderPath)
	if err == nil {
		t.Fatalf("placeholder commit evidence accepted:\n%s", output)
	}
	assertOutputContainsAll(t, output, "Commit must be a concrete git SHA")

	unresolvedPath := writeTempEvidence(t, strings.Replace(goodEvidence, "| "+commit+" |", "| ffffffffffffffffffffffffffffffffffffffff |", 1))
	output, err = runValidateRiskEvidence(t, plan, unresolvedPath)
	if err == nil {
		t.Fatalf("unresolved commit evidence accepted:\n%s", output)
	}
	assertOutputContainsAll(t, output, "does not resolve in git")
}

func TestValidateSuperAgentSkillsMirrorComparisonNormalizesLineEndings(t *testing.T) {
	code := `
import importlib.util
import pathlib
import sys
import tempfile

script = pathlib.Path("validate_super_agent_skills.py")
spec = importlib.util.spec_from_file_location("validate_super_agent_skills", script)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

if not module.mirror_bytes_equal(b"line one\r\nline two\r\n", b"line one\nline two\n"):
    sys.exit("line-ending equivalent mirrors must compare equal")
if not module.mirror_bytes_equal(b"line one  \r\nline two\t\r\n\r\n", b"line one\nline two\n"):
    sys.exit("trailing whitespace-only mirror drift must compare equal")
if module.mirror_bytes_equal(b"line one\r\nline two\r\n", b"line one\nchanged\n"):
    sys.exit("content drift must not compare equal")

root = pathlib.Path(tempfile.mkdtemp())
(root / "references/ui-styling/scripts").mkdir(parents=True)
(root / "references/ui-styling/scripts/.coverage").write_bytes(b"sqlite coverage")
(root / "SKILL.md").write_text("skill", encoding="utf-8")
files = module.rel_files(root)
if "references/ui-styling/scripts/.coverage" in files:
    sys.exit("coverage artifact must not participate in provider mirror comparison")
if "SKILL.md" not in files:
    sys.exit("real skill files must still participate in provider mirror comparison")
`
	cmd := exec.Command("python3", "-c", code)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validator mirror comparator rejected expected contract: %v\n%s", err, out)
	}
}

func TestValidateSuperAgentSkillsRejectsPlaceholderAndStaleSkillTokens(t *testing.T) {
	code := `
import importlib.util
import pathlib
import sys
import tempfile

script = pathlib.Path("validate_super_agent_skills.py")
spec = importlib.util.spec_from_file_location("validate_super_agent_skills", script)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

root = pathlib.Path(tempfile.mkdtemp())
skills = root / ".agents" / "skills"
bad = skills / "skill-11"
bad.mkdir(parents=True)
(bad / "SKILL.md").write_text("---\nname: skill-11\ndescription: 11\n---\n11\n", encoding="utf-8")

failures = []
module.check_skill_package_schema(failures, skills)
if not any("placeholder" in item for item in failures):
    sys.exit("placeholder skill package was accepted: " + repr(failures))

good = skills / "good"
good.mkdir(parents=True)
(good / "SKILL.md").write_text(
    "---\nname: good\ndescription: Valid skill description\n---\n# Good\n\nUseful body for validation.\n",
    encoding="utf-8",
)
(good / "stale.md").write_text("mcp-go-agent-orchestration and ~/.claude/skills/design/scripts/logo/generate.py\n", encoding="utf-8")
failures = []
module.check_forbidden_skill_tree_tokens(failures, skills)
if not any("mcp-go-agent-orchestration" in item for item in failures):
    sys.exit("stale orchestration token was accepted: " + repr(failures))
if not any("~/.claude/skills/design/scripts" in item for item in failures):
    sys.exit("stale design script path was accepted: " + repr(failures))
`
	cmd := exec.Command("python3", "-c", code)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validator stale skill guard rejected expected contract: %v\n%s", err, out)
	}
}

func TestCICommitGuardFallsBackToOriginMainForLocalRun(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, "scripts/ci_commit_guard.sh", 0o755)
	copyCommitTitleGuard(t, root, "")
	runFixTestGuardGit(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "refs/remotes/origin/main"))

	writeFixTestGuardFile(t, root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 更新 local guard")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	out, err := runCICommitGuard(t, root, map[string]string{
		"GITHUB_EVENT_NAME":   "",
		"GITHUB_BASE_SHA":     "",
		"GITHUB_HEAD_SHA":     "",
		"GITHUB_EVENT_BEFORE": "",
		"GITHUB_SHA":          "",
	})
	if err != nil {
		t.Fatalf("local ci commit guard failed: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out,
		"[ci-commit-guard] Chinese commit message guard: "+base+".."+head,
		"Chinese commit message guard OK",
		"[ci-commit-guard] fix-test guard: "+base+".."+head,
		"fix-test guard OK",
	)
}

func writePrePushFakeMake(t *testing.T, binDir string) {
	t.Helper()
	path := filepath.Join(binDir, "make")
	content := `#!/usr/bin/env bash
printf 'make %s\n' "$*" >>"$HOOK_SCOPE_LOG"
for target in ${HOOK_SCOPE_FAIL_MAKE_TARGETS:-}; do
  if [ "$*" = "$target" ]; then
    exit 1
  fi
done
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake make: %v", err)
	}
}

func writePrePushFakePython3(t *testing.T, binDir string) {
	t.Helper()
	path := filepath.Join(binDir, "python3")
	content := "#!/usr/bin/env bash\nprintf 'python3 %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake python3: %v", err)
	}
}

func activeRiskIDsForFixture(t *testing.T, plan string) []string {
	t.Helper()
	text := readScript(t, plan)
	idRe := regexp.MustCompile(`^\|\s*(P[0-9]-[0-9]{2})\s*\|`)
	reserved := map[string]bool{
		"P1-07": true,
		"P1-32": true,
		"P2-01": true,
		"P2-24": true,
		"P2-27": true,
		"P2-28": true,
		"P3-04": true,
		"P3-07": true,
	}
	seen := map[string]bool{}
	for line := range strings.SplitSeq(text, "\n") {
		match := idRe.FindStringSubmatch(line)
		if len(match) != 2 || reserved[match[1]] {
			continue
		}
		seen[match[1]] = true
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func buildRiskEvidenceFixture(activeIDs []string, disposition map[string][]string, commit string) string {
	var b strings.Builder
	b.WriteString("# Risk Evidence\n\n")
	b.WriteString("## Active Evidence\n\n")
	b.WriteString("| ID | Lane | RED | GREEN | Commit | Residual Risk |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, id := range activeIDs {
		fmt.Fprintf(&b, "| %s | lane | red | green | %s | none |\n", id, commit)
	}
	for _, section := range []string{"Adjusted Readiness Dispositions", "Guard-Only Dispositions", "Evidence-Only Dispositions"} {
		fmt.Fprintf(&b, "\n## %s\n\n", section)
		b.WriteString("| ID | Disposition | Evidence |\n")
		b.WriteString("|---|---|---|\n")
		for _, id := range disposition[section] {
			fmt.Fprintf(&b, "| %s | recorded | command |\n", id)
		}
	}
	return b.String()
}

func currentShortCommitForTest(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--short=8", "HEAD")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeTempEvidence(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "evidence.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
	return path
}

func runValidateRiskEvidence(t *testing.T, plan, evidence string) (string, error) {
	t.Helper()
	cmd := exec.Command("python3", "validate_risk_evidence.py", "--plan", plan, "--evidence", evidence)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	return string(out), err
}
