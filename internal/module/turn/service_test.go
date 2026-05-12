package turn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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

func TestActiveProviderIDPrefersLiveHandle(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-1", "provider-new")
	if got := activeProviderID(activeTurn{providerID: "provider-old", handle: handle}); got != "provider-new" {
		t.Fatalf("activeProviderID() = %q, want provider-new", got)
	}
	if got := activeProviderID(activeTurn{providerID: "provider-old"}); got != "provider-old" {
		t.Fatalf("activeProviderID() fallback = %q, want provider-old", got)
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
	if session.lastInterrupt.TurnID != "provider-1" {
		t.Fatalf("InterruptTurn turn id = %q, want provider-1", session.lastInterrupt.TurnID)
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

func (s *stubPromptAssemblyService) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (s *stubPromptAssemblyService) AssembleTurn(_ context.Context, input contract.TurnInput) (contract.TurnAssembly, error) {
	s.lastTurnInput = input
	return s.turn, nil
}

func (s *stubPromptAssemblyService) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
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

// TestServiceLookupByDedupeKeyRoundTrip exercises the
// PrepareTurn -> StartTurn -> LookupByDedupeKey path end-to-end. The
// dedupe key set on PrepareInput must flow through to TurnRequest
// and end up registered on the tracker so a subsequent LookupByDedupeKey
// hits the live turn.
func TestServiceLookupByDedupeKeyRoundTrip(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger())
	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(_ context.Context, _ dto.TurnRequest) (contract.TurnHandle, error) {
			return &stubTurnHandle{localID: "turn-1", providerID: "p-1", done: make(chan struct{})}, nil
		},
	}

	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt:    "dedupe me",
		DedupeKey: " dedupe-round-trip ",
	})
	if err != nil {
		t.Fatalf("PrepareTurn err = %v", err)
	}
	if req.DedupeKey != "dedupe-round-trip" {
		t.Fatalf("PrepareTurn should forward trimmed DedupeKey, got %q", req.DedupeKey)
	}

	if _, err := svc.StartTurn(context.Background(), session, req); err != nil {
		t.Fatalf("StartTurn err = %v", err)
	}

	status, ok, err := svc.LookupByDedupeKey(context.Background(), "dedupe-round-trip")
	if err != nil {
		t.Fatalf("LookupByDedupeKey err = %v", err)
	}
	if !ok {
		t.Fatal("LookupByDedupeKey ok = false, want true")
	}
	if status.LocalID == "" {
		t.Fatalf("status missing LocalID: %+v", status)
	}
}

// TestServiceLookupByDedupeKeyEmpty verifies an empty key short-circuits
// through the tracker and returns ok=false without error.
func TestServiceLookupByDedupeKeyEmpty(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger())
	_, ok, err := svc.LookupByDedupeKey(context.Background(), "  ")
	if err != nil {
		t.Fatalf("LookupByDedupeKey err = %v", err)
	}
	if ok {
		t.Fatal("empty dedupe key must miss")
	}
}
