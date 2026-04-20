package turn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestPrepareTurnKeepsSkillPromptsAndNormalizesInputs(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt: "Please use @debug and [skill:deploy-tool] on this issue.",
		Images: []string{"https://example.com/screen.png", "https://example.com/screen.png"},
		Files:  []string{"./README.md", "./README.md", "./malware.exe"},
		Skills: []dto.SkillRef{{Name: "explicit", Prompt: "explicit guidance"}},
		CandidateSkills: []dto.SkillRef{
			{Name: "debug", Prompt: "debug guidance"},
			{Name: "deploy-tool", Prompt: "deploy guidance"},
		},
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	if got := len(req.Inputs); got != 3 {
		t.Fatalf("len(req.Inputs) = %d, want 3", got)
	}
	if req.Inputs[0].Type != "text" || req.Inputs[0].Content != "Please use @debug and [skill:deploy-tool] on this issue." {
		t.Fatalf("first input = %#v, want prompt text", req.Inputs[0])
	}
	if req.Inputs[1].Type != "image" || req.Inputs[1].URL != "https://example.com/screen.png" {
		t.Fatalf("second input = %#v, want remote image", req.Inputs[1])
	}
	if req.Inputs[2].Type != "mention" || req.Inputs[2].Path != "./README.md" {
		t.Fatalf("third input = %#v, want deduped mention", req.Inputs[2])
	}

	gotNames := skillNames(req.Skills)
	if len(gotNames) != 3 || gotNames[0] != "explicit" || gotNames[1] != "debug" || gotNames[2] != "deploy-tool" {
		t.Fatalf("skill names = %#v, want explicit + auto-matched", gotNames)
	}
	if req.Skills[1].Prompt != "debug guidance" || req.Skills[2].Prompt != "deploy guidance" {
		t.Fatalf("skill prompts were not preserved: %#v", req.Skills)
	}
}

func TestPrepareTurnBuildsProviderSkillPromptFromHydratedManualSkills(t *testing.T) {
	t.Parallel()

	const fullHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	lookup := &stubSkillLookup{
		infos: []skillpkg.SkillInfo{{
			Name:        "debug",
			Dir:         "/tmp/skills/debug",
			Summary:     "debug helpers",
			ContentHash: fullHash,
		}},
		bodies: map[string]string{
			filepath.Join("/tmp/skills/debug", "SKILL.md"): "full debug body",
		},
	}
	svc := newService(silentLogger(), nil, nil, lookup, staticSkillPortResolver{port: providerSkillPromptPort{}}).(*service)
	session := &stubSession{threadID: "thread-provider-skill-prompt"}

	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Provider:             "codex",
		Prompt:               "please debug the failure",
		Skills:               []dto.SkillRef{{Name: "debug"}},
		ManualSkillSelection: true,
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if len(req.Skills) != 1 {
		t.Fatalf("len(req.Skills) = %d, want 1", len(req.Skills))
	}
	got := req.Skills[0]
	if got.Prompt != "full debug body" || got.Summary != "debug helpers" {
		t.Fatalf("hydrated skill = %+v, want prompt + summary", got)
	}
	if got.Version != fullHash[:12] {
		t.Fatalf("Version = %q, want %q", got.Version, fullHash[:12])
	}
	if got.Source != dto.SkillSourceManual {
		t.Fatalf("Source = %q, want manual", got.Source)
	}
	if !strings.Contains(req.SkillPrompt, "skills:\n- debug") {
		t.Fatalf("SkillPrompt = %q, want skill list prelude", req.SkillPrompt)
	}
	if !strings.Contains(req.SkillPrompt, "[skill:debug::full@v1]") || !strings.Contains(req.SkillPrompt, "full debug body") {
		t.Fatalf("SkillPrompt = %q, want hydrated full skill block", req.SkillPrompt)
	}
}

func TestPrepareTurnManualSkillSelectionDisablesAutoMatch(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt:               "Please use @debug on this issue.",
		ManualSkillSelection: true,
		CandidateSkills:      []dto.SkillRef{{Name: "debug", Prompt: "debug guidance"}},
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if req.ManualSkillSelection != true {
		t.Fatal("ManualSkillSelection = false, want true")
	}
	if len(req.Skills) != 0 {
		t.Fatalf("Skills = %#v, want no auto-matched skills in manual mode", req.Skills)
	}
}

