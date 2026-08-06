package memory

import (
	"fmt"
	"path/filepath"
	"strings"
)

// MemoryMode 表示 start prompt 中 memory 规则的注入模式。
// gate 会在 standard、combined 和 kairos 之间选择，provider 只消费最终渲染出的规则文本。
type MemoryMode string

const (
	MemoryModeStandard MemoryMode = "standard"
	MemoryModeCombined MemoryMode = "combined"
	MemoryModeKairos   MemoryMode = "kairos"
)

// MemoryRuleOptions 控制规则文本渲染时可见的 memory 行为。
// 路径字段只用于提示模型写入位置，不会触发目录创建或磁盘读写。
type MemoryRuleOptions struct {
	SkipIndex                bool
	SearchPastContextEnabled bool
	ExtraGuidelines          []string
	AutoMemPath              string
	TeamMemPath              string
}

// MemoryTypeBehavior 描述某类记忆的保存、读取和信任规则。
// 这些规则只进入 prompt 文本，真正的权限和路径校验仍在 runtime 执行。
type MemoryTypeBehavior struct {
	Summary string
	Save    []string
	Access  []string
	Trust   []string
}

// MemoryRuleEngine 持有 memory prompt 的规则模板和类型顺序。
// 实例化时会拷贝默认规则，防止测试或调用方修改包级模板影响其它 provider 会话。
type MemoryRuleEngine struct {
	order []MemoryType
	rules map[MemoryType]MemoryTypeBehavior
}

func newStandardMemoryTypeBehaviors() map[MemoryType]MemoryTypeBehavior {
	return map[MemoryType]MemoryTypeBehavior{
		MemoryTypeUser: {
			Summary: "Information about the user's role, goals, responsibilities, knowledge, and collaboration preferences so future work can be tailored to their perspective.",
			Save: []string{
				"Store durable details about the user's role, preferences, responsibilities, and knowledge so future responses can be tailored to them specifically.",
				"Avoid negative judgments or irrelevant personal evaluations.",
			},
			Access: []string{
				"Read `user` memory when the answer should be informed by the user's profile, perspective, working style, or knowledge level.",
			},
			Trust: []string{
				"Re-confirm if the user's goals, responsibilities, or preferences appear to have changed.",
			},
		},
		MemoryTypeFeedback: {
			Summary: "Guidance about how to approach work — both what to avoid and what to keep doing.",
			Save: []string{
				"Record from failure and success: store confirmed corrections and validated non-obvious successful approaches that should guide future conversations.",
				"Structure the body as `rule`, `Why:`, and `How to apply:` so future turns can judge edge cases.",
			},
			Access: []string{
				"Read `feedback` memory to guide behavior so the user does not need to repeat the same working guidance twice.",
			},
			Trust: []string{
				"Trust only feedback that was explicitly confirmed or repeatedly validated; revise or delete it when it stops working.",
			},
		},
		MemoryTypeProject: {
			Summary: "Information about ongoing work, goals, initiatives, bugs, or incidents that is not otherwise derivable from code or git history.",
			Save: []string{
				"Store who is doing what, why, or by when when that context is not derivable from code or git.",
				"Convert relative dates to absolute dates and structure the body as `fact`, `Why:`, and `How to apply:` because project memory decays quickly.",
			},
			Access: []string{
				"Read `project` memory when broader project context, motivation, deadlines, owners, decisions, or incident history should shape the answer.",
			},
			Trust: []string{
				"Re-check dates, owners, and incident status against current code, docs, or direct user input before acting; keep dates absolute.",
			},
		},
		MemoryTypeReference: {
			Summary: "Pointers to where up-to-date information can be found in external systems and why it matters.",
			Save: []string{
				"Store where to look in external systems and what it is for; keep pointers only, not copied snapshots.",
			},
			Access: []string{
				"Read `reference` memory when the user references an external system or current work may require consulting docs, Slack, Grafana, Linear, or runbooks.",
			},
			Trust: []string{
				"Verify the pointer still resolves and that the underlying external data is current before relying on it.",
			},
		},
	}
}

