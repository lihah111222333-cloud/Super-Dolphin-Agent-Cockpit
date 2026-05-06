package contract

import "errors"

// Store-level sentinel errors shared across modules.
// These mirror the sentinels in platform/db but live in contract so that
// module-layer code never needs to import platform/db directly.
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict")
)

// IsNotFound reports whether err (or any error in its chain) matches
// the store-not-found sentinel.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
