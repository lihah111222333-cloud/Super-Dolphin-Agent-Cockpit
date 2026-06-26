// Package idempotency 提供进程内幂等执行和结果复用工具。
package idempotency

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
)

// Registry 按 key 合并并发执行，并在成功后复用结果。
// 普通失败不会缓存，调用方可重试；被 Retain 标记的失败会保留给后续查询。
type Registry[T any] struct {
	flight       singleflight.Group
	fingerprints sync.Map
	results      sync.Map
}

// result 保存一次幂等执行的指纹、结果或被保留的错误。
type result[T any] struct {
	fingerprint string
	value       T
	err         error
}

// retainedError 标记错误需要写入 Registry，供后续相同 key 查询。
type retainedError struct{ error }

// Unwrap 暴露底层错误，保留 errors.Is/As 的匹配能力。
func (e retainedError) Unwrap() error { return e.error }

// Retain 标记错误需要作为幂等结果保留；nil 错误保持 nil。
func Retain(err error) error {
	if err == nil {
		return nil
	}
	return retainedError{err}
}

// shouldRetain 判断错误链中是否带有 Retain 标记。
func shouldRetain(err error) bool {
	var retained retainedError
	return errors.As(err, &retained)
}

// IsRetained 判断错误是否会被 Registry 保留。
func IsRetained(err error) bool { return shouldRetain(err) }

// RetainOnError 在清理也失败时合并并保留错误，避免后续重试覆盖故障现场。
func RetainOnError(cause, cleanupErr error) error {
	if cleanupErr != nil {
		return Retain(errors.Join(cause, cleanupErr))
	}
	return cause
}

// RetainMappedError 将外部 key 映射到 intent ID 后保留错误。
// 映射不存在或错误未带 Retain 标记时返回 false。
func RetainMappedError[T any](mapping *sync.Map, registry *Registry[T], key string, err error) bool {
	if !IsRetained(err) {
		return false
	}
	if intentID, ok := mapping.Load(strings.TrimSpace(key)); ok {
		registry.RetainError(intentID.(string), err)
	}
	return true
}

// MappedError 通过外部 key 查找 intent ID 上已保留的错误。
func MappedError[T any](mapping *sync.Map, registry *Registry[T], key string) (error, bool) {
	if intentID, ok := mapping.Load(strings.TrimSpace(key)); ok {
		return registry.Error(intentID.(string))
	}
	return nil, false
}

// ForgetMappedUnlessError 在没有保留错误时同时清理映射和 Registry 状态。
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

// Do 保证同一个 key 的并发调用只执行一次 fn。
// 同一 key 若携带不同 fingerprint 会 fail-fast，防止参数不一致的请求复用旧结果。
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

// DoJSON 将结构化 fingerprint 序列化成稳定 JSON 后参与幂等执行。
func (r *Registry[T]) DoJSON(key string, fingerprint any, fn func() (T, error)) (T, error) {
	payload, err := JSONFingerprint(fingerprint)
	if err != nil {
		var zero T
		return zero, err
	}
	return r.Do(key, payload, fn)
}

// JSONFingerprint 将值编码为 Registry 可比较的 JSON 字符串。
func JSONFingerprint(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("idempotency fingerprint: %w", err)
	}
	return string(payload), nil
}

// reserveFingerprint 记录 key 首次使用的 fingerprint，并拒绝后续不一致参数。
func (r *Registry[T]) reserveFingerprint(key, fingerprint string) error {
	actual, loaded := r.fingerprints.LoadOrStore(key, fingerprint)
	if loaded && actual.(string) != fingerprint {
		return fmt.Errorf("idempotency key %q already used with different parameters", key)
	}
	return nil
}

// replayCached 校验 fingerprint 后返回已缓存的值或保留错误。
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

// Forget 在拥有方流程完成或失败清理后删除指定 key 的幂等状态。
func (r *Registry[T]) Forget(key string) {
	r.flight.Forget(key)
	r.fingerprints.Delete(key)
	r.results.Delete(key)
}

// RetainError 将已有成功/进行中 key 改写为保留错误，供后续调用读取。
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

// Error 返回指定 key 上已保留的错误。
func (r *Registry[T]) Error(key string) (error, bool) {
	if cached, ok := r.results.Load(key); ok {
		err := cached.(result[T]).err
		return err, err != nil
	}
	return nil, false
}

// NormalizeKey 校验外部幂等 key 的长度和字符集，拒绝空白、过短或含特殊字符的值。
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
