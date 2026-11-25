package sync

import (
	"testing"

	sdk "github.com/bsv-blockchain/go-sdk/wallet"
)

func TestClassifyOutputTwetch(t *testing.T) {
	c := classifyOutput([]string{"twetch"}, 2180)
	if c == nil || c.basket != "twetch" || c.protocol != sdk.InternalizeProtocolBasketInsertion {
		t.Fatalf("twetch output = %+v", c)
	}
	if len(c.tags) != 1 || c.tags[0] != "twetch" {
		t.Fatalf("tags = %v", c.tags)
	}
	if c := classifyOutput([]string{"p2pkh:1abc"}, 2180); c == nil || c.protocol != sdk.InternalizeProtocolWalletPayment {
		t.Fatalf("plain funds = %+v", c)
	}
}
