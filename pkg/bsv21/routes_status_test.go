package bsv21

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	lookuppkg "github.com/b-open-io/1sat-stack/pkg/lookup"
	storage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

func TestOutputStatus(t *testing.T) {
	ctx := context.Background()
	tokenId := strings.Repeat("ab", 32) + "_0"

	f, err := storage.NewSQLiteFactory(t.TempDir() + "/topic")
	if err != nil {
		t.Fatal(err)
	}
	factory := f.Factory()
	lookup := lookuppkg.NewBSV21Lookup(factory)

	s, err := store.NewBadgerStore("badger://?memory=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	outpoint := func(b byte) *transaction.Outpoint {
		txid := chainhash.Hash{}
		txid[0] = b
		return &transaction.Outpoint{Txid: txid, Index: 0}
	}
	valid, spent, queued, unknown := outpoint(1), outpoint(2), outpoint(3), outpoint(4)

	// LoadOutputs creates the token_outputs schema for the topic.
	if _, err := lookup.LoadOutputs(ctx, tokenId, []*transaction.Outpoint{valid}); err != nil {
		t.Fatal(err)
	}
	ts, err := factory("tm_" + tokenId)
	if err != nil {
		t.Fatal(err)
	}
	spendTxid := chainhash.Hash{0xff}
	for _, row := range []struct {
		op    *transaction.Outpoint
		spend []byte
	}{{valid, nil}, {spent, spendTxid[:]}} {
		if _, err := ts.DB().Exec(
			`INSERT INTO token_outputs (outpoint, token_id, op, lock_type, address, amount, spend_txid, score) VALUES (?, ?, 'transfer', 'p2pkh', 'addr', '1', ?, 1)`,
			row.op.Bytes(), tokenId, row.spend,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ZAdd(ctx, []byte("q:tm_"+tokenId), store.ScoredMember{Member: queued.Bytes(), Score: 1}); err != nil {
		t.Fatal(err)
	}

	manager := &TokenManager{store: s, logger: slog.Default()}
	app := fiber.New()
	NewRoutes(&RoutesDeps{Lookup: lookup, Manager: manager}).Register(app)

	body, _ := json.Marshal([]string{valid.String(), spent.String(), queued.String(), unknown.String()})
	req := httptest.NewRequest("POST", "/"+tokenId+"/outputs/status", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	var got []OutputStatusResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{OutputValid, OutputSpent, OutputQueued, OutputUnknown}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d: %s", len(got), len(want), raw)
	}
	for i, w := range want {
		if got[i].State != w {
			t.Errorf("result %d (%s): state %q, want %q", i, got[i].Outpoint, got[i].State, w)
		}
	}
}
