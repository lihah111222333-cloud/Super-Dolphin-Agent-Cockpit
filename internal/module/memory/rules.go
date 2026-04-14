package memory

import "strings"

type MemoryMode string

const (
	MemoryModeStandard MemoryMode = "standard"
	MemoryModeKairos   MemoryMode = "kairos"
	MemoryModeTeam     MemoryMode = "team"
)

type MemoryRuleOptions struct {
	SkipIndex       bool
	ExtraGuidelines []string
}

type MemoryTypeBehavior struct {
	Summary string
	Save    []string
	Access  []string
	Trust   []string
}

type MemoryRuleEngine struct {
	order []MemoryType
	rules map[MemoryType]MemoryTypeBehavior
}

var standardMemoryTypeBehaviors = map[MemoryType]MemoryTypeBehavior{
	MemoryTypeUser: {
		Summary: "Durable user role, goals, responsibilities, knowledge background, and collaboration preferences.",
		Save: []string{
			"Store durable user role, goals, responsibilities, knowledge background, and collaboration preferences.",
			"Do not save negative judgments or unrelated personal evaluations.",
		},
		Access: []string{
			"Read `user` memory when role, goals, working style, knowledge level, or collaboration preferences affect the answer.",
		},
		Trust: []string{
			"Re-confirm if the user's goals or preferences appear to have changed.",
		},
	},
	MemoryTypeFeedback: {
		Summary: "Confirmed corrections and validated non-obvious ways of working.",
		Save: []string{
			"Store confirmed corrections plus validated non-obvious successful ways of working.",
			"Structure the body as `rule`, `Why:`, and `How to apply:`.",
		},
		Access: []string{
			"Read `feedback` memory when choosing how to execute, review, or communicate a similar task.",
		},
		Trust: []string{
			"Trust only feedback that was explicitly confirmed or repeatedly validated; if it stops working, revise or delete it.",
		},
	},
	MemoryTypeProject: {
		Summary: "Project background, decision rationale, deadlines, ownership, goals, and incidents that are not derivable from code or git.",
		Save: []string{
			"Store project background, decision rationale, deadlines, ownership, goals, or incidents that cannot be derived from code or git.",
			"Convert relative dates to absolute dates and structure the body as `fact`, `Why:`, and `How to apply:`.",
		},
		Access: []string{
			"Read `project` memory when business context, deadlines, owners, decisions, or incident history matter.",
		},
		Trust: []string{
			"Re-check dates, owners, and incident status against current code, docs, or direct user input before acting; keep dates absolute.",
		},
	},
	MemoryTypeReference: {
		Summary: "Pointers to external systems that explain where to find information and why it matters.",
		Save: []string{
			"Store where to find external information and why it matters; keep pointers only, not copied snapshots.",
		},
		Access: []string{
			"Read `reference` memory when current work may require consulting external systems such as docs, Slack, Grafana, Linear, or runbooks.",
		},
		Trust: []string{
			"Verify the pointer still resolves and that the underlying external data is current before relying on it.",
		},
	},
}

var standardOverviewLines = []string{
	"Persistent memory stores durable, surprising facts for future turns; it is not a transcript, a scratchpad, or a replacement for current observation.",
	"Use memory to preserve behaviorally relevant context while keeping authorization, visibility, and retrieval boundaries intact.",
}

var standardSaveRules = []string{
	"Explicit `remember` saves immediately; explicit `forget` finds and deletes the matching memory.",
	"Prefer updating an existing topic over creating duplicates.",
	"Keep `name`, `description`, and `type` frontmatter aligned with the body.",
	"Organize memory by semantic topic, not by time.",
	"Save or delete only after runtime `sanitize + resolve + authorize` succeeds; `deny`, `not_visible`, and `local_unavailable` are hard stop conditions.",
	"The prompt layer must not probe or `mkdir` memory directories; runtime may ensure them separately.",
	"Update or delete stale or incorrect memory instead of letting it drift.",
	"`MEMORY.md` index lines must stay one-per-line, pointer-only, roughly 150 characters or less, and must not inline full memory bodies.",
}

var standardAccessRules = []string{
	"Read memory when it appears relevant or when the user refers to previous context.",
	"If the user explicitly asks to recall, check, or remember, you must read memory.",
	"If the user says ignore or not use memory, act as if `MEMORY.md` is empty; do not apply, reference, compare, or mention remembered content.",
	"Only use memory text surfaced by runtime or returned by tools; do not guess paths, scopes, or hidden entries from names.",
	"Visibility is decided by runtime `sanitize + resolve + authorize`; knowing a `name`, `path`, or `@agent` does not grant access.",
	"Treat `deny`, `not_visible`, and `local_unavailable` as unavailable; do not retry via another root or scope.",
}

var standardTrustRules = []string{
	"Memory is past truth, not guaranteed current truth.",
	"If memory conflicts with current observation, trust the current observation and update or delete stale memory.",
	"Verify referenced files and paths still exist before relying on them.",
	"Verify referenced functions, flags, and types still exist before relying on them.",
	"Before making a recommendation the user may act on, validate the current state first.",
	"For recent or current repository status, prefer current code or `git log` over memory snapshots.",
}

