package beef

import (
	"context"
	"sync"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
)

func TestFilesystemBeefStorage_PutConcurrentSameTxid(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFilesystemBeefStorage(dir)
	if err != nil {
		t.Fatal(err)
	}

	txid, err := chainhash.NewHashFromHex("84ab4ea33132e23da0a74bcc17021ee607b4c9740dd5bd6935cd6d6bcc7f98d1")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte{0x01, 0x02, 0x03, 0x04}

	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.Put(context.Background(), txid, payload)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Put: %v", err)
		}
	}

	got, err := store.Get(context.Background(), txid)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %v want %v", got, payload)
	}
}
