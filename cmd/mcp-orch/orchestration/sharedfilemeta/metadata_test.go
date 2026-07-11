package sharedfilemeta

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
)

func TestValidateWriteRequestRejectsUnsafeMetadata(t *testing.T) {
	for name, req := range map[string]nodeexec.SharedFileWriteRequest{
		"path escape":   {Path: "../escape.txt", Content: "ok", ContentType: "text/plain", OwnerNode: "dag-a/node-b", ProducerActor: "automation:node-b"},
		"missing owner": {Path: "reports/build.log", Content: "ok", ContentType: "text/plain", ProducerActor: "automation:node-b"},
		"bad type":      {Path: "reports/build.log", Content: "ok", ContentType: "text/html", OwnerNode: "dag-a/node-b", ProducerActor: "automation:node-b"},
	} {
		if _, err := ValidateWriteRequest(req); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestMarshalMetadataRecordsOwnerAndProducer(t *testing.T) {
	req := nodeexec.SharedFileWriteRequest{
		Path:          "reports/build.log",
		Content:       "ok",
		ContentType:   "text/plain",
		OwnerNode:     "dag-a/node-b",
		ProducerActor: "automation:node-b",
	}
	cleaned, err := ValidateWriteRequest(req)
	if err != nil {
		t.Fatalf("ValidateWriteRequest returned error: %v", err)
	}
	content, err := MarshalMetadata(cleaned, req)
	if err != nil {
		t.Fatalf("MarshalMetadata returned error: %v", err)
	}
	for _, want := range []string{`"owner_node":"dag-a/node-b"`, `"producer_actor":"automation:node-b"`, `"content_type":"text/plain"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("metadata missing %s: %s", want, content)
		}
	}
}
