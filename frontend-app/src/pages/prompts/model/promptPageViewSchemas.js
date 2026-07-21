import { z } from "zod";

export const ACTIVE_PROMPT_PREF_KEY = "settings.activePromptKey";
export const PROMPTS_REQUEST_TIMEOUT_MS = 8000;
export const PROMPT_KIND_OPTIONS = Object.freeze([
  { key: "expert", label: "专家能力" },
  { key: "recall", label: "参考资料" },
  { key: "default_rule", label: "默认规则" },
]);
export const PROMPT_DRAFT_NOT_READY_MESSAGE =
  "这条内容还需要完善后才能保存，请调整描述后重新生成。";
export const PROMPT_DRAFT_REVIEW_MESSAGE = "保存前请先确认提示里的风险。";
export const PROMPT_ISSUE_COPY = Object.freeze({
  missing_title: "需要补充一个清晰名称。",
  missing_summary: "需要补充一句简短说明。",
  missing_when_to_use: "需要说明 AI 什么时候会使用它。",
  missing_when_not_to_use: "需要说明哪些问题不适合使用它。",
  missing_workflow: "需要补充 AI 执行时的具体步骤。",
  missing_output: "需要写清楚输出会包含哪些栏目或结构。",
  vague_when_to_use: "适用场景还太泛，需要具体到任务或问题类型。",
  vague_output: "需要写清楚输出会包含哪些栏目或结构。",
  missing_save_boundary:
    "需要说明保存边界：没有明确保存工具或用户确认时，只能输出建议保存的条目，不能声称已经保存。",
  missing_recall_topic: "资料需要有一个可检索主题。",
  missing_recall_body: "资料正文不能为空。",
  missing_default_rule_body: "默认规则正文不能为空。",
  missing_hit_examples: "需要补充适合使用的例子。",
  missing_miss_examples: "需要补充不适合使用的例子。",
  missing_source_facts: "需要先从原文提取关键要点，再整理成可用内容。",
  missing_source_fact_coverage:
    "原文里的关键要点没有覆盖完整，建议按缺口重新整理。",
  source_fact_not_applied: "原文里的关键要点没有写入保存内容。",
  external_system_prompt:
    "这是外部模型或产品的系统提示词，不能直接作为默认规则启用。",
  external_system_prompt_source:
    "这是外部模型或产品的系统提示词，保存前需要确认来源和用途。",
  identity_pollution:
    "内容里包含模型或供应商身份声明，不能写入专家能力或默认规则。",
  tool_protocol_pollution:
    "内容里包含外部工具协议，不能写入专家能力或默认规则。",
  overbroad_scope: "适用范围太宽，建议收窄到具体任务或问题。",
  default_rule_conflict: "和已有默认规则可能重复或冲突，保存前需要确认。",
  project_prompt_duplicate:
    "当前项目已有相似提示词，建议先确认是否更新已有内容。",
  builtin_prompt_duplicate: "系统已内置相似能力，不需要再保存一份。",
  duplicate_recall_topic:
    "当前项目已有同名资料主题，请更新已有资料或换一个更具体的主题。",
});
export const promptListItemSchema = z
  .object({
    id: z.string().trim().min(1),
    name: z.string().trim().min(1),
    content: z.string(),
    description: z.string(),
    agentType: z.string().trim().min(1),
    when_to_use: z.string(),
    createdAt: z.string().trim().min(1),
    updatedAt: z.string().trim().min(1),
    match_when: z.unknown().optional(),
    priority: z.number().finite().int().optional(),
    enabled: z.boolean(),
    scope: z.enum(["project", "global"]),
    tags: z.array(z.string()).superRefine((tags, ctx) => {
      const kinds = tags.filter((tag) => tag.startsWith("intent:"));
      if (
        kinds.length !== 1 ||
        !["intent:expert", "intent:recall", "intent:default_rule"].includes(
          kinds[0],
        )
      ) {
        ctx.addIssue({
          code: "custom",
          message: "prompt tags must contain exactly one supported intent kind",
        });
      }
    }),
    state: z.literal("pending_confirm").optional(),
    draft_key: z.string().trim().min(1).optional(),
    draft_status: z.literal("ready_to_save").optional(),
    source_type: z.string().optional(),
    card: z.unknown().optional(),
    issues: z.array(z.unknown()).optional(),
  })
  .strict();
export const promptListResponseSchema = z
  .object({ prompts: z.array(promptListItemSchema) })
  .strict();
export const dashboardJsonValueSchema = z.lazy(() =>
  z.union([
    z.string(),
    z.number(),
    z.boolean(),
    z.null(),
    z.array(dashboardJsonValueSchema),
    z.record(z.string(), dashboardJsonValueSchema),
  ]),
);
export const dashboardPromptItemSchema = z
  .object({
    id: z.number().finite().int(),
    prompt_key: z.string().trim().min(1),
    title: z.string(),
    agent_key: z.string(),
    tool_name: z.string(),
    prompt_text: z.string(),
    when_to_use: z.string(),
    variables: dashboardJsonValueSchema,
    tags: z.array(z.string()),
    enabled: z.boolean(),
    manually_edited: z.boolean(),
    match_when: dashboardJsonValueSchema.optional(),
    priority: z.number().finite().int(),
    created_by: z.string(),
    updated_by: z.string(),
    created_at: z.string().trim().min(1),
    updated_at: z.string().trim().min(1),
    description: z.string(),
  })
  .strict();
export const dashboardPromptListResponseSchema = z
  .object({ prompts: z.array(dashboardPromptItemSchema) })
  .strict();
