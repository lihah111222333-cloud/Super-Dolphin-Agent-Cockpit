package claudecli

import "testing"

func TestClaudeSystemNoiseTagPairsAreFresh(t *testing.T) {
	first := claudeSystemNoiseTagPairs()
	second := claudeSystemNoiseTagPairs()
	first[0].open = "<changed>"
	if got := second[0].open; got != "<environment_context>" {
		t.Fatalf("independent system noise tags changed to %q", got)
	}
}
