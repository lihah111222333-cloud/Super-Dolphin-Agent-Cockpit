package app

import "reflect"

// isNilBusinessStore 统一识别 nil interface 与其中承载的 typed nil Store。
func isNilBusinessStore[Store any](store Store) bool {
	return isNilBusinessStoreValue(reflect.ValueOf(store))
}

// isNilBusinessStoreValue 将反射 nil 判断集中在一个经过全部 nil-capable Kind 覆盖的分支内。
func isNilBusinessStoreValue(value reflect.Value) bool {
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
