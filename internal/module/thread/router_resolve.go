package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func shouldSkipRoutedPrompt(s *service, req *StartRequest) bool {
	switch {
	case req == nil:
		return true
	case strings.TrimSpace(req.BaseInstructions) != "":
		pkglogger.Info("router: skip, base_instructions already set by caller",
			"agent_key", req.AgentKey, "base_instructions_len", len(req.BaseInstructions))
		return true
	case s == nil || s.promptCatalogPort() == nil:
		pkglogger.Info("router: skip, prompt catalog not wired")
		return true
	default:
		return false
	}
}

// resolveRoutedPrompt 为 thread/start 选择并注入运行时 prompt 模板。
// 调用方已提供 BaseInstructions 时不再路由；缺少可信 CWD 会直接报错，避免绕过模板可见性边界。
func (s *service) resolveRoutedPrompt(ctx context.Context, req *StartRequest) error {
	if shouldSkipRoutedPrompt(s, req) {
		return nil
	}
	trustedCWD := strings.TrimSpace(req.CWD)
	if trustedCWD == "" {
		return fmt.Errorf("invalid params: prompt routing requires trusted cwd")
	}

	// 一次性读取启用候选；规则匹配成本低，200 条上限足够覆盖常见项目模板集合。
	catalog := s.promptCatalogPort()
	templates, err := catalog.ListTemplates(ctx, PromptListFilter{CWD: trustedCWD, Limit: 200})
	if err != nil {
		return fmt.Errorf("router: list prompt_templates: %w", err)
	}

	// match_when 自动路由只在没有显式 PromptKey/AgentKey 时运行，位于显式 pin 与默认模板之间。
	// 它选择优先级最高且规则命中当前 BuildCtx 的模板。
	defaultPromptRequired := routedPromptDefaultRequired(req)
	s.maybeAutoRouteByMatchWhen(req, templates)

	picked, err := s.pickRoutedTemplate(ctx, req, templates)
	if err != nil {
		return err
	}
	if picked == nil || !picked.Enabled {
		if routedPromptDefaultMissing(defaultPromptRequired, req, templates) {
			return fmt.Errorf("router: required default prompt %q is missing", defaultPromptKey)
		}
		pkglogger.Info("router: no prompt_template matched",
			"requested_agent_key", req.AgentKey,
			"candidate_count", len(templates))
		return nil
	}
	pkglogger.Info("router: prompt_template matched",
		"prompt_key", picked.PromptKey,
		"agent_key", picked.AgentKey,
		"candidate_count", len(templates),
		"prompt_text_len", len(picked.PromptText))
	return s.applyPickedRoutedTemplate(ctx, req, picked)
}

func routedPromptDefaultRequired(req *StartRequest) bool {
	return req != nil &&
		strings.TrimSpace(req.PromptKey) == "" &&
		strings.TrimSpace(req.AgentKey) == ""
}

func routedPromptDefaultMissing(required bool, req *StartRequest, templates []PromptTemplate) bool {
	if !required || req == nil || strings.TrimSpace(req.PromptKey) != "" || strings.TrimSpace(req.AgentKey) != "" {
		return false
	}
	return findByPromptKey(templates, defaultPromptKey) == nil
}

// applyPickedRoutedTemplate 将命中的 prompt_template 写回启动请求。
// 成功时会生成 prompt_versions 快照；快照写入失败直接返回错误，避免 thread 指向不可追溯的 prompt。
func (s *service) applyPickedRoutedTemplate(
	ctx context.Context,
	req *StartRequest,
	picked *PromptTemplate,
) error {
	versionPromptText, blocks, err := s.routedTemplateInstructions(ctx, req, picked)
	if err != nil {
		return err
	}
	// 写入 prompt_versions 快照，保证后续分析能复现本次注入的完整 prompt。
	catalog := s.promptCatalogPort()
	// 先记录 agent_key / prompt_key，便于失败日志和调用方定位命中的模板。
	req.AgentKey = picked.AgentKey
	req.AgentTitle = picked.Title
	req.PromptKey = picked.PromptKey
	if len(blocks) > 0 {
		req.BaseInstructionBlocks = blocks
		req.BaseInstructions = ""
	} else {
		req.BaseInstructions = versionPromptText
		req.BaseInstructionBlocks = nil
	}
	if !promptCatalogCanInsertVersion(catalog) {
		req.PromptVersionID = nil
		return nil
	}
	versionID, verr := catalog.InsertVersion(ctx, PromptTemplateVersion{
		PromptKey:       picked.PromptKey,
		Title:           picked.Title,
		AgentKey:        picked.AgentKey,
		ToolName:        picked.ToolName,
		PromptText:      versionPromptText,
		Variables:       append(json.RawMessage(nil), picked.Variables...),
		Tags:            append(json.RawMessage(nil), picked.Tags...),
		Description:     picked.Description,
		Enabled:         picked.Enabled,
		CreatedBy:       picked.CreatedBy,
		UpdatedBy:       picked.UpdatedBy,
		SourceUpdatedAt: &picked.UpdatedAt,
	})
	if verr != nil {
		pkglogger.Warn("router: materialize prompt_versions failed",
			"err", verr, "prompt_key", picked.PromptKey)
		req.PromptVersionID = nil
		return fmt.Errorf("router: materialize prompt_versions for %q: %w", picked.PromptKey, verr)
	}
	req.PromptVersionID = &versionID
	return nil
}