func TestPrepareTurnSkillResolverMatrix(t *testing.T) {
	t.Parallel()

	const (
		skillName = "matrix"
		fullHash  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	)

	lookup := &stubSkillLookup{
		infos: []skillpkg.SkillInfo{{
			Name:        skillName,
			Dir:         "/tmp/skills/matrix",
			Summary:     "matrix summary",
			ContentHash: fullHash,
		}},
		bodies: map[string]string{
			filepath.Join("/tmp/skills/matrix", "SKILL.md"): "matrix body",
		},
	}
	matcher := &stubRuntimeMatcher{}
	makeService := func() *service {
		svc := newService(silentLogger(), nil, nil, lookup).(*service)
		svc.skillMatcher = matcher
		svc.expandedState = newExpandedStateStore(5)
		return svc
	}
	makeMatch := func(kind skillpkg.RuntimeMatchKind) skillpkg.RuntimeSkillMatch {
		return skillpkg.RuntimeSkillMatch{
			Skill: skillpkg.SkillInfo{
				Name:        skillName,
				Dir:         "/tmp/skills/matrix",
				Summary:     "matrix summary",
				ContentHash: fullHash,
			},
			Kind:         kind,
			MatchedTerms: []string{"matrix"},
		}
	}
	manualRef := dto.SkillRef{Name: skillName}
	version := shortSkillHash(fullHash)
	cases := []struct {
		name       string
		selected   []dto.SkillRef
		runtime    []skillpkg.RuntimeSkillMatch
		withCarry  bool
		wantCount  int
		wantMode   dto.SkillMode
		wantSource dto.SkillSource
	}{
		{name: "manual/explicit", selected: []dto.SkillRef{manualRef}, wantCount: 1, wantMode: dto.SkillModeFull, wantSource: dto.SkillSourceManual},
		{name: "manual/auto", runtime: []skillpkg.RuntimeSkillMatch{makeMatch(skillpkg.RuntimeMatchKindExplicit)}, wantCount: 1, wantMode: dto.SkillModeFull, wantSource: dto.SkillSourceManual},
		{name: "manual/carry", selected: []dto.SkillRef{manualRef}, withCarry: true, wantCount: 1, wantMode: dto.SkillModeFull, wantSource: dto.SkillSourceManual},
		{name: "force/explicit", selected: []dto.SkillRef{manualRef}, runtime: []skillpkg.RuntimeSkillMatch{makeMatch(skillpkg.RuntimeMatchKindForce)}, wantCount: 1, wantMode: dto.SkillModeFull, wantSource: dto.SkillSourceManual},
		{name: "force/auto", runtime: []skillpkg.RuntimeSkillMatch{makeMatch(skillpkg.RuntimeMatchKindForce)}, wantCount: 1, wantMode: dto.SkillModeFull, wantSource: dto.SkillSourceForce},
		{name: "force/carry", runtime: []skillpkg.RuntimeSkillMatch{makeMatch(skillpkg.RuntimeMatchKindForce)}, withCarry: true},
		{name: "trigger/explicit", selected: []dto.SkillRef{manualRef}, runtime: []skillpkg.RuntimeSkillMatch{makeMatch(skillpkg.RuntimeMatchKindTrigger)}, wantCount: 1, wantMode: dto.SkillModeFull, wantSource: dto.SkillSourceManual},
		{name: "trigger/auto", runtime: []skillpkg.RuntimeSkillMatch{makeMatch(skillpkg.RuntimeMatchKindTrigger)}, wantCount: 1, wantMode: dto.SkillModeSummary, wantSource: dto.SkillSourceTrigger},
		{name: "trigger/carry", runtime: []skillpkg.RuntimeSkillMatch{makeMatch(skillpkg.RuntimeMatchKindTrigger)}, withCarry: true},
		{name: "miss/explicit", selected: []dto.SkillRef{manualRef}, wantCount: 1, wantMode: dto.SkillModeFull, wantSource: dto.SkillSourceManual},
		{name: "miss/auto"},
		{name: "miss/carry", withCarry: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc := makeService()
			session := &stubSession{threadID: "thread-matrix"}
			matcher.matches = tc.runtime
			matcher.calls = 0
			if tc.withCarry {
				svc.expandedState.CommitTurn("thread-matrix", 1, []expandedResolvedSkill{{
					Ref:         dto.SkillRef{Name: skillName, Version: version, Mode: dto.SkillModeFull},
					ContentHash: fullHash,
				}})
			}
			req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
				Prompt: "please use matrix",
				Skills: tc.selected,
			})
			if err != nil {
				t.Fatalf("PrepareTurn() error = %v", err)
			}
			if got := len(req.Skills); got != tc.wantCount {
				t.Fatalf("len(req.Skills) = %d, want %d: %#v", got, tc.wantCount, req.Skills)
			}
			if tc.wantCount == 0 {
				return
			}
			got := req.Skills[0]
			if got.Name != skillName {
				t.Fatalf("skill name = %q, want %q", got.Name, skillName)
			}
			if got.Mode.Effective() != tc.wantMode {
				t.Fatalf("skill mode = %q, want %q", got.Mode.Effective(), tc.wantMode)
			}
			if got.Source != tc.wantSource {
				t.Fatalf("skill source = %q, want %q", got.Source, tc.wantSource)
			}
			if got.Source != dto.SkillSourceTrigger && strings.TrimSpace(got.Prompt) == "" {
				t.Fatalf("full/manual skill should hydrate prompt, got %+v", got)
			}
			if got.Source == dto.SkillSourceTrigger && strings.TrimSpace(got.Summary) == "" {
				t.Fatalf("trigger skill should carry summary, got %+v", got)
			}
		})
	}
}

