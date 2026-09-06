package commandintent

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// Assembler 是唯一允许同时观察 transport、authorization、identity 与 command draft 的组合层。
type Assembler struct {
	authorizer SendAuthorizer
	identities IdentityReader
	content    ContentAdmissionProofIssuer
	reserver   SendProfileReserver
	signer     orchestrationcommand.SendProfileDigestSigner
}

// NewAssembler 构造未装配的 send draft assembler；任一 owner 缺失都失败。
func NewAssembler(authorizer SendAuthorizer, identities IdentityReader, content ContentAdmissionProofIssuer, reserver SendProfileReserver, signer orchestrationcommand.SendProfileDigestSigner) (*Assembler, error) {
	for _, item := range []struct {
		name       string
		dependency any
	}{
		{name: "send authorizer", dependency: authorizer},
		{name: "identity reader", dependency: identities},
		{name: "content admission proof issuer", dependency: content},
		{name: "send profile reserver", dependency: reserver},
		{name: "send profile digest signer", dependency: signer},
	} {
		if nilDependency(item.dependency) {
			return nil, fmt.Errorf("%s is required", item.name)
		}
	}
	return &Assembler{authorizer: authorizer, identities: identities, content: content, reserver: reserver, signer: signer}, nil
}

// ReserveAuthorizedSendDraft 验证并组装完整快照，只返回 immutable profile record。
func (a *Assembler) ReserveAuthorizedSendDraft(ctx context.Context, request ReserveAuthorizedSendDraftRequest) (orchestrationcommand.CompleteIntentRecord, error) {
	if err := a.ready(ctx); err != nil {
		return orchestrationcommand.CompleteIntentRecord{}, err
	}
	principal, err := common.RequireCallerPrincipal(ctx)
	if err != nil {
		return orchestrationcommand.CompleteIntentRecord{}, err
	}
	if principal.PrincipalRef != request.Access.CallerPrincipalRef || principal.ScopeRef != request.Access.ScopeRef {
		return orchestrationcommand.CompleteIntentRecord{}, accesswait.ErrAccessDenied
	}
	authorization, err := a.authorizer.AuthorizeSend(ctx, request.Access)
	if err != nil {
		return orchestrationcommand.CompleteIntentRecord{}, err
	}
	if err := validateAuthorization(request.Access, authorization); err != nil {
		return orchestrationcommand.CompleteIntentRecord{}, err
	}
	source, target, err := a.currentBindings(ctx, authorization)
	if err != nil {
		return orchestrationcommand.CompleteIntentRecord{}, err
	}
	contentRef, err := a.issueContentProof(ctx, request, principal, authorization)
	if err != nil {
		return orchestrationcommand.CompleteIntentRecord{}, err
	}
	intent, profile, err := a.buildSignedDraft(ctx, request, principal, authorization, source, target, contentRef)
	if err != nil {
		return orchestrationcommand.CompleteIntentRecord{}, err
	}
	return a.reserver.Reserve(ctx, intent, profile)
}

// ready 校验assembler五个显式owner依赖与调用context。
func (a *Assembler) ready(ctx context.Context) error {
	if a == nil || nilDependency(a.authorizer) || nilDependency(a.identities) || nilDependency(a.content) || nilDependency(a.reserver) || nilDependency(a.signer) {
		return errors.New("send command intent assembler is not initialized")
	}
	if ctx == nil {
		return ErrContextRequired
	}
	return ctx.Err()
}

func (a *Assembler) issueContentProof(ctx context.Context, request ReserveAuthorizedSendDraftRequest, principal common.CallerPrincipal, authorization accesswait.Authorization) (string, error) {
	proofRequest, err := buildContentProofRequest(request, principal, authorization)
	if err != nil {
		return "", err
	}
	proof, err := a.content.IssueContentAdmissionProof(ctx, proofRequest)
	if err != nil {
		return "", err
	}
	if err := validateContentProof(proofRequest, proof, principal, authorization); err != nil {
		return "", err
	}
	return proof.CanonicalRef, nil
}

func (a *Assembler) currentBindings(ctx context.Context, authorization accesswait.Authorization) (identity.CurrentBinding, identity.CurrentBinding, error) {
	source, err := a.identities.GetCurrent(ctx, authorization.Grant.Source.AgentID)
	if err != nil {
		return identity.CurrentBinding{}, identity.CurrentBinding{}, err
	}
	target, err := a.identities.GetCurrent(ctx, authorization.Grant.Target.AgentID)
	if err != nil {
		return identity.CurrentBinding{}, identity.CurrentBinding{}, err
	}
	if !bindingMatchesFence(source, authorization.Grant.Source) || !bindingMatchesFence(target, authorization.Grant.Target) {
		return identity.CurrentBinding{}, identity.CurrentBinding{}, orchestrationcommand.ErrBindingFence
	}
	return source, target, nil
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
