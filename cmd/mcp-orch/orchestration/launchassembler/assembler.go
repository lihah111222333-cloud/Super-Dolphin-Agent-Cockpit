// Package launchassembler builds launch intents only; it never dispatches providers.
package launchassembler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorreservation"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

var ErrInvalid = errors.New("invalid launch request")

// Request 是launch intent的不可变来源、内容和幂等输入。
type Request struct {
	CommandID, CallerPrincipalRef, ScopeRef                             string
	IdempotencyNamespace, IdempotencyDigest, RequestDigest, DigestKeyID string
	ContentRef, SourceFenceRef, TargetFenceRef                          string
	OperatorReservationID, OperatorSourceID                             string
	OperatorGeneration                                                  int64
	ContentKind, Profile                                                string
}

// Assembler 只拥有launch intent和operator reservation两个持久owner。
type Assembler struct {
	commands     *orchestrationcommand.Store
	reservations *operatorreservation.Store
}

// New requires both immutable intent and source-fenced reservation owners.
// New 构造只负责创建不可变launch intent的assembler。
func New(commands *orchestrationcommand.Store, reservations *operatorreservation.Store) (*Assembler, error) {
	if commands == nil || reservations == nil {
		return nil, ErrInvalid
	}
	return &Assembler{commands: commands, reservations: reservations}, nil
}

// Assemble 校验当前operator reservation并保存launch_agent意图，不执行provider。
func (a *Assembler) Assemble(ctx context.Context, r Request) (orchestrationcommand.Record, error) {
	if a == nil || a.commands == nil || a.reservations == nil || ctx == nil {
		return orchestrationcommand.Record{}, ErrInvalid
	}
	if err := validate(r); err != nil {
		return orchestrationcommand.Record{}, err
	}
	res, err := a.reservations.Resolve(ctx, r.IdempotencyNamespace, r.IdempotencyDigest)
	if err != nil {
		return orchestrationcommand.Record{}, err
	}
	if err := verifyReservationBinding(res.OrchestrationOperatorReservation, r); err != nil {
		return orchestrationcommand.Record{}, err
	}
	return a.commands.Reserve(ctx, orchestrationcommand.Intent{CommandID: r.CommandID, CommandKind: "launch_agent", CallerPrincipalRef: r.CallerPrincipalRef, ScopeRef: r.ScopeRef, IdempotencyNamespace: r.IdempotencyNamespace, IdempotencyKeyDigest: r.IdempotencyDigest, RequestDigest: r.RequestDigest, DigestKeyID: r.DigestKeyID, RequestContentRef: r.ContentRef, SourceFenceRef: r.SourceFenceRef, TargetFenceRef: r.TargetFenceRef})
}

// verifyReservationBinding 比较reservation与launch请求的全部来源和内容绑定字段。
// verifyReservationBinding 比较reservation与launch请求的来源和内容字段。
func verifyReservationBinding(row sqlc.OrchestrationOperatorReservation, r Request) error {
	if row.ReservationID != r.OperatorReservationID || row.SourceID != r.OperatorSourceID || row.Generation != r.OperatorGeneration || row.CallerPrincipalRef != r.CallerPrincipalRef || row.PrincipalRef != r.CallerPrincipalRef || row.ScopeRef != r.ScopeRef || row.SourceFenceRef != r.SourceFenceRef {
		return fmt.Errorf("%w: reservation binding", ErrInvalid)
	}
	if row.ContentCandidate != r.ContentRef || row.ProfileRef != r.Profile {
		return fmt.Errorf("%w: content binding", ErrInvalid)
	}
	return nil
}

// validate 校验launch intent的必填身份、摘要和profile字段。
func validate(r Request) error {
	for _, v := range []string{r.CommandID, r.CallerPrincipalRef, r.ScopeRef, r.IdempotencyNamespace, r.IdempotencyDigest, r.RequestDigest, r.DigestKeyID, r.ContentRef, r.SourceFenceRef, r.OperatorReservationID, r.OperatorSourceID, r.ContentKind, r.Profile} {
		if strings.TrimSpace(v) != v || v == "" {
			return ErrInvalid
		}
	}
	if r.TargetFenceRef != "" && strings.TrimSpace(r.TargetFenceRef) != r.TargetFenceRef {
		return ErrInvalid
	}
	if r.OperatorGeneration <= 0 || r.ContentKind != "operator_content_ref" {
		return ErrInvalid
	}
	if len(r.IdempotencyDigest) != 64 || len(r.RequestDigest) != 64 {
		return ErrInvalid
	}
	return nil
}
