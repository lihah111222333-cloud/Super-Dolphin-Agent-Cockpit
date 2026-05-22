package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type methodologyPromptExpectation struct {
	PromptKey           string
	OriginalDescription string
	OriginalWhenToUse   string
	OriginalTags        []string
	ForwardFragments    []string
	ForwardTags         []string
}

func TestPromptTemplateRuntimeMetadataMigrationUpdatesMethodologyExpertsAndRollbackRestores(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	for _, seed := range methodologyPromptExpectations() {
		insertMethodologyRuntimeTemplate(t, ctx, conn, seed, "system.seed", "system.seed", false)
	}

	applyPromptTemplateRuntimeMetadataMigration0106(t, ctx, conn, schema)

	for _, want := range methodologyPromptExpectations() {
		row := queryEnterprisePromptRow(t, ctx, conn, want.PromptKey)
		requireMethodologyForwardState(t, want, row)
	}

	dataRestore := extractRollbackSQLBlock(t, "0106 data restore")
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, dataRestore, "0106 data restore")

	for _, want := range methodologyPromptExpectations() {
		row := queryEnterprisePromptRow(t, ctx, conn, want.PromptKey)
		requireEnterprisePromptEqual(t, want.PromptKey, row, enterprisePromptRow{
			Description: want.OriginalDescription,
			WhenToUse:   want.OriginalWhenToUse,
			Tags:        row.Tags,
			CreatedBy:   "system.seed",
			UpdatedBy:   "system.seed",
		})
		requireEnterpriseTagsExact(t, want.PromptKey, row.Tags, want.OriginalTags)
	}
}

func TestPromptTemplateRuntimeMetadataMigrationPreservesUserOwnedOrEditedMethodologyExpert(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	cases := []struct {
		name           string
		createdBy      string
		updatedBy      string
		manuallyEdited bool
	}{
		{name: "rpc_created", createdBy: "rpc.prompts", updatedBy: "rpc.prompts"},
		{name: "rpc_updated", createdBy: "system.seed", updatedBy: "rpc.prompts"},
		{name: "manually_edited", createdBy: "system.seed", updatedBy: "system.seed", manuallyEdited: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			truncatePromptCleanupTemplates(t, ctx, conn)
			insertMethodologyRuntimeTemplate(t, ctx, conn, methodologyPromptExpectations()[0], tc.createdBy, tc.updatedBy, tc.manuallyEdited)

			applyPromptTemplateRuntimeMetadataMigration0106(t, ctx, conn, schema)
			dataRestore := extractRollbackSQLBlock(t, "0106 data restore")
			applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, dataRestore, "0106 data restore")

			row := queryEnterprisePromptRow(t, ctx, conn, "main/planning")
			requireEnterprisePromptEqual(t, "main/planning", row, enterprisePromptRow{
				Description:    methodologyPromptExpectations()[0].OriginalDescription,
				WhenToUse:      methodologyPromptExpectations()[0].OriginalWhenToUse,
				Tags:           row.Tags,
				CreatedBy:      tc.createdBy,
				UpdatedBy:      tc.updatedBy,
				ManuallyEdited: tc.manuallyEdited,
			})
			requireEnterpriseTagsExact(t, "main/planning", row.Tags, methodologyPromptExpectations()[0].OriginalTags)
		})
	}
}

func insertMethodologyRuntimeTemplate(t *testing.T, ctx context.Context, conn *pgx.Conn, seed methodologyPromptExpectation, createdBy, updatedBy string, manuallyEdited bool) {
	t.Helper()

	_, err := conn.Exec(ctx, `
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, prompt_text, tags, description, when_to_use,
    enabled, manually_edited, created_by, updated_by
) VALUES ($1, $1, 'main', 'methodology body', $2::jsonb, $3, $4, TRUE, $5, $6, $7)
`, seed.PromptKey, jsonTags(t, seed.OriginalTags), seed.OriginalDescription, seed.OriginalWhenToUse, manuallyEdited, createdBy, updatedBy)
	if err != nil {
		t.Fatalf("insert methodology prompt %s: %v", seed.PromptKey, err)
	}
}

func requireMethodologyForwardState(t *testing.T, want methodologyPromptExpectation, row enterprisePromptRow) {
	t.Helper()

	for _, fragment := range want.ForwardFragments {
		if !strings.Contains(row.WhenToUse, fragment) {
			t.Fatalf("%s when_to_use = %q, want fragment %q", want.PromptKey, row.WhenToUse, fragment)
		}
	}
	requireEnterpriseTags(t, want.PromptKey, row.Tags, want.ForwardTags)
	requireEnterprisePromptEqual(t, want.PromptKey, row, enterprisePromptRow{
		Description: row.Description,
		WhenToUse:   row.WhenToUse,
		Tags:        row.Tags,
		CreatedBy:   "system.seed",
		UpdatedBy:   "migration:0106",
	})
}

func methodologyPromptExpectations() []methodologyPromptExpectation {
	return []methodologyPromptExpectation{
		{
			PromptKey:           "main/planning",
			OriginalDescription: "规划 — 任务拆分 / 方案设计 / 路线图（编程和非编程通用）",
			OriginalWhenToUse:   "任务拆解、实施计划、里程碑、风险和依赖梳理",
			OriginalTags:        []string{"帮我规划", "任务规划", "step by step", "拆分任务", "make a plan", "planning this", "制定计划", "分步实施", "里程碑", "roadmap", "技术方案", "implementation plan", "怎么落地", "项目计划", "实施计划", "scope.global"},
			ForwardFragments:    []string{"阶段化规格", "依赖", "风险", "用户确认", "验收点", "不写实现代码"},
			ForwardTags:         []string{"method:planning", "phase:requirements", "phase:design", "phase:task_breakdown", "needs:user_confirmation", "output:handoff_plan", "evidence:acceptance_link"},
		},
		{
			PromptKey:           "main/code-review",
			OriginalDescription: "代码审核 — 用户请求 review/审核代码",
			OriginalWhenToUse:   "代码审查、diff 风险评估、回归与安全问题检查",
			OriginalTags:        []string{"code review", "审 diff", "审核代码", "review 一下", "review 这段", "review this", "帮我 review", "可以 review", "审查代码", "看看这段代码", "code-review", "scope.global"},
			ForwardFragments:    []string{"findings-first", "严重等级", "file:line", "事实", "推断风险", "未知或未验证"},
			ForwardTags:         []string{"method:code_review", "review:findings_first", "review:severity", "review:file_line", "review:evidence_type", "review:test_gap"},
		},
		{
			PromptKey:           "main/code-debug",
			OriginalDescription: "调试 — 用户报错/请求排查",
			OriginalWhenToUse:   "错误排查、panic/exception/traceback 分析、最小复现定位",
			OriginalTags:        []string{"这个 bug", "为什么报错", "why fails", "why does it fail", "stack trace", "堆栈信息", "panic", "报错了", "不 work", "不工作", "debug 一下", "帮我查 bug", "排查一下", "traceback", "exception", "报错信息", "scope.global"},
			ForwardFragments:    []string{"错误证据", "最小复现", "根因定位", "验证闭环"},
			ForwardTags:         []string{"method:debug", "debug:error_evidence", "debug:minimal_repro", "debug:root_cause", "debug:verification", "debug:unverified_boundary"},
		},
	}
}