func newStandardOverviewLines() []string {
	return []string{
		"Persistent memory stores durable, surprising facts for future turns; it is not a transcript, a scratchpad, or a replacement for current observation.",
		"Use memory to preserve behaviorally relevant context while keeping authorization, visibility, and retrieval boundaries intact.",
	}
}

func standardMemorySystemLines(autoDir string) []string {
	root := strings.TrimSpace(autoDir)
	if root == "" {
		root = "<auto-memory-root>"
	}
	return append([]string{
		fmt.Sprintf("Standard mode uses a single auto-memory root at `%s`; treat `%s` as the pointer index entrypoint and keep durable topic files organized beneath that root.", root, filepath.ToSlash(memoryIndexPath(root))),
	}, newStandardOverviewLines()...)
}

func newStandardSaveRules() []string {
	return []string{
		"Explicit `remember` saves immediately; explicit `forget` finds and deletes the matching memory.",
		"Each durable fact belongs in its own topic file; `MEMORY.md` stays a pointer index rather than a second copy of the body.",
		"Prefer updating an existing topic over creating duplicates.",
		"Keep `name`, `description`, and `type` frontmatter aligned with the body.",
		"Use the standard topic-file frontmatter template:\n  ---\n  name: <memory name>\n  description: <specific one-line relevance hook>\n  type: user|feedback|project|reference\n  title: <short display title, max 12 chars, optional>\n  ---\n  <durable memory body>",
		"When saving a memory, include a concise `title` (max 12 characters) that captures the core point for card-view display. Omit if the description is already short and clear enough.",
		"Organize memory by semantic topic, not by time.",
		"Save or delete only after runtime `sanitize + resolve + authorize` succeeds; `deny`, `not_visible`, and `local_unavailable` are hard stop conditions.",
		"The prompt layer must not probe or `mkdir` memory directories; runtime may ensure them separately.",
		"Update or delete stale or incorrect memory instead of letting it drift.",
		"`MEMORY.md` index lines must stay one-per-line, pointer-only, roughly 150 characters or less, and must not inline full memory bodies.",
	}
}

func newStandardAutoDetectSignalRules() []string {
	return []string{
		"When you detect the following signals during conversation, save a memory proactively without the user saying \"remember\":",
		"User corrects your behavior → save as feedback",
		"User repeats the same instruction for the second time → save as feedback (high priority)",
		"User makes an explicit technical or product decision → save as project",
		"User expresses frustration about a recurring mistake (\"又来了\", \"上次也是\") → save as feedback (high priority)",
		"User states a personal preference, habit, or context about themselves (\"我喜欢…\", \"我的习惯是…\", timezone, language, editor settings) → save as feedback",
		"User states project environment facts, tech stack, or constraints (\"我们用的是 PostgreSQL\", \"CI 跑在 GitHub Actions\", \"必须兼容 v1\") → save as project",
		"Do not ask for confirmation. The dedup filter will handle duplicates automatically.",
	}
}

func newStandardAccessRules() []string {
	return []string{
		"Read memory when it appears relevant or when the user refers to previous context.",
		"If the user explicitly asks to recall, check, or remember, you must read memory.",
		"If the user says ignore or not use memory, act as if `MEMORY.md` is empty; do not apply, reference, compare, or mention remembered content.",
		"Only use memory text surfaced by runtime or returned by tools; do not guess paths, scopes, or hidden entries from names.",
		"Visibility is decided by runtime `sanitize + resolve + authorize`; knowing a `name`, `path`, or `@agent` does not grant access.",
		"`scope` is an ACL boundary, not a fifth memory type; `user`, `project`, and `local` visibility still require the same authorizer.",
		"Treat `deny`, `not_visible`, and `local_unavailable` as unavailable; do not retry via another root or scope.",
	}
}

func newStandardTrustRules() []string {
	return []string{
		"Memory is what was true when it was written, not guaranteed current truth.",
		"If memory conflicts with current observation, trust the current observation and update or delete stale memory.",
		"Verify referenced files and paths still exist before relying on them.",
		"Verify referenced functions and flags still exist before relying on them; V3 also re-checks referenced type names as the same kind of stale claim.",
		"Before making a recommendation the user may act on, validate the current state first.",
		"For recent or current repository status, prefer current code or `git log` over memory snapshots.",
	}
}

