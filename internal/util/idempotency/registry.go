// Package idempotency contains in-process helpers for idempotent calls.
package idempotency

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
)

// Registry deduplicates concurrent work by key and replays successful results.
// Failed calls are not stored, so callers can retry a partially failed launch.
type Registry[T any] struct {
	flight       singleflight.Group
	fingerprints sync.Map
	results      sync.Map
}

type result[T any] struct {
	fingerprint string
	value       T
	err         error
}

type retainedError struct{ error }

// Unwrap 返回底层错误。
func (e retainedError) Unwrap() error { return e.error }

// Retain 保留本次幂等结果。
func Retain(err error) error {
	if err == nil {
		return nil
	}
	return retainedError{err}
}

func shouldRetain(err error) bool {
	var retained retainedError
	return errors.As(err, &retained)
}

// IsRetained 判断指定 key 是否仍被保留。
func IsRetained(err error) bool { return shouldRetain(err) }

// RetainOnError 在错误满足条件时保留结果。
func RetainOnError(cause, cleanupErr error) error {
	if cleanupErr != nil {
		return Retain(errors.Join(cause, cleanupErr))
	}
	return cause
}

// RetainMappedError 保留映射后的错误结果。
func RetainMappedError[T any](mapping *sync.Map, registry *Registry[T], key string, err error) bool {
	if !IsRetained(err) {
		return false
	}
	if intentID, ok := mapping.Load(strings.TrimSpace(key)); ok {
		registry.RetainError(intentID.(string), err)
	}
	return true
}

// MappedError 返回已记录的映射错误。
func MappedError[T any](mapping *sync.Map, registry *Registry[T], key string) (error, bool) {
	if intentID, ok := mapping.Load(strings.TrimSpace(key)); ok {
		return registry.Error(intentID.(string))
	}
	return nil, false
}

// ForgetMappedUnlessError 处理forgetmappedunless错误。
func ForgetMappedUnlessError[T any](mapping *sync.Map, registry *Registry[T], key string) {
	key = strings.TrimSpace(key)
	intentID, ok := mapping.Load(key)
	if !ok {
		return
	}
	id := intentID.(string)
	if _, retained := registry.Error(id); retained {
		return
	}
	mapping.Delete(key)
	registry.Forget(id)
}

// Do runs fn once per key while concurrent callers wait for the same result.
// The same key with a different fingerprint is rejected after success.
// Do 保证同一个 key 只执行一次函数。
func (r *Registry[T]) Do(key, fingerprint string, fn func() (T, error)) (T, error) {
	if err := r.reserveFingerprint(key, fingerprint); err != nil {
		var zero T
		return zero, err
	}
	if cached, ok := r.results.Load(key); ok {
		return replayCached[T](cached, key, fingerprint)
	}
	value, err, _ := r.flight.Do(key, func() (any, error) {
		if cached, ok := r.results.Load(key); ok {
			return replayCached[T](cached, key, fingerprint)
		}
		out, runErr := fn()
		if runErr != nil {
			if shouldRetain(runErr) {
				r.results.Store(key, result[T]{fingerprint: fingerprint, err: runErr})
			}
			return out, runErr
		}
		r.results.Store(key, result[T]{fingerprint: fingerprint, value: out})
		return out, nil
	})
	if err != nil {
		if !shouldRetain(err) {
			r.fingerprints.CompareAndDelete(key, fingerprint)
		}
		var zero T
		return zero, err
	}
	return value.(T), nil
}

// DoJSON 用 JSON 指纹参与幂等执行。
func (r *Registry[T]) DoJSON(key string, fingerprint any, fn func() (T, error)) (T, error) {
	payload, err := JSONFingerprint(fingerprint)
	if err != nil {
		var zero T
		return zero, err
	}
	return r.Do(key, payload, fn)
}

// JSONFingerprint 生成稳定的 JSON 指纹。
func JSONFingerprint(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("idempotency fingerprint: %w", err)
	}
	return string(payload), nil
}

func (r *Registry[T]) reserveFingerprint(key, fingerprint string) error {
	actual, loaded := r.fingerprints.LoadOrStore(key, fingerprint)
	if loaded && actual.(string) != fingerprint {
		return fmt.Errorf("idempotency key %q already used with different parameters", key)
	}
	return nil
}

func replayCached[T any](cached any, key, fingerprint string) (T, error) {
	hit := cached.(result[T])
	if hit.fingerprint != fingerprint {
		var zero T
		return zero, fmt.Errorf("idempotency key %q already used with different parameters", key)
	}
	if hit.err != nil {
		var zero T
		return zero, hit.err
	}
	return hit.value, nil
}

// Forget drops cached state for a key once the owning workflow has completed
// or has cleaned up a failed partial launch.
// Forget 删除指定 key 的幂等记录。
func (r *Registry[T]) Forget(key string) {
	r.flight.Forget(key)
	r.fingerprints.Delete(key)
	r.results.Delete(key)
}

// RetainError 保留失败结果供后续读取。
func (r *Registry[T]) RetainError(key string, err error) {
	if err == nil {
		return
	}
	if cached, ok := r.results.Load(key); ok {
		hit := cached.(result[T])
		var zero T
		hit.value, hit.err = zero, Retain(err)
		r.results.Store(key, hit)
	}
}

// Error 返回错误文本。
func (r *Registry[T]) Error(key string) (error, bool) {
	if cached, ok := r.results.Load(key); ok {
		err := cached.(result[T]).err
		return err, err != nil
	}
	return nil, false
}

// NormalizeKey 规范化键。
func NormalizeKey(field, raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if len(id) < 16 || len(id) > 128 {
		return "", fmt.Errorf("%s length must be 16..128 characters", field)
	}
	if strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		return "", fmt.Errorf("%s contains invalid characters", field)
	}
	return id, nil
}
