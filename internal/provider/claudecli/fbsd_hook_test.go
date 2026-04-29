package claudecli

import (
	"encoding/json"
	"sync"
	"testing"
)

// fakeRecorder 收集所有 (name, anchor) 调用，用于断言 hook 行为。
type fakeRecorder struct {
	mu    sync.Mutex
	calls []struct{ name, anchor string }
}

func (r *fakeRecorder) record(name, anchor string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, struct{ name, anchor string }{name, anchor})
}

func (r *fakeRecorder) snapshot() []struct{ name, anchor string } {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]struct{ name, anchor string }, len(r.calls))
	copy(out, r.calls)
	return out
}

func resetRecorder(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetFBSDRecorder(nil) })
}

func TestParseSkillReadPath_Match(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"file_path": "/tmp/ws/.claude/skills/tdd/references/01-red-green.md"})
	name, anchor, ok := parseSkillReadPath(in)
	if !ok {
		t.Fatal("expected match")
	}
	if name != "tdd" || anchor != "red-green" {
		t.Errorf("got name=%q anchor=%q want tdd/red-green", name, anchor)
	}
}

func TestParseSkillReadPath_NonSkillsPath(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"file_path": "/tmp/ws/src/main.go"})
	if _, _, ok := parseSkillReadPath(in); ok {
		t.Error("non-skills path should not match")
	}
}

func TestParseSkillReadPath_NotMd(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"file_path": "/x/.claude/skills/y/references/01-foo.txt"})
	if _, _, ok := parseSkillReadPath(in); ok {
		t.Error(".txt under references should not match")
	}
}

func TestParseSkillReadPath_NoNNPrefix(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"file_path": "/x/.claude/skills/y/references/red-green.md"})
	if _, _, ok := parseSkillReadPath(in); ok {
		t.Error("filename without NN- digit prefix should not match")
	}
}

func TestParseSkillReadPath_EmptyPath(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"file_path": ""})
	if _, _, ok := parseSkillReadPath(in); ok {
		t.Error("empty path should not match")
	}
}

func TestParseSkillReadPath_MalformedJSON(t *testing.T) {
	if _, _, ok := parseSkillReadPath(json.RawMessage([]byte("{not json"))); ok {
		t.Error("malformed JSON should not match")
	}
}

func TestRecordSkillReadIfApplicable_HitsRecorder(t *testing.T) {
	resetRecorder(t)
	r := &fakeRecorder{}
	SetFBSDRecorder(r.record)
	in, _ := json.Marshal(map[string]string{"file_path": "/x/.claude/skills/tdd/references/02-anti.md"})
	recordSkillReadIfApplicable("Read", in)
	calls := r.snapshot()
	if len(calls) != 1 || calls[0].name != "tdd" || calls[0].anchor != "anti" {
		t.Errorf("got %+v, want [{tdd anti}]", calls)
	}
}

func TestRecordSkillReadIfApplicable_NonReadIgnored(t *testing.T) {
	resetRecorder(t)
	r := &fakeRecorder{}
	SetFBSDRecorder(r.record)
	in, _ := json.Marshal(map[string]string{"file_path": "/x/.claude/skills/tdd/references/01-red-green.md"})
	recordSkillReadIfApplicable("Bash", in)
	if len(r.snapshot()) != 0 {
		t.Errorf("non-Read tool should not record: %+v", r.snapshot())
	}
}

func TestRecordSkillReadIfApplicable_NoRecorderNoOp(t *testing.T) {
	resetRecorder(t)
	// no SetFBSDRecorder called → nil recorder
	in, _ := json.Marshal(map[string]string{"file_path": "/x/.claude/skills/tdd/references/01-red-green.md"})
	// Must not panic
	recordSkillReadIfApplicable("Read", in)
}

func TestSetFBSDRecorder_NilClears(t *testing.T) {
	resetRecorder(t)
	r := &fakeRecorder{}
	SetFBSDRecorder(r.record)
	SetFBSDRecorder(nil)
	in, _ := json.Marshal(map[string]string{"file_path": "/x/.claude/skills/tdd/references/01-red-green.md"})
	recordSkillReadIfApplicable("Read", in)
	if len(r.snapshot()) != 0 {
		t.Errorf("after SetFBSDRecorder(nil) recorder should be cleared: %+v", r.snapshot())
	}
}
