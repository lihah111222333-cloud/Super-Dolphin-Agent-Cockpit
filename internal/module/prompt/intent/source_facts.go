package intent

import (
	"strings"
	"unicode"
)

// source profile 常量用于标识外部输入材料的来源类型，影响后续必填事实类别。
const (
	promptIntentSourceProfileExternalPrompt = "external_prompt" // 外部 system/provider/persona prompt
	promptIntentSourceProfileReferenceDoc   = "reference_doc"   // 参考资料/文档
	promptIntentSourceProfileTableData      = "table_data"      // 表格/价格表
	promptIntentSourceProfileWorkflowSOP    = "workflow_sop"    // 流程/操作规范
	promptIntentSourceProfileAPIDoc         = "api_doc"         // API 文档
	promptIntentSourceProfileMeetingNotes   = "meeting_notes"   // 会议纪要
	promptIntentSourceProfileBusinessRule   = "business_rule"   // 业务规则
	promptIntentSourceProfileUnknown        = "unknown"         // 未知来源类型
)

// promptIntentSourceFactRequirement 描述特定 source profile 下必须覆盖的关键要点类别。
type promptIntentSourceFactRequirement struct {
	category string   // source_facts.category 规范化后的目标类别
	label    string   // 面向用户的问题提示标签
	terms    []string // 原文命中这些词时该类别才必填
	always   bool     // true 表示该来源类型下无条件必填
}

// promptIntentSourceFactIssues 根据 source profile 检查卡片是否覆盖了必要的来源事实类别，
// 并检查已提取的事实是否已落实到卡片内容中。
func promptIntentSourceFactIssues(_ Kind, rawInput string, card Card) []Issue {
	rawText := normalizePromptIntentText(rawInput)
	profile := promptIntentResolveSourceProfile(rawText, card.SourceProfile)
	if profile == promptIntentSourceProfileUnknown {
		return nil
	}
	required := promptIntentRequiredSourceFactCategories(profile, rawText)
	if len(required) == 0 {
		return nil
	}
	var issues []Issue
	categories := promptIntentSourceFactCategories(card.SourceFacts)
	if len(categories) == 0 {
		issues = append(issues, Issue{
			Code:     "missing_source_facts",
			Severity: "review",
			Message:  promptIntentSourceProfileLabel(profile) + "需要先提取原文关键要点，再整理成可用内容。",
		})
	}
	if missing := missingPromptIntentSourceFactCategories(categories, required); len(missing) > 0 {
		issues = append(issues, Issue{
			Code:     "missing_source_fact_coverage",
			Severity: "review",
			Message:  promptIntentSourceProfileLabel(profile) + "的关键要点覆盖不完整：" + strings.Join(missing, "、"),
		})
	}
	issues = append(issues, promptIntentSourceFactApplicationIssues(card)...)
	return issues
}

// promptIntentResolveSourceProfile 先尝试从原文推断 source profile，失败则使用卡片声明值。
func promptIntentResolveSourceProfile(rawText, candidate string) string {
	if profile := promptIntentInferSourceProfile(rawText); profile != promptIntentSourceProfileUnknown {
		return profile
	}
	return promptIntentNormalizeSourceProfile(candidate)
}

// promptIntentNormalizeSourceProfile 将用户/LLM 传入的 source_profile 字符串规范化为枚举值，
// 无法识别时返回 unknown。
func promptIntentNormalizeSourceProfile(value string) string {
	profile := strings.ToLower(strings.TrimSpace(value))
	profile = strings.ReplaceAll(profile, "-", "_")
	profile = strings.ReplaceAll(profile, " ", "_")
	switch profile {
	case promptIntentSourceProfileExternalPrompt,
		promptIntentSourceProfileReferenceDoc,
		promptIntentSourceProfileTableData,
		promptIntentSourceProfileWorkflowSOP,
		promptIntentSourceProfileAPIDoc,
		promptIntentSourceProfileMeetingNotes,
		promptIntentSourceProfileBusinessRule:
		return profile
	default:
		return promptIntentSourceProfileUnknown
	}
}

