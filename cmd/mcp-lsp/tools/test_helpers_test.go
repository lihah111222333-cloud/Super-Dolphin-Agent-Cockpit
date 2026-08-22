package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeReproFile(t *testing.T, root, relPath, content string) string {
	t.Helper()
	target := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return target
}

func marshalReproParams(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

func requireEmptyListEnvelope(t *testing.T, got any) emptyListEnvelope {
	t.Helper()
	envelope, ok := got.(emptyListEnvelope)
	if !ok {
		t.Fatalf("result type = %T, want emptyListEnvelope", got)
	}
	if !envelope.Success || envelope.Meta.Count != 0 || len(envelope.Data) != 0 {
		t.Fatalf("empty envelope = %#v, want success empty result", envelope)
	}
	return envelope
}
