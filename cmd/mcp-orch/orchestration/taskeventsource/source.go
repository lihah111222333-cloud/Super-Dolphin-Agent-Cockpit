// Package taskeventsource 把 active identity binding 固定成不可自动跟随 recover 的 task 事件源。
package taskeventsource

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
)

const (
	maxIdentityComponent = 128
	maxFenceReference    = 512
)

// ErrInvalidSource 标识无法安全固定到单一 active binding 的 task event source。
var ErrInvalidSource = errors.New("task event source is invalid")

// Source 是从单个 active binding 固定出的 source/epoch 与完整 binding fence。
type Source struct {
	SourceID       string `json:"source_id"`
	SourceEpoch    string `json:"source_epoch"`
	AgentID        string `json:"agent_id"`
	IncarnationID  string `json:"incarnation_id"`
	BindingVersion int64  `json:"binding_version"`
	BindingDigest  string `json:"binding_digest"`
	SourceFenceRef string `json:"source_fence_ref"`
}

// FromCurrentBinding 只接受 schema v1 的 active 完整 binding，并固定其 incarnation epoch。
func FromCurrentBinding(binding identity.CurrentBinding) (Source, error) {
	if binding.State != "active" {
		return Source{}, invalidSource("binding state must be active")
	}
	if binding.SchemaVersion != 1 {
		return Source{}, invalidSource("binding schema_version must be 1")
	}
	if err := validatePathComponent("agent_id", binding.AgentID); err != nil {
		return Source{}, err
	}
	if err := validatePathComponent("incarnation_id", binding.IncarnationID); err != nil {
		return Source{}, err
	}
	if binding.BindingVersion <= 0 {
		return Source{}, invalidSource("binding_version must be positive")
	}
	if err := validateDigest(binding.BindingDigest); err != nil {
		return Source{}, err
	}
	if err := validateText("source_fence_ref", binding.SourceFenceRef, maxFenceReference); err != nil {
		return Source{}, err
	}
	source := Source{
		SourceID:       "task/" + binding.AgentID,
		SourceEpoch:    binding.IncarnationID,
		AgentID:        binding.AgentID,
		IncarnationID:  binding.IncarnationID,
		BindingVersion: binding.BindingVersion,
		BindingDigest:  binding.BindingDigest,
		SourceFenceRef: binding.SourceFenceRef,
	}
	if err := source.validate(); err != nil {
		return Source{}, err
	}
	return source, nil
}

// Selector 固定一个 accesswait selector；event types 保持调用方给定的规范顺序且不共享 backing array。
func (s Source) Selector(lane eventstore.Lane, targetAgentID string, eventTypes []string) (accesswait.EventSelector, error) {
	if err := s.validate(); err != nil {
		return accesswait.EventSelector{}, err
	}
	if _, err := eventstore.CanonicalCursor(s.SourceID, s.SourceEpoch, lane, 1); err != nil {
		return accesswait.EventSelector{}, invalidSourceError("selector source tuple", err)
	}
	selector := accesswait.EventSelector{
		Version:       1,
		SourceID:      s.SourceID,
		SourceEpoch:   s.SourceEpoch,
		Lane:          lane,
		TargetAgentID: targetAgentID,
		EventTypes:    append([]string(nil), eventTypes...),
	}
	if err := selector.Validate(); err != nil {
		return accesswait.EventSelector{}, invalidSourceError("selector", err)
	}
	return selector, nil
}

// Cursor 返回固定在本 Source incarnation 上的 canonical cursor。
func (s Source) Cursor(lane eventstore.Lane, sequence int64) (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	cursor, err := eventstore.CanonicalCursor(s.SourceID, s.SourceEpoch, lane, sequence)
	if err != nil {
		return "", invalidSourceError("cursor", err)
	}
	return cursor, nil
}

// validate 重验可由调用方直接构造的 Source，阻止伪造 derived tuple 或残缺 fence。
func (s Source) validate() error {
	if err := validatePathComponent("agent_id", s.AgentID); err != nil {
		return err
	}
	if err := validatePathComponent("incarnation_id", s.IncarnationID); err != nil {
		return err
	}
	if s.SourceID != "task/"+s.AgentID || s.SourceEpoch != s.IncarnationID {
		return invalidSource("source tuple does not match task binding")
	}
	if s.BindingVersion <= 0 {
		return invalidSource("binding_version must be positive")
	}
	if err := validateDigest(s.BindingDigest); err != nil {
		return err
	}
	if err := validateText("source_fence_ref", s.SourceFenceRef, maxFenceReference); err != nil {
		return err
	}
	return nil
}

func validatePathComponent(name, value string) error {
	if err := validateText(name, value, maxIdentityComponent); err != nil {
		return err
	}
	if strings.ContainsAny(value, "/\\") {
		return invalidSource(name + " must be a safe cursor component")
	}
	return nil
}

// validateText 拒绝空白变形、非法 UTF-8、控制字符与超长 fence 文本。
func validateText(name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return invalidSource(fmt.Sprintf("%s must be bounded canonical text", name))
	}
	return nil
}

func validateDigest(value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return invalidSource("binding_digest must be lowercase 256-bit hex")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return invalidSource("binding_digest must be hexadecimal")
	}
	return nil
}

func invalidSource(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSource, message)
}

func invalidSourceError(scope string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrInvalidSource, scope, err)
}
