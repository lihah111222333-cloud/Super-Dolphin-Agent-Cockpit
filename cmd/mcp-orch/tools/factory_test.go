package tools

import (
	"strings"
	"testing"
)

// TestRequireEnum_Valid 验证合法值返回 trim 后的字符串。
//
// TestRequireEnum_Valid checks the happy path: a trimmed value inside the
// allowed set is returned as-is.
func TestRequireEnum_Valid(t *testing.T) {
	got, err := requireEnum("  running  ", "status", []string{"running", "succeeded"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "running" {
		t.Fatalf("got = %q, want running", got)
	}
}

// TestRequireEnum_Empty 验证空串与纯空白返必填错误。
//
// TestRequireEnum_Empty checks empty / whitespace inputs yield a "required"
// error consistent with requireTrimmed.
func TestRequireEnum_Empty(t *testing.T) {
	cases := []string{"", "   ", "\t\n"}
	for _, raw := range cases {
		raw := raw
		t.Run("input="+raw, func(t *testing.T) {
			_, err := requireEnum(raw, "status", []string{"running"})
			if err == nil {
				t.Fatalf("expected error for raw=%q", raw)
			}
			if !strings.Contains(err.Error(), "required") {
				t.Fatalf("err = %v, want contain 'required'", err)
			}
		})
	}
}

// TestRequireEnum_Invalid 验证非法值返中英双语错误，且包含 field / allowed 候选。
//
// TestRequireEnum_Invalid verifies an out-of-set value yields a bilingual
// error that names the field and lists allowed candidates.
func TestRequireEnum_Invalid(t *testing.T) {
	_, err := requireEnum("bogus", "trigger_source", []string{"manual", "auto"})
	if err == nil {
		t.Fatalf("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"trigger_source", "bogus", "manual", "auto", "invalid"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("err %q missing %q", msg, want)
		}
	}
}

// TestRequireEnum_TrimsWhitespace 验证 trim 后命中也算合法。
//
// TestRequireEnum_TrimsWhitespace ensures leading/trailing whitespace is
// trimmed before comparison.
func TestRequireEnum_TrimsWhitespace(t *testing.T) {
	got, err := requireEnum("\tauto\n", "trigger_source", []string{"manual", "auto"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "auto" {
		t.Fatalf("got = %q, want auto", got)
	}
}
