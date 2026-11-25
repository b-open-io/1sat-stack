package parse

import (
	"testing"

	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
)

// The gib events are a reader's feed, not an ingestion path: nothing
// submits them to the overlay any more, and `gib:{origin}` is what the site
// subscribes to for live updates on one repository. They must keep being
// emitted for every commit head the indexer sees.
func TestParseGibEmitsTheRepositoryFeed(t *testing.T) {
	const origin = "c657be5a7dacd7bb7343d92b7195d1366dbecd3ec31874576189efd28eee007c_0"
	const root = "e6f27ce723b2923e93227ecef64b6cecf9464bd0c6ba71a66502aa20c5de82a1_3"

	identity, _ := ec.PrivateKeyFromHex("0000000000000000000000000000000000000000000000000000000000000002")
	lockKey, _ := ec.PrivateKeyFromHex("0000000000000000000000000000000000000000000000000000000000000003")
	fields, err := gibtpl.Fields(origin, "main", root, identity.PubKey(), "")
	if err != nil {
		t.Fatal(err)
	}
	head, err := gibtpl.LockingScript(lockKey.PubKey(), fields)
	if err != nil {
		t.Fatal(err)
	}

	res, err := ParseGib(&ParseContext{LockingScript: *head, Satoshis: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("a commit head parsed as nothing")
	}
	if res.Tag != TagGib {
		t.Fatalf("tag = %q, want %q", res.Tag, TagGib)
	}
	if len(res.Events) != 2 || res.Events[0] != "gib" || res.Events[1] != "gib:"+origin {
		t.Fatalf("events = %v, want [gib gib:%s]", res.Events, origin)
	}
	decoded, ok := res.Data.(*gibtpl.Head)
	if !ok || decoded.Branch != "main" || decoded.Root != root {
		t.Fatalf("data = %+v", res.Data)
	}

	// The same events come out of the full parser run, which is what the
	// indexer drives and what publishes to the feed.
	results, err := Parse(&ParseContext{LockingScript: *head, Satoshis: 1}, []string{TagGib})
	if err != nil {
		t.Fatal(err)
	}
	if got := results[TagGib]; got == nil || len(got.Events) != 2 {
		t.Fatalf("parse run = %+v", results[TagGib])
	}

	// Anything else is not a head, and emits nothing.
	p2pkh, _ := script.NewFromHex("76a914000000000000000000000000000000000000000088ac")
	if res, err := ParseGib(&ParseContext{LockingScript: *p2pkh, Satoshis: 1}); err != nil || res != nil {
		t.Fatalf("p2pkh parsed as gib: %+v, %v", res, err)
	}
	if res, err := ParseGib(&ParseContext{LockingScript: *head, Satoshis: 0}); err != nil || res != nil {
		t.Fatalf("zero-sat output parsed as gib: %+v, %v", res, err)
	}
}