func promptCatalogCanInsertVersion(catalog PromptCatalog) bool {
	return catalog != nil && catalog.CanInsertPromptVersion()
}

// routedTemplateInstructions 读取命中模板的 section 并生成最终注入文本。
// section 读取失败会阻断启动；enable_when 过滤后为空时返回空内容，让上层保留无注入状态。
func (s *service) routedTemplateInstructions(ctx context.Context, req *StartRequest, picked *PromptTemplate) (string, []contract.BaseInstructionBlock, error) {
	catalog := s.promptCatalogPort()
	sections, serr := catalog.ListSectionsByTemplateID(ctx, picked.ID)
	if serr != nil {
		return "", nil, fmt.Errorf("router: list prompt_template_sections for %q: %w", picked.PromptKey, serr)
	}
	if len(sections) == 0 {
		return picked.PromptText, nil, nil
	}
	blocks := convertRuntimeSectionsToBlocks(sections)
	if len(blocks) == 0 {
		return picked.PromptText, nil, nil
	}
	if req != nil {
		blocks = contract.PrepareBaseInstructionBlocks(blocks, buildStartCtx(*req, s.cfg, s.toolRegistry), req.Prompt, s.enableWhenEval)
	} else {
		blocks = contract.PrepareBaseInstructionBlocks(blocks, contract.BuildCtx{}, "", s.enableWhenEval)
	}
	if len(blocks) == 0 {
		return "", nil, nil
	}
	return contract.TextFromBaseInstructionBlocks(blocks), blocks, nil
}

// defaultPromptKey 是调用方未指定 agent_key 时使用的默认 prompt_template。
// 该 key 固定代表普通主线程 persona，避免每次启动都依赖外部配置决定默认身份。
const defaultPromptKey = "main/default"

// pickRoutedTemplate 按固定优先级选择启动时要注入的 prompt_template。
// PromptKey 显式 pin 最高，AgentKey 次之；二者都为空时使用默认模板。这里不做用户意图分类，
// 避免 harness 层覆盖上游 CLI 自己的会话内判断。
func (s *service) pickRoutedTemplate(
	_ context.Context,
	req *StartRequest,
	templates []PromptTemplate,
) (*PromptTemplate, error) {
	// PromptKey 是最具体的 UI pin；未命中时只标记 stale，不静默替换为其它模板。
	if pinned := strings.TrimSpace(req.PromptKey); pinned != "" {
		picked := findEnabledByPromptKey(templates, pinned)
		if picked != nil {
			if isRuntimePromptAssetTemplate(*picked) {
				req.PromptKeyStale = true
				pkglogger.Warn("router: pinned prompt_key targets runtime asset template",
					"prompt_key", pinned)
				return nil, nil
			}
			req.AgentKey = picked.AgentKey
			req.AgentTitle = picked.Title
			return picked, nil
		}
		// 保留原 PromptKey，让 UI 能与本地偏好比较后安全清理。
		req.PromptKeyStale = true
		pkglogger.Warn("router: pinned prompt_key not found",
			"prompt_key", pinned,
			"candidate_count", len(templates))
		return nil, nil
	}
	if explicit := strings.TrimSpace(req.AgentKey); explicit != "" {
		picked := firstEnabledByAgentKey(templates, explicit)
		if picked != nil {
			req.AgentTitle = picked.Title
		}
		return picked, nil
	}
	picked := findByPromptKey(templates, defaultPromptKey)
	if picked != nil && promptTemplateLaunchable(*picked) {
		req.AgentKey = picked.AgentKey
		req.AgentTitle = picked.Title
		return picked, nil
	}
	return nil, nil
}

