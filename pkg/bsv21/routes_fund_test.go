package bsv21

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

func TestFundingNeeded(t *testing.T) {
	const fee, min = 1000, 10_000_000
	headroom := int64(fundingHeadroomOutputs * fee)
	cases := []struct {
		name        string
		credits     uint64
		outputCount int64
		queueDepth  int64
		want        int64
	}{
		{"unfunded, small backlog: minimum governs", 0, 0, 5, min + headroom},
		{"unfunded, backlog above minimum", 0, 0, 20_000, 20_000*fee + headroom},
		{"partly funded toward minimum", 4_000_000, 0, 5, min - 4_000_000 + headroom},
		{"active, balance covers backlog", min, 100, 5, 0},
		{"active, backlog exceeds balance", min, 9_000, 2_000, 2_000*fee - (min - 9_000*fee) + headroom},
		{"minimum met, fees used up", min, 10_000, 0, headroom},
	}
	for _, c := range cases {
		status := NewTokenStatus("t", "a", c.credits, c.outputCount, fee, min, false, false)
		if got := fundingNeeded(status, c.queueDepth); got != c.want {
			t.Errorf("%s: fundingNeeded = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestPostFundingValidatesPayment(t *testing.T) {
	tokenId := strings.Repeat("cd", 32) + "_0"
	op, _ := transaction.OutpointFromString(tokenId)
	feeAddress, err := GenerateFeeAddress(op)
	if err != nil {
		t.Fatal(err)
	}
	feeScript, err := feeLockingScript(feeAddress)
	if err != nil {
		t.Fatal(err)
	}

	beefPaying := func(lockingScript *script.Script) []byte {
		parent := transaction.NewTransaction()
		parent.AddOutput(&transaction.TransactionOutput{Satoshis: 20_000, LockingScript: feeScript})
		tx := transaction.NewTransaction()
		tx.AddInput(&transaction.TransactionInput{
			SourceTXID:        parent.TxID(),
			SourceTransaction: parent,
			SourceTxOutIndex:  0,
			UnlockingScript:   &script.Script{},
		})
		tx.AddOutput(&transaction.TransactionOutput{Satoshis: 15_000, LockingScript: lockingScript})
		b, err := tx.BEEF()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	app := fiber.New()
	NewRoutes(&RoutesDeps{}).Register(app)
	post := func(body []byte) (int, string) {
		req := httptest.NewRequest("POST", "/"+tokenId+"/fund", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		var e ErrorResponse
		_ = json.Unmarshal(raw, &e)
		return resp.StatusCode, e.Message
	}

	if code, msg := post([]byte("not beef")); code != 400 || !strings.HasPrefix(msg, "invalid BEEF") {
		t.Errorf("garbage body: %d %q", code, msg)
	}
	other := &script.Script{script.OpTRUE}
	if code, msg := post(beefPaying(other)); code != 400 || msg != "transaction does not pay the token's fee address" {
		t.Errorf("non-paying tx: %d %q", code, msg)
	}
	// A paying tx passes validation and stops at the missing broadcaster.
	if code, msg := post(beefPaying(feeScript)); code != 503 {
		t.Errorf("paying tx: %d %q, want 503", code, msg)
	}
}

func TestTruncateUTF8(t *testing.T) {
	if got := truncateUTF8("EMST", 32); got != "EMST" {
		t.Errorf("short label changed: %q", got)
	}
	got := truncateUTF8(strings.Repeat("🪙", 10), 32) // 40 bytes of 4-byte runes
	if len(got) != 32 || !utf8.ValidString(got) {
		t.Errorf("got %q: %d bytes, valid %v", got, len(got), utf8.ValidString(got))
	}
	got = truncateUTF8("ab"+strings.Repeat("🪙", 10), 32) // rune boundary falls before 32
	if len(got) != 30 || !utf8.ValidString(got) {
		t.Errorf("got %q: %d bytes, valid %v", got, len(got), utf8.ValidString(got))
	}
}
