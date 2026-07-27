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
