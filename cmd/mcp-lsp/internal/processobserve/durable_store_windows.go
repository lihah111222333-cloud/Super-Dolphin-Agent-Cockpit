//go:build windows

package processobserve

import (
	"context"
	"errors"
)

// ErrDurablePlatformNotVerified is deliberately returned on Windows until a
// native DACL/reparse/handle implementation and native tests are available.
// It is an explicit N/V result, never an insecure fallback or a green claim.
var ErrDurablePlatformNotVerified = errors.New("durable process observation store Windows DACL/reparse/handle contract is not verified")

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
