package tasktransport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// This file is a transport-neutral, candidate-only validator. It does not
// parse credentials, resolve a durable room/session, read an event owner, or
// register an HTTP/stdio handler. Callers must supply already authenticated
// server snapshots and keep the returned decision non-authoritative.

const (
	StreamMethodPost   = "POST"
	StreamMethodGet    = "GET"
	StreamMethodDelete = "DELETE"

	StreamModeListen    = "listen"
	StreamModeResume    = "resume"
	maxStreamIDLength   = 256
	maxEventIDLength    = 512
	minStreamWaitWindow = 10 * time.Millisecond
	maxStreamWaitWindow = 60 * time.Second
)

var (
	ErrStreamRequestInvalid  = errors.New("task stream request is invalid")
	ErrStreamCarrierMismatch = errors.New("task stream method carrier is invalid")
	ErrStreamResumeMismatch  = errors.New("task stream resume binding does not match")
	ErrStreamScopeMismatch   = errors.New("task stream scope does not match")
	ErrStreamUnauthorized    = errors.New("task stream caller is unauthorized")
	ErrStreamDeadline        = errors.New("task stream deadline is invalid")
	ErrStreamCancelled       = errors.New("task stream request is cancelled")
)

// StreamScope is server-owned task/room scope. Room fields are optional as a
// pair; when present they must be exact values from the authenticated owner.
type StreamScope struct {
	TaskID       string
	TaskScopeRef string
	RoomID       string
	RoomScopeRef string
}

// StreamBinding is the immutable session/stream and authenticated identity
// snapshot supplied by a future transport owner.
type StreamBinding struct {
	EndpointRef string
	Profile     string
	BootEpoch   string
	SessionID   string
	StreamID    string
	Caller      CallerBinding
	Scope       StreamScope
}

// StreamResumeRequest contains untrusted method carriers and projected
// caller/scope claims. It is a structural request, never an authorization.
type StreamResumeRequest struct {
	Method       string
	Mode         string
	SessionID    string
	StreamID     string
	LastEventID  string
	Cursor       *CursorClaim
	Caller       CallerBinding
	Scope        StreamScope
	DeadlineAt   time.Time
	CancelSignal bool
}

// ValidateAt 在单次安全时间观察下校验请求与服务端绑定，整个过程不读写 owner 状态。
func (r StreamResumeRequest) ValidateAt(binding StreamBinding, now time.Time) error {
	if !validUTC(now) {
		return fmt.Errorf("%w: validation time must be UTC", ErrStreamRequestInvalid)
	}
	if r.CancelSignal {
		return ErrStreamCancelled
	}
	if err := validateStreamBinding(binding, now); err != nil {
		return err
	}
	if err := validateStreamRequestIdentity(r, binding); err != nil {
		return err
	}
	if err := validateStreamScope(r.Scope, binding.Scope); err != nil {
		return err
	}
	if err := r.Caller.ValidateAt(now); err != nil || !sameCaller(r.Caller, binding.Caller) {
		return ErrStreamUnauthorized
	}
	if err := validateStreamDeadline(r.DeadlineAt, now); err != nil {
		return err
	}
	return validateStreamCarriers(r)
}

// ValidateContext 在触达任何 owner 前拒绝已取消的请求。
func (r StreamResumeRequest) ValidateContext(ctx context.Context, binding StreamBinding, now time.Time) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrStreamRequestInvalid)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrStreamCancelled, err)
	}
	return r.ValidateAt(binding, now)
}

// validateStreamBinding 校验 endpoint、session、caller 与 task scope 的服务端快照。
func validateStreamBinding(binding StreamBinding, now time.Time) error {
	for name, value := range map[string]string{
		"endpoint_ref": binding.EndpointRef,
		"profile":      binding.Profile,
		"boot_epoch":   binding.BootEpoch,
	} {
		if !validText(value, maxEventIDLength, true) {
			return fmt.Errorf("%w: %s is not canonical", ErrStreamRequestInvalid, name)
		}
	}
	if err := validateStreamID(binding.SessionID, "session_id"); err != nil {
		return err
	}
	if err := validateStreamID(binding.StreamID, "stream_id"); err != nil {
		return err
	}
	if err := binding.Caller.ValidateAt(now); err != nil {
		return ErrStreamUnauthorized
	}
	if binding.Scope.TaskID != binding.Caller.TaskID || binding.Scope.TaskScopeRef != binding.Caller.ScopeRef {
		return ErrStreamScopeMismatch
	}
	return validateStreamScope(binding.Scope, binding.Scope)
}

