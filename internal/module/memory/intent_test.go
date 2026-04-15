package memory

import "testing"

func TestDetectSaveIntentEnglish(t *testing.T) {
	intent := DetectSaveIntent("Remember that always split unrelated changes into separate diffs.")
	if !intent.Detected {
		t.Fatal("expected save intent to be detected")
	}
	if got, want := intent.Content, "always split unrelated changes into separate diffs."; got != want {
		t.Fatalf("Content = %q, want %q", got, want)
	}
	if got, want := intent.Type, MemoryTypeFeedback; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
}

func TestDetectSaveIntentChinese(t *testing.T) {
	intent := DetectSaveIntent("记住：你偏好简洁直接的回复风格。")
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
	intent := DetectSaveIntent("Save Grafana dashboard lives at https://grafana.example.com/team/core. to memory")
	if !intent.Detected {
		t.Fatal("expected reference save intent to be detected")
	}
	if got, want := intent.Type, MemoryTypeReference; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
}

func TestDetectSaveIntentIgnoresAssistantConfirmation(t *testing.T) {
	for _, response := range []string{"我会先检查代码，再给你方案。", "Noted.", "I've noted: keep replies terse."} {
		if intent := DetectSaveIntent(response); intent.Detected {
			t.Fatalf("DetectSaveIntent(%q) unexpectedly detected %+v", response, intent)
		}
	}
}

func TestDetectForgetIntentEnglish(t *testing.T) {
	intent := DetectForgetIntent("Forget that concise direct replies are preferred.")
	if !intent.Detected {
		t.Fatal("expected forget intent to be detected")
	}
	if got, want := intent.Query, "concise direct replies are preferred."; got != want {
		t.Fatalf("Query = %q, want %q", got, want)
	}
}

func TestDetectForgetIntentChinese(t *testing.T) {
	intent := DetectForgetIntent("忘记：简洁直接的回复风格。")
	if !intent.Detected {
		t.Fatal("expected Chinese forget intent to be detected")
	}
	if got, want := intent.Query, "简洁直接的回复风格。"; got != want {
		t.Fatalf("Query = %q, want %q", got, want)
	}
}

func TestDetectForgetIntentIgnoresGenericForgetIt(t *testing.T) {
	for _, response := range []string{"forget it", "忘记这个", "delete this from memory"} {
		if intent := DetectForgetIntent(response); intent.Detected {
			t.Fatalf("DetectForgetIntent(%q) unexpectedly detected %+v", response, intent)
		}
	}
}
