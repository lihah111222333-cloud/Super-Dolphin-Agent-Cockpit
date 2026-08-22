package main

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
)

func TestRunNodeCLIBatchRequiresStrictRequestBase64(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := runNodeCLI([]string{"batch", "--request-base64", "bad"}, stdout, stderr, nil); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("invalid batch request result code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	request := heterocinode.BatchRequest{
		SchemaVersion: heterocinode.BatchRequestSchemaVersion,
		GoExecutable:  "/definitely/missing/go",
		Workloads: []heterocinode.BatchWorkload{{
			UnitID: "unit-a", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", ExecutionPolicy: heterocinode.BatchExecutionPolicyNormal, CommandDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExecutionDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}
	data, err := heterocinode.EncodeBatchRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code := runNodeCLI([]string{"batch", "--request-base64", base64.StdEncoding.EncodeToString(data)}, stdout, stderr, nil)
	if code == 0 || stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatalf("missing executable result code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
