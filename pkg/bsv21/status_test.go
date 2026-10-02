package bsv21

import "testing"

func TestIsActiveRequiresMinFunding(t *testing.T) {
	cases := []struct {
		name        string
		credits     uint64
		outputCount int64
		want        bool
	}{
		{"below minimum", 9_999_000, 0, false},
		{"at minimum", 10_000_000, 0, true},
		{"minimum met, fees used up", 10_000_000, 10_000, false},
		{"minimum met, balance left", 10_000_000, 9_999, true},
	}
	for _, c := range cases {
		ts := NewTokenStatus("t", "a", c.credits, c.outputCount, 1000, 10_000_000, false, false)
		if got := ts.IsActive(); got != c.want {
			t.Errorf("%s: IsActive() = %v, want %v", c.name, got, c.want)
		}
	}

	if !NewTokenStatus("t", "a", 0, 0, 0, 0, true, false).IsActive() {
		t.Error("whitelisted token should be active without funding")
	}
}
