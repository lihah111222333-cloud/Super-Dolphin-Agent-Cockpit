package toolbridge

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAdaptMCPResponsePreservesTypedContentBlocks(t *testing.T) {
	resource := json.RawMessage(`{"uri":"file:///tmp/report.txt","mimeType":"text/plain","text":"report"}`)
	annotations := json.RawMessage(`{"audience":["assistant"],"priority":0.8}`)
	meta := json.RawMessage(`{"com.example/trace":"trace-1"}`)
	resp := peerToolCallResponse{Content: []peerToolCallContent{
		{Type: "text", Text: "  keep surrounding whitespace  ", Annotations: annotations, Meta: meta},
		{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png", Annotations: annotations},
		{Type: "audio", Data: "YXVkaW8=", MIMEType: "audio/wav", Meta: meta},
		{Type: "resource", Resource: resource},
		{
			Type:        "resource_link",
			URI:         "file:///tmp/report.txt",
			Name:        "report",
			Title:       "Report",
			Description: "generated report",
			MIMEType:    "text/plain",
			Size:        int64Pointer(6),
			Icons:       json.RawMessage(`[{"src":"data:image/png;base64,aWNvbg==","mimeType":"image/png"}]`),
		},
	}}

	got := requireSuccessfulAdaptedContent(t, resp)
	assertAdaptedTextContent(t, got.ContentItems[0], resp.Content[0])
	assertAdaptedBinaryContent(t, got.ContentItems[1], "image", "aW1hZ2U=", "image/png")
	assertAdaptedBinaryContent(t, got.ContentItems[2], "audio", "YXVkaW8=", "audio/wav")
	assertAdaptedResourceContentIsCopied(t, got.ContentItems[3], resource)

	wire := requireMCPContentBlocks(t, got.ContentItems)
	assertContentBlockJSONEqual(t, wire[0], `{"type":"text","text":"  keep surrounding whitespace  ","annotations":{"audience":["assistant"],"priority":0.8},"_meta":{"com.example/trace":"trace-1"}}`)
	assertContentBlockJSONEqual(t, wire[1], `{"type":"image","data":"aW1hZ2U=","mimeType":"image/png","annotations":{"audience":["assistant"],"priority":0.8}}`)
	assertContentBlockJSONEqual(t, wire[2], `{"type":"audio","data":"YXVkaW8=","mimeType":"audio/wav","_meta":{"com.example/trace":"trace-1"}}`)
	assertContentBlockJSONEqual(t, wire[3], `{"type":"resource","resource":{"uri":"file:///tmp/report.txt","mimeType":"text/plain","text":"report"}}`)
	assertContentBlockJSONEqual(t, wire[4], `{"type":"resource_link","uri":"file:///tmp/report.txt","name":"report","title":"Report","description":"generated report","mimeType":"text/plain","size":6,"icons":[{"src":"data:image/png;base64,aWNvbg==","mimeType":"image/png"}]}`)
}

func requireSuccessfulAdaptedContent(t *testing.T, resp peerToolCallResponse) *ToolCallResult {
	t.Helper()
	got, err := adaptMCPResponse(resp)
	if err != nil {
		t.Fatalf("adaptMCPResponse() error = %v", err)
	}
	if !got.Success {
		t.Fatalf("adaptMCPResponse() Success = false, want true: %#v", got)
	}
	if len(got.ContentItems) != len(resp.Content) {
		t.Fatalf("content length = %d, want %d", len(got.ContentItems), len(resp.Content))
	}
	return got
}

func assertAdaptedTextContent(t *testing.T, got ToolCallContentItem, want peerToolCallContent) {
	t.Helper()
	if got.Type != "inputText" || got.Text != want.Text {
		t.Fatalf("text content = %#v, want exact whitespace-preserving text", got)
	}
}

func assertAdaptedBinaryContent(t *testing.T, got ToolCallContentItem, contentType, data, mimeType string) {
	t.Helper()
	if got.Type != contentType || got.Data != data || got.MIMEType != mimeType {
		t.Fatalf("%s content = %#v", contentType, got)
	}
}

func assertAdaptedResourceContentIsCopied(t *testing.T, got ToolCallContentItem, resource json.RawMessage) {
	t.Helper()
	if !jsonBytesEqual(got.Resource, resource) {
		t.Fatalf("resource content = %s, want %s", got.Resource, resource)
	}
	resource[2] = 'X'
	if !json.Valid(got.Resource) {
		t.Fatal("adapted resource aliases caller-owned bytes, want deep copy")
	}
}

func requireMCPContentBlocks(t *testing.T, items []ToolCallContentItem) []map[string]any {
	t.Helper()
	wire, err := toMCPContent(items)
	if err != nil {
		t.Fatalf("toMCPContent() error = %v", err)
	}
	return wire
}

func TestAdaptMCPResponseTreatsPureImageAsNonEmptySuccess(t *testing.T) {
	got, err := adaptMCPResponse(peerToolCallResponse{Content: []peerToolCallContent{{
		Type:     "image",
		Data:     "aW1hZ2U=",
		MIMEType: "image/png",
	}}})
	if err != nil {
		t.Fatalf("adaptMCPResponse() error = %v", err)
	}
	if !got.Success || len(got.ContentItems) != 1 || got.ContentItems[0].Type != "image" {
		t.Fatalf("adaptMCPResponse() = %#v, want successful image result", got)
	}
}

func TestContentBlockValidationFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		item peerToolCallContent
		want string
	}{
		{name: "image missing data", item: peerToolCallContent{Type: "image", MIMEType: "image/png"}, want: "image data"},
		{name: "audio missing mime", item: peerToolCallContent{Type: "audio", Data: "YQ=="}, want: "audio mimeType"},
		{name: "resource missing object", item: peerToolCallContent{Type: "resource"}, want: "resource object"},
		{name: "resource link missing uri", item: peerToolCallContent{Type: "resource_link", Name: "missing-uri"}, want: "resource_link uri"},
		{name: "image mixed with text", item: peerToolCallContent{Type: "image", Text: "not one-hot", Data: "aW1hZ2U=", MIMEType: "image/png"}, want: "fields from another variant"},
		{name: "unknown variant", item: peerToolCallContent{Type: "video", Data: "dmlkZW8="}, want: `unsupported content type "video"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := adaptMCPResponse(peerToolCallResponse{Content: []peerToolCallContent{tt.item}})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("adaptMCPResponse() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func jsonBytesEqual(left, right []byte) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func assertContentBlockJSONEqual(t *testing.T, got map[string]any, want string) {
	t.Helper()
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal content block: %v", err)
	}
	if !jsonBytesEqual(raw, []byte(want)) {
		t.Fatalf("content block = %s, want %s", raw, want)
	}
}
