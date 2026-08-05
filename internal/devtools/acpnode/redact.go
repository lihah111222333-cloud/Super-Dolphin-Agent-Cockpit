package acpnode

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

const (
	maxRedactDepth   = 16
	maxRedactMembers = 256
	maxRedactBytes   = 1 << 20
)

// Redactor 使用进程内随机盐生成不可逆、定长的日志指纹。
type Redactor struct{ salt [32]byte }

// NewRedactor 生成本进程专用盐值，用于日志指纹而非可逆脱敏。
func NewRedactor() (*Redactor, error) {
	r := &Redactor{}
	if _, err := rand.Read(r.salt[:]); err != nil {
		return nil, err
	}
	return r, nil
}

// LogValue 将任意日志值限制深度和成员数，并递归隐藏原始文本。
// LogValue returns a bounded, recursively redacted value. It never returns
// caller-provided strings or map keys, including when the input is a
// stringified/double-encoded JSON document or an error chain.
func (r *Redactor) LogValue(v any) any {
	if r == nil {
		return nil
	}
	state := redactState{seen: make(map[visit]struct{}), errors: make(map[string]struct{})}
	return r.logValue(v, 0, &state)
}

type visit struct {
	kind reflect.Kind
	ptr  uintptr
}

type redactState struct {
	seen    map[visit]struct{}
	errors  map[string]struct{}
	members int
}

// logValue 识别 JSON、错误链和反射值，统一送入受限的脱敏路径。
func (r *Redactor) logValue(v any, depth int, state *redactState) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if isNilReflectValue(rv) {
		return nil
	}
	if depth >= maxRedactDepth {
		return r.fingerprint(safeString(v))
	}
	if err, ok := v.(error); ok {
		return r.logError(err, depth, state)
	}
	if raw, ok := v.(json.RawMessage); ok {
		return r.logJSONBytes(raw, depth, state)
	}
	if bytes, ok := v.([]byte); ok {
		return r.logJSONBytes(bytes, depth, state)
	}
	switch x := v.(type) {
	case string:
		return r.logString(x, depth, state)
	case []any:
		return r.logSlice(reflect.ValueOf(x), depth, state)
	case map[string]any:
		return r.logMap(reflect.ValueOf(x), depth, state)
	}
	return r.logReflect(rv, depth, state)
}

// logString 对疑似 JSON 字符串先解析，再对所有普通文本生成指纹。
func (r *Redactor) logString(value string, depth int, state *redactState) any {
	if len(value) > maxRedactBytes {
		return r.fingerprint(value[:maxRedactBytes])
	}
	trimmed := strings.TrimSpace(value)
	if trimmed != "" && len(trimmed) <= maxRedactBytes && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "\"")) {
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			return r.logValue(decoded, depth+1, state)
		}
	}
	return r.fingerprint(value)
}

func (r *Redactor) logJSONBytes(raw []byte, depth int, state *redactState) any {
	if len(raw) > maxRedactBytes {
		return r.fingerprint(string(raw[:maxRedactBytes]))
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		return r.logValue(decoded, depth+1, state)
	}
	return r.fingerprint(string(raw))
}

// logReflect 安全遍历指针、容器和标量，避免无效值或循环导致泄漏。
func (r *Redactor) logReflect(rv reflect.Value, depth int, state *redactState) any {
	if !rv.IsValid() {
		return nil
	}
	switch rv.Kind() {
	case reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return r.logValue(rv.Elem().Interface(), depth+1, state)
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		if !r.enter(rv, state) {
			return r.fingerprint("cycle")
		}
		defer r.leave(rv, state)
		return r.logValue(rv.Elem().Interface(), depth+1, state)
	case reflect.Map, reflect.Slice, reflect.Array:
		return r.logCompositeReflect(rv, depth, state)
	default:
		return r.logScalarReflect(rv, depth, state)
	}
}

func (r *Redactor) logCompositeReflect(rv reflect.Value, depth int, state *redactState) any {
	if rv.Kind() == reflect.Map {
		return r.logMap(rv, depth, state)
	}
	return r.logSlice(rv, depth, state)
}

