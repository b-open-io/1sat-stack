package ordlock

import (
	"bytes"
	"slices"
	"testing"

	template "github.com/b-open-io/1sat-stack/pkg/template/ordlock"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func v2TestScript(t *testing.T) *script.Script {
	t.Helper()
	p2pkh, err := script.NewFromHex("76a914" + "2222222222222222222222222222222222222222" + "88ac")
	if err != nil {
		t.Fatal(err)
	}
	payout := &transaction.TransactionOutput{Satoshis: 1000, LockingScript: p2pkh}
	args := [][]byte{bytes.Repeat([]byte{0x11}, 20), payout.Bytes()}
	scr := script.NewFromBytes(nil)
	from := 0
	for _, slot := range template.OrdLockV2Slots {
		*scr = append(*scr, template.OrdLockV2Template[from:slot.ByteOffset]...)
		if err := scr.AppendPushData(args[slot.ParamIndex]); err != nil {
			t.Fatal(err)
		}
		from = slot.ByteOffset + 1
	}
	*scr = append(*scr, template.OrdLockV2Template[from:]...)
	return scr
}

func TestV2AdmissionAndRetentionRequireCompleteListings(t *testing.T) {
	valid := v2TestScript(t)
	prior := transaction.NewTransaction()
	prior.Outputs = []*transaction.TransactionOutput{
		{Satoshis: 1, LockingScript: valid},
		{Satoshis: 1, LockingScript: script.NewFromBytes(template.OrdLockV2Prefix)},
		{Satoshis: 1, LockingScript: script.NewFromBytes(append(bytes.Clone(*valid), 0x00))},
		{Satoshis: 2, LockingScript: valid},
		{Satoshis: 1, LockingScript: script.NewFromBytes(template.OrdLockPrefix)},
	}
	spend := transaction.NewTransaction()
	spend.Outputs = prior.Outputs
	for i := range prior.Outputs {
		spend.AddInputFromTx(prior, uint32(i), nil)
	}
	beef := transaction.NewBeef()
	if _, err := beef.MergeTransaction(spend); err != nil {
		t.Fatal(err)
	}
	tm := &TopicManagerV2{}
	admit, err := tm.IdentifyAdmissibleOutputs(t.Context(), beef, spend.TxID(), []uint32{0, 1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	// Outputs 0 and 2 are listings (2 carries trailing data after the template,
	// which recognition ignores); 1 and 4 are bare prefixes, 3 is not 1 sat.
	if !slices.Equal(admit.OutputsToAdmit, []uint32{0, 2}) || !slices.Equal(admit.CoinsToRetain, []uint32{0, 2}) {
		t.Fatalf("admittance = %+v, want outputs/inputs 0 and 2", admit)
	}
	needed, err := tm.IdentifyNeededInputs(t.Context(), beef, spend.TxID())
	if err != nil {
		t.Fatal(err)
	}
	want0 := transaction.Outpoint{Txid: *prior.TxID(), Index: 0}
	want2 := transaction.Outpoint{Txid: *prior.TxID(), Index: 2}
	if len(needed) != 2 || *needed[0] != want0 || *needed[1] != want2 {
		t.Fatalf("needed inputs = %v, want %s and %s", needed, want0.String(), want2.String())
	}
}
