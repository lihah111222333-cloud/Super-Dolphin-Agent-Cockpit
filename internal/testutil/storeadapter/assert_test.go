package storeadaptertest

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type nestedFixture struct {
	Label string
	Count int
}

type fieldsFixtureSource struct {
	Active  bool
	Count   int
	Limit   uint
	Address uintptr
	Ratio   float64
	Small   complex64
	Large   complex128
	Name    string
	Nested  nestedFixture
	Pointer *nestedFixture
	Slice   []nestedFixture
	Map     map[string]nestedFixture
	Time    time.Time
}

type fieldsFixtureTarget struct {
	Active  bool
	Count   int
	Limit   uint
	Address uintptr
	Ratio   float64
	Small   complex64
	Large   complex128
	Name    string
	Nested  nestedFixture
	Pointer *nestedFixture
	Slice   []nestedFixture
	Map     map[string]nestedFixture
	Time    time.Time
}

func TestAssertFieldsMapCoversOneHotAndSupportedShapes(t *testing.T) {
	AssertFieldsMap(t, func(source fieldsFixtureSource) fieldsFixtureTarget {
		return fieldsFixtureTarget{
			Active:  source.Active,
			Count:   source.Count,
			Limit:   source.Limit,
			Address: source.Address,
			Ratio:   source.Ratio,
			Small:   source.Small,
			Large:   source.Large,
			Name:    source.Name,
			Nested:  source.Nested,
			Pointer: source.Pointer,
			Slice:   source.Slice,
			Map:     source.Map,
			Time:    source.Time,
		}
	})
}

func TestAssertFieldsMapERetainsErrorMapper(t *testing.T) {
	AssertFieldsMapE(t, func(source fieldsFixtureSource) (fieldsFixtureTarget, error) {
		return fieldsFixtureTarget{
			Active:  source.Active,
			Count:   source.Count,
			Limit:   source.Limit,
			Address: source.Address,
			Ratio:   source.Ratio,
			Small:   source.Small,
			Large:   source.Large,
			Name:    source.Name,
			Nested:  source.Nested,
			Pointer: source.Pointer,
			Slice:   source.Slice,
			Map:     source.Map,
			Time:    source.Time,
		}, nil
	})
}

func TestAssertFieldsMapEFailsFastOnMapperError(t *testing.T) {
	if os.Getenv("STOREADAPTER_ERROR_MAPPER") != "" {
		AssertFieldsMapE(t, func(source sourceBase) (targetBase, error) {
			return targetBase{Name: source.Name}, errors.New("mapper failed")
		})
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestAssertFieldsMapEFailsFastOnMapperError$")
	command.Env = append(os.Environ(), "STOREADAPTER_ERROR_MAPPER=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("expected error mapper to fail")
	}
	if want := "map field Name: mapper failed"; !strings.Contains(string(output), want) {
		t.Fatalf("expected output to contain %q, got:\n%s", want, output)
	}
}

func TestAssertFieldsMapFailsFastOnUnsupportedKind(t *testing.T) {
	if os.Getenv("STOREADAPTER_UNSUPPORTED_KIND") != "" {
		AssertFieldsMap(t, func(source unsupportedSource) unsupportedTarget {
			return unsupportedTarget{Items: source.Items}
		})
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestAssertFieldsMapFailsFastOnUnsupportedKind$")
	command.Env = append(os.Environ(), "STOREADAPTER_UNSUPPORTED_KIND=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("expected unsupported kind to fail")
	}
	if want := "sample field Items: unsupported store adapter sample type [1]string (kind array)"; !strings.Contains(string(output), want) {
		t.Fatalf("expected output to contain %q, got:\n%s", want, output)
	}
}

func TestAssertFieldsMapChecksFieldSetsInBothDirections(t *testing.T) {
	mode := os.Getenv("STOREADAPTER_FIELD_SET_MODE")
	if mode != "" {
		runFieldSetMismatch(t, mode)
		return
	}

	tests := map[string]string{
		"target_missing": "target storeadaptertest.targetBase lacks compatible exported field Extra (int)",
		"source_missing": "source storeadaptertest.sourceBase lacks exported field Extra",
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestAssertFieldsMapChecksFieldSetsInBothDirections$")
			command.Env = append(os.Environ(), "STOREADAPTER_FIELD_SET_MODE="+name)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("expected %s mismatch to fail", name)
			}
			if !strings.Contains(string(output), want) {
				t.Fatalf("expected output to contain %q, got:\n%s", want, output)
			}
		})
	}
}

type sourceExtra struct {
	Name  string
	Extra int
}

type targetBase struct {
	Name string
}

type sourceBase struct {
	Name string
}

type targetExtra struct {
	Name  string
	Extra int
}

type unsupportedSource struct {
	Items [1]string
}

type unsupportedTarget struct {
	Items [1]string
}

func runFieldSetMismatch(t *testing.T, mode string) {
	t.Helper()
	switch mode {
	case "target_missing":
		AssertFieldsMap(t, func(source sourceExtra) targetBase { return targetBase{Name: source.Name} })
	case "source_missing":
		AssertFieldsMap(t, func(source sourceBase) targetExtra { return targetExtra{Name: source.Name} })
	default:
		t.Fatalf("unknown field-set mode %q", mode)
	}
}
