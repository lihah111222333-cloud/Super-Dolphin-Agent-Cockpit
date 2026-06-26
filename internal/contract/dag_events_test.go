package contract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// ParseDAGEvents / FilterEventsByKind 单测覆盖 DAG 事件 JSON 解析与 kind 过滤边界：
//   - nil / 空数组 / "null" → (nil, nil)
//   - 单条 node_spawn → 字段完整解出
//   - 混合 kind → ParseDAGEvents 全解出；FilterEventsByKind 按 kind 过滤
//   - 未知 kind → 不报错，Kind 字段保原值
//   - 非数组 → error
//   - 元素非 object → error 含 element ordinal

func TestParseDAGEvents_NilEmpty_ReturnsNil(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{"nil", nil},
		{"len_zero", json.RawMessage{}},
		{"literal_null", json.RawMessage(`null`)},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDAGEvents(c.raw)
			if err != nil {
				t.Fatalf("%s: err = %v, want nil", c.name, err)
			}
			if got != nil {
				t.Errorf("%s: got = %v, want nil", c.name, got)
			}
		})
	}
}

func TestParseDAGEvents_SingleNodeSpawn(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"kind":"node_spawn","node_key":"writer","prev_thread_id":"t-1","thread_id":"t-2","ts":"2026-05-12T10:00:00Z"}]`)
	got, err := ParseDAGEvents(raw)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := []DAGEvent{{Kind: "node_spawn", NodeKey: "writer", PrevThreadID: "t-1", ThreadID: "t-2", TS: "2026-05-12T10:00:00Z"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got=%+v, want=%+v", got, want)
	}
}

func TestParseDAGEvents_MixedKinds(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[
		{"kind":"node_spawn","node_key":"a","thread_id":"t1","ts":"2026-05-12T10:00:00Z"},
		{"kind":"future_kind","node_key":"b","ts":"2026-05-12T10:01:00Z"},
		{"kind":"node_spawn","node_key":"c","prev_thread_id":"t1","thread_id":"t2","ts":"2026-05-12T10:02:00Z"}
	]`)
	got, err := ParseDAGEvents(raw)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(got), got)
	}
	if got[1].Kind != "future_kind" {
		t.Errorf("unknown kind dropped/changed: %+v", got[1])
	}

	onlySpawn := FilterEventsByKind(got, DAGEventKindNodeSpawn)
	if len(onlySpawn) != 2 {
		t.Errorf("FilterEventsByKind node_spawn = %d, want 2", len(onlySpawn))
	}
	for _, ev := range onlySpawn {
		if ev.Kind != "node_spawn" {
			t.Errorf("filter leak: %+v", ev)
		}
	}

	// FilterEventsByKind("") 应直接返回原切片（noop）。
	all := FilterEventsByKind(got, "")
	if !reflect.DeepEqual(all, got) {
		t.Errorf("FilterEventsByKind('') should noop, got=%+v want=%+v", all, got)
	}
}

func TestParseDAGEvents_OuterNotArray_ReturnsError(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"oops":"i am an object"}`)
	_, err := ParseDAGEvents(raw)
	if err == nil {
		t.Fatal("want err, got nil")
	}
	if !strings.Contains(err.Error(), "outer array") {
		t.Errorf("err = %v, want 'outer array' phrase", err)
	}
}

func TestParseDAGEvents_ElementNotObject_ReturnsError(t *testing.T) {
	t.Parallel()
	// 第 2 个 element 是字符串 → unmarshal 到 DAGEvent 失败。
	raw := json.RawMessage(`[{"kind":"node_spawn"},"oops",{"kind":"node_spawn"}]`)
	_, err := ParseDAGEvents(raw)
	if err == nil {
		t.Fatal("want err, got nil")
	}
	if !strings.Contains(err.Error(), "[1]") {
		t.Errorf("err = %v, should include element index [1]", err)
	}
}

func TestFilterEventsByKind_EmptyInput(t *testing.T) {
	t.Parallel()
	got := FilterEventsByKind(nil, DAGEventKindNodeSpawn)
	if len(got) != 0 {
		t.Errorf("nil events filter: got %d, want 0", len(got))
	}
}
