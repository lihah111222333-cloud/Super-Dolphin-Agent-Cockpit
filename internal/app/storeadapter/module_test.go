package storeadapter

import "testing"

func TestNewModuleReturnsOption(t *testing.T) {
	if newModule() == nil {
		t.Fatal("newModule() returned nil")
	}
}
