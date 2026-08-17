package eci

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

type invalidCreateRequestCase struct {
	name   string
	mutate func(*CreateRequest)
}

func invalidCreateRequestCases() []invalidCreateRequestCase {
	return []invalidCreateRequestCase{
		{"missing image cache snapshot", func(request *CreateRequest) { request.ImageCacheSnapshotID = "" }},
		{"invalid group name", func(request *CreateRequest) { request.ContainerGroupName = "Shard" }},
		{"invalid resources", func(request *CreateRequest) { request.Resources = Resources{CPU: 4, MemoryGiB: 20} }},
		{"missing main image", func(request *CreateRequest) { request.MainImage = "" }},
		{"mutable init image", func(request *CreateRequest) { request.InitImage = "registry.example/accepted-gate:latest" }},
		{"missing task command", func(request *CreateRequest) { request.Command = nil }},
		{"invalid task environment name", func(request *CreateRequest) { request.Environment = map[string]string{"1INVALID": "value"} }},
		{"long init environment value", func(request *CreateRequest) {
			request.InitContainer.Environment = map[string]string{"VALID": strings.Repeat("x", 257)}
		}},
		{"duplicate volume name", func(request *CreateRequest) { request.WorkVolume.Name = request.SourceVolume.Name }},
		{"noncanonical source volume", func(request *CreateRequest) { request.SourceVolume.Name = "input-data" }},
		{"noncanonical work volume", func(request *CreateRequest) { request.WorkVolume.Name = "cache-data" }},
		{"noncanonical temp volume", func(request *CreateRequest) { request.TempVolume.Name = "scratch-data" }},
		{"missing task mount", func(request *CreateRequest) { request.MainVolumeMounts = request.MainVolumeMounts[:1] }},
		{"unknown task volume", func(request *CreateRequest) { request.MainVolumeMounts[0].Name = "other-data" }},
		{"overlapping init mounts", func(request *CreateRequest) { request.InitVolumeMounts[1].MountPath = "/input/source/nested" }},
	}
}

func TestValidateResourcesAcceptsOnlyTheThreeNormalClasses(t *testing.T) {
	for _, resource := range []struct {
		class string
		Resources
	}{{class: "small", Resources: Resources{CPU: 2, MemoryGiB: 4}}, {class: "medium", Resources: Resources{CPU: 4, MemoryGiB: 8}}, {class: "maximum", Resources: Resources{CPU: 8, MemoryGiB: 16}}} {
		if err := validateResources(resource.CPU, resource.MemoryGiB, resource.class, cicontract.ExecutionModeNormal); err != nil {
			t.Fatalf("validateResources(%g, %g) failed: %v", resource.CPU, resource.MemoryGiB, err)
		}
	}
	if err := validateResources(6, 12, "custom", cicontract.ExecutionModeNormal); err == nil {
		t.Fatal("validateResources(6, 12) unexpectedly passed")
	}
}

func TestValidateResourcesRejectsRetiredNormalMemoryTiers(t *testing.T) {
	for _, resource := range []struct {
		cpu    float64
		memory float64
	}{
		{cpu: 2, memory: 2},
		{cpu: 2, memory: 8},
		{cpu: 2, memory: 16},
		{cpu: 4, memory: 4},
		{cpu: 4, memory: 16},
		{cpu: 4, memory: 32},
		{cpu: 8, memory: 8},
		{cpu: 8, memory: 32},
	} {
		if err := validateResources(resource.cpu, resource.memory, "small", cicontract.ExecutionModeNormal); err == nil {
			t.Fatalf("validateResources(%g, %g) unexpectedly accepted a retired resource tier", resource.cpu, resource.memory)
		}
	}
}

func TestResourceValidationAPIsRequireExplicitIdentity(t *testing.T) {
	validateType := reflect.TypeOf(validateResources)
	if validateType.IsVariadic() || validateType.NumIn() != 4 {
		t.Fatalf("validateResources signature = variadic=%t params=%d, want explicit cpu/memory/class/mode", validateType.IsVariadic(), validateType.NumIn())
	}
}

func TestCreateRequestCarriesResourceClassAndExecutionMode(t *testing.T) {
	typ := reflect.TypeFor[CreateRequest]()
	for _, field := range []string{"ResourceClassID", "ExecutionMode"} {
		if _, ok := typ.FieldByName(field); !ok {
			t.Fatalf("CreateRequest is missing canonical resource field %q", field)
		}
	}
}

func TestValidateResourcesUsesExecutionModeAndCanonicalClass(t *testing.T) {
	if err := validateResources(2, 4, "custom", cicontract.ExecutionModeNormal); err == nil {
		t.Fatal("normal resource validation accepted custom class with canonical tuple")
	}
	if err := validateResources(4, 8, "calibration", cicontract.ExecutionModeCalibration); err != nil {
		t.Fatalf("calibration resource validation rejected fixed resource: %v", err)
	}
	if err := validateResources(4, 8, "medium", cicontract.ExecutionModeCalibration); err == nil {
		t.Fatal("calibration resource validation accepted normal class")
	}
	if err := validateResources(4, 8, "calibration", cicontract.ExecutionModeNormal); err == nil {
		t.Fatal("normal resource validation accepted calibration class")
	}
}