// promptIntentInferSourceProfile 根据原文特征自动推断来源类型，按外部prompt→API文档→表格→流程→会议→业务规则→参考资料顺序匹配。
func promptIntentInferSourceProfile(rawText string) string {
	switch {
	case promptIntentLooksLikeExternalSystemPrompt(rawText):
		return promptIntentSourceProfileExternalPrompt
	case promptIntentLooksLikeAPIDoc(rawText):
		return promptIntentSourceProfileAPIDoc
	case promptIntentLooksLikeTableData(rawText):
		return promptIntentSourceProfileTableData
	case promptIntentLooksLikeWorkflowSOP(rawText):
		return promptIntentSourceProfileWorkflowSOP
	case promptIntentLooksLikeMeetingNotes(rawText):
		return promptIntentSourceProfileMeetingNotes
	case promptIntentLooksLikeBusinessRuleSource(rawText):
		return promptIntentSourceProfileBusinessRule
	case promptIntentLooksLikeReferenceDoc(rawText):
		return promptIntentSourceProfileReferenceDoc
	default:
		return promptIntentSourceProfileUnknown
	}
}

// promptIntentSourceFactCategories 将已提取的 source_facts 转换为类别集合（含别名扩展），
// 用于快速检查必需类别是否覆盖。
func promptIntentSourceFactCategories(facts []SourceFact) map[string]bool {
	categories := map[string]bool{}
	for _, fact := range facts {
		category := promptIntentNormalizeSourceFactCategory(fact.Category)
		if category != "" && strings.TrimSpace(fact.Summary) != "" {
			categories[category] = true
			for _, alias := range promptIntentSourceFactCategoryAliases(category) {
				categories[alias] = true
			}
		}
	}
	return categories
}

// missingPromptIntentSourceFactCategories 返回必需类别中尚未被覆盖的标签列表。
func missingPromptIntentSourceFactCategories(
	categories map[string]bool,
	required []promptIntentSourceFactRequirement,
) []string {
	var missing []string
	for _, requirement := range required {
		if !categories[requirement.category] {
			missing = append(missing, requirement.label)
		}
	}
	return missing
}

// promptIntentRequiredSourceFactCategories 根据 source profile 和原文内容，
// 返回该场景下必须覆盖的来源事实类别列表（按 terms 按需筛选）。
func promptIntentRequiredSourceFactCategories(profile, rawText string) []promptIntentSourceFactRequirement {
	switch profile {
	case promptIntentSourceProfileExternalPrompt:
		return promptIntentFilterSourceFactRequirements(rawText, promptIntentExternalPromptSourceFacts())
	case promptIntentSourceProfileTableData:
		return promptIntentFilterSourceFactRequirements(rawText, promptIntentTableDataSourceFacts())
	case promptIntentSourceProfileAPIDoc:
		return promptIntentFilterSourceFactRequirements(rawText, promptIntentAPIDocSourceFacts())
	case promptIntentSourceProfileWorkflowSOP:
		return promptIntentFilterSourceFactRequirements(rawText, promptIntentWorkflowSOPSourceFacts())
	case promptIntentSourceProfileMeetingNotes:
		return promptIntentFilterSourceFactRequirements(rawText, promptIntentMeetingNotesSourceFacts())
	case promptIntentSourceProfileReferenceDoc:
		return promptIntentFilterSourceFactRequirements(rawText, promptIntentReferenceDocSourceFacts())
	case promptIntentSourceProfileBusinessRule:
		return promptIntentFilterSourceFactRequirements(rawText, promptIntentBusinessRuleSourceFacts())
	default:
		return nil
	}
}

