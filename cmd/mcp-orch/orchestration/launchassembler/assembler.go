// Package launchassembler builds launch intents only; it never dispatches providers.
package launchassembler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorreservation"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

var ErrInvalid = errors.New("invalid launch request")

type Request struct {
	CommandID, CallerPrincipalRef, ScopeRef                             string
	IdempotencyNamespace, IdempotencyDigest, RequestDigest, DigestKeyID string
	ContentRef, SourceFenceRef, TargetFenceRef                          string
	OperatorReservationID, OperatorSourceID                             string
	OperatorGeneration                                                  int64
	ContentKind, Profile                                                string
}
type Assembler struct {
	commands     *orchestrationcommand.Store
	reservations *operatorreservation.Store
}

// New requires both immutable intent and source-fenced reservation owners.
func New(commands *orchestrationcommand.Store, reservations *operatorreservation.Store) (*Assembler, error) {
	if commands == nil || reservations == nil {
		return nil, ErrInvalid
	}
	return &Assembler{commands: commands, reservations: reservations}, nil
}

// Assemble validates the caller-supplied proof and reserves an immutable launch intent.
func (a *Assembler) Assemble(ctx context.Context, r Request) (orchestrationcommand.Record, error) {
	if a == nil || ctx == nil {
		return orchestrationcommand.Record{}, ErrInvalid
	}
	if err := validate(r); err != nil {
		return orchestrationcommand.Record{}, err
	}
	res, err := a.reservations.Resolve(ctx, r.IdempotencyNamespace, r.IdempotencyDigest)
	if err != nil {
		return orchestrationcommand.Record{}, err
	}
	x := res.OrchestrationOperatorReservation
	if x.ReservationID != r.OperatorReservationID || x.SourceID != r.OperatorSourceID || x.Generation != r.OperatorGeneration || x.CallerPrincipalRef != r.CallerPrincipalRef || x.PrincipalRef != r.CallerPrincipalRef || x.ScopeRef != r.ScopeRef || x.SourceFenceRef != r.SourceFenceRef {
		return orchestrationcommand.Record{}, fmt.Errorf("%w: reservation binding", ErrInvalid)
	}
	if x.ContentCandidate != r.ContentRef || x.ProfileRef != r.Profile {
		return orchestrationcommand.Record{}, fmt.Errorf("%w: content binding", ErrInvalid)
	}
	return a.commands.Reserve(ctx, orchestrationcommand.Intent{CommandID: r.CommandID, CommandKind: "launch_agent", CallerPrincipalRef: r.CallerPrincipalRef, ScopeRef: r.ScopeRef, IdempotencyNamespace: r.IdempotencyNamespace, IdempotencyKeyDigest: r.IdempotencyDigest, RequestDigest: r.RequestDigest, DigestKeyID: r.DigestKeyID, RequestContentRef: r.ContentRef, SourceFenceRef: r.SourceFenceRef, TargetFenceRef: r.TargetFenceRef})
}
func validate(r Request) error {
	for _, v := range []string{r.CommandID, r.CallerPrincipalRef, r.ScopeRef, r.IdempotencyNamespace, r.IdempotencyDigest, r.RequestDigest, r.DigestKeyID, r.ContentRef, r.SourceFenceRef, r.OperatorReservationID, r.OperatorSourceID, r.ContentKind, r.Profile} {
		if strings.TrimSpace(v) != v || v == "" {
			return ErrInvalid
		}
	}
	if r.TargetFenceRef != "" && strings.TrimSpace(r.TargetFenceRef) != r.TargetFenceRef {
		return ErrInvalid
	}
	if r.OperatorGeneration <= 0 {
		return ErrInvalid
	}
	if len(r.IdempotencyDigest) != 64 || len(r.RequestDigest) != 64 {
		return ErrInvalid
	}
	return nil
}
