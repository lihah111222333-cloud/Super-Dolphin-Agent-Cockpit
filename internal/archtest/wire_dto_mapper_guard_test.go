package archtest

import (
	"reflect"
	"strings"
	"testing"
)

func TestWireDTOMapperJSONFieldsFailClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{name: "non struct", typ: reflect.TypeFor[string](), want: "must be a struct"},
		{name: "zero fields", typ: reflect.TypeFor[struct{}](), want: "has zero JSON fields"},
		{
			name: "duplicate JSON tag",
			typ: reflect.StructOf([]reflect.StructField{
				{Name: "First", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
				{Name: "Second", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
			}),
			want: `duplicates JSON field "same"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := wireDTOMapperJSONFields(tc.typ)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wireDTOMapperJSONFields() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWireDTOMapperExemptionsFailClosed(t *testing.T) {
	t.Parallel()

	valid := WireDTOMapperExemption{
		Field:     "timestamp",
		Direction: "producer -> consumer",
		Reason:    "ordering is external",
		Evidence:  "mapper symbol",
		Owner:     "eventsurface",
	}
	cases := []struct {
		name       string
		exemptions []WireDTOMapperExemption
		want       string
	}{
		{name: "empty field", exemptions: []WireDTOMapperExemption{{Direction: valid.Direction, Reason: valid.Reason, Evidence: valid.Evidence, Owner: valid.Owner}}, want: "field"},
		{name: "empty direction", exemptions: []WireDTOMapperExemption{{Field: valid.Field, Reason: valid.Reason, Evidence: valid.Evidence, Owner: valid.Owner}}, want: "direction"},
		{name: "empty reason", exemptions: []WireDTOMapperExemption{{Field: valid.Field, Direction: valid.Direction, Evidence: valid.Evidence, Owner: valid.Owner}}, want: "reason"},
		{name: "empty evidence", exemptions: []WireDTOMapperExemption{{Field: valid.Field, Direction: valid.Direction, Reason: valid.Reason, Owner: valid.Owner}}, want: "evidence"},
		{name: "empty owner", exemptions: []WireDTOMapperExemption{{Field: valid.Field, Direction: valid.Direction, Reason: valid.Reason, Evidence: valid.Evidence}}, want: "owner"},
		{name: "duplicate", exemptions: []WireDTOMapperExemption{valid, valid}, want: "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := wireDTOMapperExemptionRegistry(tc.exemptions)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wireDTOMapperExemptionRegistry() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWireDTOMapperProjectionRegistryAndCoverageFailClosed(t *testing.T) {
	t.Parallel()

	valid := WireDTOMapperProjection{Field: "value", ConsumerKey: "value"}
	registryCases := []struct {
		name        string
		projections []WireDTOMapperProjection
		want        string
	}{
		{name: "empty field", projections: []WireDTOMapperProjection{{ConsumerKey: valid.ConsumerKey}}, want: "empty field"},
		{name: "empty consumer key", projections: []WireDTOMapperProjection{{Field: valid.Field}}, want: "empty consumer key"},
		{name: "duplicate producer consumer", projections: []WireDTOMapperProjection{valid, valid}, want: "duplicate"},
		{
			name: "expected output mixed with direct consumer",
			projections: []WireDTOMapperProjection{
				valid,
				{Field: valid.Field, ConsumerKey: "derived", ExpectedOutput: func(any) map[string]any { return nil }},
			},
			want: "only consumer registration",
		},
	}
	for _, tc := range registryCases {
		t.Run(tc.name, func(t *testing.T) {
			assertWireDTOProjectionRegistryError(t, tc.projections, tc.want)
		})
	}

	assertWireDTOCoverageFailures(t, valid)
}

func assertWireDTOProjectionRegistryError(t *testing.T, projections []WireDTOMapperProjection, want string) {
	t.Helper()
	_, err := wireDTOMapperProjectionRegistry(projections)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("wireDTOMapperProjectionRegistry() error = %v, want %q", err, want)
	}
}

func assertWireDTOCoverageFailures(t *testing.T, valid WireDTOMapperProjection) {
	t.Helper()
	fields := []wireDTOJSONField{{jsonName: "value"}}
	projections, err := wireDTOMapperProjectionRegistry([]WireDTOMapperProjection{valid})
	if err != nil {
		t.Fatalf("wireDTOMapperProjectionRegistry(valid) error = %v", err)
	}
	if err := validateWireDTOMapperCoverage(fields, nil, nil); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("validateWireDTOMapperCoverage(missing) error = %v", err)
	}
	if err := validateWireDTOMapperCoverage(fields, nil, map[string]map[string]WireDTOMapperProjection{"value": projections["value"], "stale": projections["value"]}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("validateWireDTOMapperCoverage(stale) error = %v", err)
	}
	exemptions := map[string]WireDTOMapperExemption{"value": {Field: "value"}}
	if err := validateWireDTOMapperCoverage(fields, exemptions, projections); err == nil || !strings.Contains(err.Error(), "both projected and exempt") {
		t.Fatalf("validateWireDTOMapperCoverage(overlap) error = %v", err)
	}
}

func TestWireDTOMapperExactDeltaRejectsWrongKeyValueAndExtraDelta(t *testing.T) {
	t.Parallel()

	projections, err := wireDTOMapperProjectionRegistry([]WireDTOMapperProjection{{Field: "value", ConsumerKey: "value"}})
	if err != nil {
		t.Fatalf("wireDTOMapperProjectionRegistry() error = %v", err)
	}
	baseline := map[string]any{"stable": "same", "value": "old"}
	cases := []struct {
		name string
		got  map[string]any
		want string
	}{
		{name: "wrong key", got: map[string]any{"stable": "same", "wrong": "wire-mapper-sentinel"}, want: "output delta keys"},
		{name: "wrong value", got: map[string]any{"stable": "same", "value": "wrong"}, want: "want \"wire-mapper-sentinel\""},
		{name: "extra delta", got: map[string]any{"stable": "same", "value": "wire-mapper-sentinel", "extra": "unexpected"}, want: "output delta keys"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := assertWireDTOMapperExactDelta(baseline, tc.got, projections["value"], nil, "wire-mapper-sentinel")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("assertWireDTOMapperExactDelta() error = %v, want %q", err, tc.want)
			}
		})
	}
	if err := assertWireDTOMapperExactDelta(
		baseline,
		map[string]any{"stable": "same", "value": "wire-mapper-sentinel"},
		projections["value"],
		nil,
		"wire-mapper-sentinel",
	); err != nil {
		t.Fatalf("assertWireDTOMapperExactDelta(correct) error = %v", err)
	}
}