func newStandardExclusionRules() []string {
	return []string{
		"Never store secrets, credentials, tokens, API keys, or passwords in any memory type.",
		"Do not store code patterns, conventions, architecture, file paths, or project structure; derive those by reading the current project state.",
		"Do not store git history, recent changes, or who-changed-what; `git log` / `git blame` are authoritative.",
		"Do not store debugging solutions or fix recipes; the fix should live in code and the commit message carries the context.",
		"Do not store anything already documented in `CLAUDE.md` files.",
		"Do not store ephemeral task details such as in-progress work, temporary state, or current conversation context.",
		"Do not store PR lists, activity summaries, progress trackers, tasks, or plans.",
		"Even if the user explicitly asks, these exclusions still apply.",
		"If asked to save a PR list or activity summary, ask what was surprising or non-obvious about it and save only that durable part.",
	}
}

func newStandardPlanRules() []string {
	return []string{
		"Plans and non-trivial implementation strategies belong in plan mechanisms, not memory.",
		"Current step breakdowns, progress tracking, and task state belong in tasks, not memory.",
	}
}

// V3 intentionally keeps `searching past context` runtime-driven rather than
// Claude's model-driven directory/log search. Only surface this section when
// the runtime SearchPastContext gate is enabled so the prompt never promises a
// retrieval path that is unavailable in the current session.
func newStandardSearchingPastContextRules() []string {
	return []string{
		"V3 intentionally keeps `searching past context` runtime-driven: durable memory is searched first, and budgeted transcript snippets may be surfaced only when memory misses or confidence is weak.",
		"Do not probe memory directories, hidden roots, or session transcript logs from the prompt layer.",
		"Only use runtime-surfaced memory or transcript snippets included in context, and apply the access/trust rules above before acting on them.",
	}
}

// NewMemoryRuleEngine 构造标准记忆规则引擎。
// 每次调用都会创建独立行为表，调用方不能改写其它 provider 会话的规则。
func NewMemoryRuleEngine() *MemoryRuleEngine {
	behaviors := newStandardMemoryTypeBehaviors()
	engine := &MemoryRuleEngine{
		order: newDiskMemoryTypes(),
		rules: make(map[MemoryType]MemoryTypeBehavior, len(behaviors)),
	}
	for _, memoryType := range engine.order {
		engine.rules[memoryType] = cloneBehavior(behaviors[memoryType])
	}
	return engine
}

// BuildMemoryLines 使用默认规则引擎生成标准模式的记忆提示文本。
// skipIndex 和 extraGuidelines 只影响提示内容，不触发任何磁盘读写。
func BuildMemoryLines(skipIndex, searchPastContextEnabled bool, extraGuidelines []string) string {
	return NewMemoryRuleEngine().BuildMemoryLines(MemoryRuleOptions{
		SkipIndex:                skipIndex,
		SearchPastContextEnabled: searchPastContextEnabled,
		ExtraGuidelines:          extraGuidelines,
	})
}

// LoadMemoryPrompt 根据记忆模式返回可注入的系统提示片段。
// autoEnabled 关闭或模式未知时返回 nil，提示汇编器据此跳过 memory 规则区块。
func LoadMemoryPrompt(mode MemoryMode, autoEnabled, skipIndex, searchPastContextEnabled bool, extraGuidelines []string) *string {
	return NewMemoryRuleEngine().LoadMemoryPrompt(mode, autoEnabled, MemoryRuleOptions{
		SkipIndex:                skipIndex,
		SearchPastContextEnabled: searchPastContextEnabled,
		ExtraGuidelines:          extraGuidelines,
	})
}

// RulesForType 返回指定记忆类型的行为规则副本。
// 未知类型返回 false；已知类型也返回克隆值，避免调用方改动引擎内部状态。
func (e *MemoryRuleEngine) RulesForType(memoryType MemoryType) (MemoryTypeBehavior, bool) {
	behavior, ok := resolvedRuleEngine(e).rules[ParseMemoryType(string(memoryType))]
	if !ok {
		return MemoryTypeBehavior{}, false
	}
	return cloneBehavior(behavior), true
}