func TestPrepareTurnSkillResolverTopK(t *testing.T) {
	t.Parallel()

	forceInfos := make([]skillpkg.SkillInfo, 0, 10)
	triggerInfos := make([]skillpkg.SkillInfo, 0, 10)
	bodies := make(map[string]string)
	matches := make([]skillpkg.RuntimeSkillMatch, 0, 10)
	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("force-%d", i)
		hash := fmt.Sprintf("%064x", i)
		dir := filepath.Join("/tmp/skills", name)
		forceInfos = append(forceInfos, skillpkg.SkillInfo{Name: name, Dir: dir, Summary: name + " summary", ContentHash: hash})
		bodies[filepath.Join(dir, "SKILL.md")] = name + " body"
		matches = append(matches, skillpkg.RuntimeSkillMatch{
			Skill:        forceInfos[len(forceInfos)-1],
			Kind:         skillpkg.RuntimeMatchKindForce,
			MatchedTerms: repeatStrings(name, 7-i),
		})
	}
	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("trigger-%d", i)
		hash := fmt.Sprintf("%064x", 100+i)
		dir := filepath.Join("/tmp/skills", name)
		triggerInfos = append(triggerInfos, skillpkg.SkillInfo{Name: name, Dir: dir, Summary: name + " summary", ContentHash: hash})
		bodies[filepath.Join(dir, "SKILL.md")] = name + " body"
		matches = append(matches, skillpkg.RuntimeSkillMatch{
			Skill:        triggerInfos[len(triggerInfos)-1],
			Kind:         skillpkg.RuntimeMatchKindTrigger,
			MatchedTerms: repeatStrings(name, 5-i),
		})
	}
	lookup := &stubSkillLookup{
		infos:  append(forceInfos, triggerInfos...),
		bodies: bodies,
	}
	matcher := &stubRuntimeMatcher{matches: matches}
	svc := newService(silentLogger(), nil, nil, lookup).(*service)
	svc.skillMatcher = matcher
	svc.expandedState = newExpandedStateStore(5)
	req, err := svc.PrepareTurn(context.Background(), &stubSession{threadID: "thread-topk"}, PrepareInput{Prompt: "top-k please"})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	got := make(map[string]dto.SkillRef, len(req.Skills))
	for _, ref := range req.Skills {
		got[ref.Name] = ref
	}
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("force-%d", i)
		ref, ok := got[name]
		if !ok {
			t.Fatalf("missing force top-k winner %q in %#v", name, req.Skills)
		}
		if ref.Source != dto.SkillSourceForce || ref.Mode.Effective() != dto.SkillModeFull {
			t.Fatalf("force winner mismatch for %q: %+v", name, ref)
		}
	}
	if _, ok := got["force-6"]; ok {
		t.Fatalf("force-6 should be trimmed by top-k: %#v", req.Skills)
	}
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("trigger-%d", i)
		ref, ok := got[name]
		if !ok {
			t.Fatalf("missing trigger top-k winner %q in %#v", name, req.Skills)
		}
		if ref.Source != dto.SkillSourceTrigger || ref.Mode.Effective() != dto.SkillModeSummary {
			t.Fatalf("trigger winner mismatch for %q: %+v", name, ref)
		}
	}
	if _, ok := got["trigger-4"]; ok {
		t.Fatalf("trigger-4 should be trimmed by top-k: %#v", req.Skills)
	}
}

