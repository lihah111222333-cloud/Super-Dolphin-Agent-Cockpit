package probe

import "testing"

func TestAdd(t *testing.T) {
	c := &Calculator{}
	if r := c.Add(5); r != 5 {
		t.Errorf("got %f want 5", r)
	}
}
