package ecosystemalias

import (
	"errors"
	"reflect"
	"testing"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestTopicManagerIdentifyAdmissibleOutputs(t *testing.T) {
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()
	validFields := decodeFirstPositiveFields(t)
	valid := decodeBuildLockAfter(t, validFields, owner, 3)
	secondValid := topicSignedScript(t, "lkup", "xn--bcher-kva.example", owner)

	wrongProtocolFields := decodeCloneFields(validFields)
	wrongProtocolFields[FieldProtocol] = []byte("not-ecosystem-alias")
	wrongProtocol := decodeBuildLockAfter(t, wrongProtocolFields, owner, 3)

	badSignatureFields := decodeCloneFields(validFields)
	badSignatureFields[FieldSignature] = []byte{0xde, 0xad, 0xbe, 0xef}
	badSignature := decodeBuildLockAfter(t, badSignatureFields, owner, 3)

	tests := []struct {
		name    string
		outputs []*transaction.TransactionOutput
		want    []uint32
	}{
		{
			name: "mixed valid invalid and nonmatching outputs",
			outputs: []*transaction.TransactionOutput{
				{Satoshis: 1, LockingScript: &script.Script{script.OpTRUE}},
				{Satoshis: 1, LockingScript: valid},
				{Satoshis: 1, LockingScript: wrongProtocol},
				{Satoshis: 0, LockingScript: valid},
				{Satoshis: 100, LockingScript: secondValid},
			},
			want: []uint32{1, 4},
		},
		{
			name: "multiple conflicting valid claims",
			outputs: []*transaction.TransactionOutput{
				{Satoshis: 1, LockingScript: valid},
				{Satoshis: 1, LockingScript: topicSignedScript(t, "handcash", "other.example", owner)},
				{Satoshis: 1, LockingScript: topicSignedScript(t, "other", "handcash.io", owner)},
			},
			want: []uint32{0, 1, 2},
		},
		{
			name: "malformed lock-after shape",
			outputs: []*transaction.TransactionOutput{
				{Satoshis: 1, LockingScript: decodeBuildLockAfter(t, validFields, owner, 2)},
			},
			want: nil,
		},
		{
			name: "malformed signature",
			outputs: []*transaction.TransactionOutput{
				{Satoshis: 1, LockingScript: badSignature},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beef, txid := topicTestBeef(t, tt.outputs)
			got, err := (&TopicManager{}).IdentifyAdmissibleOutputs(t.Context(), beef, txid, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.OutputsToAdmit, tt.want) {
				t.Fatalf("outputs admitted %v, want %v", got.OutputsToAdmit, tt.want)
			}
			if got.CoinsToRetain != nil || got.CoinsRemoved != nil || got.AncillaryTxids != nil {
				t.Fatalf("unexpected non-output instructions: %+v", got)
			}
		})
	}
}

func TestTopicManagerRejectsMissingTransaction(t *testing.T) {
	missing := &chainhash.Hash{0x01}
	otherBeef, _ := topicTestBeef(t, []*transaction.TransactionOutput{{
		Satoshis:      1,
		LockingScript: &script.Script{script.OpTRUE},
	}})

	tests := []struct {
		name string
		beef *transaction.Beef
		txid *chainhash.Hash
	}{
		{name: "nil beef", beef: nil, txid: missing},
		{name: "nil txid", beef: transaction.NewBeef(), txid: nil},
		{name: "empty beef", beef: transaction.NewBeef(), txid: missing},
		{name: "different transaction", beef: otherBeef, txid: missing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&TopicManager{}).IdentifyAdmissibleOutputs(t.Context(), tt.beef, tt.txid, nil)
			if !errors.Is(err, engine.ErrInvalidBeef) {
				t.Fatalf("error %v, want %v", err, engine.ErrInvalidBeef)
			}
			if !reflect.DeepEqual(got, overlay.AdmittanceInstructions{}) {
				t.Fatalf("unexpected instructions for invalid BEEF: %+v", got)
			}
		})
	}
}

