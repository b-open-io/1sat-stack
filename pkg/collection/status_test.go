package collection

import (
	"encoding/json"
	"testing"
)

func TestCollectionStatusJSON(t *testing.T) {
	s := &CollectionStatus{
		CollectionID:  "abc_0",
		FeeAddress:    "1Addr",
		Name:          "Cats",
		IsWhitelisted: false,
		Credits:       5000,
		FeePerOutput:  1000,
	}
	s.SetOutputCount(2)
	s.UpdateBalance(int64(s.Credits) - s.Debits())

	if !s.IsActive() {
		t.Fatal("expected funded collection to be active")
	}
	if got := s.RecordOutput(); got != 2000 {
		t.Fatalf("balance after charge = %d, want 2000", got)
	}

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"collection_id", "fee_address", "credits", "fee_per_output", "output_count", "debits", "balance", "is_active"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing %s in %s", key, raw)
		}
	}
	if got["is_active"] != true {
		t.Fatalf("is_active = %v, want true", got["is_active"])
	}
	if got["output_count"] != float64(3) || got["balance"] != float64(2000) {
		t.Fatalf("count/balance = %v/%v", got["output_count"], got["balance"])
	}
}

func TestCollectionStatusWhitelistIgnoresBalance(t *testing.T) {
	s := &CollectionStatus{IsWhitelisted: true}
	if !s.IsActive() {
		t.Fatal("whitelist must stay active with a zero balance")
	}
	s.IsWhitelisted = false
	s.IsBlacklisted = true
	s.UpdateBalance(100)
	if s.IsActive() {
		t.Fatal("blacklist must stay inactive")
	}
}
