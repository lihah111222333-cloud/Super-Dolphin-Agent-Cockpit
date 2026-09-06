package sqliteauthority

import (
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

// TestThreeLayerDigestGolden 固定 resolution、expected 与 finalization 三层摘要合同。
func TestThreeLayerDigestGolden(t *testing.T) {
	resolution, expected, finalization := threeLayerDigestFixture(t)
	goldens := []struct {
		name string
		got  string
		want string
	}{
		{name: "resolution", got: resolution.ResolutionRequestDigest, want: "9767789ca5656e40a4725bb62bf91ad13217286a320326e81e5a6d91c588ada6"},
		{name: "expected", got: expected.ExpectedResolutionDigest, want: "c35397a4cba5b08e87f0207838e2e03ac0c20ac8e2354229c42ae00cfcd927dc"},
		{name: "finalization", got: finalization.FinalizationRequestDigest, want: "09f042e7d959b29a8e85b92a884b259d0c712d6df6999418cb4f8c61485f9391"},
	}
	for _, golden := range goldens {
		t.Run(golden.name, func(t *testing.T) {
			if golden.got != golden.want {
				t.Fatalf("digest = %q, want %q", golden.got, golden.want)
			}
		})
	}
}

// TestExpectedResolutionDigestCoversEveryNonSelfLeaf 逐叶验证 expected 摘要没有遗漏字段。
func TestExpectedResolutionDigestCoversEveryNonSelfLeaf(t *testing.T) {
	_, expected, _ := threeLayerDigestFixture(t)
	for _, path := range expectedDigestLeafPaths(reflect.TypeOf(expected), nil) {
		t.Run(path.name, func(t *testing.T) {
			mutated := expected
			field := reflect.ValueOf(&mutated).Elem().FieldByIndex(path.index)
			mutateDigestFixtureLeaf(t, field)
			got, err := ComputeExpectedResolutionDigest(mutated)
			if err != nil {
				t.Fatalf("ComputeExpectedResolutionDigest() error = %v", err)
			}
			if got == expected.ExpectedResolutionDigest {
				t.Fatalf("field %s is omitted from expected digest", path.name)
			}
		})
	}
}

// TestExpectedHeadRootChangesExpectedAndFinalizationOnly 验证 head root 通过 expected 摘要传递到最终摘要。
func TestExpectedHeadRootChangesExpectedAndFinalizationOnly(t *testing.T) {
	resolution, expected, finalization := threeLayerDigestFixture(t)
	mutated := expected
	mutated.ExpectedHeadRootDigest = schemaTestDigest("d")
	mutated, err := BindExpectedResolutionDigest(mutated)
	if err != nil {
		t.Fatalf("BindExpectedResolutionDigest() error = %v", err)
	}
	mutatedFinalization, err := BindFinalizationRequest(mutated)
	if err != nil {
		t.Fatalf("BindFinalizationRequest() error = %v", err)
	}
	if mutated.ResolutionRequestDigest != resolution.ResolutionRequestDigest {
		t.Fatal("head root mutation changed resolution request digest")
	}
	if mutated.ExpectedResolutionDigest == expected.ExpectedResolutionDigest {
		t.Fatal("head root mutation did not change expected resolution digest")
	}
	if mutatedFinalization.FinalizationRequestDigest == finalization.FinalizationRequestDigest {
		t.Fatal("head root mutation did not change finalization request digest")
	}
}

type expectedDigestLeafPath struct {
	name  string
	index []int
}

// expectedDigestLeafPaths 动态枚举 ExpectedResolution 除 self digest 外的全部叶子。
func expectedDigestLeafPaths(valueType reflect.Type, prefix []int) []expectedDigestLeafPath {
	var paths []expectedDigestLeafPath
	for fieldIndex := 0; fieldIndex < valueType.NumField(); fieldIndex++ {
		field := valueType.Field(fieldIndex)
		if field.Name == "ExpectedResolutionDigest" {
			continue
		}
		path := append(append([]int(nil), prefix...), fieldIndex)
		if field.Type.Kind() == reflect.Struct {
			for _, nested := range expectedDigestLeafPaths(field.Type, path) {
				nested.name = field.Name + "." + nested.name
				paths = append(paths, nested)
			}
			continue
		}
		paths = append(paths, expectedDigestLeafPath{name: field.Name, index: path})
	}
	return paths
}

// mutateDigestFixtureLeaf 保持叶子类型不变并注入 one-hot 变化。
func mutateDigestFixtureLeaf(t *testing.T, field reflect.Value) {
	t.Helper()
	switch field.Kind() {
	case reflect.String:
		field.SetString(field.String() + "x")
	case reflect.Int64:
		field.SetInt(field.Int() + 1)
	default:
		t.Fatalf("unsupported expected digest leaf %s", field.Kind())
	}
}

// threeLayerDigestFixture 构造与路径、时钟无关的固定三层摘要输入。
func threeLayerDigestFixture(t *testing.T) (ResolutionRequest, ExpectedResolution, FinalizationRequest) {
	t.Helper()
	handle := schemaTestHandle(t)
	resolution, err := FinalizeResolutionRequestDigest(ResolutionRequest{SchemaVersion: AuthoritySchemaVersion,
		ResolutionID: "resolution-golden", Handle: handle, Kind: recoverycustody.ReceiptAvailability,
		FinalizationOperationID: "finalization-golden"})
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest() error = %v", err)
	}
	expected, err := BindExpectedResolutionDigest(ExpectedResolution{ResolutionRequest: resolution,
		HeadID: "head-golden", ExpectedHeadRevision: 7, ExpectedHeadRootDigest: schemaTestDigest("8"),
		ExpectedAntiRollbackGeneration: 9, ExpectedAnchorDigest: schemaTestDigest("9"),
		SecurityTimeLineageDigest: schemaTestDigest("a"), ExpectedSecurityTimeGeneration: 11,
		ExpectedSecurityTimeRevision: 12})
	if err != nil {
		t.Fatalf("BindExpectedResolutionDigest() error = %v", err)
	}
	finalization, err := BindFinalizationRequest(expected)
	if err != nil {
		t.Fatalf("BindFinalizationRequest() error = %v", err)
	}
	return resolution, expected, finalization
}
