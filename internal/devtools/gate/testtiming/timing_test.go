package testtiming

import (
	"bytes"
	"strings"
	"testing"
)

func TestEventWriterEmitsExactPlainTimingAndStructuredTimings(t *testing.T) {
	var output bytes.Buffer
	writer := NewEventWriter(&output)
	stream := strings.Join([]string{
		"[test-with-guard] copylocks skip: no affected registered package",
		`{"Action":"output","Time":"2026-07-28T00:00:00Z","Test":"TestFast","Output":"=== RUN   TestFast\n"}`,
		`{"Action":"pass","Time":"2026-07-28T00:00:00Z","Test":"TestFast","Elapsed":0.125}`,
		`{"Action":"fail","Time":"2026-07-28T00:00:01Z","Test":"TestSlow/subcase","Elapsed":1.5,"Output":"--- FAIL: TestSlow/subcase (1.50s)\n"}`,
	}, "\n") + "\n"
	if _, err := writer.Write([]byte(stream)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	got := output.String()
	for _, required := range []string{
		"=== RUN   TestFast",
		"--- FAIL: TestSlow/subcase",
		LogPrefix + "name=TestFast status=pass duration_ms=125",
		LogPrefix + "name=TestSlow/subcase status=fail duration_ms=1500",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("plain output %q is missing %q", got, required)
		}
	}
	if strings.Contains(got, `{"Action"`) {
		t.Fatalf("plain output leaked JSON event: %q", got)
	}
	timings := writer.Timings()
	if len(timings) != 2 ||
		timings[0] != (Timing{Name: "TestFast", Status: StatusPass, DurationMS: 125}) ||
		timings[1] != (Timing{Name: "TestSlow/subcase", Status: StatusFail, DurationMS: 1500}) {
		t.Fatalf("timings = %#v", timings)
	}
}

func TestEventWriterRejectsDuplicateAndIncompleteEvents(t *testing.T) {
	for name, stream := range map[string]string{
		"duplicate": strings.Join([]string{
			`{"Time":"2026-07-28T00:00:00Z","Action":"pass","Test":"TestOne","Elapsed":0.1}`,
			`{"Time":"2026-07-28T00:00:01Z","Action":"pass","Test":"TestOne","Elapsed":0.2}`,
		}, "\n") + "\n",
		"incomplete": `{"Time":"2026-07-28T00:00:00Z","Action":"pass"`,
	} {
		t.Run(name, func(t *testing.T) {
			writer := NewEventWriter(&bytes.Buffer{})
			_, writeErr := writer.Write([]byte(stream))
			closeErr := writer.Close()
			if writeErr == nil && closeErr == nil {
				t.Fatal("timing writer accepted invalid event stream")
			}
		})
	}
}

func TestValidateListRejectsDuplicateTiming(t *testing.T) {
	timings := []Timing{
		{Name: "TestOne", Status: StatusPass, DurationMS: 10},
		{Name: "TestOne", Status: StatusPass, DurationMS: 11},
	}
	if err := ValidateList(timings, 2); err == nil {
		t.Fatal("ValidateList() accepted duplicate timing")
	}
}
