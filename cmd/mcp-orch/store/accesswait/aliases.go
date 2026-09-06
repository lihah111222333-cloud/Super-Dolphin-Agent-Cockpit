package accesswait

import "context"

// CreateAccessGrant 是与 owner 名称一致的显式别名，保持调用方语义不变。
func (s *Store) CreateAccessGrant(ctx context.Context, request AccessGrantRequest) (AccessGrant, error) {
	return s.CreateGrant(ctx, request)
}

// GetAccessGrant 读取授权快照，不改变任何状态。
func (s *Store) GetAccessGrant(ctx context.Context, grantID string) (AccessGrant, error) {
	return s.GetGrant(ctx, grantID)
}

// RevokeAccessGrant 执行带 revision 的授权撤销。
func (s *Store) RevokeAccessGrant(ctx context.Context, request RevokeGrantRequest) (AccessGrant, error) {
	return s.RevokeGrant(ctx, request)
}

// AuthorizeAccessGrant 执行一次独立 capability 判决。
func (s *Store) AuthorizeAccessGrant(ctx context.Context, request AccessRequest) (Authorization, error) {
	return s.Authorize(ctx, request)
}
