package heteroci

import (
	"strconv"
	"testing"
)

const linuxCapacityTestGiB int64 = 1024 * 1024 * 1024

func validLinuxCapacityFacts() LinuxCapacityFacts {
	return LinuxCapacityFacts{
		DockerLogicalCPUs: 8,
		DockerMemoryBytes: 16 * linuxCapacityTestGiB,
		CgroupCPUMax:      "400000 100000",
		CgroupMemoryMax:   strconv.FormatInt(8*linuxCapacityTestGiB, 10),
	}
}

func TestDeriveLinuxCapacityUsesDockerAndCgroupLimits(t *testing.T) {
	total, available, err := DeriveLinuxCapacity(validLinuxCapacityFacts())
	if err != nil {
		t.Fatalf("DeriveLinuxCapacity() unexpected error: %v", err)
	}
	want := ResourceCapacity{
		CPUMilli:       4000,
		MemoryBytes:    8 * linuxCapacityTestGiB,
		MaxConcurrency: 4,
	}
	if total != want {
		t.Fatalf("total capacity = %+v, want %+v", total, want)
	}
	if available != want {
		t.Fatalf("available capacity = %+v, want %+v", available, want)
	}
}

func TestDeriveLinuxCapacityUsesDockerUpperBoundForUnlimitedCgroups(t *testing.T) {
	facts := validLinuxCapacityFacts()
	facts.CgroupCPUMax = "max 100000"
	facts.CgroupMemoryMax = "max"

	total, available, err := DeriveLinuxCapacity(facts)
	if err != nil {
		t.Fatalf("DeriveLinuxCapacity() unexpected error: %v", err)
	}
	want := ResourceCapacity{
		CPUMilli:       8000,
		MemoryBytes:    16 * linuxCapacityTestGiB,
		MaxConcurrency: 8,
	}
	if total != want || available != want {
		t.Fatalf("unlimited cgroup capacity total=%+v available=%+v, want both %+v", total, available, want)
	}
}

func TestDeriveLinuxCapacityAcceptsFractionalCPUAndTrailingNewlines(t *testing.T) {
	facts := validLinuxCapacityFacts()
	facts.CgroupCPUMax = "150000 100000\n"
	facts.CgroupMemoryMax = strconv.FormatInt(4*linuxCapacityTestGiB, 10) + "\n"

	total, available, err := DeriveLinuxCapacity(facts)
	if err != nil {
		t.Fatalf("DeriveLinuxCapacity() unexpected error: %v", err)
	}
	want := ResourceCapacity{
		CPUMilli:       1500,
		MemoryBytes:    4 * linuxCapacityTestGiB,
		MaxConcurrency: 2,
	}
	if total != want || available != want {
		t.Fatalf("fractional capacity total=%+v available=%+v, want both %+v", total, available, want)
	}
}

func TestDeriveLinuxCapacitySubtractsReservedCapacity(t *testing.T) {
	base := validLinuxCapacityFacts()
	base.Reserved = ResourceCapacity{
		CPUMilli:       1000,
		MemoryBytes:    2 * linuxCapacityTestGiB,
		MaxConcurrency: 1,
	}

	total, available, err := DeriveLinuxCapacity(base)
	if err != nil {
		t.Fatalf("DeriveLinuxCapacity() unexpected error: %v", err)
	}
	wantTotal := ResourceCapacity{
		CPUMilli:       4000,
		MemoryBytes:    8 * linuxCapacityTestGiB,
		MaxConcurrency: 4,
	}
	wantAvailable := ResourceCapacity{
		CPUMilli:       3000,
		MemoryBytes:    6 * linuxCapacityTestGiB,
		MaxConcurrency: 3,
	}
	if total != wantTotal {
		t.Fatalf("total capacity = %+v, want %+v", total, wantTotal)
	}
	if available != wantAvailable {
		t.Fatalf("available capacity = %+v, want %+v", available, wantAvailable)
	}

	base.Reserved = wantTotal
	_, available, err = DeriveLinuxCapacity(base)
	if err != nil {
		t.Fatalf("DeriveLinuxCapacity() exact reservation unexpected error: %v", err)
	}
	if available != (ResourceCapacity{}) {
		t.Fatalf("available capacity after exact reservation = %+v, want all zero", available)
	}
}

