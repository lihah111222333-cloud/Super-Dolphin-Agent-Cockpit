package archtest

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteruntime"
)

// TestHeterociBatchResourceBudgetFieldChain keeps the canonical producer,
// batch wire DTO, execution facts and transport flag consumers in one guard.
func TestHeterociBatchResourceBudgetFieldChain(t *testing.T) {
	producer := remoteCIBatchResourceFields(reflect.TypeFor[heteroci.ExecutionUnit]())
	wire := remoteCIBatchResourceFields(reflect.TypeFor[heterocinode.BatchWorkload]())
	facts := remoteCIBatchResourceFields(reflect.TypeFor[remoteruntime.ExecutionFacts]())
	for field, jsonName := range map[string]string{"CPUMilli": "cpu_milli", "MemoryBytes": "memory_bytes"} {
		if producer[field] != jsonName {
			t.Fatalf("ExecutionUnit.%s JSON field = %q, want %q", field, producer[field], jsonName)
		}
		if wire[field] != jsonName {
			t.Fatalf("BatchWorkload.%s JSON field = %q, want %q", field, wire[field], jsonName)
		}
		if _, ok := facts[field]; !ok {
			t.Fatalf("ExecutionFacts is missing %s", field)
		}
	}

	root := findRepoRoot(t)
	markers := map[string][]string{
		"internal/devtools/remotecicli/runtime_adapters.go": {
			"portable.CPUMilli != unit.CPUMilli", "portable.MemoryBytes != unit.MemoryBytes",
			"CPUMilli: unit.CPUMilli", "MemoryBytes: unit.MemoryBytes",
			"facts.CPUMilli = cpuMilli", "facts.MemoryBytes = memoryBytes",
			"deriveExactBatchWorkloadDigest(request.Workloads)",
		},
		"internal/devtools/remoteruntime/transport.go": {
			`"--cpu-milli"`, `"--memory-bytes"`, "execution.CPUMilli", "execution.MemoryBytes",
		},
		"internal/devtools/heterocinode/batch.go": {
			`BatchRequestSchemaVersion = "heteroci-node-batch-request/v5"`,
			"workload resource budget must be positive",
		},
	}
	for relative, required := range markers {
		source := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
		for _, marker := range required {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing resource-budget field guard marker %q", relative, marker)
			}
		}
	}
}

func remoteCIBatchResourceFields(typ reflect.Type) map[string]string {
	fields := make(map[string]string)
	if typ == nil || typ.Kind() != reflect.Struct {
		return fields
	}
	for field := range typ.Fields() {
		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if jsonName == "" {
			jsonName = field.Name
		}
		if field.Name == "CPUMilli" || field.Name == "MemoryBytes" {
			fields[field.Name] = jsonName
		}
	}
	return fields
}
