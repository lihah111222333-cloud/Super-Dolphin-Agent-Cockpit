package tasktransport

import (
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// PreflightSpec is the untrusted, action-specific portion of a preflight
// request.  Caller identity is deliberately absent: it is projected from the
// server-validated principal and attribution supplied to BuildPreflightRequest.
// The spec contains references only; it does not own grants, cursors, content,
// reservations, or any other effectful resource.
type PreflightSpec struct {
	SchemaVersion          int           `json:"schema_version"`
	RequestID              string        `json:"request_id"`
	Action                 Action        `json:"action"`
	Grant                  GrantClaim    `json:"grant"`
	GrantRef               GrantRef      `json:"grant_ref"`
	Parent                 ParentBinding `json:"parent"`
	Cursor                 *CursorClaim  `json:"cursor,omitempty"`
	ContentRef             string        `json:"content_ref,omitempty"`
	ResourceReservationRef string        `json:"resource_reservation_ref,omitempty"`
	IntentDigest           string        `json:"intent_digest"`
	RequestedAt            time.Time     `json:"requested_at"`
}

// BuildPreflightRequest projects an authenticated task caller into an
// AuthorizationRequest and validates all action-specific claims.  Principal
// and attribution are server-owned snapshots; no caller-controlled identity
// or metadata is accepted.  The returned request is structural only and does
// not authorize, persist, reserve, enqueue, or invoke an owner.
func BuildPreflightRequest(principal common.CallerPrincipal, attribution common.CallerAttribution, spec PreflightSpec, now time.Time) (AuthorizationRequest, error) {
	if err := principal.ValidateAt(now); err != nil {
		return AuthorizationRequest{}, fmt.Errorf("%w: principal: %v", ErrCallerUnavailable, err)
	}
	if strings.EqualFold(principal.Profile, "operator") {
		return AuthorizationRequest{}, ErrOperatorPrincipal
	}
	if err := attribution.Validate(); err != nil {
		return AuthorizationRequest{}, fmt.Errorf("%w: attribution: %v", ErrCallerUnavailable, err)
	}
	caller := callerBindingFromSnapshots(principal, attribution)
	request := AuthorizationRequest{
		SchemaVersion:          spec.SchemaVersion,
		RequestID:              spec.RequestID,
		Action:                 spec.Action,
		Caller:                 caller,
		Grant:                  spec.Grant,
		GrantRef:               spec.GrantRef,
		Parent:                 spec.Parent,
		Cursor:                 spec.Cursor,
		ContentRef:             spec.ContentRef,
		ResourceReservationRef: spec.ResourceReservationRef,
		IntentDigest:           spec.IntentDigest,
		RequestedAt:            spec.RequestedAt,
	}
	if err := request.ValidateAt(now); err != nil {
		return AuthorizationRequest{}, err
	}
	return request, nil
}

// BuildAuthorizationRequest is the explicit builder name used by transport
// adapters.  It is an alias of BuildPreflightRequest and retains the same
// non-authorizing, zero-effect contract.
func BuildAuthorizationRequest(principal common.CallerPrincipal, attribution common.CallerAttribution, spec PreflightSpec, now time.Time) (AuthorizationRequest, error) {
	return BuildPreflightRequest(principal, attribution, spec, now)
}

// callerBindingFromSnapshots is the single projection seam from common's
// private authenticated values into the transport-neutral candidate.
func callerBindingFromSnapshots(principal common.CallerPrincipal, attribution common.CallerAttribution) CallerBinding {
	return CallerBinding{
		PrincipalRef:  principal.PrincipalRef,
		AuthContextID: principal.AuthContext,
		ScopeRef:      principal.ScopeRef,
		Profile:       principal.Profile,
		TaskID:        attribution.TaskID,
		SessionID:     principal.AuthContext,
		BootEpoch:     attribution.Issuer.BindingRef,
		Source:        attribution.Source,
		Issuer:        attribution.Issuer,
		ExpiresAt:     principal.ExpiresAt,
	}
}
