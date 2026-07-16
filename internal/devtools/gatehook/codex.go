package gatehook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const maxCodexHookInputBytes = 1024 * 1024

// CodexHookEvent 是 adapter 接受的 Stop 事件枚举。
type CodexHookEvent string

const (
	CodexHookEventStop         CodexHookEvent = "Stop"
	CodexHookEventSubagentStop CodexHookEvent = "SubagentStop"
)

type codexHookWire struct {
	SessionID            *string         `json:"session_id"`
	TranscriptPath       *string         `json:"transcript_path"`
	CWD                  *string         `json:"cwd"`
	HookEventName        *CodexHookEvent `json:"hook_event_name"`
	Model                string          `json:"model"`
	PermissionMode       *PermissionMode `json:"permission_mode"`
	TurnID               *string         `json:"turn_id"`
	AgentID              *string         `json:"agent_id"`
	AgentType            string          `json:"agent_type"`
	AgentTranscriptPath  *string         `json:"agent_transcript_path"`
	StopHookActive       *bool           `json:"stop_hook_active"`
	LastAssistantMessage *string         `json:"last_assistant_message"`
}

// CodexHookInput 是严格解析后供 identity 与 worktree 规范化使用的公共字段。
type CodexHookInput struct {
	SessionID      string         `json:"session_id"`
	TurnID         string         `json:"turn_id"`
	CWD            string         `json:"cwd"`
	HookEventName  CodexHookEvent `json:"hook_event_name"`
	PermissionMode PermissionMode `json:"permission_mode"`
	AgentID        string         `json:"agent_id,omitempty"`
	StopHookActive bool           `json:"stop_hook_active"`
}

// ParseCodexHook 严格解码一个官方 Stop 或 SubagentStop JSON 对象。
func ParseCodexHook(input io.Reader) (CodexHookInput, error) {
	payload, err := io.ReadAll(io.LimitReader(input, maxCodexHookInputBytes+1))
	if err != nil {
		return CodexHookInput{}, fmt.Errorf("read Codex hook JSON: %w", err)
	}
	if len(payload) > maxCodexHookInputBytes {
		return CodexHookInput{}, fmt.Errorf("Codex hook JSON exceeds %d bytes", maxCodexHookInputBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var wire codexHookWire
	if err := decoder.Decode(&wire); err != nil {
		return CodexHookInput{}, fmt.Errorf("decode Codex hook JSON: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return CodexHookInput{}, errors.New("Codex hook stdin must contain exactly one JSON object")
		}
		return CodexHookInput{}, fmt.Errorf("decode trailing Codex hook JSON: %w", err)
	}
	normalized, err := normalizeCodexWire(wire)
	if err != nil {
		return CodexHookInput{}, err
	}
	return normalized, nil
}

// NormalizeCodexHook 将官方 JSON 固定为 submit 或父 invocation status 请求。
func NormalizeCodexHook(ctx context.Context, input io.Reader) (Request, error) {
	hook, err := ParseCodexHook(input)
	if err != nil {
		return Request{}, err
	}
	repository, source, err := CurrentWorktreeSource(ctx, hook.CWD)
	if err != nil {
		return Request{}, err
	}
	invocation, err := codexInvocationIdentity(hook)
	if err != nil {
		return Request{}, err
	}
	if hook.StopHookActive {
		status := StatusRequest{
			Repository:            repository,
			Invocation:            invocation,
			ExpectedSourceTreeSHA: source.SourceTreeSHA,
			ParentInvocationOnly:  true,
		}
		request := Request{Kind: RequestKindStatus, Status: &status}
		return request, request.Validate()
	}
	entrypoint := gatecontract.CIEntrypointCodexStop
	if hook.HookEventName == CodexHookEventSubagentStop {
		entrypoint = gatecontract.CIEntrypointCodexSubagentStop
	}
	submit := SubmitRequest{
		Entrypoint:     entrypoint,
		Profile:        gatecontract.ProfileLocalFast,
		Repository:     repository,
		Invocation:     invocation,
		Source:         source,
		PermissionMode: hook.PermissionMode,
	}
	request := Request{Kind: RequestKindSubmit, Submit: &submit}
	return request, request.Validate()
}

// normalizeCodexWire 校验公共字段后按事件收敛 agent identity。
func normalizeCodexWire(wire codexHookWire) (CodexHookInput, error) {
	if err := validateCodexCommonFields(wire); err != nil {
		return CodexHookInput{}, err
	}
	normalized := CodexHookInput{
		SessionID:      *wire.SessionID,
		TurnID:         *wire.TurnID,
		CWD:            filepath.Clean(*wire.CWD),
		HookEventName:  *wire.HookEventName,
		PermissionMode: *wire.PermissionMode,
		StopHookActive: *wire.StopHookActive,
	}
	return normalizeCodexAgent(wire, normalized)
}

// validateCodexCommonFields 校验身份、cwd、事件、权限与递归标记的存在性。
func validateCodexCommonFields(wire codexHookWire) error {
	if err := requiredCodexString("session_id", wire.SessionID); err != nil {
		return err
	}
	if err := requiredCodexString("turn_id", wire.TurnID); err != nil {
		return err
	}
	if err := requiredCodexString("cwd", wire.CWD); err != nil {
		return err
	}
	if !filepath.IsAbs(*wire.CWD) {
		return errors.New("Codex cwd must be an absolute path")
	}
	if wire.HookEventName == nil {
		return errors.New("Codex hook_event_name is required")
	}
	if wire.PermissionMode == nil {
		return errors.New("Codex permission_mode is required")
	}
	if err := wire.PermissionMode.Validate(); err != nil {
		return err
	}
	if wire.StopHookActive == nil {
		return errors.New("Codex stop_hook_active is required")
	}
	return nil
}

func requiredCodexString(name string, value *string) error {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fmt.Errorf("Codex %s is required", name)
	}
	return nil
}

// normalizeCodexAgent 仅从公共 agent_id 建立 SubagentStop identity。
func normalizeCodexAgent(wire codexHookWire, normalized CodexHookInput) (CodexHookInput, error) {
	switch normalized.HookEventName {
	case CodexHookEventStop:
		if wire.AgentID != nil {
			return CodexHookInput{}, errors.New("Stop must not include agent_id")
		}
	case CodexHookEventSubagentStop:
		if wire.AgentID == nil || strings.TrimSpace(*wire.AgentID) == "" {
			return CodexHookInput{}, errors.New("SubagentStop agent_id is required")
		}
		normalized.AgentID = *wire.AgentID
	default:
		return CodexHookInput{}, fmt.Errorf("unsupported Codex hook event %q", normalized.HookEventName)
	}
	return normalized, nil
}

func codexInvocationIdentity(hook CodexHookInput) (InvocationIdentity, error) {
	owner := sha256Identity("gatehook/codex-owner/v1", hook.SessionID, hook.AgentID)
	key := sha256Identity(
		"gatehook/codex-invocation/v1",
		string(hook.HookEventName),
		hook.SessionID,
		hook.TurnID,
		hook.AgentID,
	)
	identity := InvocationIdentity{Owner: owner, Key: key}
	return identity, identity.Validate()
}