func TestPrepareTurnExpandedStateTTLHashAndThreadIsolation(t *testing.T) {
	t.Parallel()

	lookup := &stubSkillLookup{
		infos: []skillpkg.SkillInfo{{
			Name:        "carry",
			Dir:         "/tmp/skills/carry",
			Summary:     "carry summary",
			ContentHash: strings.Repeat("a", 64),
		}},
		bodies: map[string]string{
			filepath.Join("/tmp/skills/carry", "SKILL.md"): "carry body v1",
		},
	}
	matcher := &stubRuntimeMatcher{
		matches: []skillpkg.RuntimeSkillMatch{{
			Skill:        lookup.infos[0],
			Kind:         skillpkg.RuntimeMatchKindForce,
			MatchedTerms: []string{"carry"},
		}},
	}
	svc := newService(silentLogger(), nil, nil, lookup).(*service)
	svc.skillMatcher = matcher
	svc.expandedState = newExpandedStateStore(5)

	for turn := 1; turn <= 6; turn++ {
		req, err := svc.PrepareTurn(context.Background(), &stubSession{threadID: "thread-carry"}, PrepareInput{Prompt: "please carry this"})
		if err != nil {
			t.Fatalf("turn %d PrepareTurn() error = %v", turn, err)
		}
		switch turn {
		case 1, 6:
			if len(req.Skills) != 1 || req.Skills[0].Name != "carry" {
				t.Fatalf("turn %d should inject carry once: %#v", turn, req.Skills)
			}
		default:
			if len(req.Skills) != 0 {
				t.Fatalf("turn %d should be suppressed by TTL: %#v", turn, req.Skills)
			}
		}
	}

	lookup.infos[0].ContentHash = strings.Repeat("b", 64)
	lookup.infos[0].Summary = "carry summary v2"
	lookup.bodies[filepath.Join("/tmp/skills/carry", "SKILL.md")] = "carry body v2"
	matcher.matches = []skillpkg.RuntimeSkillMatch{{
		Skill:        lookup.infos[0],
		Kind:         skillpkg.RuntimeMatchKindForce,
		MatchedTerms: []string{"carry"},
	}}
	req, err := svc.PrepareTurn(context.Background(), &stubSession{threadID: "thread-carry"}, PrepareInput{Prompt: "please carry this"})
	if err != nil {
		t.Fatalf("hash change PrepareTurn() error = %v", err)
	}
	if len(req.Skills) != 1 || req.Skills[0].Prompt != "carry body v2" {
		t.Fatalf("hash change should re-inject immediately: %#v", req.Skills)
	}

	otherThreadReq, err := svc.PrepareTurn(context.Background(), &stubSession{threadID: "thread-carry-2"}, PrepareInput{Prompt: "please carry this"})
	if err != nil {
		t.Fatalf("thread isolation PrepareTurn() error = %v", err)
	}
	if len(otherThreadReq.Skills) != 1 || otherThreadReq.Skills[0].Name != "carry" {
		t.Fatalf("different thread must not reuse previous carry state: %#v", otherThreadReq.Skills)
	}
}