// LoadMemoryPrompt 按当前模式选择标准、combined 或 kairos 规则提示。
// 该函数只组装提示文本，不解析路径、不读写记忆文件。
func (e *MemoryRuleEngine) LoadMemoryPrompt(mode MemoryMode, autoEnabled bool, opts MemoryRuleOptions) *string {
	if !autoEnabled {
		return nil
	}
	switch strings.TrimSpace(string(mode)) {
	case "", string(MemoryModeStandard):
		return resolvedRuleEngine(e).loadStandardMemoryPrompt(opts)
	case string(MemoryModeCombined):
		return resolvedRuleEngine(e).loadCombinedMemoryPrompt(opts)
	case string(MemoryModeKairos):
		return resolvedRuleEngine(e).loadKairosMemoryPrompt(opts)
	default:
		return nil
	}
}

// loadStandardMemoryPrompt 渲染标准模式规则。
// 空结果返回 nil，避免向 prompt 注入空动态区块。
func (e *MemoryRuleEngine) loadStandardMemoryPrompt(opts MemoryRuleOptions) *string {
	text := strings.TrimSpace(e.BuildMemoryLines(opts))
	if text == "" {
		return nil
	}
	return &text
}

// loadCombinedMemoryPrompt 渲染 private/team 双 scope 模式规则。
// 只有 private 和 team 根目录都存在时才会生成文本。
func (e *MemoryRuleEngine) loadCombinedMemoryPrompt(opts MemoryRuleOptions) *string {
	text := strings.TrimSpace(buildCombinedMemoryPrompt(resolvedRuleEngine(e), opts))
	if text == "" {
		return nil
	}
	return &text
}

// loadKairosMemoryPrompt 渲染 kairos daily log 模式规则。
// 该模式复用 daily log 提示，不暴露标准 taxonomy 的写入路径。
func (e *MemoryRuleEngine) loadKairosMemoryPrompt(opts MemoryRuleOptions) *string {
	text := strings.TrimSpace(BuildDailyLogPrompt(opts.SkipIndex, opts.SearchPastContextEnabled, opts.ExtraGuidelines))
	if text == "" {
		return nil
	}
	return &text
}

// BuildMemoryLines 组装标准模式下的完整规则章节。
// 章节顺序稳定，便于测试快照和 provider 侧 prompt diff 定位变化。
func (e *MemoryRuleEngine) BuildMemoryLines(opts MemoryRuleOptions) string {
	engine := resolvedRuleEngine(e)
	sections := make([]string, 0, 9)
	sectionIndex := 0
	nextTitle := func(name string) string {
		sectionIndex++
		return fmt.Sprintf("### %d. %s", sectionIndex, name)
	}
	sections = append(sections, renderSection(nextTitle("memory system"), standardMemorySystemLines(opts.AutoMemPath)))
	sections = append(sections, renderSection(nextTitle("taxonomy"), engine.taxonomyLines()))
	sections = append(sections, renderSection(nextTitle("exclusions"), newStandardExclusionRules()))
	sections = append(sections, engine.renderBehaviorSection(nextTitle("save rules / how to save memories"), append(newStandardSaveRules(), indexRule(opts.SkipIndex)), func(b MemoryTypeBehavior) []string { return b.Save }))
	sections = append(sections, renderSection(nextTitle("auto-detect signals"), newStandardAutoDetectSignalRules()))
	sections = append(sections, engine.renderBehaviorSection(nextTitle("access rules / when to access memories"), newStandardAccessRules(), func(b MemoryTypeBehavior) []string { return b.Access }))
	sections = append(sections, engine.renderBehaviorSection(nextTitle("trust rules / before recommending from memory"), newStandardTrustRules(), func(b MemoryTypeBehavior) []string { return b.Trust }))
	sections = append(sections, renderSection(nextTitle("memory vs plan/tasks"), newStandardPlanRules()))
	if extraLines := normalizeStringSlice(opts.ExtraGuidelines); len(extraLines) > 0 {
		sections = append(sections, renderSection(nextTitle("extra guidelines"), extraLines))
	}
	if section := searchingPastContextSection(nextTitle("searching past context"), opts.SearchPastContextEnabled); section != "" {
		sections = append(sections, section)
	}
	return strings.Join(sections, "\n\n")
}