// promptIntentFilterSourceFactRequirements 从规则列表中过滤出原文实际触发的必需类别，
// always=true 的条目始终保留，否则需要原文包含对应 terms 才保留。
func promptIntentFilterSourceFactRequirements(
	rawText string,
	rules []promptIntentSourceFactRequirement,
) []promptIntentSourceFactRequirement {
	out := make([]promptIntentSourceFactRequirement, 0, len(rules))
	seen := map[string]bool{}
	for _, rule := range rules {
		if seen[rule.category] || (!rule.always && !containsAnyPromptIntentTerm(rawText, rule.terms)) {
			continue
		}
		seen[rule.category] = true
		out = append(out, rule)
	}
	return out
}

// promptIntentExternalPromptSourceFacts 返回外部 prompt 必须保留或转写的关键事实类别。
// 身份和安全边界始终必填，工具协议等只在原文实际出现时要求覆盖。
func promptIntentExternalPromptSourceFacts() []promptIntentSourceFactRequirement {
	return []promptIntentSourceFactRequirement{
		{category: "identity", label: "外部身份", always: true},
		{category: "safety", label: "安全边界", always: true},
		{category: "tool_protocol", label: "外部工具协议", terms: promptIntentExternalToolProtocolTerms()},
		{category: "search_reading", label: "搜索和读取代码", terms: []string{"search", "read", "file", "codebase"}},
		{category: "code_change", label: "代码修改", terms: []string{"code change", "modify", "edit", "implementation"}},
		{category: "dependency_api", label: "依赖/API 检查", terms: []string{"dependency", "dependencies", "library", "api", "external api"}},
		{category: "debugging", label: "调试", terms: []string{"debug", "root cause", "debugging"}},
		{category: "task_management", label: "任务管理", terms: []string{"todo", "task list", "one task", "in_progress"}},
		{category: "output", label: "输出要求", terms: []string{"output", "response", "report", "summary"}},
	}
}

// promptIntentTableDataSourceFacts 返回表格/价格表资料需要覆盖的字段、关键行和查询范围。
func promptIntentTableDataSourceFacts() []promptIntentSourceFactRequirement {
	return []promptIntentSourceFactRequirement{
		{category: "topic", label: "资料主题", always: true},
		{category: "fields", label: "字段含义", always: true},
		{category: "key_rows", label: "关键行", always: true},
		{category: "units", label: "单位/币种", terms: []string{"price", "pricing", "amount", "currency", "价格", "金额", "费用", "元", "币种", "%", "单位"}},
		{category: "scope", label: "适用范围", always: true},
		{category: "query_examples", label: "可查询问题", always: true},
	}
}

// promptIntentAPIDocSourceFacts 返回 API 文档需要覆盖的接口、鉴权、返回和错误边界。
func promptIntentAPIDocSourceFacts() []promptIntentSourceFactRequirement {
	return []promptIntentSourceFactRequirement{
		{category: "endpoint", label: "接口地址", always: true},
		{category: "method", label: "请求方法", always: true},
		{category: "parameters", label: "请求参数", terms: []string{"parameter", "parameters", "params", "参数", "字段"}},
		{category: "auth", label: "鉴权方式", terms: []string{"auth", "authorization", "bearer", "token", "鉴权", "认证"}},
		{category: "response", label: "返回结构", terms: []string{"response", "return", "returns", "返回", "响应"}},
		{category: "errors", label: "错误码", terms: []string{"error", "errors", "status code", "错误码", "状态码"}},
		{category: "limits", label: "调用限制", terms: []string{"limit", "rate limit", "quota", "限制", "频率"}},
		{category: "examples", label: "调用示例", terms: []string{"example", "curl", "示例", "例子"}},
	}
}

