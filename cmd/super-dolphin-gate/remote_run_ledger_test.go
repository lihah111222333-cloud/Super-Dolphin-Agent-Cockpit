package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestEmitRemoteRunResultKeepsStdoutAsOneJSONValue(t *testing.T) {
	var stdout bytes.Buffer
	result := remoteci.RunResult{SchemaVersion: 1, JobID: "job-1", Status: gate.ResultStatusPassed}
	if err := emitRemoteRunResult(&stdout, nil, result, nil); err != nil {
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

func TestEmitRemoteRunResultPreservesExecutionErrorWhenAuthorityIsMissing(t *testing.T) {
	store, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	runErr := errors.New("persist provisional run failed")
	result := remoteci.RunResult{SchemaVersion: 1, JobID: "job-missing", Status: gate.ResultStatusFailed}
	err = emitRemoteRunResult(io.Discard, store, result, runErr)
	if !strings.Contains(err.Error(), runErr.Error()) {
		t.Fatalf("emitRemoteRunResult() error = %v, want execution message", err)
	}
	if !strings.Contains(err.Error(), "render remote CI timing ledger") {
		t.Fatalf("emitRemoteRunResult() error = %v, want render failure", err)
	}
}