// buildCombinedMemoryPrompt 组装 private/team 双 scope 模式提示。
// 任一根目录为空都返回空字符串，避免给模型承诺不可用的写入位置。
func buildCombinedMemoryPrompt(engine *MemoryRuleEngine, opts MemoryRuleOptions) string {
	engine = resolvedRuleEngine(engine)
	autoDir := strings.TrimSpace(opts.AutoMemPath)
	teamDir := strings.TrimSpace(opts.TeamMemPath)
	if autoDir == "" || teamDir == "" {
		return ""
	}
	sections := make([]string, 0, 10)
	sectionIndex := 0
	nextTitle := func(name string) string {
		sectionIndex++
		return fmt.Sprintf("### %d. %s", sectionIndex, name)
	}
	sections = append(sections, renderSection(nextTitle("memory system"), combinedMemorySystemLines(autoDir, teamDir)))
	sections = append(sections, renderSection(nextTitle("memory scope"), combinedMemoryScopeLines(autoDir, teamDir)))
	sections = append(sections, renderSection(nextTitle("taxonomy"), combinedTaxonomyLines(engine)))
	sections = append(sections, renderSection(nextTitle("exclusions"), combinedExclusionRules()))
	sections = append(sections, renderSection(nextTitle("save rules / how to save memories"), combinedSaveRules(opts.SkipIndex, autoDir, teamDir)))
	sections = append(sections, renderSection(nextTitle("auto-detect signals"), newStandardAutoDetectSignalRules()))
	sections = append(sections, renderSection(nextTitle("access rules / when to access memories"), combinedAccessRules()))
	sections = append(sections, renderSection(nextTitle("trust rules / before recommending from memory"), newStandardTrustRules()))
	sections = append(sections, renderSection(nextTitle("memory vs plan/tasks"), newStandardPlanRules()))
	if extraLines := normalizeStringSlice(opts.ExtraGuidelines); len(extraLines) > 0 {
		sections = append(sections, renderSection(nextTitle("extra guidelines"), extraLines))
	}
	if section := searchingPastContextSection(nextTitle("searching past context"), opts.SearchPastContextEnabled); section != "" {
		sections = append(sections, section)
	}
	return strings.Join(sections, "\n\n")
}

// combinedMemorySystemLines 生成 combined 模式的系统说明。
// 内容明确记忆目录已由 runtime 准备好，模型不应自行探测或创建目录。
func combinedMemorySystemLines(autoDir, teamDir string) []string {
	return []string{
		fmt.Sprintf("You have a persistent, file-based memory system with two directories: a private directory at `%s` and a shared team directory at `%s`.", autoDir, teamDir),
		"Both directories already exist — write to them directly with the Write tool; do not run `mkdir` or probe for existence from the prompt layer.",
		"Use memory to preserve durable user context, validated collaboration guidance, shared project context, and external pointers that are not derivable from current code or git state.",
		"Explicit `remember` saves immediately; explicit `forget` finds and deletes the matching memory in the appropriate scope.",
		"Team memory is synced for the session only when runtime readiness, auth, and feature gates allow it; treat sync as best-effort and do not assume every session saw the latest shared state.",
	}
}

// combinedMemoryScopeLines 说明 private/team scope 的可见性差异。
// 这些文案用于帮助模型选择写入范围，不代表访问控制实现本身。
func combinedMemoryScopeLines(autoDir, teamDir string) []string {
	return []string{
		fmt.Sprintf("`private` memories are shared only with the current user and are stored at `%s`.", autoDir),
		fmt.Sprintf("`team` memories are shared across contributors in this project and are stored at `%s`.", teamDir),
		"Choose scope deliberately: personal preferences stay private; project-wide conventions, coordination context, and shared external pointers usually belong in team memory.",
	}
}

