// Package directpeercandidate defines the unregistered direct-peer to command
// owner seam. It carries no transport, provider, Fx, or dispatcher behavior.
package directpeercandidate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	command "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

var (
	ErrInvalidAdapter     = errors.New("direct-peer command adapter is invalid")
	ErrCanonicalIdentity  = errors.New("direct-peer message has no canonical command identity")
	ErrReplayUnresolved   = errors.New("direct-peer accepted replay is unresolved")
	ErrProviderOutcome    = errors.New("direct-peer provider outcome is unknown")
	ErrMessageIDAsReceipt = errors.New("direct-peer message id cannot be a provider receipt")
)

// CanonicalIdentity is the stable command/submission lookup identity. It is
// deliberately separate from direct-peer message_id and provider receipts.
type CanonicalIdentity struct {
	CommandID    string
	SubmissionID string
}

// IdentityResolver obtains the canonical identity from an owner-issued
// mapping. The adapter never derives command identity from message_id.
type IdentityResolver interface {
	Resolve(context.Context, directpeer.Message) (CanonicalIdentity, error)
}

// SubmissionQuerier is the read-only canonical command owner boundary.
type SubmissionQuerier interface {
	Query(context.Context, command.SubmissionKey) (command.Submission, error)
}

// Replay is a non-authorizing projection of an accepted direct-peer message
// and its canonical command submission record.
type Replay struct {
	MessageID  string
	Identity   CanonicalIdentity
	Submission command.Submission
}

// Adapter only resolves/querys an existing command. It cannot dispatch or
// mutate provider state and is intentionally not registered in production.
type Adapter struct {
	resolver IdentityResolver
	query    SubmissionQuerier
}

// New 创建带显式 owner 依赖的 direct-peer 候选适配器。
func New(resolver IdentityResolver, query SubmissionQuerier) (*Adapter, error) {
	if isNil(resolver) || isNil(query) {
		return nil, fmt.Errorf("%w: resolver and query are required", ErrInvalidAdapter)
	}
	return &Adapter{resolver: resolver, query: query}, nil
}

// ResolveAcceptedReplay 按原始 canonical command identity 查询 accepted 消息；缺失身份时拒绝猜测。
func (a *Adapter) ResolveAcceptedReplay(ctx context.Context, message directpeer.Message) (Replay, error) {
	if err := a.validateReplay(ctx, message); err != nil {
		return Replay{}, err
	}
	identity, err := a.resolver.Resolve(ctx, message)
	if err != nil {
		return Replay{}, errors.Join(ErrCanonicalIdentity, err)
	}
	if err := identity.Validate(); err != nil {
		return Replay{}, err
	}
	if identity.SubmissionID == message.MessageID {
		return Replay{}, ErrMessageIDAsReceipt
	}
	record, err := a.query.Query(ctx, command.SubmissionKey{CommandID: identity.CommandID, SubmissionID: identity.SubmissionID})
	if err != nil {
		return Replay{}, err
	}
	if record.Request.CommandID != identity.CommandID || record.Request.SubmissionID != identity.SubmissionID {
		return Replay{}, fmt.Errorf("%w: queried submission identity changed", ErrCanonicalIdentity)
	}
	return Replay{MessageID: message.MessageID, Identity: identity, Submission: record}, nil
}

// ObserveErrorOnlyProviderError 只读取原始 submission；error-only 结果不能转换为 definitive failure。
func (a *Adapter) ObserveErrorOnlyProviderError(ctx context.Context, key command.SubmissionKey, providerErr error) (command.Submission, error) {
	if a == nil || isNil(a.query) {
		return command.Submission{}, ErrInvalidAdapter
	}
	if ctx == nil || providerErr == nil {
		return command.Submission{}, ErrProviderOutcome
	}
	record, err := a.query.Query(ctx, key)
	if err != nil {
		return command.Submission{}, err
	}
	if record.Status == command.SubmissionProviderFailed {
		return command.Submission{}, fmt.Errorf("%w: error-only provider cannot assert definitive failure", ErrProviderOutcome)
	}
	return record, errors.Join(ErrProviderOutcome, command.ErrSubmissionUnresolved, providerErr)
}

// ValidateProviderReceipt 拒绝把 direct-peer message identity 重用为 provider receipt。
func ValidateProviderReceipt(message directpeer.Message, receiptID string) error {
	if strings.TrimSpace(message.MessageID) == "" || strings.TrimSpace(receiptID) == "" {
		return ErrMessageIDAsReceipt
	}
	if message.MessageID == receiptID {
		return ErrMessageIDAsReceipt
	}
	return nil
}

// validateReplay 检查适配器依赖和消息状态，拒绝无 canonical identity 的 accepted 重放。
func (a *Adapter) validateReplay(ctx context.Context, message directpeer.Message) error {
	if a == nil || isNil(a.resolver) || isNil(a.query) || ctx == nil {
		return ErrInvalidAdapter
	}
	if strings.TrimSpace(message.MessageID) == "" || message.Status != "accepted" {
		return fmt.Errorf("%w: message must be accepted with a stable message id", ErrReplayUnresolved)
	}
	if strings.TrimSpace(message.SubmissionID) != "" {
		return fmt.Errorf("%w: direct-peer submission id is not canonical provider receipt", ErrMessageIDAsReceipt)
	}
	return nil
}

// Validate 验证 canonical command 与 submission identity 的有界稳定格式。
func (i CanonicalIdentity) Validate() error {
	if !bounded(i.CommandID, 128) || !bounded(i.SubmissionID, 128) {
		return fmt.Errorf("%w: canonical command and submission ids are required", ErrCanonicalIdentity)
	}
	return nil
}

func bounded(value string, limit int) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
