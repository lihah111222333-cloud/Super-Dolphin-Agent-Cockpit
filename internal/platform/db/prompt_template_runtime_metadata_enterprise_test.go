package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type enterprisePromptExpectation struct {
	PromptKey            string
	OriginalDescription  string
	OriginalWhenToUse    string
	OriginalTags         []string
	ForwardFragments     []string
	ForwardWhenToUsePart string
	ForwardTags          []string
}

type enterprisePromptRow struct {
	Description    string
	WhenToUse      string
	Tags           string
	CreatedBy      string
	UpdatedBy      string
	ManuallyEdited bool
}

func TestPromptTemplateRuntimeMetadataMigrationUpdatesEnterprisePresetsAndRollbackRestores(t *testing.T) {
	ctx, conn, schema := setupPromptTemplateCleanupMigrationTest(t)
	for _, seed := range enterprisePromptExpectations() {
		insertEnterpriseRuntimeTemplate(t, ctx, conn, seed.PromptKey, seed.OriginalDescription, seed.OriginalWhenToUse, seed.OriginalTags, "system.seed", "system.seed", false)
	}
	insertEnterpriseRuntimeTemplate(t, ctx, conn, "main/paper_summarizer", "removed original", "removed when", []string{"research", "scope.global"}, "system.seed", "system.seed", false)

	applyPromptTemplateRuntimeMetadataMigration0106(t, ctx, conn, schema)

	for _, want := range enterprisePromptExpectations() {
		requireEnterprisePromptForwardState(t, ctx, conn, want)
	}
	requireEnterprisePromptUnchanged(t, ctx, conn, "main/paper_summarizer", "removed original", "removed when", "system.seed")

	dataRestore := extractRollbackSQLBlock(t, "0106 data restore")
	applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, dataRestore, "0106 data restore")

	for _, want := range enterprisePromptExpectations() {
		requireEnterprisePromptRollbackState(t, ctx, conn, want)
	}
}

func TestPromptTemplateRuntimeMetadataMigrationPreservesUserOwnedOrEditedEnterprisePreset(t *testing.T) {
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
			insertEnterpriseRuntimeTemplate(t, ctx, conn, "main/morning_briefer", "user description", "user when", []string{"scope.global", "user-owned"}, tc.createdBy, tc.updatedBy, tc.manuallyEdited)

			applyPromptTemplateRuntimeMetadataMigration0106(t, ctx, conn, schema)
			dataRestore := extractRollbackSQLBlock(t, "0106 data restore")
			applyPromptTemplateCleanupSQLBlock(t, ctx, conn, schema, dataRestore, "0106 data restore")

			row := queryEnterprisePromptRow(t, ctx, conn, "main/morning_briefer")
			requireEnterprisePromptEqual(t, "main/morning_briefer", row, enterprisePromptRow{
				Description:    "user description",
				WhenToUse:      "user when",
				Tags:           row.Tags,
				CreatedBy:      tc.createdBy,
				UpdatedBy:      tc.updatedBy,
				ManuallyEdited: tc.manuallyEdited,
			})
			if !jsonTextArrayContains(row.Tags, "user-owned") {
				t.Fatalf("user-owned enterprise tags changed: %s", row.Tags)
			}
		})
	}
}

func insertEnterpriseRuntimeTemplate(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, description, whenToUse string, tags []string, createdBy, updatedBy string, manuallyEdited bool) {
	t.Helper()

	_, err := conn.Exec(ctx, `
INSERT INTO prompt_templates (
    prompt_key, title, agent_key, prompt_text, tags, description, when_to_use,
    enabled, manually_edited, created_by, updated_by
) VALUES ($1, $1, 'enterprise', 'enterprise body', $2::jsonb, $3, $4, TRUE, $5, $6, $7)
`, promptKey, jsonTags(t, tags), description, whenToUse, manuallyEdited, createdBy, updatedBy)
	if err != nil {
		t.Fatalf("insert enterprise prompt %s: %v", promptKey, err)
	}
}

