package runtimeadapter

import "testing"

func TestNewModuleReturnsOption(t *testing.T) {
	if newModule() == nil {
		t.Fatal("newModule() returned nil")
	}
}
