package wails

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestNewWindowOptionsMatchesDesktopDefaults(t *testing.T) {
	t.Parallel()

	options := newWindowOptions("Super Agent", true, "main", "", "")
	if options.Width != 1440 || options.Height != 900 {
		t.Fatalf("window size = %dx%d, want 1440x900", options.Width, options.Height)
	}
	if options.MinWidth != 800 || options.MinHeight != 600 {
		t.Fatalf("window minimum size = %dx%d, want 800x600", options.MinWidth, options.MinHeight)
	}
	if !options.EnableFileDrop {
		t.Fatal("EnableFileDrop = false, want true")
	}
}

func TestBuildFilesDroppedPayloadIncludesDropTargetDetails(t *testing.T) {
	t.Parallel()

	payload, ok := buildFilesDroppedPayload([]string{"/tmp/a.txt"}, &application.DropTargetDetails{
		X:         11,
		Y:         17,
		ElementID: "composer-drop-zone",
		ClassList: []string{"drop", "active"},
		Attributes: map[string]string{
			"data-role": "composer",
		},
	})
	if !ok {
		t.Fatal("buildFilesDroppedPayload() ok = false, want true")
	}
	files, _ := payload["files"].([]string)
	if len(files) != 1 || files[0] != "/tmp/a.txt" {
		t.Fatalf("payload files = %#v, want single dropped file", payload["files"])
	}
	details, _ := payload["details"].(map[string]any)
	if details["id"] != "composer-drop-zone" {
		t.Fatalf("details id = %#v, want %q", details["id"], "composer-drop-zone")
	}
	if details["x"] != 11 || details["y"] != 17 {
		t.Fatalf("details coords = (%#v,%#v), want (11,17)", details["x"], details["y"])
	}
}

func TestBuildFilesDroppedPayloadRejectsEmptyFileList(t *testing.T) {
	t.Parallel()

	if payload, ok := buildFilesDroppedPayload(nil, nil); ok || payload != nil {
		t.Fatalf("buildFilesDroppedPayload() = (%#v, %t), want (nil, false)", payload, ok)
	}
}