// promptIntentWorkflowSOPSourceFacts 返回流程 SOP 需要覆盖的触发条件、输入、步骤和输出。
func promptIntentWorkflowSOPSourceFacts() []promptIntentSourceFactRequirement {
	return []promptIntentSourceFactRequirement{
		{category: "trigger", label: "触发条件", terms: []string{"trigger", "when", "if", "触发", "当", "如果"}},
		{category: "inputs", label: "输入材料", always: true},
		{category: "steps", label: "执行步骤", always: true},
		{category: "roles", label: "角色分工", terms: []string{"role", "owner", "负责人", "角色", "审批人", "申请人"}},
		{category: "exceptions", label: "例外处理", terms: []string{"exception", "except", "fallback", "例外", "异常", "紧急"}},
		{category: "outputs", label: "输出结果", always: true},
	}
}

// promptIntentMeetingNotesSourceFacts 返回会议纪要需要覆盖的事实、决策、行动项和待确认事项。
func promptIntentMeetingNotesSourceFacts() []promptIntentSourceFactRequirement {
	return []promptIntentSourceFactRequirement{
		{category: "facts", label: "事实背景", always: true},
		{category: "decisions", label: "会议决策", terms: []string{"decision", "decisions", "decided", "决定", "决策", "结论"}},
		{category: "action_items", label: "行动项", terms: []string{"action item", "todo", "待办", "行动项"}},
		{category: "owners", label: "负责人", terms: []string{"owner", "owners", "负责人", "责任人"}},
		{category: "dates", label: "日期/截止时间", terms: []string{"date", "deadline", "due", "日期", "截止", "时间"}},
		{category: "open_questions", label: "未决问题", terms: []string{"open question", "question", "待确认", "未决", "问题"}},
	}
}

// promptIntentReferenceDocSourceFacts 返回参考资料需要覆盖的主题、要点、来源和使用方式。
func promptIntentReferenceDocSourceFacts() []promptIntentSourceFactRequirement {
	return []promptIntentSourceFactRequirement{
		{category: "topic", label: "资料主题", always: true},
		{category: "key_points", label: "关键要点", always: true},
		{category: "source", label: "来源", terms: []string{"source", "from", "来源", "出处"}},
		{category: "usage", label: "使用方式", always: true},
		{category: "limits", label: "限制条件", terms: []string{"limit", "only", "不得", "限制", "边界"}},
	}
}

// promptIntentBusinessRuleSourceFacts 返回业务规则需要覆盖的规则正文、适用条件和执行方式。
func promptIntentBusinessRuleSourceFacts() []promptIntentSourceFactRequirement {
	return []promptIntentSourceFactRequirement{
		{category: "rule", label: "规则内容", always: true},
		{category: "conditions", label: "适用条件", always: true},
		{category: "exceptions", label: "例外处理", terms: []string{"exception", "except", "除非", "例外", "异常"}},
		{category: "enforcement", label: "执行方式", always: true},
		{category: "examples", label: "示例", terms: []string{"example", "例如", "示例", "例子"}},
	}
}

// promptIntentLooksLikeExternalCodingPrompt 判断原文是否像外部编码助手提示词。
// 该结果只参与来源分类，不直接决定安全问题级别。
func promptIntentLooksLikeExternalCodingPrompt(rawText string) bool {
	return containsAnyPromptIntentTerm(rawText, []string{
		"coding assistant",
		"codebase",
		"pair programming",
		"making code changes",
		"code changes",
		"debugging",
		"modify code",
	})
}

// promptIntentLooksLikeAPIDoc 判断原文是否像 API 文档或接口说明。
func promptIntentLooksLikeAPIDoc(rawText string) bool {
	return containsAnyPromptIntentTerm(rawText, []string{
		"api doc",
		"api docs",
		"api document",
		"api documentation",
		"api 文档",
		"endpoint",
		"接口",
		"/v1/",
		"authorization",
		"bearer token",
		"错误码",
		"rate limit",
	})
}

