package orchestration

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
)

func TestStoreSharedFileWriterRecordsOwnerMetadata(t *testing.T) {
	store := &stubSharedfileStore{}
	writer, ok := NewStoreSharedFileWriter(store).(nodeexec.SharedFileMetadataWriter)
	if !ok {
		t.Fatal("store shared file writer must support metadata writes")
	}
	err := writer.WriteSharedFileWithMetadata(context.Background(), nodeexec.SharedFileWriteRequest{
		Path:          "reports/build.log",
		Content:       "ok",
		ContentType:   "text/plain",
		OwnerNode:     "dag-a/node-b",
		ProducerActor: "automation:node-b",
	})
	if err != nil {
		t.Fatalf("WriteSharedFileWithMetadata returned error: %v", err)
	}
	if len(store.upserts) != 2 || store.upserts[0].UpdatedBy != "automation:node-b" ||
		!strings.Contains(store.upserts[1].Content, `"owner_node":"dag-a/node-b"`) {
		t.Fatalf("metadata upserts = %+v, want main file plus owner marker", store.upserts)
	}
}