func TestTopicManagerIdentifyNeededInputs(t *testing.T) {
	beef, txid := topicTestBeef(t, []*transaction.TransactionOutput{{
		Satoshis:      1,
		LockingScript: &script.Script{script.OpTRUE},
	}})

	tests := []struct {
		name string
		beef *transaction.Beef
		txid *chainhash.Hash
	}{
		{name: "submitted transaction", beef: beef, txid: txid},
		{name: "empty beef", beef: transaction.NewBeef(), txid: &chainhash.Hash{0x02}},
		{name: "nil arguments", beef: nil, txid: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&TopicManager{}).IdentifyNeededInputs(t.Context(), tt.beef, tt.txid)
			if err != nil {
				t.Fatal(err)
			}
			if got != nil {
				t.Fatalf("needed inputs %v, want nil", got)
			}
		})
	}
}

func TestTopicManagerDoesNotRetainSpentAliasInputs(t *testing.T) {
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()
	prior := transaction.NewTransaction()
	prior.Outputs = []*transaction.TransactionOutput{{
		Satoshis:      1,
		LockingScript: decodeBuildLockAfter(t, decodeFirstPositiveFields(t), owner, 3),
	}}

	spend := transaction.NewTransaction()
	spend.AddInputFromTx(prior, 0, nil)
	spend.Outputs = []*transaction.TransactionOutput{{
		Satoshis:      1,
		LockingScript: topicSignedScript(t, "replacement", "replacement.example", owner),
	}}
	beef := transaction.NewBeef()
	if _, err := beef.MergeTransaction(spend); err != nil {
		t.Fatal(err)
	}
	txid := spend.TxID()

	tests := []struct {
		name          string
		previousCoins []uint32
	}{
		{name: "spent alias input", previousCoins: []uint32{0}},
		{name: "spent alias input with unrelated prior coin", previousCoins: []uint32{0, 99}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&TopicManager{}).IdentifyAdmissibleOutputs(t.Context(), beef, txid, tt.previousCoins)
			if err != nil {
				t.Fatal(err)
			}
			if got.CoinsToRetain != nil {
				t.Fatalf("retained coins %v, want none", got.CoinsToRetain)
			}
			if !reflect.DeepEqual(got.OutputsToAdmit, []uint32{0}) {
				t.Fatalf("outputs admitted %v, want [0]", got.OutputsToAdmit)
			}
		})
	}
}

func TestTopicManagerMetadataUsesExactTopic(t *testing.T) {
	metadata := (&TopicManager{}).GetMetaData()
	if metadata == nil {
		t.Fatal("metadata must not be nil")
	}
	if metadata.Name != TopicName {
		t.Fatalf("metadata name %q, want %q", metadata.Name, TopicName)
	}
	if (&TopicManager{}).GetDocumentation() == "" {
		t.Fatal("documentation must not be empty")
	}
}

func topicSignedScript(t *testing.T, alias, domain string, owner []byte) *script.Script {
	t.Helper()
	signer := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000001")
	certifier := signer.PubKey().Compressed()
	var certifierKey [CertifierKeyLen]byte
	copy(certifierKey[:], certifier)
	digest := Digest(alias, domain, certifierKey)
	signature, err := signer.Sign(digest[:])
	if err != nil {
		t.Fatal(err)
	}
	fields := [][]byte{
		[]byte(ProtocolName),
		[]byte(ProtocolVersion),
		[]byte(alias),
		[]byte(domain),
		certifier,
		signature.Serialize(),
	}
	return decodeBuildLockAfter(t, fields, owner, 3)
}

func topicTestBeef(t *testing.T, outputs []*transaction.TransactionOutput) (*transaction.Beef, *chainhash.Hash) {
	t.Helper()
	tx := transaction.NewTransaction()
	tx.Outputs = outputs
	beef := transaction.NewBeef()
	if _, err := beef.MergeTransaction(tx); err != nil {
		t.Fatal(err)
	}
	return beef, tx.TxID()
}