// promptIntentLooksLikeTableData 判断原文是否像表格、价格表或 spreadsheet 数据。
func promptIntentLooksLikeTableData(rawText string) bool {
	return containsAnyPromptIntentTerm(rawText, []string{
		"pricing table",
		"价格表",
		"价目表",
		"excel",
		"csv",
		"spreadsheet",
		"表格",
		"套餐",
		"币种",
		"columns",
		"rows",
	})
}

// promptIntentLooksLikeWorkflowSOP 判断原文是否像流程、runbook 或操作检查清单。
func promptIntentLooksLikeWorkflowSOP(rawText string) bool {
	return containsAnyPromptIntentTerm(rawText, []string{
		"sop",
		"workflow sop",
		"runbook",
		"checklist",
		"流程：",
		"步骤",
		"审批",
		"操作规范",
		"检查清单",
	})
}

// promptIntentLooksLikeMeetingNotes 判断原文是否像会议纪要。
func promptIntentLooksLikeMeetingNotes(rawText string) bool {
	return containsAnyPromptIntentTerm(rawText, []string{
		"meeting notes",
		"meeting minutes",
		"action item",
		"会议",
		"纪要",
		"决策",
		"决定",
		"待办",
	})
}

// promptIntentLooksLikeBusinessRuleSource 判断原文是否像业务规则或政策说明。
func promptIntentLooksLikeBusinessRuleSource(rawText string) bool {
	return containsAnyPromptIntentTerm(rawText, []string{
		"business rule",
		"policy",
		"业务规则",
		"规则：",
		"适用条件",
		"除非",
	})
}

// promptIntentLooksLikeReferenceDoc 判断原文是否像通用参考文档。
// 短文本不参与该分类，避免普通问题被误判成资料沉淀。
func promptIntentLooksLikeReferenceDoc(rawText string) bool {
	if compactRuneLen(rawText) < 80 {
		return false
	}
	return containsAnyPromptIntentTerm(rawText, []string{
		"reference",
		"documentation",
		"manual",
		"faq",
		"参考资料",
		"文档",
		"说明书",
		"知识库",
		"手册",
	})
}

// promptIntentNormalizeSourceFactCategory 将 category 字符串规范化（小写、空格/连字符转下划线）。
func promptIntentNormalizeSourceFactCategory(value string) string {
	category := strings.ToLower(strings.TrimSpace(value))
	category = strings.ReplaceAll(category, "-", "_")
	category = strings.ReplaceAll(category, " ", "_")
	return category
}