// combinedTaxonomyLines 渲染 combined 模式下各记忆类型的默认 scope 倾向。
// 类型定义来自规则引擎，scope 文案只用于提示层决策。
func combinedTaxonomyLines(engine *MemoryRuleEngine) []string {
	engine = resolvedRuleEngine(engine)
	scopes := map[MemoryType]string{
		MemoryTypeUser:      "always private.",
		MemoryTypeFeedback:  "default private; use team only for project-wide conventions every contributor should follow.",
		MemoryTypeProject:   "private or team, but strongly bias toward team when the context affects collaborators.",
		MemoryTypeReference: "usually team because external pointers are most useful when shared.",
	}
	lines := make([]string, 0, len(engine.order)+1)
	for _, memoryType := range engine.order {
		behavior := engine.rules[memoryType]
		lines = append(lines, "`"+string(memoryType)+"`: "+behavior.Summary+" Scope: "+scopes[memoryType])
	}
	lines = append(lines, "`mode` and `scope` are separate dimensions, not a fifth memory type.")
	return lines
}

// combinedExclusionRules 返回 combined 模式额外禁写规则。
// 团队记忆会被共享，因此在标准排除项之外再次强调密钥禁止写入。
func combinedExclusionRules() []string {
	return append(
		newStandardExclusionRules(),
		"Never store secrets, credentials, tokens, API keys, or passwords in shared team memory.",
	)
}

// combinedSaveRules 生成 combined 模式的保存步骤说明。
// skipIndex 开启时只允许写 topic 文件，避免模型继续维护可能被截断的入口索引。
func combinedSaveRules(skipIndex bool, autoDir, teamDir string) []string {
	indexPrivate := filepath.ToSlash(memoryIndexPath(autoDir))
	indexTeam := filepath.ToSlash(memoryIndexPath(teamDir))
	rules := []string{
		"Choose the private or team directory according to the scope guidance for the memory type; `user` is always private, `feedback` stays private unless every collaborator should follow it, and shared project conventions or external pointers usually belong in team memory.",
		"Keep `name`, `description`, `type`, and `title` frontmatter aligned with the body.",
		"When saving a memory, include a concise `title` (max 12 characters) that captures the core point for card-view display. Omit if the description is already short and clear enough.",
		"Organize memories semantically by topic, not chronologically.",
		"Update or remove memories that turn out to be wrong or outdated.",
		"Do not write duplicate memories. First check whether an existing memory should be updated instead.",
		// combined 模式下 feedback 名称会影响 private/team 冲突处理，写前需要检查另一 scope。
		"When saving a `feedback` in combined mode, first scan the already-injected `MEMORY.md` indexes for any same-name `feedback` in the other scope. If found, prefer updating the team version (it overrides private for project-wide guidance) or rename to avoid conflict.",
	}
	if skipIndex {
		return append([]string{
			"When `skipIndex` is enabled, write or update the topic file only and leave both `MEMORY.md` indexes rebuild to an explicit recovery step.",
		}, rules...)
	}
	return append([]string{
		fmt.Sprintf("Combined mode is a two-step save: write the topic file, then update the matching directory's `MEMORY.md` pointer index (`%s` for private or `%s` for team).", indexPrivate, indexTeam),
		fmt.Sprintf("Each directory has its own `MEMORY.md` index. Keep both indexes pointer-only, one entry per line, and under roughly %d lines because oversized entrypoints are truncated before injection.", entrypointMaxLines),
		"Never inline full memory bodies into either `MEMORY.md`; keep each entry to a short hook that points at the topic file.",
	}, rules...)
}

