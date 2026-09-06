// Package commandintent 组装 transport principal、access authorization 与 ID binding，
// 只生成不可授权的 send_message intent/profile draft。
package commandintent

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

// ErrContextRequired marks a nil context before any owner or content work is
// attempted.  A nil context is a programmer error, not an invitation to use a
// background context implicitly.
var ErrContextRequired = errors.New("command intent context is required")

// SendAuthorizer 是 access owner 的 send 判决窄口。
type SendAuthorizer interface {
	AuthorizeSend(context.Context, accesswait.AccessRequest) (accesswait.Authorization, error)
}

// IdentityReader 只读取 ID owner 的最新 binding；profile store 会在写事务内再次回验。
type IdentityReader interface {
	GetCurrent(context.Context, string) (identity.CurrentBinding, error)
}

// SendProfileReserver 只记录 immutable draft，不提供 pending/accepted/dispatch 方法。
type SendProfileReserver interface {
	Reserve(context.Context, orchestrationcommand.Intent, orchestrationcommand.SendMessageProfile) (orchestrationcommand.CompleteIntentRecord, error)
}

// ReserveAuthorizedSendDraftRequest 只携 caller-stable key digest 与 CONTENT lookup候选；候选不能直接进入intent。
type ReserveAuthorizedSendDraftRequest struct {
	Access                     accesswait.AccessRequest `json:"access"`
	CandidateCommandID         string                   `json:"candidate_command_id"`
	IdempotencyNamespace       string                   `json:"idempotency_namespace"`
	IdempotencyKeyDigest       string                   `json:"idempotency_key_digest"`
	RequestContentCandidateRef string                   `json:"request_content_candidate_ref"`
	DigestKeyID                string                   `json:"digest_key_id"`
	DigestKeyVersion           int64                    `json:"digest_key_version"`
}