// promptIntentSourceFactApplicationIssues 检查 preserve/translate 要点是否已落实到卡片保存内容中，
// 未落实的类别汇总为一条 block 问题。
func promptIntentSourceFactApplicationIssues(card Card) []Issue {
	target := promptIntentSourceFactTargetText(card)
	missing := make([]string, 0, len(card.SourceFacts))
	seen := map[string]bool{}
	for _, fact := range card.SourceFacts {
		if !promptIntentSourceFactRequiresApplication(fact) {
			continue
		}
		if promptIntentSourceFactApplied(fact.Summary, target) {
			continue
		}
		label := promptIntentSourceFactReadableCategory(fact.Category)
		if !seen[label] {
			seen[label] = true
			missing = append(missing, label)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []Issue{{
		Code:     "source_fact_not_applied",
		Severity: "block",
		Message:  "原文关键要点尚未进入保存内容：" + strings.Join(missing, "、"),
	}}
}

// promptIntentSourceFactRequiresApplication 判断某条事实是否需要落实（非 drop 且摘要非空）。
func promptIntentSourceFactRequiresApplication(fact SourceFact) bool {
	if strings.TrimSpace(fact.Summary) == "" {
		return false
	}
	return strings.TrimSpace(strings.ToLower(fact.Disposition)) != "drop"
}

// promptIntentSourceFactTargetText 拼接卡片中所有需要检查"事实落实"的文本字段，
// 用于 promptIntentSourceFactApplied 的全文检索。
func promptIntentSourceFactTargetText(card Card) string {
	return strings.Join([]string{
		card.Title,
		card.Summary,
		card.WhenToUse,
		card.WhenNotToUse,
		strings.Join(card.Workflow, "\n"),
		strings.Join(card.Constraints, "\n"),
		card.Output,
		card.SaveBoundary,
		card.RecallTopic,
		card.RecallBody,
		card.DefaultRuleBody,
		strings.Join(card.HitExamples, "\n"),
		strings.Join(card.MissExamples, "\n"),
	}, "\n")
}

// promptIntentSourceFactApplied 判断事实摘要是否已体现在目标文本中。
// 优先全文匹配，否则提取关键词后要求 ≥2 个词命中（词数 ≤2 时要求全部命中）。
func promptIntentSourceFactApplied(summary, target string) bool {
	summary = normalizePromptIntentText(summary)
	target = normalizePromptIntentText(target)
	if summary == "" {
		return true
	}
	if target == "" {
		return false
	}
	if strings.Contains(target, summary) {
		return true
	}
	terms := promptIntentSourceFactApplicationTerms(summary)
	if len(terms) == 0 {
		return true
	}
	matches := 0
	for term := range terms {
		if strings.Contains(target, term) {
			matches++
		}
	}
	if len(terms) <= 2 {
		return matches == len(terms)
	}
	return matches >= 2
}

// promptIntentSourceFactApplicationTerms 从摘要文本提取用于检索的关键词集合：
// ASCII 单词（≥2字符）直接加入，中文字符按相邻双字组合提取二元组。
func promptIntentSourceFactApplicationTerms(text string) map[string]bool {
	terms := map[string]bool{}
	var ascii strings.Builder
	var han []rune
	flushASCII := func() {
		token := strings.ToLower(ascii.String())
		if promptIntentSourceFactTermUseful(token) {
			terms[token] = true
		}
		ascii.Reset()
	}
	flushHan := func() {
		for _, term := range promptIntentHanSourceFactTerms(han) {
			terms[term] = true
		}
		han = han[:0]
	}
	for _, r := range text {
		if r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-') {
			flushHan()
			ascii.WriteRune(r)
			continue
		}
		if unicode.Is(unicode.Han, r) {
			flushASCII()
			han = append(han, r)
			continue
		}
		flushASCII()
		flushHan()
	}
	flushASCII()
	flushHan()
	return terms
}

// promptIntentHanSourceFactTerms 从汉字 rune 切片提取相邻二元组关键词（长度 ≥2）。
func promptIntentHanSourceFactTerms(runes []rune) []string {
	if len(runes) < 2 {
		return nil
	}
	out := make([]string, 0, len(runes)-1)
	for i := 0; i < len(runes)-1; i++ {
		term := string(runes[i : i+2])
		if promptIntentSourceFactTermUseful(term) {
			out = append(out, term)
		}
	}
	return out
}

// promptIntentSourceFactTermUseful 过滤掉过于常见的中文词汇和过短的词，避免误判。
func promptIntentSourceFactTermUseful(term string) bool {
	term = strings.TrimSpace(term)
	if len([]rune(term)) < 2 {
		return false
	}
	ignored := map[string]bool{
		"资料": true, "主题": true, "包括": true, "用户": true, "查询": true,
		"问题": true, "使用": true, "输出": true, "方式": true, "需要": true,
		"关键": true, "文档": true, "参考": true, "保存": true, "内容": true,
	}
	return !ignored[term]
}

// promptIntentSourceFactCategoryAliases 返回某 category 的同义别名列表，
// 用于将 LLM 生成的变体名称统一映射到标准类别。
func promptIntentSourceFactCategoryAliases(category string) []string {
	aliases := map[string][]string{
		"column":         {"fields"},
		"columns":        {"fields"},
		"row":            {"key_rows"},
		"rows":           {"key_rows"},
		"key_row":        {"key_rows"},
		"unit":           {"units"},
		"currency":       {"units"},
		"price_unit":     {"units"},
		"price_units":    {"units"},
		"query":          {"query_examples"},
		"queries":        {"query_examples"},
		"example":        {"query_examples", "examples"},
		"examples":       {"query_examples", "examples"},
		"param":          {"parameters"},
		"params":         {"parameters"},
		"parameter":      {"parameters"},
		"endpoints":      {"endpoint"},
		"url":            {"endpoint"},
		"path":           {"endpoint"},
		"methods":        {"method"},
		"auth":           {"auth"},
		"authentication": {"auth"},
		"authorization":  {"auth"},
		"return":         {"response"},
		"returns":        {"response"},
		"responses":      {"response"},
		"error":          {"errors"},
		"error_code":     {"errors"},
		"error_codes":    {"errors"},
		"status_code":    {"errors"},
		"status_codes":   {"errors"},
		"limit":          {"limits"},
		"rate_limit":     {"limits"},
		"rate_limits":    {"limits"},
		"quota":          {"limits"},
		"todo":           {"action_items"},
		"todos":          {"action_items"},
		"tasks":          {"action_items"},
		"owner":          {"owners"},
		"responsible":    {"owners"},
	}
	return aliases[category]
}

// promptIntentSourceFactReadableCategory 将内部 category 键转换为面向用户的中文标签，
// 用于在问题消息中展示可读的类别名称。
func promptIntentSourceFactReadableCategory(category string) string {
	labels := map[string]string{
		"identity":        "外部身份",
		"communication":   "沟通方式",
		"search_reading":  "搜索和读取代码",
		"code_change":     "代码修改",
		"dependency_api":  "依赖/API 检查",
		"debugging":       "调试",
		"safety":          "安全边界",
		"task_management": "任务管理",
		"output":          "输出要求",
		"tool_protocol":   "外部工具协议",
		"topic":           "资料主题",
		"fields":          "字段含义",
		"key_rows":        "关键行",
		"units":           "单位/币种",
		"scope":           "适用范围",
		"query_examples":  "可查询问题",
		"endpoint":        "接口地址",
		"method":          "请求方法",
		"parameters":      "请求参数",
		"auth":            "鉴权方式",
		"response":        "返回结构",
		"errors":          "错误码",
		"limits":          "调用限制",
		"examples":        "示例",
		"trigger":         "触发条件",
		"inputs":          "输入材料",
		"steps":           "执行步骤",
		"roles":           "角色分工",
		"exceptions":      "例外处理",
		"outputs":         "输出结果",
		"facts":           "事实背景",
		"decisions":       "会议决策",
		"action_items":    "行动项",
		"owners":          "负责人",
		"dates":           "日期/截止时间",
		"open_questions":  "未决问题",
		"key_points":      "关键要点",
		"source":          "来源",
		"usage":           "使用方式",
		"rule":            "规则内容",
		"conditions":      "适用条件",
		"enforcement":     "执行方式",
	}
	normalized := promptIntentNormalizeSourceFactCategory(category)
	if label := labels[normalized]; label != "" {
		return label
	}
	return strings.TrimSpace(category)
}

// promptIntentSourceProfileLabel 将 source profile 枚举值转换为面向用户的中文标签。
func promptIntentSourceProfileLabel(profile string) string {
	switch profile {
	case promptIntentSourceProfileExternalPrompt:
		return "外部提示词"
	case promptIntentSourceProfileReferenceDoc:
		return "参考资料"
	case promptIntentSourceProfileTableData:
		return "表格资料"
	case promptIntentSourceProfileWorkflowSOP:
		return "流程资料"
	case promptIntentSourceProfileAPIDoc:
		return "API 文档"
	case promptIntentSourceProfileMeetingNotes:
		return "会议资料"
	case promptIntentSourceProfileBusinessRule:
		return "业务规则"
	default:
		return "原文"
	}
}
