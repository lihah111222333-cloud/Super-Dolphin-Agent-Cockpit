//go:build windows

package processobserve

import (
	"context"
)



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
func (r *secureRoot) deleteDurableRecord(string) error         { return ErrDurablePlatformNotVerified }
