package txo

import "testing"

func TestIsPublicOrdLockSearch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys [][]byte
		want bool
	}{
		{name: "bare event", keys: [][]byte{[]byte("ordlock")}, want: true},
		{name: "prefixed event", keys: [][]byte{[]byte("ev:ordlock")}, want: true},
		{name: "owner only", keys: [][]byte{[]byte("own:1BuyerAddress")}, want: false},
		{name: "prefixed owner", keys: [][]byte{[]byte("ev:own:1BuyerAddress")}, want: false},
		{name: "owner intersect listing event", keys: [][]byte{[]byte("ev:ordlock"), []byte("own:1BuyerAddress")}, want: false},
		{name: "unrelated event", keys: [][]byte{[]byte("ev:type:image/png")}, want: false},
		{name: "empty", keys: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPublicOrdLockSearch(tt.keys); got != tt.want {
				t.Fatalf("isPublicOrdLockSearch(%q) = %v, want %v", tt.keys, got, tt.want)
			}
		})
	}
}
