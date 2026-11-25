package owner

import "testing"

func TestIncludeJungleBusTx(t *testing.T) {
	tests := []struct {
		name        string
		blockHeight uint32
		lastHeight  float64
		want        bool
	}{
		{name: "mempool always included", blockHeight: 0, lastHeight: 800_000, want: true},
		{name: "mempool included on first sync", blockHeight: 0, lastHeight: 0, want: true},
		{name: "mined at cursor included", blockHeight: 800_000, lastHeight: 800_000, want: true},
		{name: "mined after cursor included", blockHeight: 800_001, lastHeight: 800_000, want: true},
		{name: "mined before cursor skipped", blockHeight: 799_999, lastHeight: 800_000, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := includeJungleBusTx(tt.blockHeight, tt.lastHeight); got != tt.want {
				t.Fatalf("includeJungleBusTx(%d, %v) = %v, want %v", tt.blockHeight, tt.lastHeight, got, tt.want)
			}
		})
	}
}

func TestShouldSkipIngest(t *testing.T) {
	pendingMempool := float64(1_700_000_000)
	pendingConfirmed := float64(800_000)

	tests := []struct {
		name         string
		blockHeight  uint32
		pendingScore *float64
		inImmutable  bool
		want         bool
	}{
		{name: "new mempool ingested", blockHeight: 0, want: false},
		{name: "new mined ingested", blockHeight: 800_001, want: false},
		{name: "known mempool skipped on mempool fetch", blockHeight: 0, pendingScore: &pendingMempool, want: true},
		{name: "known mempool reingested on mine", blockHeight: 800_001, pendingScore: &pendingMempool, want: false},
		{name: "already confirmed pending skipped", blockHeight: 800_001, pendingScore: &pendingConfirmed, want: true},
		{name: "immutable skipped", blockHeight: 800_001, inImmutable: true, want: true},
		{name: "immutable skipped even for mempool fetch", blockHeight: 0, inImmutable: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipIngest(tt.blockHeight, tt.pendingScore, tt.inImmutable)
			if got != tt.want {
				t.Fatalf("shouldSkipIngest() = %v, want %v", got, tt.want)
			}
		})
	}
}