func TestPrepareTurnTruncatesInputCount(t *testing.T) {
	t.Parallel()

	items := make([]InputItem, 0, maxTurnInputItems+32)
	for i := range maxTurnInputItems + 32 {
		items = append(items, InputItem{Type: "mention", Path: fmt.Sprintf("./doc-%03d.md", i)})
	}

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{Inputs: items})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if got := len(req.Inputs); got != maxTurnInputItems {
		t.Fatalf("len(req.Inputs) = %d, want %d", got, maxTurnInputItems)
	}
}

func TestPrepareTurnUsesExecutableBinaryDirForManifest(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	want := filepath.Join(filepath.Dir(exe), "mcp-lsp")
	if got := commandForBinary(req.MCP, "lsp"); got != want {
		t.Fatalf("lsp command = %q, want %q", got, want)
	}
}

func TestPrepareTurnPrefersExplicitBinaryDir(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{BinaryDir: "/tmp/turn-bin"})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	want := filepath.Join("/tmp/turn-bin", "mcp-lsp")
	if got := commandForBinary(req.MCP, "lsp"); got != want {
		t.Fatalf("lsp command = %q, want %q", got, want)
	}
}

func TestPrepareTurnInjectsTurnAssembly(t *testing.T) {
	t.Parallel()

	assembly := &stubPromptAssemblyService{
		turn: contract.TurnAssembly{UserContextText: "assembled user context"},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), assembly)
	session := &stubSession{
		threadID: "thread-1",
		runtimeConfig: map[string]any{
			"provider":                     "codex-runtime",
			"gitRoot":                      "/runtime-repo",
			"language":                     "Chinese",
			"enabledTools":                 []string{"spawn_agent"},
			"additionalWorkingDirectories": []string{"/repo/runtime-extra"},
			"mcpTools":                     []string{"mcp__orch__orchestration_send_message"},
			"mcpInstructions":              map[string]any{"orch": "Use orchestration runtime fallback."},
			"sessionFlags":                 map[string]any{"runtime_only": true},
		},
	}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt: "please verify the cache",
		CWD:    "/repo",
		Model:  "claude-sonnet",
		RuntimeUserContext: map[string]string{
			"workerToolsContext": "Workers can use bash and read tools.",
			"terminalFocus":      "The terminal is unfocused — the user is not actively watching.",
		},
		ThreadRuntimeConfig: map[string]any{
			"provider":                     "codex-thread",
			"gitRoot":                      "/thread-repo",
			"isWorktree":                   true,
			"language":                     "Japanese",
			"enabledTools":                 []string{"lsp_file", "lsp_grep"},
			"additionalWorkingDirectories": []string{"/repo/thread-extra"},
			"mcpTools":                     []string{"mcp__lsp__lsp_grep"},
			"mcpInstructions":              map[string]any{"lsp": "Use LSP thread fallback."},
			"sessionFlags":                 map[string]any{"verification_required": true},
		},
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if req.TurnAssembly.UserContextText != "assembled user context" {
		t.Fatalf("TurnAssembly = %#v, want injected user context", req.TurnAssembly)
	}
	if assembly.lastTurnInput.ThreadID != "thread-1" {
		t.Fatalf("last turn thread id = %q, want thread-1", assembly.lastTurnInput.ThreadID)
	}
	if assembly.lastTurnInput.UserText != "please verify the cache" {
		t.Fatalf("last turn user text = %q, want prompt text", assembly.lastTurnInput.UserText)
	}
	if assembly.lastTurnInput.CWD != "/repo" {
		t.Fatalf("last turn cwd = %q, want /repo", assembly.lastTurnInput.CWD)
	}
	if assembly.lastTurnInput.Model != "claude-sonnet" {
		t.Fatalf("last turn model = %q, want claude-sonnet", assembly.lastTurnInput.Model)
	}
	if assembly.lastTurnInput.Provider != "codex-thread" || assembly.lastTurnInput.GitRoot != "/thread-repo" || !assembly.lastTurnInput.IsWorktree {
		t.Fatalf("last turn env context = %#v", assembly.lastTurnInput)
	}
	if assembly.lastTurnInput.Language != "Japanese" {
		t.Fatalf("last turn language = %q, want Japanese", assembly.lastTurnInput.Language)
	}
	if got := assembly.lastTurnInput.EnabledTools; len(got) != 2 || got[0] != "lsp_file" || got[1] != "lsp_grep" {
		t.Fatalf("EnabledTools = %#v, want LSP tool set", got)
	}
	if got := assembly.lastTurnInput.AdditionalWorkingDirectories; len(got) != 1 || got[0] != "/repo/thread-extra" {
		t.Fatalf("AdditionalWorkingDirectories = %#v, want thread-state dirs", got)
	}
	if len(assembly.lastTurnInput.MCPSnapshot.Servers) == 0 {
		t.Fatalf("MCP snapshot = %#v, want manifest-derived servers", assembly.lastTurnInput.MCPSnapshot)
	}
	if got := assembly.lastTurnInput.MCPSnapshot.Tools; !slices.Contains(got, "mcp__lsp__lsp_grep") {
		t.Fatalf("MCPSnapshot.Tools = %#v, want thread-state tool present", got)
	}
	if assembly.lastTurnInput.MCPSnapshot.Instructions["lsp"] != "Use LSP thread fallback." {
		t.Fatalf("MCPSnapshot.Instructions = %#v", assembly.lastTurnInput.MCPSnapshot.Instructions)
	}
	if !assembly.lastTurnInput.SessionFlags["verification_required"] {
		t.Fatalf("SessionFlags = %#v, want verification_required", assembly.lastTurnInput.SessionFlags)
	}
	if assembly.lastTurnInput.SessionFlags["runtime_only"] {
		t.Fatalf("SessionFlags = %#v, want thread-state fallback to win", assembly.lastTurnInput.SessionFlags)
	}
	if assembly.lastTurnInput.RuntimeUserContext["workerToolsContext"] != "Workers can use bash and read tools." {
		t.Fatalf("RuntimeUserContext = %#v, want propagated worker tools context", assembly.lastTurnInput.RuntimeUserContext)
	}
	if assembly.lastTurnInput.RuntimeUserContext["terminalFocus"] == "" {
		t.Fatalf("RuntimeUserContext = %#v, want terminal focus enhancement", assembly.lastTurnInput.RuntimeUserContext)
	}
}

