package memory

import (
	"fmt"
	"strings"
)

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

var standardOverviewLines = []string{
	"Persistent memory stores durable, surprising facts for future turns; it is not a transcript, a scratchpad, or a replacement for current observation.",
	"Use memory to preserve behaviorally relevant context while keeping authorization, visibility, and retrieval boundaries intact.",
}

var standardSaveRules = []string{
	"Explicit `remember` saves immediately; explicit `forget` finds and deletes the matching memory.",
	"Each durable fact belongs in its own topic file; `MEMORY.md` stays a pointer index rather than a second copy of the body.",
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
	"`scope` is an ACL boundary, not a fifth memory type; `user`, `project`, and `local` visibility still require the same authorizer.",
	"Treat `deny`, `not_visible`, and `local_unavailable` as unavailable; do not retry via another root or scope.",
}

var standardTrustRules = []string{
	"Memory is what was true when it was written, not guaranteed current truth.",
	"If memory conflicts with current observation, trust the current observation and update or delete stale memory.",
	"Verify referenced files and paths still exist before relying on them.",
	"Verify referenced functions and flags still exist before relying on them; V3 also re-checks referenced type names as the same kind of stale claim.",
	"Before making a recommendation the user may act on, validate the current state first.",
	"For recent or current repository status, prefer current code or `git log` over memory snapshots.",
}

var standardExclusionRules = []string{
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

var standardPlanRules = []string{
	"Plans and non-trivial implementation strategies belong in plan mechanisms, not memory.",
	"Current step breakdowns, progress tracking, and task state belong in tasks, not memory.",
}

var standardSearchingPastContextRules = []string{
	"`Searching past context` is intentionally deferred in V3 to runtime retrieval work instead of prompt-level directory or transcript grep.",
	"Do not probe memory directories, hidden roots, or session transcript logs from the prompt layer.",
	"Only use memory surfaced by runtime or included in context, and apply the access/trust rules above before acting on it.",
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
	sections := make([]string, 0, 9)
	sectionIndex := 0
	nextTitle := func(name string) string {
		sectionIndex++
		return fmt.Sprintf("### %d. %s", sectionIndex, name)
	}
	sections = append(sections, renderSection(nextTitle("memory system"), standardOverviewLines))
	sections = append(sections, renderSection(nextTitle("taxonomy"), engine.taxonomyLines()))
	sections = append(sections, renderSection(nextTitle("exclusions"), standardExclusionRules))
	sections = append(sections, engine.renderBehaviorSection(nextTitle("save rules / how to save memories"), append(cloneStrings(standardSaveRules), indexRule(opts.SkipIndex)), func(b MemoryTypeBehavior) []string { return b.Save }))
	sections = append(sections, engine.renderBehaviorSection(nextTitle("access rules / when to access memories"), standardAccessRules, func(b MemoryTypeBehavior) []string { return b.Access }))
	sections = append(sections, engine.renderBehaviorSection(nextTitle("trust rules / before recommending from memory"), standardTrustRules, func(b MemoryTypeBehavior) []string { return b.Trust }))
	sections = append(sections, renderSection(nextTitle("memory vs plan/tasks"), standardPlanRules))
	if extraLines := normalizeStringSlice(opts.ExtraGuidelines); len(extraLines) > 0 {
		sections = append(sections, renderSection(nextTitle("extra guidelines"), extraLines))
	}
	sections = append(sections, renderSection(nextTitle("searching past context"), standardSearchingPastContextRules))
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
		return "When `skipIndex` is enabled, write or update the topic file only and leave `MEMORY.md` rebuild to an explicit recovery step."
	}
	return "Standard mode is a two-step save: write the topic file, then keep `MEMORY.md` updated as the pointer index."
}
