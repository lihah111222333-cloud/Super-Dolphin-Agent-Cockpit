package memory

import "testing"

func TestDetectSaveIntentEnglish(t *testing.T) {
	intent := DetectSaveIntent("I've noted: Always split unrelated changes into separate diffs.")
	if !intent.Detected {
		t.Fatal("expected save intent to be detected")
	}
	if got, want := intent.Content, "Always split unrelated changes into separate diffs."; got != want {
		t.Fatalf("Content = %q, want %q", got, want)
	}
	if got, want := intent.Type, MemoryTypeFeedback; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
}

func TestDetectSaveIntentChinese(t *testing.T) {
	intent := DetectSaveIntent("记住了：你偏好简洁直接的回复风格。")
	if !intent.Detected {
		t.Fatal("expected Chinese save intent to be detected")
	}
	if got, want := intent.Content, "你偏好简洁直接的回复风格。"; got != want {
		t.Fatalf("Content = %q, want %q", got, want)
	}
	if got, want := intent.Type, MemoryTypeUser; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
}

func TestDetectSaveIntentReference(t *testing.T) {
	intent := DetectSaveIntent("Saved to memory: Grafana dashboard lives at https://grafana.example.com/team/core.")
	if !intent.Detected {
		t.Fatal("expected reference save intent to be detected")
	}
	if got, want := intent.Type, MemoryTypeReference; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
}

func TestDetectSaveIntentIgnoresOrdinaryReply(t *testing.T) {
	for _, response := range []string{"我会先检查代码，再给你方案。", "Noted."} {
		if intent := DetectSaveIntent(response); intent.Detected {
			t.Fatalf("DetectSaveIntent(%q) unexpectedly detected %+v", response, intent)
		}
	}
}