func TestSteerTurnPropagatesTurnAssembly(t *testing.T) {
	t.Parallel()

	assembly := &stubPromptAssemblyService{
		turn: contract.TurnAssembly{UserContextText: "assembled steer context"},
	}
	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			return newStubTurnHandle(req.LocalID, "provider-2"), nil
		},
		steer: func(_ context.Context, req dto.SteerRequest) error {
			if req.TurnAssembly.UserContextText != "assembled steer context" {
				t.Fatalf("SteerTurn assembly = %#v, want injected user context", req.TurnAssembly)
			}
			return nil
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), assembly)
	started, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-2",
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if _, err := svc.SteerTurn(context.Background(), session, "local-2", PrepareInput{Prompt: "steer this", CWD: "/repo"}); err != nil {
		t.Fatalf("SteerTurn() error = %v", err)
	}
	if session.lastSteer.TurnAssembly.UserContextText != "assembled steer context" {
		t.Fatalf("last steer assembly = %#v, want injected user context", session.lastSteer.TurnAssembly)
	}
	if started == nil {
		t.Fatal("started handle = nil, want active handle")
	}
	if assembly.lastTurnInput.UserText != "steer this" {
		t.Fatalf("last turn user text = %q, want steer prompt", assembly.lastTurnInput.UserText)
	}
}

func TestInterruptTurnWaitsForSettle(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-1", "provider-1")
	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			time.AfterFunc(20*time.Millisecond, func() {
				handle.complete(errors.New("turn aborted"))
			})
			return nil
		},
	}

	svc := NewService(silentLogger())
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-1",
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	interruptStatus, err := svc.InterruptTurn(context.Background(), session, "user")
	if err != nil {
		t.Fatalf("InterruptTurn() error = %v", err)
	}
	if interruptStatus.LocalID != "local-1" || interruptStatus.State != "interrupted" {
		t.Fatalf("InterruptTurn() status = %#v, want local-1/interrupted", interruptStatus)
	}
	if session.lastInterrupt.ThreadID != "thread-1" {
		t.Fatalf("InterruptTurn thread id = %q, want thread-1", session.lastInterrupt.ThreadID)
	}

	status, err := svc.TrackTurn(context.Background(), "local-1")
	if err != nil {
		t.Fatalf("TrackTurn() error = %v", err)
	}
	if status.State != "interrupted" {
		t.Fatalf("status.State = %q, want interrupted", status.State)
	}
}

