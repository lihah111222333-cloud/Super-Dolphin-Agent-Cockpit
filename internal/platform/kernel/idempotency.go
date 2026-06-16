package kernel

import (
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/util/idempotency"
)

// IdempotencyRegistry deduplicates concurrent work by key.
type IdempotencyRegistry[T any] = idempotency.Registry[T]

// RetainIdempotencyError marks err as a retained idempotency result.
func RetainIdempotencyError(err error) error {
	return idempotency.Retain(err)
}

// RetainIdempotencyOnError combines a primary error with cleanup failure when retention is needed.
func RetainIdempotencyOnError(cause, cleanupErr error) error {
	return idempotency.RetainOnError(cause, cleanupErr)
}

// MappedIdempotencyError returns a retained error mapped from key to a registry id.
func MappedIdempotencyError[T any](mapping *sync.Map, registry *IdempotencyRegistry[T], key string) (error, bool) {
	return idempotency.MappedError(mapping, registry, key)
}

// ForgetMappedIdempotencyUnlessError drops cached idempotency state unless an error is retained.
func ForgetMappedIdempotencyUnlessError[T any](mapping *sync.Map, registry *IdempotencyRegistry[T], key string) {
	idempotency.ForgetMappedUnlessError(mapping, registry, key)
}

// NormalizeIdempotencyKey validates and normalizes an idempotency key.
func NormalizeIdempotencyKey(field, raw string) (string, error) {
	return idempotency.NormalizeKey(field, raw)
}
