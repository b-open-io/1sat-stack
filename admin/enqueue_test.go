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
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

func newEnqueueApp(t *testing.T) (*fiber.App, store.Store) {
	t.Helper()
	s, err := store.NewBadgerStoreFromConfig(&store.BadgerConfig{InMemory: true}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := &Routes{store: s, logger: slog.Default()}
	app := fiber.New()
	app.Post("/queue/:name", r.handleEnqueue)
	return app, s
}

// A txid lands on q:<name> as 32 raw bytes and an outpoint as 36, exactly the
// encoding OverlaySync.parseQueueMember expects from the event bridge.
func TestEnqueueEncodesLikeTheEventBridge(t *testing.T) {
	app, s := newEnqueueApp(t)
	const txid = "c705a20c429ca8b7dc0ea5048a3b0845c69fa13d1471f48740e050a8bd38d263"

	for _, tc := range []struct {
		member string
		want   int
	}{{txid, 32}, {txid + "_0", 36}} {
		body, _ := json.Marshal(map[string]string{"member": tc.member})
		req := httptest.NewRequest("POST", "/queue/ordlock2", bytes.NewReader(body))
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

	op, _ := transaction.OutpointFromString(txid + "_0")
	members, err := s.ZRange(context.Background(), txo.KeyQueue("ordlock2"), store.ScoreRange{})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, m := range members {
		if len(m.Member) == 32 || bytes.Equal(m.Member, op.Bytes()) {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("queue members = %d, want the txid and the outpoint", len(members))
	}
}

func TestEnqueueRejectsBadInput(t *testing.T) {
	app, _ := newEnqueueApp(t)
	for _, body := range []string{`{}`, `{"member":"nope"}`, `{"member":"zz"}`} {
		req := httptest.NewRequest("POST", "/queue/ordlock2", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		if resp.StatusCode != 400 {
			t.Fatalf("%s: status %d, want 400", body, resp.StatusCode)
		}
	}
}