// combinedAccessRules 生成 combined 模式的读取和不可见处理规则。
// scope 是权限边界，提示层不能通过猜测路径或换 scope 绕过 runtime 授权。
func combinedAccessRules() []string {
	return []string{
		"Read private or team memory when it seems relevant, or when the user references prior work with teammates or others in the organization.",
		"If the user explicitly asks to recall, check, or remember, you must read memory.",
		"If the user says ignore or not use memory, act as if both `MEMORY.md` indexes were empty; do not apply, reference, compare, or mention remembered content.",
		"Only use memory text surfaced by runtime or returned by tools; do not guess paths, scopes, or hidden entries from names.",
		"Visibility is decided by runtime `sanitize + resolve + authorize`; knowing a `name`, `path`, or `@agent` does not grant access.",
		"`scope` is an ACL boundary, not a fifth memory type.",
		"Treat `deny`, `not_visible`, and `local_unavailable` as unavailable; do not retry via another root or scope.",
		// feedback/project 类型在协作场景更容易影响后续贡献者，读取规则需单独强调。
		"Read `feedback` memory to guide behavior so the user — and the next contributor working on this project — does not need to repeat the same working guidance twice.",
		"Read `project` memory when shared decisions, owners, or affected modules might shape your answer; flag breaking changes for collaborators when a project memory says they are affected.",
	}
}

// taxonomyLines 渲染标准模式下的记忆类型说明。
// 返回值只包含提示文案，不暴露内部规则结构给调用方。
func (e *MemoryRuleEngine) taxonomyLines() []string {
	engine := resolvedRuleEngine(e)
	lines := make([]string, 0, len(engine.order)+1)
	for _, memoryType := range engine.order {
		behavior := engine.rules[memoryType]
		lines = append(lines, "`"+string(memoryType)+"`: "+behavior.Summary)
	}
	lines = append(lines, "`mode` and `scope` are separate dimensions, not a fifth memory type.")
	return lines
}

// renderBehaviorSection 按记忆类型渲染 save/access/trust 等行为章节。
// base 规则先于类型细则出现，确保通用禁止项不会被类型规则冲淡。
func (e *MemoryRuleEngine) renderBehaviorSection(title string, base []string, pick func(MemoryTypeBehavior) []string) string {
	engine := resolvedRuleEngine(e)
	parts := []string{title, renderBullets(base)}
	for _, memoryType := range engine.order {
		parts = append(parts, "#### "+string(memoryType), renderBullets(pick(engine.rules[memoryType])))
	}
	return strings.Join(nonEmpty(parts), "\n")
}

// resolvedRuleEngine 返回可用规则引擎，nil 时创建独立默认实例。
func resolvedRuleEngine(engine *MemoryRuleEngine) *MemoryRuleEngine {
	if engine != nil {
		return engine
	}
	return NewMemoryRuleEngine()
}

// renderSection 将标题和清理后的 bullet 行合并成一个 prompt 章节。
// 空章节返回空字符串，由上层 nonEmpty 过滤。
func renderSection(title string, lines []string) string {
	cleaned := normalizeStringSlice(lines)
	if len(cleaned) == 0 {
		return ""
	}
	parts := []string{title, renderBullets(cleaned)}
	return strings.Join(nonEmpty(parts), "\n")
}

func searchingPastContextSection(title string, enabled bool) string {
	if !enabled {
		return ""
	}
	return renderSection(title, newStandardSearchingPastContextRules())
}

func renderBullets(lines []string) string {
	cleaned := normalizeStringSlice(lines)
	if len(cleaned) == 0 {
		return ""
	}
	bullets := make([]string, 0, len(cleaned))
	for _, line := range cleaned {
		bullets = append(bullets, "- "+line)
	}
	return strings.Join(bullets, "\n")
}

func cloneBehavior(behavior MemoryTypeBehavior) MemoryTypeBehavior {
	return MemoryTypeBehavior{
		Summary: behavior.Summary,
		Save:    cloneStrings(behavior.Save),
		Access:  cloneStrings(behavior.Access),
		Trust:   cloneStrings(behavior.Trust),
	}
}

func cloneStrings(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	return append([]string(nil), lines...)
}

func nonEmpty(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}

func indexRule(skipIndex bool) string {
	if skipIndex {
		return "When `skipIndex` is enabled, write or update the topic file only and leave `MEMORY.md` rebuild to an explicit recovery step."
	}
	return "Standard mode is a two-step save: write the topic file, then keep `MEMORY.md` updated as the pointer index."
}
