package main

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestEmitRemoteRunResultKeepsStdoutAsOneJSONValue(t *testing.T) {
	var stdout bytes.Buffer
	result := remoteci.RunResult{SchemaVersion: 1, JobID: "job-1", Status: gate.ResultStatusPassed}
	if err := emitRemoteRunResult(&stdout, result, nil); err != nil {
		t.Fatalf("emitRemoteRunResult() error = %v", err)
	}
	decoder := json.NewDecoder(&stdout)
	var got remoteci.RunResult
	if err := decoder.Decode(&got); err != nil {
		t.Fatalf("decode stdout JSON = %v; stdout=%q", err, stdout.String())
	}
	if got.JobID != result.JobID {
		t.Fatalf("stdout JobID = %q, want %q", got.JobID, result.JobID)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("stdout has more than one JSON value: %v; stdout=%q", err, stdout.String())
	}
}