func TestDeriveLinuxCapacityRejectsInvalidReservedCapacity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reserved ResourceCapacity
	}{
		{
			name:     "cpu over total",
			reserved: ResourceCapacity{CPUMilli: 4001},
		},
		{
			name:     "memory over total",
			reserved: ResourceCapacity{MemoryBytes: 8*linuxCapacityTestGiB + 1},
		},
		{
			name:     "concurrency over total",
			reserved: ResourceCapacity{MaxConcurrency: 5},
		},
		{
			name:     "negative cpu",
			reserved: ResourceCapacity{CPUMilli: -1},
		},
		{
			name:     "negative memory",
			reserved: ResourceCapacity{MemoryBytes: -1},
		},
		{
			name:     "negative concurrency",
			reserved: ResourceCapacity{MaxConcurrency: -1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := validLinuxCapacityFacts()
			facts.Reserved = tc.reserved
			assertLinuxCapacityRejects(t, facts)
		})
	}
}

func TestDeriveLinuxCapacityRejectsInvalidDockerFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*LinuxCapacityFacts)
	}{
		{name: "zero logical cpus", mutate: func(facts *LinuxCapacityFacts) { facts.DockerLogicalCPUs = 0 }},
		{name: "negative logical cpus", mutate: func(facts *LinuxCapacityFacts) { facts.DockerLogicalCPUs = -1 }},
		{name: "zero memory", mutate: func(facts *LinuxCapacityFacts) { facts.DockerMemoryBytes = 0 }},
		{name: "negative memory", mutate: func(facts *LinuxCapacityFacts) { facts.DockerMemoryBytes = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := validLinuxCapacityFacts()
			tc.mutate(&facts)
			assertLinuxCapacityRejects(t, facts)
		})
	}
}

func TestDeriveLinuxCapacityRejectsInvalidCPUMax(t *testing.T) {
	for _, value := range []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "whitespace only", value: " \n"},
		{name: "one field", value: "100000"},
		{name: "unlimited without period", value: "max"},
		{name: "three fields", value: "400000 100000 extra"},
		{name: "zero quota", value: "0 100000"},
		{name: "zero period", value: "400000 0"},
		{name: "negative quota", value: "-1 100000"},
		{name: "negative period", value: "400000 -1"},
		{name: "non-number quota", value: "quota 100000"},
		{name: "non-number period", value: "400000 period"},
		{name: "quota overflow", value: "9223372036854775808 100000"},
		{name: "period overflow", value: "400000 9223372036854775808"},
		{name: "quota times one thousand overflow", value: "9223372036854776 100000"},
		{name: "quota rounds to zero milli", value: "1 100000"},
	} {
		t.Run(value.name, func(t *testing.T) {
			facts := validLinuxCapacityFacts()
			facts.CgroupCPUMax = value.value
			assertLinuxCapacityRejects(t, facts)
		})
	}
}

func TestDeriveLinuxCapacityRejectsInvalidMemoryMax(t *testing.T) {
	for _, value := range []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "whitespace only", value: " \n"},
		{name: "multiple fields", value: "8589934592 1"},
		{name: "zero", value: "0"},
		{name: "negative", value: "-1"},
		{name: "unit suffix", value: "8GiB"},
		{name: "non-number", value: "memory"},
		{name: "overflow", value: "9223372036854775808"},
	} {
		t.Run(value.name, func(t *testing.T) {
			facts := validLinuxCapacityFacts()
			facts.CgroupMemoryMax = value.value
			assertLinuxCapacityRejects(t, facts)
		})
	}
}

func assertLinuxCapacityRejects(t *testing.T, facts LinuxCapacityFacts) {
	t.Helper()
	if _, _, err := DeriveLinuxCapacity(facts); err == nil {
		t.Fatalf("DeriveLinuxCapacity(%+v) accepted invalid input", facts)
	}
}
