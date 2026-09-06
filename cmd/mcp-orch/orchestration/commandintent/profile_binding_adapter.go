package commandintent

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// TransactionalIdentityReader 是 ID owner 在调用方 DBTX 中读取 current binding 的窄口。
type TransactionalIdentityReader interface {
	GetCurrentWithDBTX(context.Context, storedb.DBTX, string) (identity.CurrentBinding, error)
}

// ProfileBindingAdapter 把 identity.CurrentBinding 显式映射为 command profile selector。
type ProfileBindingAdapter struct{ identities TransactionalIdentityReader }

// NewProfileBindingAdapter 构造 profile store 的事务身份 reader；不持有写能力。
func NewProfileBindingAdapter(identities TransactionalIdentityReader) (*ProfileBindingAdapter, error) {
	if nilDependency(identities) {
		return nil, errors.New("transactional identity reader is required")
	}
	return &ProfileBindingAdapter{identities: identities}, nil
}

// GetSendProfileCurrentBinding 在原 DBTX 中读取最新 binding 并保留全部 ID-1 字段。
func (a *ProfileBindingAdapter) GetSendProfileCurrentBinding(ctx context.Context, dbtx storedb.DBTX, agentID string) (orchestrationcommand.ActiveBindingSelector, error) {
	if a == nil || nilDependency(a.identities) {
		return orchestrationcommand.ActiveBindingSelector{}, errors.New("profile binding adapter is not initialized")
	}
	binding, err := a.identities.GetCurrentWithDBTX(ctx, dbtx, agentID)
	if err != nil {
		return orchestrationcommand.ActiveBindingSelector{}, err
	}
	return selectorFromBinding(binding)
}
