package wails

import "testing"

func TestNewRPCHandlersRegistersNativeDialogRoutes(t *testing.T) {
	t.Parallel()

	handlers := NewRPCHandlers(&App{}, nil, nil).Handlers
	for _, method := range []string{"ui/selectProjectDir", "ui/selectFiles", "ui/openNewWindow"} {
		if _, ok := handlers[method]; !ok {
			t.Fatalf("handler %q is not registered", method)
		}
	}
}

func TestHandleCopyTextHeadlessReturnsSoftFailure(t *testing.T) {
	t.Parallel()

	result, err := handleCopyText(&App{}, "hello")
	if err != nil {
		t.Fatalf("handleCopyText() error = %v", err)
	}
	if ok, _ := result["ok"].(bool); ok {
		t.Fatalf("handleCopyText() ok = true, want false")
	}
	if result["error"] != "clipboard not available in headless mode" {
		t.Fatalf("handleCopyText() error = %#v", result["error"])
	}
}