func TestSteerTurnAppendsToActiveTurn(t *testing.T) {
	t.Parallel()

	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			handle := newStubTurnHandle(req.LocalID, "provider-2")
			return handle, nil
		},
		steer: func(_ context.Context, req dto.SteerRequest) error {
			if req.ExpectedTurnID != "provider-2" {
				t.Fatalf("SteerTurn expected turn id = %q, want provider-2", req.ExpectedTurnID)
			}
			if len(req.Inputs) != 1 || req.Inputs[0].Type != "text" || req.Inputs[0].Content != "steer this" {
				t.Fatalf("SteerTurn request = %#v", req)
			}
			return nil
		},
	}

	svc := NewService(silentLogger())
	started, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-2",
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	handle, err := svc.SteerTurn(context.Background(), session, "local-2", PrepareInput{Prompt: "steer this"})
	if err != nil {
		t.Fatalf("SteerTurn() error = %v", err)
	}
	if handle != started {
		t.Fatalf("SteerTurn() handle = %#v, want active handle %#v", handle, started)
	}
}

func TestForceCompleteTurnLeavesFinalStateToWatcher(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-2", "provider-2")
	session := &stubSession{
		threadID: "thread-2",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		forceComplete: func(context.Context, dto.ForceCompleteRequest) error {
			time.AfterFunc(20*time.Millisecond, func() {
				handle.complete(nil)
			})
			return nil
		},
	}

	svc := NewService(silentLogger())
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-2",
		ThreadID: "thread-2",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if err := svc.ForceCompleteTurn(context.Background(), session); err != nil {
		t.Fatalf("ForceCompleteTurn() error = %v", err)
	}
	if session.lastForceComplete.ThreadID != "thread-2" {
		t.Fatalf("ForceCompleteTurn thread id = %q, want thread-2", session.lastForceComplete.ThreadID)
	}
	if session.lastForceComplete.ProviderID != "provider-2" {
		t.Fatalf("ForceCompleteTurn provider id = %q, want provider-2", session.lastForceComplete.ProviderID)
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, err := svc.TrackTurn(context.Background(), "local-2")
		if err != nil {
			t.Fatalf("TrackTurn() error = %v", err)
		}
		if status.State == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status.State = %q, want completed", status.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type stubPromptAssemblyService struct {
	turn          contract.TurnAssembly
	lastTurnInput contract.TurnInput
}

type stubRuntimeMatcher struct {
	matches []skillpkg.RuntimeSkillMatch
	err     error
	calls   int
	last    skillpkg.RuntimeMatchParams
}

func (s *stubRuntimeMatcher) MatchRuntime(_ context.Context, p skillpkg.RuntimeMatchParams) ([]skillpkg.RuntimeSkillMatch, error) {
	s.calls++
	s.last = p
	if s.err != nil {
		return nil, s.err
	}
	out := make([]skillpkg.RuntimeSkillMatch, 0, len(s.matches))
	for _, match := range s.matches {
		copyMatch := match
		copyMatch.MatchedTerms = append([]string(nil), match.MatchedTerms...)
		out = append(out, copyMatch)
	}
	return out, nil
}

func (s *stubPromptAssemblyService) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (s *stubPromptAssemblyService) AssembleTurn(_ context.Context, input contract.TurnInput) (contract.TurnAssembly, error) {
	s.lastTurnInput = input
	return s.turn, nil
}

func (*stubPromptAssemblyService) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

type stubSession struct {
	threadID          string
	caps              dto.CapabilitySet
	runtimeConfig     map[string]any
	startTurn         func(context.Context, dto.TurnRequest) (contract.TurnHandle, error)
	steer             func(context.Context, dto.SteerRequest) error
	interrupt         func(context.Context, dto.InterruptRequest) error
	forceComplete     func(context.Context, dto.ForceCompleteRequest) error
	lastInterrupt     dto.InterruptRequest
	lastSteer         dto.SteerRequest
	lastForceComplete dto.ForceCompleteRequest
}

func (s *stubSession) ThreadID() string { return s.threadID }

func (s *stubSession) RolloutPath() string { return "" }

func (s *stubSession) Capabilities() dto.CapabilitySet { return s.caps }

func (s *stubSession) RuntimeConfigSnapshot() map[string]any { return s.runtimeConfig }

func (s *stubSession) StartTurn(ctx context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	if s.startTurn != nil {
		return s.startTurn(ctx, req)
	}
	return nil, errors.New("startTurn not configured")
}

func (s *stubSession) Interrupt(ctx context.Context, req dto.InterruptRequest) error {
	s.lastInterrupt = req
	if s.interrupt != nil {
		return s.interrupt(ctx, req)
	}
	return nil
}

func (s *stubSession) Steer(ctx context.Context, req dto.SteerRequest) error {
	s.lastSteer = req
	if s.steer != nil {
		return s.steer(ctx, req)
	}
	return nil
}

func (s *stubSession) ForceComplete(ctx context.Context, req dto.ForceCompleteRequest) error {
	s.lastForceComplete = req
	if s.forceComplete != nil {
		return s.forceComplete(ctx, req)
	}
	return nil
}

func (s *stubSession) ListThreads(context.Context) ([]dto.ThreadRef, error) { return nil, nil }

func (s *stubSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}

func (s *stubSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}

func (s *stubSession) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }

