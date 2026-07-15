package main

import (
	"os/exec"
	"testing"
)

func TestValidateSuperAgentSkillsReviewContract(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{
			name: "accepts repository review contract",
			code: `
rows = []
for index in range(1, 20):
    if index == 1:
        rows.append("| D01 架构边界 | registry drift | DefaultBackendBoundaryRegistry()、codemap 13、archtest |")
    elif index == 8:
        rows.append("| D08 Skill/Memory/Prompt/Thread | route drift | Skill 用 codemap 07A；Thread 写侧用 codemap 07B；provider mirror 用 codemap 09；Memory/Prompt/Thread 用 codemap 11；命中 Dream 时才用 codemap 12 |")
    else:
        rows.append(f"| D{index:02d} dimension | risk | evidence |")

review = "\n".join(rows) + """
D01-D19 coverage ledger: Applied or N/A + reason.
Each lane records review object, dimensions, evidence, validation, and residual risks; lane PASS is not repo PASS.
D17 的生产字段; D18 回答; D19 回答; canonical .agents/skills.
Follow docs/契约/fix-workflow-convention.md: Repro -> Root Cause -> RED -> Fix -> GREEN -> Guard -> Residual Retest -> Report.
Gate truth comes from .githooks/pre-commit, .githooks/pre-push, .githooks/README.md, and scripts/ai_maintenance_gates.sh; do not copy a static command list.
Bind evidence to worktree, staged tree, commit, or push range.
priority | dimension | coverage | reachability | file:line_start-line_end | violated_contract | evidence | fix | bug_locking_test | gate
"""
failures = []
module.check_review_skill(failures, review)
if failures:
    raise SystemExit("repository review contract was rejected: " + repr(failures))
`,
		},
		{
			name: "rejects semantic regressions",
			code: `
rows = [f"| D{index:02d} dimension | risk | evidence |" for index in range(1, 20)]
rows[0] = "| D01 架构边界 | hard-coded directory list | cmd/app/module paths |"
rows[7] = "| D08 Skill/Memory/Prompt/Thread | route drift | validator、codemap 11/12 |"
review = "\n".join(rows) + "\nD17 的生产字段; D18 回答; D19 回答; canonical .agents/skills.\n"
failures = []
module.check_review_skill(failures, review)
want = (
    "D01 canonical boundary routing",
    "D08 repository navigation",
    "D01-D19 coverage ledger",
    "multi-lane evidence ledger",
    "fix workflow",
    "authoritative gate routing",
    "review object binding",
    "output schema",
)
for marker in want:
    if not any(marker in failure for failure in failures):
        raise SystemExit(f"missing {marker!r} failure: {failures!r}")
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := `
import importlib.util
import pathlib

script = pathlib.Path("validate_super_agent_skills.py")
spec = importlib.util.spec_from_file_location("validate_super_agent_skills", script)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
` + tt.code
			cmd := exec.Command("python3", "-c", code)
			cmd.Dir = "."
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("review contract validator failed: %v\n%s", err, out)
			}
		})
	}
}