// logScalarReflect 保留安全的基础类型，其余不可导出值只输出指纹。
func (r *Redactor) logScalarReflect(rv reflect.Value, depth int, state *redactState) any {
	switch rv.Kind() {
	case reflect.String:
		return r.logString(rv.String(), depth, state)
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint()
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	default:
		return r.fingerprint(safeString(rv.Interface()))
	}
}

func (r *Redactor) logMap(rv reflect.Value, depth int, state *redactState) any {
	if rv.IsNil() {
		return nil
	}
	if !r.enter(rv, state) {
		return r.fingerprint("cycle")
	}
	defer r.leave(rv, state)
	out := make(map[string]any)
	iter := rv.MapRange()
	for iter.Next() {
		state.members++
		if state.members > maxRedactMembers {
			return r.fingerprint("member-limit")
		}
		key := safeString(iter.Key().Interface())
		out[r.fingerprint(key)] = r.logValue(iter.Value().Interface(), depth+1, state)
	}
	return out
}

// logSlice 统一处理切片和数组，并限制递归容器的成员总量。
func (r *Redactor) logSlice(rv reflect.Value, depth int, state *redactState) any {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return nil
	}
	if !r.enter(rv, state) {
		return r.fingerprint("cycle")
	}
	defer r.leave(rv, state)
	if rv.Len() > maxRedactMembers {
		return r.fingerprint("member-limit")
	}
	out := make([]any, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		state.members++
		if state.members > maxRedactMembers {
			return r.fingerprint("member-limit")
		}
		out = append(out, r.logValue(rv.Index(i).Interface(), depth+1, state))
	}
	return out
}

// logError 脱敏错误消息及其单链或多链原因，并检测错误循环。
func (r *Redactor) logError(err error, depth int, state *redactState) any {
	if err == nil {
		return nil
	}
	state.members++
	if state.members > maxRedactMembers {
		return r.fingerprint("member-limit")
	}
	key := errorIdentity(err)
	if _, exists := state.errors[key]; exists {
		return r.fingerprint("error-cycle")
	}
	state.errors[key] = struct{}{}
	out := map[string]any{"message": r.fingerprint(safeErrorString(err))}
	switch unwrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children := make([]any, 0, len(unwrapped.Unwrap()))
		for _, child := range unwrapped.Unwrap() {
			children = append(children, r.logError(child, depth+1, state))
		}
		out["chain"] = children
	case interface{ Unwrap() error }:
		child := unwrapped.Unwrap()
		if child != nil {
			out["cause"] = r.logError(child, depth+1, state)
		}
	}
	return out
}

func (r *Redactor) enter(rv reflect.Value, state *redactState) bool {
	ptr := uintptr(0)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		ptr = rv.Pointer()
	}
	if ptr == 0 {
		return true
	}
	v := visit{kind: rv.Kind(), ptr: ptr}
	if _, exists := state.seen[v]; exists {
		return false
	}
	state.seen[v] = struct{}{}
	return true
}

func (r *Redactor) leave(rv reflect.Value, state *redactState) {
	ptr := uintptr(0)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		ptr = rv.Pointer()
	}
	if ptr != 0 {
		delete(state.seen, visit{kind: rv.Kind(), ptr: ptr})
	}
}

func isNilReflectValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func safeErrorString(err error) string {
	if err == nil {
		return ""
	}
	return safeString(err.Error())
}

func safeString(v any) string {
	if v == nil {
		return "<nil>"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func errorIdentity(err error) string {
	if err == nil {
		return "<nil>"
	}
	rv := reflect.ValueOf(err)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		return rv.Type().String() + ":" + strconv.FormatUint(uint64(rv.Pointer()), 16)
	}
	return rv.Type().String() + ":" + safeErrorString(err)
}

func (r *Redactor) fingerprint(s string) string {
	if len(s) > maxRedactBytes {
		s = s[:maxRedactBytes]
	}
	h := hmac.New(sha256.New, r.salt[:])
	if _, err := h.Write([]byte(s)); err != nil {
		return "hmac-sha256:error"
	}
	return fmt.Sprintf("hmac-sha256:%x", h.Sum(nil))
}