func validateStreamRequestIdentity(request StreamResumeRequest, binding StreamBinding) error {
	if request.SessionID != binding.SessionID || request.StreamID != binding.StreamID {
		return ErrStreamResumeMismatch
	}
	if err := validateStreamID(request.SessionID, "session_id"); err != nil {
		return err
	}
	return validateStreamID(request.StreamID, "stream_id")
}

func validateStreamScope(request, binding StreamScope) error {
	if err := validateTaskScope(request, binding); err != nil {
		return err
	}
	return validateRoomScope(request, binding)
}

func validateTaskScope(request, binding StreamScope) error {
	if !validText(request.TaskID, maxEventIDLength, true) || !validText(request.TaskScopeRef, maxEventIDLength, true) {
		return fmt.Errorf("%w: task scope is incomplete", ErrStreamScopeMismatch)
	}
	if request.TaskID != binding.TaskID || request.TaskScopeRef != binding.TaskScopeRef {
		return ErrStreamScopeMismatch
	}
	return nil
}

// validateRoomScope 校验 Room scope 成对出现并与服务端快照逐字一致。
func validateRoomScope(request, binding StreamScope) error {
	if binding.RoomID == "" || binding.RoomScopeRef == "" {
		return validateAbsentRoomScope(request, binding)
	}
	if !validText(binding.RoomID, maxEventIDLength, true) || !validText(binding.RoomScopeRef, maxEventIDLength, true) || request.RoomID != binding.RoomID || request.RoomScopeRef != binding.RoomScopeRef {
		return ErrStreamScopeMismatch
	}
	return nil
}

func validateAbsentRoomScope(request, binding StreamScope) error {
	if binding.RoomID != "" || binding.RoomScopeRef != "" || request.RoomID != "" || request.RoomScopeRef != "" {
		return ErrStreamScopeMismatch
	}
	return nil
}

func validateStreamDeadline(deadline, now time.Time) error {
	if !validUTC(deadline) || !deadline.After(now) || deadline.Sub(now) < minStreamWaitWindow || deadline.Sub(now) > maxStreamWaitWindow {
		return ErrStreamDeadline
	}
	return nil
}

func validateStreamCarriers(request StreamResumeRequest) error {
	switch request.Method {
	case StreamMethodPost, StreamMethodDelete:
		return validatePostDeleteCarrier(request)
	case StreamMethodGet:
		return validateGetCarrier(request)
	default:
		return ErrStreamCarrierMismatch
	}
}

func validatePostDeleteCarrier(request StreamResumeRequest) error {
	if request.Mode != "" || request.LastEventID != "" || request.Cursor != nil {
		return ErrStreamCarrierMismatch
	}
	return nil
}

func validateGetCarrier(request StreamResumeRequest) error {
	switch request.Mode {
	case StreamModeListen:
		if request.LastEventID != "" || request.Cursor != nil {
			return ErrStreamCarrierMismatch
		}
		return nil
	case StreamModeResume:
		return validateResumeCarrier(request)
	default:
		return ErrStreamCarrierMismatch
	}
}

// validateResumeCarrier 校验 resume 必须携带同 session/stream 的事件游标。
func validateResumeCarrier(request StreamResumeRequest) error {
	if !validText(request.LastEventID, maxEventIDLength, true) || request.Cursor == nil {
		return ErrStreamResumeMismatch
	}
	if err := request.Cursor.Validate(); err != nil {
		return ErrStreamResumeMismatch
	}
	if request.Cursor.SessionID != request.SessionID || request.Cursor.StreamID != request.StreamID || request.Cursor.SessionID != request.Caller.SessionID || request.Cursor.BootEpoch != request.Caller.BootEpoch || !sameFence(request.Cursor.SourceFence, request.Caller.Source) {
		return ErrStreamResumeMismatch
	}
	return nil
}

func validateStreamID(value, name string) error {
	if value == "" || len(value) > maxStreamIDLength || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not canonical", ErrStreamRequestInvalid, name)
	}
	return nil
}