func requireEnterprisePromptForwardState(t *testing.T, ctx context.Context, conn *pgx.Conn, want enterprisePromptExpectation) {
	t.Helper()

	row := queryEnterprisePromptRow(t, ctx, conn, want.PromptKey)
	for _, fragment := range want.ForwardFragments {
		if !strings.Contains(row.Description, fragment) {
			t.Fatalf("%s description = %q, want fragment %q", want.PromptKey, row.Description, fragment)
		}
	}
	if !strings.Contains(row.WhenToUse, want.ForwardWhenToUsePart) {
		t.Fatalf("%s when_to_use = %q, want fragment %q", want.PromptKey, row.WhenToUse, want.ForwardWhenToUsePart)
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

func requireEnterprisePromptRollbackState(t *testing.T, ctx context.Context, conn *pgx.Conn, want enterprisePromptExpectation) {
	t.Helper()

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

func requireEnterprisePromptUnchanged(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey, description, whenToUse, updatedBy string) {
	t.Helper()

	row := queryEnterprisePromptRow(t, ctx, conn, promptKey)
	requireEnterprisePromptEqual(t, promptKey, row, enterprisePromptRow{
		Description: description,
		WhenToUse:   whenToUse,
		Tags:        row.Tags,
		CreatedBy:   "system.seed",
		UpdatedBy:   updatedBy,
	})
	for _, tag := range []string{"schema:input_sources", "workflow:enterprise"} {
		if jsonTextArrayContains(row.Tags, tag) {
			t.Fatalf("%s unexpectedly gained tag %q: %s", promptKey, tag, row.Tags)
		}
	}
}

func queryEnterprisePromptRow(t *testing.T, ctx context.Context, conn *pgx.Conn, promptKey string) enterprisePromptRow {
	t.Helper()

	var row enterprisePromptRow
	err := conn.QueryRow(ctx, `
SELECT description, when_to_use, tags::text, created_by, updated_by, manually_edited
FROM prompt_templates
WHERE prompt_key = $1
`, promptKey).Scan(&row.Description, &row.WhenToUse, &row.Tags, &row.CreatedBy, &row.UpdatedBy, &row.ManuallyEdited)
	if err != nil {
		t.Fatalf("query enterprise prompt %s: %v", promptKey, err)
	}
	return row
}

func requireEnterprisePromptEqual(t *testing.T, promptKey string, got, want enterprisePromptRow) {
	t.Helper()

	checks := []struct {
		field string
		got   string
		want  string
	}{
		{field: "description", got: got.Description, want: want.Description},
		{field: "when_to_use", got: got.WhenToUse, want: want.WhenToUse},
		{field: "created_by", got: got.CreatedBy, want: want.CreatedBy},
		{field: "updated_by", got: got.UpdatedBy, want: want.UpdatedBy},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Fatalf("%s %s = %q, want %q", promptKey, check.field, check.got, check.want)
		}
	}
	if got.ManuallyEdited != want.ManuallyEdited {
		t.Fatalf("%s manually_edited = %v, want %v", promptKey, got.ManuallyEdited, want.ManuallyEdited)
	}
}

func requireEnterpriseTags(t *testing.T, promptKey, gotTags string, wantTags []string) {
	t.Helper()

	for _, tag := range wantTags {
		if !jsonTextArrayContains(gotTags, tag) {
			t.Fatalf("%s tags = %s, want tag %q", promptKey, gotTags, tag)
		}
	}
}

func requireEnterpriseTagsExact(t *testing.T, promptKey, gotTags string, wantTags []string) {
	t.Helper()

	var got []string
	if err := json.Unmarshal([]byte(gotTags), &got); err != nil {
		t.Fatalf("%s tags json = %s: %v", promptKey, gotTags, err)
	}
	if len(got) != len(wantTags) {
		t.Fatalf("%s tags = %v, want exactly %v", promptKey, got, wantTags)
	}
	wantSet := make(map[string]struct{}, len(wantTags))
	for _, tag := range wantTags {
		wantSet[tag] = struct{}{}
	}
	for _, tag := range got {
		if _, ok := wantSet[tag]; !ok {
			t.Fatalf("%s tags = %v, unexpected tag %q, want exactly %v", promptKey, got, tag, wantTags)
		}
	}
}

func enterprisePromptExpectations() []enterprisePromptExpectation {
	return []enterprisePromptExpectation{
		enterprisePromptExpectationFor("main/morning_briefer", "Create a concise morning brief from upstream context and sharedfiles.", "把笔记、链接、任务和共享文件整理成晨报", []string{"briefing", "daily", "summary", "operations", "morning", "scope.global"}, []string{"输入来源", "时间窗口", "今日重点", "风险"}, "晨报", []string{"brief:today_focus", "brief:risk"}),
		enterprisePromptExpectationFor("main/pr_summarizer", "Summarize pull request changes and review focus areas.", "PR 变更摘要、行为影响、风险区域和 review 重点", []string{"code", "pull-request", "review", "summary", "engineering", "scope.global"}, []string{"PR 范围", "行为影响", "风险区域", "review 重点"}, "PR", []string{"pr:scope", "pr:behavior_impact", "pr:risk_area", "pr:review_focus"}),
		enterprisePromptExpectationFor("main/weekly_reviewer", "Create a weekly review from project notes and task updates.", "周报复盘、完成事项、决策、风险和下周优先级", []string{"weekly", "review", "planning", "status", "follow-up", "scope.global"}, []string{"本周完成", "关键决策", "阻塞", "下周优先级"}, "一周工作", []string{"weekly:outcomes", "weekly:decisions", "weekly:blockers"}),
		enterprisePromptExpectationFor("main/data_inspector", "Inspect datasets or metrics and highlight trends, gaps, and anomalies.", "数据样本检查、字段含义、异常值和质量问题归纳", []string{"data", "metrics", "inspection", "analysis", "quality", "scope.global"}, []string{"数据来源", "字段含义", "异常值", "质量问题"}, "数据样本", []string{"data:field_meaning", "data:outlier", "data:quality_issue"}),
		enterprisePromptExpectationFor("main/email_drafter", "Draft concise emails from supplied context and requested outcomes.", "邮件起草、回复、语气调整和收件人导向改写", []string{"email", "writing", "communication", "draft", "business", "scope.global"}, []string{"收件人", "语气", "目的", "行动请求"}, "邮件", []string{"email:recipient", "email:tone", "email:purpose", "email:action_request", "email:follow_up"}),
		enterprisePromptExpectationFor("main/health_reporter", "Write operational health reports from logs, metrics, and status notes.", "系统健康、状态摘要、异常信号和行动建议", []string{"health", "ops", "status", "incident", "monitoring", "scope.global"}, []string{"监控来源", "健康状态", "异常信号", "影响范围"}, "健康报告", []string{"health:status", "health:anomaly", "health:impact"}),
		enterprisePromptExpectationFor("main/source_monitor", "Monitor source material and summarize meaningful changes.", "监控来源更新、提取变化、标出风险和跟进项", []string{"sources", "monitoring", "changes", "research", "report", "scope.global"}, []string{"来源", "变化摘要", "触发条件", "风险等级"}, "信息源变化", []string{"source:change_summary", "source:trigger", "source:risk_level"}),
		enterprisePromptExpectationFor("main/note_organizer", "Organize messy notes into structured facts, decisions, and actions.", "整理散乱笔记、归类主题、提取行动项和决策", []string{"notes", "organization", "cleanup", "actions", "knowledge", "scope.global"}, []string{"主题归类", "事实", "决策", "行动项"}, "杂乱记录", []string{"note:topic", "note:fact", "note:decision", "note:action_item"}),
		enterprisePromptExpectationFor("main/todo_prioritizer", "Prioritize tasks with dependencies, blockers, and defer decisions.", "整理待办、排序优先级、识别依赖和阻塞", []string{"todo", "priority", "planning", "backlog", "execution", "scope.global"}, []string{"待办来源", "优先级", "依赖", "阻塞"}, "待办", []string{"todo:priority", "todo:dependency", "todo:blocker"}),
	}
}

func enterprisePromptExpectationFor(promptKey, originalDescription, originalWhenToUse string, originalTags, fragments []string, whenToUsePart string, specificTags []string) enterprisePromptExpectation {
	commonTags := []string{
		"scope.global",
		"intent:enterprise_workflow",
		"workflow:enterprise",
		"schema:input_sources",
		"schema:output_structure",
		"schema:evidence",
		"schema:time_window",
		"schema:confidence",
		"schema:uncertainty",
		"schema:owner",
		"schema:next_step",
	}
	return enterprisePromptExpectation{
		PromptKey:            promptKey,
		OriginalDescription:  originalDescription,
		OriginalWhenToUse:    originalWhenToUse,
		OriginalTags:         originalTags,
		ForwardFragments:     fragments,
		ForwardWhenToUsePart: whenToUsePart,
		ForwardTags:          append(commonTags, specificTags...),
	}
}
