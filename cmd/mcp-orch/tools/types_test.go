package tools

import (
	"reflect"
	"testing"
)

// TestEnumValues_FromEnumStringSchema 验证 EnumValues 能从 EnumStringSchema
// 构造的 schema 中正确取回枚举切片，保证 handler 层与 schema 共用单源。
//
// TestEnumValues_FromEnumStringSchema verifies EnumValues correctly extracts
// the enum slice from a schema built via EnumStringSchema.
func TestEnumValues_FromEnumStringSchema(t *testing.T) {
	schema := EnumStringSchema("status", "running", "succeeded", "failed")
	got := EnumValues(schema)
	want := []string{"running", "succeeded", "failed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnumValues = %v, want %v", got, want)
	}
}

// TestEnumValues_FromAnySlice 验证遇到 JSON 解码后常见的 []any 形态也能正确还原。
//
// TestEnumValues_FromAnySlice verifies EnumValues handles []any (the shape
// JSON decoding produces) by asserting each element is a string.
func TestEnumValues_FromAnySlice(t *testing.T) {
	schema := Schema{"type": "string", "enum": []any{"a", "b"}}
	got := EnumValues(schema)
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnumValues = %v, want %v", got, want)
	}
}

// TestEnumValues_NilOrMissing 验证 nil schema 与缺失 enum 字段时返 nil。
//
// TestEnumValues_NilOrMissing verifies EnumValues returns nil when the
// schema is nil or has no "enum" field.
func TestEnumValues_NilOrMissing(t *testing.T) {
	if got := EnumValues(nil); got != nil {
		t.Fatalf("EnumValues(nil) = %v, want nil", got)
	}
	if got := EnumValues(Schema{"type": "string"}); got != nil {
		t.Fatalf("EnumValues(no-enum) = %v, want nil", got)
	}
}

// TestEnumValues_UnexpectedShape 验证非字符串元素与非切片形状直接返 nil（防御性）。
//
// TestEnumValues_UnexpectedShape verifies EnumValues returns nil when the
// enum field has an unexpected shape (defensive guard).
func TestEnumValues_UnexpectedShape(t *testing.T) {
	if got := EnumValues(Schema{"enum": []any{"ok", 42}}); got != nil {
		t.Fatalf("EnumValues(mixed) = %v, want nil", got)
	}
	if got := EnumValues(Schema{"enum": 42}); got != nil {
		t.Fatalf("EnumValues(int) = %v, want nil", got)
	}
}

// TestEnumValues_ReturnsCopy 验证返回切片是副本，调用方修改不影响 schema。
//
// TestEnumValues_ReturnsCopy verifies the returned slice is a defensive copy.
func TestEnumValues_ReturnsCopy(t *testing.T) {
	schema := EnumStringSchema("d", "x", "y")
	got := EnumValues(schema)
	got[0] = "MUTATED"
	again := EnumValues(schema)
	if again[0] != "x" {
		t.Fatalf("schema mutated via returned slice: %v", again)
	}
}
