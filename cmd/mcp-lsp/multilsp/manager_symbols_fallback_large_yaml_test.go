package multilsp

import (
	"strings"
	"testing"
)

func TestParseYAMLSymbolsContinuesAfterLargeValue(t *testing.T) {
	lines := []string{"large: " + strings.Repeat("x", 70*1024), "after: value"}
	symbols := parseYAMLSymbols(lines)
	if len(symbols) != 2 {
		t.Fatalf("symbols = %d, want 2", len(symbols))
	}
	if symbols[1].Name != "after" || symbols[1].Range.Start.Line != 1 {
		t.Fatalf("second symbol = %#v, want after on line 1", symbols[1])
	}
}