func findEnabledByPromptKey(templates []PromptTemplate, promptKey string) *PromptTemplate {
	picked := findByPromptKey(templates, promptKey)
	if picked == nil || !picked.Enabled {
		return nil
	}
	return picked
}

// convertRuntimeSectionsToBlocks 将可注入的 prompt_template_sections 转为 assembler 使用的 block。
// 未知 region 按 Dynamic 处理，使其落入非缓存尾部，避免误占 static cached-prefix。
func convertRuntimeSectionsToBlocks(sections []PromptTemplateSection) []contract.BaseInstructionBlock {
	if len(sections) == 0 {
		return nil
	}
	out := make([]contract.BaseInstructionBlock, 0, len(sections))
	for _, s := range sections {
		if !s.Enabled {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(s.TriggerType), "recall") {
			continue
		}
		if strings.TrimSpace(s.Body) == "" {
			continue
		}
		region := contract.PromptRegionDynamic
		if strings.EqualFold(strings.TrimSpace(s.Region), "static") {
			region = contract.PromptRegionStatic
		}
		out = append(out, contract.BaseInstructionBlock{
			Key:        s.SectionKey,
			Region:     region,
			Ordinal:    s.Ordinal,
			Body:       s.Body,
			EnableWhen: append([]byte(nil), s.EnableWhen...),
		})
	}
	return out
}

func firstEnabledByAgentKey(templates []PromptTemplate, agentKey string) *PromptTemplate {
	want := strings.TrimSpace(agentKey)
	if want == "" {
		return nil
	}
	for i := range templates {
		t := &templates[i]
		if promptTemplateLaunchable(*t) && strings.EqualFold(strings.TrimSpace(t.AgentKey), want) {
			return t
		}
	}
	return nil
}

func promptTemplateLaunchable(template PromptTemplate) bool {
	return template.Enabled && !isRuntimePromptAssetTemplate(template)
}

func findByPromptKey(templates []PromptTemplate, promptKey string) *PromptTemplate {
	want := strings.TrimSpace(promptKey)
	if want == "" {
		return nil
	}
	for i := range templates {
		t := &templates[i]
		if t.PromptKey == want {
			return t
		}
	}
	return nil
}

// 候选池合并注入已移除；当前路由只选择一个最终模板。
// 路由优先级为：
//   1. 显式 pin (PromptKey / AgentKey) > 2. match_when 自动路由 >
//   3. main/default 兜底。

// maybeAutoRouteByMatchWhen 在没有显式 pin 时按 match_when 自动填入 PromptKey。
// 它先评估带真实条件的 specific 池，再评估 `{}` always-match fallback 池；任何未命中都保持 PromptKey 为空，
// 让 main/default 兜底继续生效。
func (s *service) maybeAutoRouteByMatchWhen(req *StartRequest, templates []PromptTemplate) {
	if req == nil {
		return
	}
	if strings.TrimSpace(req.PromptKey) != "" {
		return
	}
	if strings.TrimSpace(req.AgentKey) != "" {
		return
	}
	if s.matchWhenEval == nil {
		return
	}
	specific, fallback := autoRouteCandidates(templates)
	if len(specific) == 0 && len(fallback) == 0 {
		return
	}
	buildCtx := buildMatchWhenCtx(req)
	userPrompt := req.Prompt
	if s.evaluateMatchWhenPool("specific", specific, len(fallback), buildCtx, userPrompt, req) {
		return
	}
	if s.evaluateMatchWhenPool("fallback", fallback, len(specific), buildCtx, userPrompt, req) {
		return
	}
	pkglogger.Info("router: match_when no auto-route hit",
		"specific_count", len(specific),
		"fallback_count", len(fallback))
}

