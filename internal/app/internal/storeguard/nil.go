package storeguard

import "reflect"

// IsNil 统一识别 nil interface 与其中承载的 typed nil Store。
func IsNil[Store any](store Store) bool {
	return isNilValue(reflect.ValueOf(store))
}

// isNilValue 将反射 nil 判断集中在全部 nil-capable Kind 分支内。
func isNilValue(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return value.IsNil()
	default:
		return false
	}
}