func (s *stubSession) Close(context.Context) error { return nil }

func (s *stubSession) ForceStop() error { return nil }

type stubTurnHandle struct {
	localID    string
	providerID string
	done       chan struct{}
	err        error
}

func newStubTurnHandle(localID, providerID string) *stubTurnHandle {
	return &stubTurnHandle{
		localID:    localID,
		providerID: providerID,
		done:       make(chan struct{}),
	}
}

func (h *stubTurnHandle) LocalID() string       { return h.localID }
func (h *stubTurnHandle) ProviderID() string    { return h.providerID }
func (h *stubTurnHandle) Done() <-chan struct{} { return h.done }
func (h *stubTurnHandle) Err() error            { return h.err }

func (h *stubTurnHandle) complete(err error) {
	h.err = err
	close(h.done)
}

func silentLogger() *pkglogger.Logger {
	return pkglogger.Get()
}

func commandForBinary(manifest dto.MCPManifest, name string) string {
	for _, binary := range manifest.Binaries {
		if binary.Name == name && len(binary.Command) > 0 {
			return binary.Command[0]
		}
	}
	return ""
}

func skillNames(refs []dto.SkillRef) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}

func repeatStrings(value string, count int) []string {
	if count <= 0 {
		return nil
	}
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, value)
	}
	return out
}

type staticSkillPortResolver struct{ port contract.SkillInjectionPort }

func (r staticSkillPortResolver) ResolveSkillInjectionPort(provider string) (contract.SkillInjectionPort, bool) {
	if strings.TrimSpace(strings.ToLower(provider)) != "codex" || r.port == nil {
		return nil, false
	}
	return r.port, true
}

type providerSkillPromptPort struct{}

func (providerSkillPromptPort) InjectL1Manifest(baseInstructions, manifest string) string {
	return baseInstructions + manifest
}

func (providerSkillPromptPort) BuildTurnSection(refs []dto.SkillRef) (string, bool) {
	if len(refs) == 0 {
		return "", false
	}
	lines := []string{"skills:"}
	blocks := make([]string, 0, len(refs))
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			continue
		}
		lines = append(lines, "- "+name)
		if block, ok := skillpkg.RenderSkillBlock(name, ref.Prompt, ref.Summary, string(ref.Mode)); ok {
			blocks = append(blocks, block)
		}
	}
	if len(lines) == 1 || len(blocks) == 0 {
		return "", false
	}
	return strings.Join([]string{strings.Join(lines, "\n"), strings.Join(blocks, "\n\n")}, "\n\n"), true
}

func (providerSkillPromptPort) ReservedTokens() int { return 0 }
