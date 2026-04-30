package fbsd

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestTier_StringConsts(t *testing.T) {
	cases := map[Tier]string{
		TierHot:    "Hot",
		TierWarm:   "Warm",
		TierCold:   "Cold",
		TierFrozen: "Frozen",
	}
	for tier, want := range cases {
		if string(tier) != want {
			t.Errorf("Tier %v string = %q, want %q", tier, string(tier), want)
		}
	}
}

func TestSkillStats_JSONRoundTrip(t *testing.T) {
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 1, 12, 30, 0, 0, time.UTC)
	in := &SkillStats{
		Calls:        []time.Time{t1, t2},
		InstalledAt:  t1,
		SectionCalls: map[string]int{"red-green": 3, "anti-patterns": 1},
	}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out SkillStats
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.Calls, out.Calls) {
		t.Errorf("Calls roundtrip differ: in=%v out=%v", in.Calls, out.Calls)
	}
	if !in.InstalledAt.Equal(out.InstalledAt) {
		t.Errorf("InstalledAt: in=%v out=%v", in.InstalledAt, out.InstalledAt)
	}
	if !reflect.DeepEqual(in.SectionCalls, out.SectionCalls) {
		t.Errorf("SectionCalls: in=%v out=%v", in.SectionCalls, out.SectionCalls)
	}
}

func TestStats_AsJSONMap(t *testing.T) {
	// Stats 序列化必须以 skill name 为 key
	s := Stats{
		"x": &SkillStats{Calls: []time.Time{time.Unix(100, 0)}},
		"y": &SkillStats{Calls: []time.Time{time.Unix(200, 0)}},
	}
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]*SkillStats
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal as map: %v\nbody: %s", err, body)
	}
	if got["x"] == nil || len(got["x"].Calls) != 1 {
		t.Errorf("x lost: %+v", got["x"])
	}
	if got["y"] == nil || len(got["y"].Calls) != 1 {
		t.Errorf("y lost: %+v", got["y"])
	}
}