// evaluateMatchWhenPool 按优先级顺序评估一个 match_when 候选池。
// 首个命中会写入 req.PromptKey 并返回 true；日志包含另一个池的数量，方便排查命中阶段。
func (s *service) evaluateMatchWhenPool(
	stage string,
	pool []PromptTemplate,
	peerCount int,
	buildCtx contract.BuildCtx,
	userPrompt string,
	req *StartRequest,
) bool {
	for i := range pool {
		cand := &pool[i]
		if !s.matchWhenEval(cand.MatchWhen, buildCtx, userPrompt) {
			continue
		}
		specificCount, fallbackCount := len(pool), peerCount
		if stage == "fallback" {
			specificCount, fallbackCount = peerCount, len(pool)
		}
		pkglogger.Info("router: match_when auto-routed",
			"stage", stage,
			"prompt_key", cand.PromptKey,
			"priority", cand.Priority,
			"specific_count", specificCount,
			"fallback_count", fallbackCount)
		req.PromptKey = cand.PromptKey
		return true
	}
	return false
}

// autoRouteCandidates 将启用模板拆成 specific 与 fallback 两个 match_when 池。
// 非空对象进入 specific，空对象 `{}` 进入 fallback；nil、null、非法 JSON 会被丢弃，因为评估器无法命中它们。
// 每个池按 Priority 降序稳定排序，调用方先评估 specific，避免 always-match 模板遮蔽结构化规则。
func autoRouteCandidates(templates []PromptTemplate) (specific, fallback []PromptTemplate) {
	specific = make([]PromptTemplate, 0, len(templates))
	fallback = make([]PromptTemplate, 0, len(templates))
	for i := range templates {
		t := &templates[i]
		if !promptTemplateLaunchable(*t) {
			continue
		}
		if len(t.MatchWhen) == 0 {
			continue
		}
		if isFallbackMatchWhen(t.MatchWhen) {
			fallback = append(fallback, *t)
			continue
		}
		if hasSpecificMatchWhen(t.MatchWhen) {
			specific = append(specific, *t)
		}
	}
	sortByPriorityDesc(specific)
	sortByPriorityDesc(fallback)
	return specific, fallback
}

// isFallbackMatchWhen 判断原始 JSON 是否为显式空对象 `{}`。
// nil、null、非对象和非法 JSON 都返回 false，避免把 jsonb null 当成 always-match。
func isFallbackMatchWhen(raw []byte) bool {
	var expr map[string]any
	if err := json.Unmarshal(raw, &expr); err != nil {
		return false
	}
	return expr != nil && len(expr) == 0
}

// hasSpecificMatchWhen 判断原始 JSON 是否为带过滤字段的非空对象。
// 其它形状会被丢弃，因为 EvaluateMatchWhen 不接受它们作为规则。
func hasSpecificMatchWhen(raw []byte) bool {
	var expr map[string]any
	if err := json.Unmarshal(raw, &expr); err != nil {
		return false
	}
	return len(expr) > 0
}

// sortByPriorityDesc 按 Priority 降序稳定排序，保留 store 返回的同优先级顺序。
func sortByPriorityDesc(rows []PromptTemplate) {
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Priority > rows[j].Priority
	})
}

// isRuntimePromptAssetTemplate 判断模板是否属于运行时资产模板。
// 资产模板只服务 recall/default-rule 动态段，不能作为 thread/start 可启动 persona。
func isRuntimePromptAssetTemplate(template PromptTemplate) bool {
	if strings.TrimSpace(template.AgentKey) == "default_rule" {
		return true
	}
	for _, tag := range runtimePromptTemplateTags(template.Tags) {
		switch strings.TrimSpace(tag) {
		case "intent:recall", "intent:default_rule":
			return true
		}
	}
	return false
}

func runtimePromptTemplateTags(raw json.RawMessage) []string {
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		pkglogger.Warn("router: prompt template tags unmarshal failed",
			"raw_len", len(raw),
			"error", err.Error(),
		)
		return nil
	}
	return tags
}

// buildMatchWhenCtx 构造路由阶段使用的轻量 BuildCtx。
// 它不会访问 config 或 registry，但会把 req.CWD 规范化为绝对路径，使 cwd_prefix/cwd_glob 规则可稳定匹配。
func buildMatchWhenCtx(req *StartRequest) contract.BuildCtx {
	return contract.BuildCtx{
		CWD:          resolvePromptCWD(req.CWD),
		GitRoot:      strings.TrimSpace(req.GitRoot),
		IsWorktree:   req.IsWorktree,
		Language:     strings.TrimSpace(req.Language),
		Provider:     strings.TrimSpace(req.Provider),
		Model:        strings.TrimSpace(req.Model),
		SessionFlags: req.SessionFlags,
	}
}
