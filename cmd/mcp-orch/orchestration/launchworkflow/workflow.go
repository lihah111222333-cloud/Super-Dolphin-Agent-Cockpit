// Package launchworkflow coordinates immutable launch intent with identity preparation.
package launchworkflow

import (
	"context"
	"errors"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/launchassembler"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

var ErrInvalid = errors.New("launch workflow request is invalid")

// Request 携带launch intent与显式child identity准备字段。
type Request struct {
	Intent                                                              launchassembler.Request
	AgentID, IdentityKind, ExternalIdentityRef, ExternalEpoch           string
	ReservationID, IncarnationID, PreparationID                         string
	IdentityDigest, BindingDigest                                       string
	ReservationGeneration, IncarnationGeneration, PreparationGeneration int64
	ExpectedBindingVersion, ExpectedIncarnationGeneration               int64
}

// Result 返回intent及identity准备阶段的持久记录。
type Result struct {
	Command     orchestrationcommand.Record
	Reservation identity.Reservation
	Incarnation identity.Incarnation
	Preparation identity.BindingPreparation
}

// Workflow 组合command和identity owner，不执行provider。
type Workflow struct {
	assembler  *launchassembler.Assembler
	identities identity.IdentityRepository
}

// New 创建仅负责身份准备的launch workflow。
func New(a *launchassembler.Assembler, i identity.IdentityRepository) (*Workflow, error) {
	if a == nil || i == nil {
		return nil, ErrInvalid
	}
	return &Workflow{assembler: a, identities: i}, nil
}

// Run 按intent、identity reservation、incarnation、binding preparation顺序执行。
func (w *Workflow) Run(ctx context.Context, r Request) (Result, error) {
	if err := validateWorkflowRequest(w, ctx, r); err != nil {
		return Result{}, err
	}
	cmd, err := w.assembler.Assemble(ctx, r.Intent)
	if err != nil {
		return Result{}, err
	}
	res, err := w.reserveIdentity(ctx, r, cmd)
	if err != nil {
		return Result{Command: cmd}, err
	}
	inc, err := w.createIncarnation(ctx, r, cmd, res)
	if err != nil {
		return Result{Command: cmd, Reservation: res}, err
	}
	prep, err := w.prepareBinding(ctx, r, cmd, res, inc)
	if err != nil {
		return Result{Command: cmd, Reservation: res, Incarnation: inc}, err
	}
	return Result{Command: cmd, Reservation: res, Incarnation: inc, Preparation: prep}, nil
}

// Finalize 仅使用外部提供的真实收据完成已准备绑定；不创建或推断运行时收据。
func (w *Workflow) Finalize(ctx context.Context, prepared Result, externalIdentityRef, activeProcessReceiptRef, identityDigest string) (identity.CurrentBinding, error) {
	if err := validateFinalize(w, ctx, prepared, externalIdentityRef, activeProcessReceiptRef, identityDigest); err != nil {
		return identity.CurrentBinding{}, ErrInvalid
	}
	return w.identities.FinalizeBinding(ctx, finalizeRequest(prepared, externalIdentityRef, activeProcessReceiptRef))
}

// validateFinalize 校验外部运行时收据和准备记录的身份一致性。
func validateFinalize(w *Workflow, ctx context.Context, prepared Result, externalIdentityRef, activeProcessReceiptRef, identityDigest string) error {
	if !hasFinalizeIdentity(w, ctx, prepared, externalIdentityRef, activeProcessReceiptRef, identityDigest) {
		return ErrInvalid
	}
	if !matchesFinalizeIdentity(prepared, externalIdentityRef, identityDigest) {
		return ErrInvalid
	}
	return nil
}

// hasFinalizeIdentity 检查Finalize所需对象和外部收据均已提供。
func hasFinalizeIdentity(w *Workflow, ctx context.Context, prepared Result, externalIdentityRef, activeProcessReceiptRef, identityDigest string) bool {
	return w != nil && ctx != nil && prepared.Preparation.PreparationID != "" && prepared.Incarnation.IncarnationID != "" && prepared.Incarnation.AgentID != "" && strings.TrimSpace(externalIdentityRef) != "" && strings.TrimSpace(activeProcessReceiptRef) != "" && strings.TrimSpace(identityDigest) != ""
}

// matchesFinalizeIdentity 检查外部身份和摘要与incarnation完全一致。
func matchesFinalizeIdentity(prepared Result, externalIdentityRef, identityDigest string) bool {
	return prepared.Incarnation.ExternalIdentityRef == externalIdentityRef && prepared.Incarnation.IdentityDigest == identityDigest
}

// finalizeRequest 将已验证字段映射为identity owner的Finalize请求。
func finalizeRequest(prepared Result, externalIdentityRef, activeProcessReceiptRef string) identity.FinalizeBindingRequest {
	return identity.FinalizeBindingRequest{
		PreparationID: prepared.Preparation.PreparationID, AgentID: prepared.Incarnation.AgentID,
		ExpectedPreparationGeneration: prepared.Preparation.PreparationGeneration,
		ExpectedBindingVersion:        prepared.Preparation.ExpectedBindingVersion,
		IncarnationID:                 prepared.Incarnation.IncarnationID, ExternalIdentityRef: externalIdentityRef,
		IdentityKind: prepared.Incarnation.IdentityKind, SourceFenceRef: prepared.Preparation.SourceFenceRef,
		ActiveProcessReceiptRef: activeProcessReceiptRef, BindingDigest: prepared.Preparation.BindingDigest,
	}
}

// validateWorkflowRequest 校验显式身份和代际字段，拒绝隐式推断。
func validateWorkflowRequest(w *Workflow, ctx context.Context, r Request) error {
	if w == nil || ctx == nil || strings.TrimSpace(r.AgentID) != r.AgentID || r.AgentID == "" || r.Intent.CommandID == "" || r.IdentityKind == "" || r.ReservationGeneration <= 0 || r.IncarnationGeneration <= 0 || r.PreparationGeneration <= 0 {
		return ErrInvalid
	}
	return nil
}

// reserveIdentity 持久化command-backed逻辑身份预留。
func (w *Workflow) reserveIdentity(ctx context.Context, r Request, c orchestrationcommand.Record) (identity.Reservation, error) {
	return w.identities.Reserve(ctx, identity.ReservationRequest{ReservationID: r.ReservationID, CommandID: c.Intent.CommandID, AgentID: r.AgentID, IdentityKind: r.IdentityKind, SourceKind: "first_launch", CallerPrincipalRef: c.Intent.CallerPrincipalRef, ScopeRef: c.Intent.ScopeRef, IdempotencyNamespace: c.Intent.IdempotencyNamespace, IdempotencyKeyDigest: c.Intent.IdempotencyKeyDigest, RequestDigest: c.Intent.RequestDigest, SourceFenceRef: c.Intent.SourceFenceRef, TargetFenceRef: c.Intent.TargetFenceRef, ExpectedBindingVersion: r.ExpectedBindingVersion, ReservationGeneration: r.ReservationGeneration})
}

// createIncarnation 创建不可变执行身份。
func (w *Workflow) createIncarnation(ctx context.Context, r Request, c orchestrationcommand.Record, res identity.Reservation) (identity.Incarnation, error) {
	return w.identities.CreateIncarnation(ctx, identity.IncarnationRequest{IncarnationID: r.IncarnationID, ReservationID: res.ReservationID, AgentID: r.AgentID, IdentityKind: r.IdentityKind, ExternalIdentityRef: r.ExternalIdentityRef, ExternalEpoch: r.ExternalEpoch, SourceFenceRef: c.Intent.SourceFenceRef, IdentityDigest: r.IdentityDigest, IncarnationGeneration: r.IncarnationGeneration})
}

// prepareBinding 创建非授权binding准备记录。
func (w *Workflow) prepareBinding(ctx context.Context, r Request, c orchestrationcommand.Record, res identity.Reservation, inc identity.Incarnation) (identity.BindingPreparation, error) {
	return w.identities.PrepareBinding(ctx, identity.BindingPreparationRequest{PreparationID: r.PreparationID, ReservationID: res.ReservationID, IncarnationID: inc.IncarnationID, AgentID: r.AgentID, IdentityKind: r.IdentityKind, SourceFenceRef: c.Intent.SourceFenceRef, BindingDigest: r.BindingDigest, PreparationGeneration: r.PreparationGeneration, ExpectedBindingVersion: r.ExpectedBindingVersion, ExpectedIncarnationGeneration: r.ExpectedIncarnationGeneration})
}
