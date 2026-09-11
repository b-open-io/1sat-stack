package txo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

func TestSearchOmitsDeprecatedListings(t *testing.T) {
	t.Parallel()

	db, err := store.NewBadgerStoreFromConfig(&store.BadgerConfig{InMemory: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	outputs := NewOutputStore(db, nil, nil)
	ctx := context.Background()
	for i, events := range [][]string{
		{"ordlock", "own:1BuyerAddress", "ordlock:spnd"},
		{"ordlock", "own:1OtherAddress", "ordlock:spnd"},
		{"type:image/png", "own:1BuyerAddress"},
	} {
		op := transaction.Outpoint{Index: uint32(i)}
		if err := outputs.SaveOutput(ctx, &IndexedOutput{Outpoint: op, Events: events}, 1, float64(i+1)); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if err := db.ZAdd(ctx, []byte("tp:tm_ordlock"), store.ScoredMember{Member: op.Bytes(), Score: float64(i + 1)}); err != nil {
				t.Fatal(err)
			}
			// Reindex a deprecated listing without adding its old public event.
			// Existing event and topic memberships remain available for recovery.
			if err := outputs.SaveOutput(ctx, &IndexedOutput{
				Outpoint: op,
				Events:   []string{"1sat", events[1]},
				Data:     map[string]any{"ordlock": map[string]any{"price": 1000}},
			}, 1, float64(i+1)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ZScore(ctx, KeyEvent("ordlock"), op.Bytes()); err != nil {
				t.Fatalf("retained listing membership: %v", err)
			}
		}
	}

	app := fiber.New()
	for _, prefix := range []string{"/txo", "/1sat/txo"} {
		NewRoutes(outputs).Register(app.Group(prefix))
	}

	tests := []struct {
		name  string
		query string
		want  []uint32
	}{
		{name: "bare event", query: "key=ordlock"},
		{name: "prefixed event", query: "key=ev:ordlock"},
		{name: "topic", query: "key=tp:tm_ordlock"},
		{name: "unscoped intersection", query: "key=ordlock&key=tp:tm_ordlock&join=intersect"},
		{name: "default union with owner", query: "key=ordlock&key=own:1BuyerAddress"},
		{name: "explicit union with owner", query: "key=ev:ordlock&key=ev:own:1BuyerAddress&join=union"},
		{name: "unknown join defaults to union", query: "key=ordlock&key=own:1BuyerAddress&join=other"},
		{name: "topic union with owner", query: "key=tp:tm_ordlock&key=own:1BuyerAddress"},
		{name: "event difference", query: "key=ordlock&key=own:1BuyerAddress&join=difference"},
		{name: "owner difference", query: "key=own:1BuyerAddress&key=ordlock&join=difference"},
		{name: "topic difference", query: "key=tp:tm_ordlock&key=own:1BuyerAddress&join=difference"},
		{name: "union with unrelated key", query: "key=ordlock&key=own:1BuyerAddress&key=type:image/png"},
		{name: "topic is not owner event", query: "key=ordlock&key=tp:own:1BuyerAddress&join=intersect"},
		{name: "owner only", query: "key=own:1BuyerAddress", want: []uint32{0, 2}},
		{name: "prefixed owner", query: "key=ev:own:1BuyerAddress", want: []uint32{0, 2}},
		{name: "owner union", query: "key=own:1BuyerAddress&key=own:1OtherAddress", want: []uint32{0, 1, 2}},
		{name: "owner event intersection", query: "key=ev:ordlock&key=own:1BuyerAddress&join=intersect", want: []uint32{0}},
		{name: "owner first intersection", query: "key=ev:own:1BuyerAddress&key=ordlock&join=intersect", want: []uint32{0}},
		{name: "owner topic intersection", query: "key=tp:tm_ordlock&key=ev:own:1BuyerAddress&join=intersect", want: []uint32{0}},
		{name: "owner event and topic intersection", query: "key=own:1BuyerAddress&key=ordlock&key=tp:tm_ordlock&join=intersect", want: []uint32{0}},
		{name: "unrelated event", query: "key=type:image/png", want: []uint32{2}},
		{name: "historical event", query: "key=ev:ordlock:spnd", want: []uint32{0, 1}},
	}

	for _, prefix := range []string{"/txo", "/1sat/txo"} {
		for _, tt := range tests {
			t.Run(prefix+"/"+tt.name, func(t *testing.T) {
				resp, err := app.Test(httptest.NewRequest(http.MethodGet, prefix+"/search?"+tt.query, nil))
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", resp.StatusCode)
				}
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				var got []IndexedOutputResponse
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				var indexes []uint32
				for _, out := range got {
					op, err := transaction.OutpointFromString(out.Outpoint)
					if err != nil {
						t.Fatal(err)
					}
					indexes = append(indexes, op.Index)
				}
				if !slices.Equal(indexes, tt.want) {
					t.Fatalf("indexes = %v, want %v", indexes, tt.want)
				}
				if tt.want == nil && string(body) != "[]" {
					t.Fatalf("omitted listings = %s, want []", body)
				}
			})
		}
		t.Run(prefix+"/outpoint recovery", func(t *testing.T) {
			op := transaction.Outpoint{}
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, prefix+"/"+op.String()+"?tags=ordlock&events=true", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			var got IndexedOutputResponse
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.Outpoint != op.String() || got.Data["ordlock"] == nil || slices.Contains(got.Events, "ordlock") {
				t.Fatalf("reindexed listing = %+v", got)
			}
		})
	}
}
