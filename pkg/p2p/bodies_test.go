package p2p

import (
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestRequestNodeBody_RoundTrip(t *testing.T) {
	body := &RequestNodeBody{
		GraphID:  transaction.Outpoint{Txid: randomHash(t), Index: 0},
		Outpoint: transaction.Outpoint{Txid: randomHash(t), Index: 2},
		Metadata: true,
	}

	data := body.Serialize()
	if len(data) != requestNodeBodySize {
		t.Fatalf("expected %d bytes, got %d", requestNodeBodySize, len(data))
	}

	got, err := DeserializeRequestNodeBody(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.GraphID != body.GraphID {
		t.Errorf("graphID mismatch")
	}
	if got.Outpoint != body.Outpoint {
		t.Errorf("outpoint mismatch")
	}
	if got.Metadata != body.Metadata {
		t.Errorf("metadata: want %v, got %v", body.Metadata, got.Metadata)
	}
}

func TestRequestNodeBody_WrongSize(t *testing.T) {
	_, err := DeserializeRequestNodeBody([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for wrong size")
	}
}

func TestNodeBody_RoundTrip(t *testing.T) {
	rawTx := make([]byte, 250)
	for i := range rawTx {
		rawTx[i] = byte(i)
	}
	proof := []byte{0xAA, 0xBB, 0xCC}

	body := &NodeBody{
		GraphID:        transaction.Outpoint{Txid: randomHash(t), Index: 0},
		OutputIndex:    1,
		RawTx:          rawTx,
		Proof:          proof,
		TxMetadata:     []byte("tx-meta"),
		OutputMetadata: []byte("out-meta"),
		Inputs: [][]byte{
			[]byte("input-hash-1"),
			[]byte("input-hash-2"),
		},
	}

	data := body.Serialize()
	got, err := DeserializeNodeBody(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.GraphID != body.GraphID {
		t.Errorf("graphID mismatch")
	}
	if got.OutputIndex != body.OutputIndex {
		t.Errorf("outputIndex: want %d, got %d", body.OutputIndex, got.OutputIndex)
	}
	assertBytes(t, "rawTx", body.RawTx, got.RawTx)
	assertBytes(t, "proof", body.Proof, got.Proof)
	assertBytes(t, "txMetadata", body.TxMetadata, got.TxMetadata)
	assertBytes(t, "outputMetadata", body.OutputMetadata, got.OutputMetadata)
	if len(got.Inputs) != 2 {
		t.Fatalf("inputs: want 2, got %d", len(got.Inputs))
	}
	assertBytes(t, "input[0]", body.Inputs[0], got.Inputs[0])
	assertBytes(t, "input[1]", body.Inputs[1], got.Inputs[1])
}

func TestNodeBody_Empty(t *testing.T) {
	body := &NodeBody{
		GraphID:     transaction.Outpoint{Txid: randomHash(t), Index: 0},
		OutputIndex: 0,
	}

	data := body.Serialize()
	got, err := DeserializeNodeBody(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.RawTx) != 0 {
		t.Errorf("expected empty rawTx")
	}
	if len(got.Proof) != 0 {
		t.Errorf("expected empty proof")
	}
	if len(got.Inputs) != 0 {
		t.Errorf("expected empty inputs")
	}
}

func TestNodeBody_LargeRawTx(t *testing.T) {
	rawTx := make([]byte, 1_000_000) // 1MB tx
	for i := range rawTx {
		rawTx[i] = byte(i % 256)
	}

	body := &NodeBody{
		GraphID:     transaction.Outpoint{Txid: randomHash(t), Index: 0},
		OutputIndex: 0,
		RawTx:       rawTx,
	}

	data := body.Serialize()
	got, err := DeserializeNodeBody(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.RawTx) != len(rawTx) {
		t.Fatalf("rawTx length: want %d, got %d", len(rawTx), len(got.RawTx))
	}
}

func TestInitialRequestBody_RoundTrip(t *testing.T) {
	body := &InitialRequestBody{
		Version: 1,
		Since:   1710460800.0,
		Limit:   100,
	}

	data := body.Serialize()
	if len(data) != initialRequestBodySize {
		t.Fatalf("expected %d bytes, got %d", initialRequestBodySize, len(data))
	}

	got, err := DeserializeInitialRequestBody(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.Version != body.Version {
		t.Errorf("version: want %d, got %d", body.Version, got.Version)
	}
	if got.Since != body.Since {
		t.Errorf("since: want %f, got %f", body.Since, got.Since)
	}
	if got.Limit != body.Limit {
		t.Errorf("limit: want %d, got %d", body.Limit, got.Limit)
	}
}

func TestInitialResponseBody_RoundTrip(t *testing.T) {
	body := &InitialResponseBody{
		Since: 1710460800.0,
		UTXOList: []UTXOEntry{
			{Txid: randomHash(t), OutputIndex: 0, Score: 100.0},
			{Txid: randomHash(t), OutputIndex: 1, Score: 200.5},
			{Txid: randomHash(t), OutputIndex: 3, Score: 300.0},
		},
	}

	data := body.Serialize()
	got, err := DeserializeInitialResponseBody(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.Since != body.Since {
		t.Errorf("since: want %f, got %f", body.Since, got.Since)
	}
	if len(got.UTXOList) != 3 {
		t.Fatalf("utxo count: want 3, got %d", len(got.UTXOList))
	}
	for i, u := range got.UTXOList {
		if u.Txid != body.UTXOList[i].Txid {
			t.Errorf("utxo[%d] txid mismatch", i)
		}
		if u.OutputIndex != body.UTXOList[i].OutputIndex {
			t.Errorf("utxo[%d] outputIndex: want %d, got %d", i, body.UTXOList[i].OutputIndex, u.OutputIndex)
		}
		if u.Score != body.UTXOList[i].Score {
			t.Errorf("utxo[%d] score: want %f, got %f", i, body.UTXOList[i].Score, u.Score)
		}
	}
}

func TestInitialResponseBody_Empty(t *testing.T) {
	body := &InitialResponseBody{Since: 0}

	data := body.Serialize()
	got, err := DeserializeInitialResponseBody(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.UTXOList) != 0 {
		t.Errorf("expected empty UTXO list")
	}
}

func TestInitialReplyBody_RoundTrip(t *testing.T) {
	body := &InitialReplyBody{
		UTXOList: []UTXOEntry{
			{Txid: randomHash(t), OutputIndex: 0, Score: 50.0},
		},
	}

	data := body.Serialize()
	got, err := DeserializeInitialReplyBody(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.UTXOList) != 1 {
		t.Fatalf("utxo count: want 1, got %d", len(got.UTXOList))
	}
	if got.UTXOList[0].Score != 50.0 {
		t.Errorf("score: want 50.0, got %f", got.UTXOList[0].Score)
	}
}

func TestNodeResponseBody_RoundTrip(t *testing.T) {
	body := &NodeResponseBody{
		RequestedInputs: []RequestedInput{
			{Outpoint: transaction.Outpoint{Txid: randomHash(t), Index: 0}, Metadata: true},
			{Outpoint: transaction.Outpoint{Txid: randomHash(t), Index: 1}, Metadata: false},
		},
	}

	data := body.Serialize()
	got, err := DeserializeNodeResponseBody(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.RequestedInputs) != 2 {
		t.Fatalf("count: want 2, got %d", len(got.RequestedInputs))
	}
	for i, ri := range got.RequestedInputs {
		if ri.Outpoint != body.RequestedInputs[i].Outpoint {
			t.Errorf("input[%d] outpoint mismatch", i)
		}
		if ri.Metadata != body.RequestedInputs[i].Metadata {
			t.Errorf("input[%d] metadata: want %v, got %v", i, body.RequestedInputs[i].Metadata, ri.Metadata)
		}
	}
}

func TestNodeResponseBody_Empty(t *testing.T) {
	body := &NodeResponseBody{}

	data := body.Serialize()
	got, err := DeserializeNodeResponseBody(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.RequestedInputs) != 0 {
		t.Errorf("expected empty inputs")
	}
}

// ensure chainhash is used (it's imported for randomHash in message_test.go)
var _ chainhash.Hash
