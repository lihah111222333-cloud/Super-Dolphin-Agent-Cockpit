package wails

import (
	"context"
	"reflect"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestNewWindowOptionsMatchesDesktopDefaults(t *testing.T) {
	t.Parallel()

	options := newWindowOptions("Super Dolphin", true, "main", "", "")
	if options.Title != "Super Dolphin" {
		t.Fatalf("window title = %q, want Super Dolphin", options.Title)
	}
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

func TestEmitFilesDroppedEventMirrorsNativeEventToWebSocketPush(t *testing.T) {
	t.Parallel()

	nativeEvents := make([]string, 0, 1)
	pushEvents := make([]string, 0, 1)
	payload := map[string]any{"files": []string{"/tmp/a.txt"}}
	app := &App{
		emitter: func(event string, data any) {
			nativeEvents = append(nativeEvents, event)
			if !reflect.DeepEqual(data, payload) {
				t.Fatalf("native payload = %#v, want original payload", data)
			}
		},
		pushRuntimeEvent: func(ctx context.Context, event string, data any) {
			if ctx == nil {
				t.Fatal("push context is nil")
			}
			pushEvents = append(pushEvents, event)
			if !reflect.DeepEqual(data, payload) {
				t.Fatalf("push payload = %#v, want original payload", data)
			}
		},
	}

	emitFilesDroppedEvent(nil, app, payload)

	if len(nativeEvents) != 1 || nativeEvents[0] != "files-dropped" {
		t.Fatalf("native events = %#v, want files-dropped once", nativeEvents)
	}
	if len(pushEvents) != 1 || pushEvents[0] != "files-dropped" {
		t.Fatalf("push events = %#v, want files-dropped once", pushEvents)
	}
}
