//go:build !darwin && !linux && !windows

package processobserve

import "context"

// secureRoot 是未经验证平台的显式拒绝实现；除无资源可释放的 close 外，
// 所有持久化入口都返回 ErrDurablePlatformNotVerified，禁止悄悄退回非持久化存储。
type secureRoot struct{}

func openDurableRoot(string) (*secureRoot, error) { return nil, ErrDurablePlatformNotVerified }
func (r *secureRoot) identity() (uint64, uint64)  { return 0, 0 }
func (r *secureRoot) close() error                { return nil }
func (r *secureRoot) withStoreLock(context.Context, func(*secureRoot) error) error {
	return ErrDurablePlatformNotVerified
}
func (r *secureRoot) readDurableRecords() (map[string]loadedDurableRecord, error) {
	return nil, ErrDurablePlatformNotVerified
}
func (r *secureRoot) publishDurableRecord(string, []byte) error { return ErrDurablePlatformNotVerified }
func (r *secureRoot) deleteDurableRecord(string) error          { return ErrDurablePlatformNotVerified }