var standardExclusionRules = []string{
	"Never store secrets, credentials, tokens, API keys, or passwords in any memory type.",
	"Do not store code patterns, conventions, architecture, file paths, or project structure.",
	"Do not store git history, recent changes, or who changed what.",
	"Do not store debugging recipes or repair playbooks.",
	"Do not store facts that already belong in `CLAUDE.md`.",
	"Do not store temporary task details, short-term status, or current conversation context.",
	"Do not store PR lists, activity summaries, progress trackers, tasks, or plans.",
	"Even if the user explicitly asks, these exclusions still apply.",
	"Store only the surprising and non-obvious durable part of a fact.",
}

var standardPlanRules = []string{
	"Plans and non-trivial implementation strategies belong in plan mechanisms, not memory.",
	"Current step breakdowns, progress tracking, and task state belong in tasks, not memory.",
	"`Searching past context` is deferred to later retrieval work; these rules only govern surfaced memory behavior.",
}

var defaultMemoryRuleEngine = NewMemoryRuleEngine()

func NewMemoryRuleEngine() *MemoryRuleEngine {
	engine := &MemoryRuleEngine{
		order: append([]MemoryType(nil), diskMemoryTypes...),
		rules: make(map[MemoryType]MemoryTypeBehavior, len(standardMemoryTypeBehaviors)),
	}
	for _, memoryType := range engine.order {
		engine.rules[memoryType] = cloneBehavior(standardMemoryTypeBehaviors[memoryType])
	}
	return engine
}

func BuildMemoryLines(skipIndex bool, extraGuidelines []string) string {
	return defaultMemoryRuleEngine.BuildMemoryLines(MemoryRuleOptions{
		SkipIndex:       skipIndex,
		ExtraGuidelines: extraGuidelines,
	})
}

func LoadMemoryPrompt(mode MemoryMode, autoEnabled, skipIndex bool, extraGuidelines []string) *string {
	return defaultMemoryRuleEngine.LoadMemoryPrompt(mode, autoEnabled, MemoryRuleOptions{
		SkipIndex:       skipIndex,
		ExtraGuidelines: extraGuidelines,
	})
}

func (e *MemoryRuleEngine) RulesForType(memoryType MemoryType) (MemoryTypeBehavior, bool) {
	behavior, ok := resolvedRuleEngine(e).rules[ParseMemoryType(string(memoryType))]
	if !ok {
		return MemoryTypeBehavior{}, false
	}
	return cloneBehavior(behavior), true
}

func (e *MemoryRuleEngine) LoadMemoryPrompt(mode MemoryMode, autoEnabled bool, opts MemoryRuleOptions) *string {
	if !autoEnabled {
		return nil
	}
	switch strings.TrimSpace(string(mode)) {
	case "", string(MemoryModeStandard):
		text := strings.TrimSpace(resolvedRuleEngine(e).BuildMemoryLines(opts))
		if text == "" {
			return nil
		}
		return &text
	default:
		return nil
	}
}

func (e *MemoryRuleEngine) BuildMemoryLines(opts MemoryRuleOptions) string {
	engine := resolvedRuleEngine(e)
	sections := []string{
		renderSection("### 1. memory system", standardOverviewLines),
		renderSection("### 2. taxonomy", engine.taxonomyLines()),
		engine.renderBehaviorSection("### 3. save rules", append(cloneStrings(standardSaveRules), indexRule(opts.SkipIndex)), func(b MemoryTypeBehavior) []string { return b.Save }),
		engine.renderBehaviorSection("### 4. access rules", standardAccessRules, func(b MemoryTypeBehavior) []string { return b.Access }),
		engine.renderBehaviorSection("### 5. trust rules", standardTrustRules, func(b MemoryTypeBehavior) []string { return b.Trust }),
		renderSection("### 6. exclusions", standardExclusionRules),
		renderSection("### 7. memory vs plan/tasks", standardPlanRules),
	}
	if extra := renderSection("### 8. extra guidelines", normalizeStringSlice(opts.ExtraGuidelines)); extra != "" {
		sections = append(sections, extra)
	}
	return strings.Join(sections, "\n\n")
}

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

func (e *MemoryRuleEngine) renderBehaviorSection(title string, base []string, pick func(MemoryTypeBehavior) []string) string {
	engine := resolvedRuleEngine(e)
	parts := []string{title, renderBullets(base)}
	for _, memoryType := range engine.order {
		parts = append(parts, "#### "+string(memoryType), renderBullets(pick(engine.rules[memoryType])))
	}
	return strings.Join(nonEmpty(parts), "\n")
}

func resolvedRuleEngine(engine *MemoryRuleEngine) *MemoryRuleEngine {
	if engine != nil {
		return engine
	}
	return defaultMemoryRuleEngine
}

func renderSection(title string, lines []string) string {
	cleaned := normalizeStringSlice(lines)
	if len(cleaned) == 0 {
		return ""
	}
	parts := []string{title, renderBullets(cleaned)}
	return strings.Join(nonEmpty(parts), "\n")
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
		return "When `skipIndex` is enabled, update the topic file only and leave `MEMORY.md` rebuild to an explicit recovery step."
	}
	return "Standard mode writes the topic file and keeps `MEMORY.md` updated as the pointer index."
}
