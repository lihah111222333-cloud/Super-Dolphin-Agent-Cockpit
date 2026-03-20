package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

type session struct {
	agentID    string
	threadID   string
	transport  *transport
	caps       dto.CapabilitySet
	recovery   *recoveryManager
	history    *rolloutReader
	logger     *slog.Logger
	dispatcher *unified.EventDispatcher
	approvals  *rpc.ApprovalManager
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	turns      map[string]*turnHandle
}

var _ contract.Session = (*session)(nil)

type turnHandle struct {
	localID    string
	providerID string
	done       chan struct{}
	mu         sync.RWMutex
	err        error
	once       sync.Once
}

type turnStartParams struct {
	ThreadID             string          `json:"threadId"`
	Input                []turnInputItem `json:"input"`
	SelectedSkills       []string        `json:"selectedSkills,omitempty"`
	ManualSkillSelection bool            `json:"manualSkillSelection,omitempty"`
	Model                string          `json:"model,omitempty"`
	Effort               string          `json:"effort,omitempty"`
	OutputSchema         json.RawMessage `json:"outputSchema,omitempty"`
}

type turnInputItem struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

type turnRPCResult struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

func newSession(
	logger *slog.Logger,
	serverURL string,
	agentID string,
	dispatcher *unified.EventDispatcher,
	approvals *rpc.ApprovalManager,
) (*session, error) {
	transport, err := newTransport(serverURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		agentID:    strings.TrimSpace(agentID),
		transport:  transport,
		caps:       cloneCaps(codexCapabilities),
		recovery:   &recoveryManager{transport: transport, logger: logger, maxRetry: 3},
		history:    &rolloutReader{transport: transport},
		logger:     logger,
		dispatcher: dispatcher,
		approvals:  approvals,
		ctx:        ctx,
		cancel:     cancel,
		turns:      map[string]*turnHandle{},
	}
	go transport.ReadLoop(ctx, s.onNotification)
	return s, nil
}

func (s *session) ThreadID() string { return s.threadID }

func (s *session) Capabilities() dto.CapabilitySet { return cloneCaps(s.caps) }

func (s *session) StartTurn(ctx context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	threadID := strings.TrimSpace(firstNonEmpty(req.ThreadID, s.threadID))
	if threadID == "" {
		return nil, errors.New("codexapp: thread id is required")
	}
	callCtx, cancel := withTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := s.transport.Call(callCtx, "turn/start", buildTurnStartParams(threadID, req))
	if err != nil {
		return nil, err
	}
	var resp turnRPCResult
	if err := json.Unmarshal(raw, &resp); err != nil || strings.TrimSpace(resp.Turn.ID) == "" {
		return nil, errors.New("codexapp: invalid turn/start response")
	}
	providerID := strings.TrimSpace(resp.Turn.ID)
	h := newTurnHandle(resolveLocalTurnID(req.LocalID, providerID), providerID)
	s.mu.Lock()
	s.turns[providerID] = h
	s.mu.Unlock()
	return h, nil
}

func (s *session) Interrupt(ctx context.Context, req dto.InterruptRequest) error {
	threadID := strings.TrimSpace(firstNonEmpty(req.ThreadID, s.threadID))
	if threadID == "" {
		return errors.New("codexapp: thread id is required")
	}
	params := map[string]any{"threadId": threadID}
	if source := strings.TrimSpace(req.Source); source != "" {
		params["source"] = source
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.transport.Call(callCtx, "turn/interrupt", params)
	return err
}

func (s *session) ListThreads(ctx context.Context) ([]dto.ThreadRef, error) {
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := s.transport.Call(callCtx, "thread/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var listed struct {
		Data []dto.ThreadRef `json:"data"`
	}
	if json.Unmarshal(raw, &listed) == nil && len(listed.Data) > 0 {
		return listed.Data, nil
	}
	var loaded struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return nil, err
	}
	threads := make([]dto.ThreadRef, 0, len(loaded.Data))
	for _, id := range loaded.Data {
		threads = append(threads, dto.ThreadRef{ID: strings.TrimSpace(id)})
	}
	return threads, nil
}

