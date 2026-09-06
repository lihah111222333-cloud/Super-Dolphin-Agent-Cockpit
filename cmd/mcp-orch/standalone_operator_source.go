package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// standaloneOperatorSource 将配置主体绑定到唯一持久化 boot 来源，不授予任务或工具权限。
type standaloneOperatorSource struct {
	base   common.CallerPrincipalIssuer
	store  *operatorsource.Store
	source operatorsource.Source
}

var _ common.CallerPrincipalIssuer = (*standaloneOperatorSource)(nil)

// activateStandaloneOperatorSource 在来源写入前校验完整主体，然后原子切换 boot 代际。
func activateStandaloneOperatorSource(ctx context.Context, db *sql.DB, issuer common.CallerPrincipalIssuer) (*standaloneOperatorSource, error) {
	if ctx == nil || db == nil || operatorIssuerIsNil(issuer) {
		return nil, errors.New("operator source requires context, database and issuer")
	}
	if err := issuer.Validate(); err != nil {
		return nil, err
	}
	principal, err := issuer.Issue()
	if err != nil {
		return nil, err
	}
	if err := validateOperatorPrincipal(principal); err != nil {
		return nil, err
	}
	store, err := operatorsource.NewStore(db)
	if err != nil {
		return nil, err
	}
	generation, err := standaloneOperatorGeneration(ctx, store)
	if err != nil {
		return nil, err
	}
	source, err := store.Activate(ctx, operatorsource.ActivateRequest{PrincipalRef: principal.PrincipalRef, ScopeRef: principal.ScopeRef, AuthContext: principal.AuthContext, ExpectedGeneration: generation})
	if err != nil {
		return nil, err
	}
	return &standaloneOperatorSource{base: issuer, store: store, source: source}, nil
}

// standaloneOperatorGeneration 仅把明确无历史记录解释为初始代际，其余查询错误必须阻断。
func standaloneOperatorGeneration(ctx context.Context, store *operatorsource.Store) (int64, error) {
	latest, err := store.GetLatest(ctx)
	if errors.Is(err, operatorsource.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return latest.Generation, nil
}

// operatorIssuerIsNil 拒绝接口封装的 nil，避免在校验前调用无效依赖。
func operatorIssuerIsNil(issuer common.CallerPrincipalIssuer) bool {
	if issuer == nil {
		return true
	}
	value := reflect.ValueOf(issuer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// validateOperatorPrincipal 只接收未过期的独立配置 operator，不把其他 profile 转为 operator。
func validateOperatorPrincipal(principal common.CallerPrincipal) error {
	if err := principal.Validate(); err != nil {
		return err
	}
	if principal.Profile != standalone.ConfiguredPrincipalProfile {
		return errors.New("operator source requires configured standalone operator profile")
	}
	return nil
}

// Validate 同时验证基础 issuer、当前快照和持久化来源，撤销后不降级到基础 issuer。
func (s *standaloneOperatorSource) Validate() error {
	_, err := s.Issue()
	return err
}

// Issue 保留基础快照全部字段，每次检查有效期、完整身份绑定和当前来源代际。
func (s *standaloneOperatorSource) Issue() (common.CallerPrincipal, error) {
	if s == nil || s.store == nil || operatorIssuerIsNil(s.base) {
		return common.CallerPrincipal{}, errors.New("operator source issuer is unavailable")
	}
	if err := s.base.Validate(); err != nil {
		return common.CallerPrincipal{}, err
	}
	principal, err := s.base.Issue()
	if err != nil {
		return common.CallerPrincipal{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.RequireCurrent(ctx, principal); err != nil {
		return common.CallerPrincipal{}, err
	}
	return principal, nil
}

// RequireCurrent 检查调用者有效性及完整 boot 绑定，并向 SQL owner 精确解析当前来源。
func (s *standaloneOperatorSource) RequireCurrent(ctx context.Context, principal common.CallerPrincipal) (operatorsource.Source, error) {
	if s == nil || s.store == nil || ctx == nil {
		return operatorsource.Source{}, errors.New("operator source resolver is unavailable")
	}
	if err := validateOperatorPrincipal(principal); err != nil {
		return operatorsource.Source{}, err
	}
	if principal.PrincipalRef != s.source.PrincipalRef || principal.ScopeRef != s.source.ScopeRef || principal.AuthContext != s.source.AuthContext {
		return operatorsource.Source{}, errors.New("caller does not match operator source binding")
	}
	return s.store.ResolveCurrent(ctx, s.selector())
}

// selector 始终使用已持有来源的完整选择器，禁止关闭时查询并撤销新一代。
func (s *standaloneOperatorSource) selector() operatorsource.Selector {
	return operatorsource.Selector{PrincipalRef: s.source.PrincipalRef, ScopeRef: s.source.ScopeRef, AuthContext: s.source.AuthContext, SourceID: s.source.SourceID, SourceFenceRef: s.source.SourceFenceRef, Generation: s.source.Generation}
}

// Close 精确撤销本 boot 来源；幂等和旧代关闭行为由持久化 owner 决定。
func (s *standaloneOperatorSource) Close(ctx context.Context) error {
	if s == nil || s.store == nil || ctx == nil {
		return errors.New("operator source close requires source and context")
	}
	if _, err := s.store.Revoke(ctx, s.selector()); err != nil {
		return fmt.Errorf("revoke standalone operator source: %w", err)
	}
	return nil
}
