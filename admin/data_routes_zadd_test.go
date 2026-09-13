package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

func newDataApp(t *testing.T) (*fiber.App, store.Store) {
	t.Helper()
	s, err := store.NewBadgerStoreFromConfig(&store.BadgerConfig{InMemory: true}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	app := fiber.New()
	NewDataRoutes(s, slog.Default()).Register(app.Group("/data"))
	return app, s
}

// zset add on a queue key stores a txid as 32 reversed bytes and an outpoint
// as 36, exactly what OverlaySync.parseQueueMember expects from the event
// bridge, so a manual replay is indistinguishable from a live event.
func TestZSetAddEncodesQueueMembersLikeTheBridge(t *testing.T) {
	app, s := newDataApp(t)
	const txid = "c705a20c429ca8b7dc0ea5048a3b0845c69fa13d1471f48740e050a8bd38d263"
	key := string(txo.KeyQueue("ordlock2"))

	for _, tc := range []struct {
		member string
		want   int
	}{{txid, 32}, {txid + "_0", 36}} {
		body, _ := json.Marshal(map[string]any{"member": tc.member, "score": 0})
		req := httptest.NewRequest("POST", "/data/zset/add/"+key, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status %d", tc.member, resp.StatusCode)
		}
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if int(out["bytes"].(float64)) != tc.want {
			t.Fatalf("%s: encoded %v bytes, want %d", tc.member, out["bytes"], tc.want)
		}
	}

	h, _ := chainhash.NewHashFromHex(txid)
	op, _ := transaction.OutpointFromString(txid + "_0")
	ctx := context.Background()
	if _, err := s.ZScore(ctx, []byte(key), h[:]); err != nil {
		t.Fatalf("txid member missing: %v", err)
	}
	if _, err := s.ZScore(ctx, []byte(key), op.Bytes()); err != nil {
		t.Fatalf("outpoint member missing: %v", err)
	}
}

func TestZSetAddRejectsMissingMember(t *testing.T) {
	app, _ := newDataApp(t)
	req := httptest.NewRequest("POST", "/data/zset/add/q:ordlock2", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != 400 {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}