func (s *session) ForkThread(ctx context.Context, req dto.ForkRequest) (dto.ForkResult, error) {
	threadID := strings.TrimSpace(firstNonEmpty(req.ThreadID, s.threadID))
	if threadID == "" {
		return dto.ForkResult{}, errors.New("codexapp: thread id is required")
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := s.transport.Call(callCtx, "thread/fork", map[string]any{"threadId": threadID})
	if err != nil {
		return dto.ForkResult{}, err
	}
	id, err := decodeThreadID(raw, "")
	if err != nil {
		return dto.ForkResult{}, err
	}
	return dto.ForkResult{NewThreadID: id}, nil
}

func (s *session) Configure(ctx context.Context, patch dto.ThreadConfigPatch) error {
	threadID := strings.TrimSpace(s.threadID)
	if threadID == "" {
		return errors.New("codexapp: thread id is required")
	}
	if patch.Model != nil {
		callCtx, cancel := withTimeout(ctx, 10*time.Second)
		defer cancel()
		_, err := s.transport.Call(callCtx, "thread/config/set", map[string]any{"threadId": threadID, "model": strings.TrimSpace(*patch.Model)})
		if err != nil {
			return err
		}
	}
	if err := s.applySlashConfig(ctx, threadID, "thread/personality/set", "personality", patch.Personality); err != nil {
		return err
	}
	return s.applySlashConfig(ctx, threadID, "thread/approvals/set", "policy", patch.Approvals)
}

func (s *session) Close(context.Context) error {
	s.failTurns(errors.New("codexapp: session closed"))
	s.cancel()
	return s.transport.Close()
}

func (s *session) ForceStop() error {
	s.failTurns(errors.New("codexapp: session stopped"))
	s.cancel()
	return s.transport.Kill()
}

func (s *session) applySlashConfig(ctx context.Context, threadID, method, key string, value *string) error {
	if value == nil {
		return nil
	}
	arg := strings.TrimSpace(*value)
	if arg == "" {
		return nil
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.transport.Call(callCtx, method, map[string]any{"threadId": threadID, key: arg, "args": arg})
	return err
}

func (s *session) onNotification(method string, params json.RawMessage) {
	s.dispatch(dto.RawProviderEvent{Type: method, Data: params})
	switch strings.TrimSpace(method) {
	case "item/commandExecution/requestApproval", "tool.approval.requested":
		s.handleApprovalRequest(method, params)
	case "turn/completed", "turn/aborted":
		s.finishTurn(params, method == "turn/completed")
	case "connection.dead":
		s.failTurns(errors.New("codexapp: connection lost"))
	}
}

func (s *session) dispatch(raw dto.RawProviderEvent) {
	if s.dispatcher != nil {
		s.dispatcher.Dispatch(raw)
	}
}

func (s *session) finishTurn(params json.RawMessage, optimistic bool) {
	payload := decodeEventPayload(params)
	turnID := strings.TrimSpace(stringValue(payload, "turnId", "turn_id"))
	if turnID == "" {
		return
	}
	h := s.takeTurn(turnID)
	if h == nil {
		return
	}
	errText := strings.TrimSpace(firstNonEmpty(
		stringValue(payload, "error", "message", "reason"),
		stringValue(nestedValue(payload, "error"), "message"),
	))
	if errText == "" && optimistic {
		h.complete(nil)
		return
	}
	if errText == "" {
		errText = "turn failed"
	}
	h.complete(errors.New(errText))
}

func (s *session) takeTurn(turnID string) *turnHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.turns[turnID]
	delete(s.turns, turnID)
	return h
}

func (s *session) failTurns(err error) {
	s.mu.Lock()
	turns := s.turns
	s.turns = map[string]*turnHandle{}
	s.mu.Unlock()
	for _, h := range turns {
		h.complete(err)
	}
}

func newTurnHandle(localID, providerID string) *turnHandle {
	return &turnHandle{
		localID:    strings.TrimSpace(localID),
		providerID: strings.TrimSpace(providerID),
		done:       make(chan struct{}),
	}
}

func (h *turnHandle) LocalID() string       { return h.localID }
func (h *turnHandle) ProviderID() string    { return h.providerID }
func (h *turnHandle) Done() <-chan struct{} { return h.done }

func (h *turnHandle) Err() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

func (h *turnHandle) complete(err error) {
	h.once.Do(func() {
		h.mu.Lock()
		h.err = err
		h.mu.Unlock()
		close(h.done)
	})
}

func buildTurnStartParams(threadID string, req dto.TurnRequest) turnStartParams {
	selectedSkills := make([]string, 0, len(req.Skills))
	for _, skill := range req.Skills {
		if name := strings.TrimSpace(skill.Name); name != "" {
			selectedSkills = append(selectedSkills, name)
		}
	}
	inputs := make([]turnInputItem, 0, len(req.Inputs))
	if skillPrompt, ok := buildSkillPromptInput(req.Skills); ok {
		inputs = append(inputs, skillPrompt)
	}
	for _, item := range req.Inputs {
		inputs = append(inputs, mapTurnInput(item))
	}
	return turnStartParams{
		ThreadID:             threadID,
		Input:                inputs,
		SelectedSkills:       selectedSkills,
		ManualSkillSelection: len(selectedSkills) > 0,
		Model:                strings.TrimSpace(req.Overrides.Model),
		Effort:               strings.TrimSpace(req.Overrides.Effort),
		OutputSchema:         req.OutputSchema,
	}
}

func cloneCaps(src dto.CapabilitySet) dto.CapabilitySet {
	out := make(dto.CapabilitySet, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
