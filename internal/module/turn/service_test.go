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
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/stretchr/testify/require"
)

func TestPrepareTurnKeepsSkillRefsMetadataOnlyAndNormalizesInputs(t *testing.T) {
	t.Parallel()

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt: "Please use @debug and [skill:deploy-tool] on this issue.",
		Images: []string{"https://example.com/screen.png", "https://example.com/screen.png"},
		Files:  []string{"./README.md", "./README.md", "./malware.exe"},
		Skills: []dto.SkillRef{{Name: "explicit", Prompt: "explicit guidance", Summary: "explicit summary"}},
		CandidateSkills: []dto.SkillRef{
			{Name: "debug", Prompt: "debug guidance", Summary: "debug summary"},
			{Name: "deploy-tool", Prompt: "deploy guidance", Summary: "deploy summary"},
		},
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	require.Len(t, req.Inputs, 3)
	require.Equal(t, "text", req.Inputs[0].Type)
	require.Equal(t, "Please use @debug and [skill:deploy-tool] on this issue.", req.Inputs[0].Content)
	require.Equal(t, "image", req.Inputs[1].Type)
	require.Equal(t, "https://example.com/screen.png", req.Inputs[1].URL)
	require.Equal(t, "mention", req.Inputs[2].Type)
	require.Equal(t, "./README.md", req.Inputs[2].Path)

	gotNames := skillNames(req.Skills)
	require.Equal(t, []string{"explicit", "debug", "deploy-tool"}, gotNames)
	for _, ref := range req.Skills {
		require.Equal(t, "", ref.Prompt)
	}
	require.Equal(t, "explicit summary", req.Skills[0].Summary)
	require.Equal(t, "debug summary", req.Skills[1].Summary)
	require.Equal(t, "deploy summary", req.Skills[2].Summary)
}

func TestPrepareTurnManualSkillSelectionDisablesAutoMatch(t *testing.T) {
	t.Parallel()

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
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

func TestPrepareTurnProviderNativeSkillsDisabledForcesManualSkillMode(t *testing.T) {
	t.Parallel()

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt:          "Please use @debug on this issue.",
		CandidateSkills: []dto.SkillRef{{Name: "debug", Prompt: "debug guidance"}},
		ThreadRuntimeConfig: map[string]any{
			"providerNativeSkills": false,
		},
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if req.ManualSkillSelection != true {
		t.Fatal("ManualSkillSelection = false, want provider-native skill isolation to force manual mode")
	}
	if len(req.Skills) != 0 {
		t.Fatalf("Skills = %#v, want no auto-matched skills when provider-native skills are disabled", req.Skills)
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

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
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

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
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

func TestPrepareTurnPrefersPeerBinDirEnvForManifest(t *testing.T) {
	peerDir := t.TempDir()
	writeTurnDummyBinary(t, peerDir, "mcp-lsp")
	t.Setenv("GO_AGENT_PEER_BIN_DIR", peerDir)

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	want := filepath.Join(peerDir, "mcp-lsp")
	if got := commandForBinary(req.MCP, "lsp"); got != want {
		t.Fatalf("lsp command = %q, want %q", got, want)
	}
}

func TestPrepareTurnPrefersExplicitBinaryDir(t *testing.T) {
	t.Parallel()

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
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
		Prompt:               "please verify the cache",
		CWD:                  "/repo",
		Model:                "claude-sonnet",
		ManualSkillSelection: true,
		Skills:               []dto.SkillRef{{Name: "debug", Prompt: "legacy skill body"}},
		RuntimeUserContext: map[string]string{
			"workerToolsContext": "Workers can use bash and read tools.",
			"terminalFocus":      "The terminal is unfocused — the user is not actively watching.",
		},
		ThreadRuntimeConfig: map[string]any{
			"promptKey":                    "main/launch-fav",
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
	require.NoError(t, err)
	assertPrepareTurnAssemblyInput(t, req, assembly)
}

func TestPrepareTurnRejectsMissingPromptAssembly(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	_, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt: "please verify the cache",
		CWD:    "/repo",
	})
	if err == nil || !strings.Contains(err.Error(), "prompt assembly") {
		t.Fatalf("PrepareTurn() error = %v, want prompt assembly dependency error", err)
	}
}

func assertPrepareTurnAssemblyInput(t *testing.T, req dto.TurnRequest, assembly *stubPromptAssemblyService) {
	t.Helper()
	require.Equal(t, "assembled user context", req.TurnAssembly.UserContextText)
	require.Len(t, req.Skills, 1)
	require.Equal(t, "", req.Skills[0].Prompt)
	require.Equal(t, "/repo", req.CWD)
	require.Equal(t, []string{"/repo/thread-extra"}, req.AdditionalWorkingDirectories)
	require.Equal(t, "thread-1", assembly.lastTurnInput.ThreadID)
	require.Equal(t, "please verify the cache", assembly.lastTurnInput.UserText)
	require.Equal(t, "main/launch-fav", assembly.lastTurnInput.PromptKey)
	require.Equal(t, "/repo", assembly.lastTurnInput.CWD)
	require.Equal(t, "claude-sonnet", assembly.lastTurnInput.Model)
	require.Equal(t, "codex-thread", assembly.lastTurnInput.Provider)
	require.Equal(t, "/thread-repo", assembly.lastTurnInput.GitRoot)
	require.True(t, assembly.lastTurnInput.IsWorktree)
	require.Equal(t, "Japanese", assembly.lastTurnInput.Language)
	require.Equal(t, []string{"lsp_file", "lsp_grep"}, assembly.lastTurnInput.EnabledTools)
	require.Equal(t, []string{"/repo/thread-extra"}, assembly.lastTurnInput.AdditionalWorkingDirectories)
	require.NotEmpty(t, assembly.lastTurnInput.MCPSnapshot.Servers)
	require.True(t, slices.Contains(assembly.lastTurnInput.MCPSnapshot.Tools, "mcp__lsp__lsp_grep"))
	require.Equal(t, "Use LSP thread fallback.", assembly.lastTurnInput.MCPSnapshot.Instructions["lsp"])
	require.True(t, assembly.lastTurnInput.SessionFlags["verification_required"])
	require.False(t, assembly.lastTurnInput.SessionFlags["runtime_only"])
	require.Equal(t, "Workers can use bash and read tools.", assembly.lastTurnInput.RuntimeUserContext["workerToolsContext"])
	require.NotEmpty(t, assembly.lastTurnInput.RuntimeUserContext["terminalFocus"])
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

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
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

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
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

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
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
	contract.Session
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

func writeTurnDummyBinary(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func skillNames(refs []dto.SkillRef) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}

// TestServiceLookupByDedupeKeyRoundTrip 覆盖 PrepareTurn -> StartTurn -> LookupByDedupeKey 闭环。
// PrepareInput 中的 dedupe key 必须传到 TurnRequest，并登记到 live turn 去重索引供后续查询命中。
func TestServiceLookupByDedupeKeyRoundTrip(t *testing.T) {
	t.Parallel()

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
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

// TestServiceLookupByDedupeKeyEmpty 验证空 key 会在去重查询入口短路。
// 返回 ok=false 且不报错，调用方可把它当成“没有可复用 turn”处理。
func TestServiceLookupByDedupeKeyEmpty(t *testing.T) {
	t.Parallel()

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
	_, ok, err := svc.LookupByDedupeKey(context.Background(), "  ")
	if err != nil {
		t.Fatalf("LookupByDedupeKey err = %v", err)
	}
	if ok {
		t.Fatal("empty dedupe key must miss")
	}
}
