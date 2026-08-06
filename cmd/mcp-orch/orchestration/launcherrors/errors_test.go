package launcherrors

import "testing"

func TestLaunchPatternFactoriesReturnIndependentCollections(t *testing.T) {
	permanent := permanentLaunchPatterns()
	otherPermanent := permanentLaunchPatterns()
	transient := transientLaunchPatterns()
	otherTransient := transientLaunchPatterns()

	if len(permanent) == 0 || len(transient) == 0 {
		t.Fatal("launch pattern collection is empty")
	}
	permanent[0] = "mutated-permanent"
	transient[0] = "mutated-transient"
	if otherPermanent[0] == "mutated-permanent" {
		t.Fatal("permanent patterns share mutable backing storage")
	}
	if otherTransient[0] == "mutated-transient" {
		t.Fatal("transient patterns share mutable backing storage")
	}
}
