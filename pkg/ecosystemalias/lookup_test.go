package ecosystemalias

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	_ "github.com/mattn/go-sqlite3"
)

type outputLoadCall struct {
	outpoint    transaction.Outpoint
	topic       string
	spent       *bool
	includeBEEF bool
}

type fakeOutputLoader struct {
	mu             sync.Mutex
	outputs        map[string]*engine.Output
	mergeAncillary map[string]*transaction.Beef
	findErrors     map[string]error
	ancillaryErrs  map[string]error
	findCalls      []outputLoadCall
	ancillarySeen  []string
}

func (f *fakeOutputLoader) FindOutput(_ context.Context, outpoint *transaction.Outpoint, topic *string, spent *bool, includeBEEF bool) (*engine.Output, error) {
	call := outputLoadCall{includeBEEF: includeBEEF}
	if outpoint != nil {
		call.outpoint = *outpoint
	}
	if topic != nil {
		call.topic = *topic
	}
	if spent != nil {
		value := *spent
		call.spent = &value
	}
	f.mu.Lock()
	f.findCalls = append(f.findCalls, call)
	f.mu.Unlock()
	if outpoint == nil {
		return nil, nil
	}
	key := outpoint.String()
	if err := f.findErrors[key]; err != nil {
		return nil, err
	}
	output := f.outputs[key]
	if output == nil {
		return nil, nil
	}
	copy := *output
	if spent != nil && copy.Spent != *spent {
		return nil, nil
	}
	return &copy, nil
}

func (f *fakeOutputLoader) LoadAncillaryBeef(_ context.Context, output *engine.Output) error {
	key := output.Outpoint.String()
	f.mu.Lock()
	f.ancillarySeen = append(f.ancillarySeen, key)
	err := f.ancillaryErrs[key]
	additional := f.mergeAncillary[key]
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if additional != nil {
		return output.Beef.MergeBeef(additional)
	}
	return nil
}

func TestLookupReturnsDirectOrderedAtomicBEEFOutputLists(t *testing.T) {
	store, closeStore := openLookupClaimStore(t)
	defer closeStore()
	loader := &fakeOutputLoader{
		outputs:        map[string]*engine.Output{},
		mergeAncillary: map[string]*transaction.Beef{},
		findErrors:     map[string]error{},
		ancillaryErrs:  map[string]error{},
	}
	service := NewLookupService(store, loader)

	tx1 := newLookupClaimTransaction(t, "alice", "example.com", 1, 1)
	tx2 := newLookupClaimTransaction(t, "alice", "other.example", 0, 2)
	tx3 := newLookupClaimTransaction(t, "alice", "example.com", 0, 3)
	tx4 := newLookupClaimTransaction(t, "bob", "example.com", 0, 4)
	claims := []StoredClaim{
		lookupStoredClaim(tx1, 1, "alice", "example.com", true, 20, 4),
		lookupStoredClaim(tx2, 0, "alice", "other.example", true, 10, 9),
		lookupStoredClaim(tx3, 0, "alice", "example.com", true, 10, 2),
		lookupStoredClaim(tx4, 0, "bob", "example.com", false, 0, 0),
	}
	for i := range claims {
		if err := store.UpsertClaim(t.Context(), &claims[i]); err != nil {
			t.Fatal(err)
		}
		tx := []*transaction.Transaction{tx1, tx2, tx3, tx4}[i]
		loader.outputs[claims[i].Outpoint.String()] = lookupEngineOutput(t, tx, claims[i].Outpoint.Index)
	}

	// The first candidate starts with unrelated BEEF and is completed only by
	// the loader's ancillary step.
	unrelated := newLookupClaimTransaction(t, "carol", "unrelated.example", 0, 99)
	loader.outputs[claims[0].Outpoint.String()].Beef = lookupBeef(t, unrelated)
	loader.outputs[claims[0].Outpoint.String()].AncillaryTxids = []*chainhash.Hash{tx1.TxID()}
	loader.mergeAncillary[claims[0].Outpoint.String()] = lookupBeef(t, tx1)

	aliasAnswer := lookupQuestion(t, service, `{"alias":"ALICE","limit":2}`)
	assertLookupOutpoints(t, aliasAnswer, []transaction.Outpoint{claims[2].Outpoint, claims[1].Outpoint})

	cursor, err := NewCursor(aliasStoreQuery("alice"), claims[1].Outpoint.Txid.String(), claims[1].Outpoint.Index)
	if err != nil {
		t.Fatal(err)
	}
	cursorQuery, err := json.Marshal(map[string]any{"alias": "ALICE", "cursor": cursor})
	if err != nil {
		t.Fatal(err)
	}
	aliasAfter := lookupQuestion(t, service, string(cursorQuery))
	assertLookupOutpoints(t, aliasAfter, []transaction.Outpoint{claims[0].Outpoint})

	domainAnswer := lookupQuestion(t, service, `{"domain":"EXAMPLE.COM"}`)
	assertLookupOutpoints(t, domainAnswer, []transaction.Outpoint{claims[2].Outpoint, claims[0].Outpoint, claims[3].Outpoint})

	wantAll := make([]transaction.Outpoint, len(claims))
	for i := range claims {
		wantAll[i] = claims[i].Outpoint
	}
	sort.Slice(wantAll, func(i, j int) bool { return wantAll[i].String() < wantAll[j].String() })
	allAnswer := lookupQuestion(t, service, `{"findAll":true}`)
	assertLookupOutpoints(t, allAnswer, wantAll)

	if aliasAnswer.Type != lookup.AnswerTypeOutputList || aliasAnswer.Result != nil || len(aliasAnswer.Formulas) != 0 {
		t.Fatalf("lookup did not return a direct output-list: %+v", aliasAnswer)
	}
	if len(loader.ancillarySeen) == 0 || !containsString(loader.ancillarySeen, claims[0].Outpoint.String()) {
		t.Fatalf("ancillary BEEF was not loaded for %s: %v", claims[0].Outpoint.String(), loader.ancillarySeen)
	}
	for _, call := range loader.findCalls {
		if call.topic != TopicName || call.spent == nil || *call.spent || !call.includeBEEF {
			t.Fatalf("hydration was not an exact unspent topic+BEEF load: %+v", call)
		}
	}
}

func TestLookupFailsClosedWhenStoredCandidateCannotHydrate(t *testing.T) {
	store, closeStore := openLookupClaimStore(t)
	defer closeStore()
	tx := newLookupClaimTransaction(t, "alice", "example.com", 0, 1)
	claim := lookupStoredClaim(tx, 0, "alice", "example.com", false, 0, 0)
	if err := store.UpsertClaim(t.Context(), &claim); err != nil {
		t.Fatal(err)
	}
	loader := &fakeOutputLoader{
		outputs: map[string]*engine.Output{
			claim.Outpoint.String(): {
				Outpoint: claim.Outpoint,
				Topic:    TopicName,
			},
		},
		mergeAncillary: map[string]*transaction.Beef{},
		findErrors:     map[string]error{},
		ancillaryErrs:  map[string]error{},
	}

	answer, err := NewLookupService(store, loader).Lookup(t.Context(), &lookup.LookupQuestion{
		Service: LookupName,
		Query:   json.RawMessage(`{"alias":"alice"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "has no BEEF") {
		t.Fatalf("error = %v, want missing BEEF", err)
	}
	if answer != nil {
		t.Fatalf("partial answer returned on hydration failure: %+v", answer)
	}
}

func TestLookupClonesSharedBEEFBeforeAncillaryHydration(t *testing.T) {
	store, closeStore := openLookupClaimStore(t)
	defer closeStore()
	parent, subject, shared, ancillary := lookupAncestry(t, 200)
	claim := lookupStoredClaim(subject, 0, "alice", "example.com", false, 0, 0)
	if err := store.UpsertClaim(t.Context(), &claim); err != nil {
		t.Fatal(err)
	}
	loader := &fakeOutputLoader{
		outputs: map[string]*engine.Output{
			claim.Outpoint.String(): {
				Outpoint: claim.Outpoint,
				Topic:    TopicName,
				Beef:     shared,
			},
		},
		mergeAncillary: map[string]*transaction.Beef{claim.Outpoint.String(): ancillary},
		findErrors:     map[string]error{},
		ancillaryErrs:  map[string]error{},
	}
	service := NewLookupService(store, loader)

	answer := lookupQuestion(t, service, `{"alias":"alice"}`)
	assertLookupOutpoints(t, answer, []transaction.Outpoint{claim.Outpoint})
	assertSharedBEEFUnchanged(t, shared, parent.TxID(), 1)

	const readers = 24
	start := make(chan struct{})
	errs := make(chan error, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			answer, err := service.Lookup(context.Background(), &lookup.LookupQuestion{
				Service: LookupName,
				Query:   json.RawMessage(`{"alias":"alice"}`),
			})
			if err == nil && (answer == nil || len(answer.Outputs) != 1) {
				err = errors.New("concurrent lookup returned the wrong output count")
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertSharedBEEFUnchanged(t, shared, parent.TxID(), 1)
}

func TestLookupFailsClosedOnInvalidAncestry(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(shared, ancillary *transaction.Beef, parent, subject *transaction.Transaction) error
	}{
		{name: "missing-ancestor"},
		{
			name: "txid-only-ancestor",
			mutate: func(shared, _ *transaction.Beef, parent, _ *transaction.Transaction) error {
				shared.MergeTxidOnly(parent.TxID())
				return nil
			},
		},
		{
			name: "invalid-bump-index",
			mutate: func(shared, ancillary *transaction.Beef, _, subject *transaction.Transaction) error {
				if err := shared.MergeBeef(ancillary); err != nil {
					return err
				}
				entry := shared.Transactions[*subject.TxID()]
				entry.DataFormat = transaction.RawTxAndBumpIndex
				entry.BumpIndex = 99
				return nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeStore := openLookupClaimStore(t)
			defer closeStore()
			parent, subject, shared, ancillary := lookupAncestry(t, 300)
			if test.mutate != nil {
				if err := test.mutate(shared, ancillary, parent, subject); err != nil {
					t.Fatal(err)
				}
			}
			claim := lookupStoredClaim(subject, 0, "alice", "example.com", false, 0, 0)
			if err := store.UpsertClaim(t.Context(), &claim); err != nil {
				t.Fatal(err)
			}
			loader := &fakeOutputLoader{
				outputs: map[string]*engine.Output{
					claim.Outpoint.String(): {
						Outpoint: claim.Outpoint,
						Topic:    TopicName,
						Beef:     shared,
					},
				},
				mergeAncillary: map[string]*transaction.Beef{},
				findErrors:     map[string]error{},
				ancillaryErrs:  map[string]error{},
			}

			answer, err := NewLookupService(store, loader).Lookup(t.Context(), &lookup.LookupQuestion{
				Service: LookupName,
				Query:   json.RawMessage(`{"alias":"alice"}`),
			})
			if err == nil || !strings.Contains(err.Error(), "incomplete or invalid ancestry") {
				t.Fatalf("error = %v, want invalid ancestry", err)
			}
			if answer != nil {
				t.Fatalf("partial answer returned for invalid ancestry: %+v", answer)
			}
		})
	}
}

func TestLookupAdmissionAndLifecycle(t *testing.T) {
	store, closeStore := openLookupClaimStore(t)
	defer closeStore()
	loader := &fakeOutputLoader{
		outputs:        map[string]*engine.Output{},
		mergeAncillary: map[string]*transaction.Beef{},
		findErrors:     map[string]error{},
		ancillaryErrs:  map[string]error{},
	}
	service := NewLookupService(store, loader)

	confirmedTx := newLookupClaimTransaction(t, "alice", "example.com", 0, 1)
	setLookupMerklePlacement(confirmedTx, 820_000, 42, true)
	confirmedAtomic := lookupAtomicBEEF(t, confirmedTx)
	confirmedOutpoint := transaction.Outpoint{Txid: *confirmedTx.TxID(), Index: 0}

	if err := service.OutputAdmittedByTopic(t.Context(), &engine.OutputAdmittedByTopic{
		Topic:       "tm_other",
		OutputIndex: ^uint32(0),
		AtomicBEEF:  []byte("not BEEF"),
	}); err != nil {
		t.Fatalf("wrong-topic admission was not ignored: %v", err)
	}
	for range 2 {
		if err := service.OutputAdmittedByTopic(t.Context(), &engine.OutputAdmittedByTopic{
			Topic: TopicName, OutputIndex: 0, AtomicBEEF: confirmedAtomic,
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertPlacement(t, store, &confirmedOutpoint, true, 820_000, 42)

	unconfirmedTx := newLookupClaimTransaction(t, "bob", "unconfirmed.example", 0, 2)
	unconfirmedOutpoint := transaction.Outpoint{Txid: *unconfirmedTx.TxID(), Index: 0}
	if err := service.OutputAdmittedByTopic(t.Context(), &engine.OutputAdmittedByTopic{
		Topic: TopicName, OutputIndex: 0, AtomicBEEF: lookupAtomicBEEF(t, unconfirmedTx),
	}); err != nil {
		t.Fatal(err)
	}
	assertPlacement(t, store, &unconfirmedOutpoint, false, 0, 0)

	unreliableTx := newLookupClaimTransaction(t, "carol", "unreliable.example", 0, 3)
	setLookupMerklePlacement(unreliableTx, 820_001, 77, false)
	unreliableOutpoint := transaction.Outpoint{Txid: *unreliableTx.TxID(), Index: 0}
	if err := service.OutputAdmittedByTopic(t.Context(), &engine.OutputAdmittedByTopic{
		Topic: TopicName, OutputIndex: 0, AtomicBEEF: lookupAtomicBEEF(t, unreliableTx),
	}); err != nil {
		t.Fatal(err)
	}
	assertPlacement(t, store, &unreliableOutpoint, false, 0, 0)

	for range 2 {
		if err := service.OutputBlockHeightUpdated(t.Context(), unconfirmedTx.TxID(), 830_000, 91); err != nil {
			t.Fatal(err)
		}
	}
	assertPlacement(t, store, &unconfirmedOutpoint, true, 830_000, 91)
	if err := service.OutputBlockHeightUpdated(t.Context(), unconfirmedTx.TxID(), 0, 123); err != nil {
		t.Fatal(err)
	}
	assertPlacement(t, store, &unconfirmedOutpoint, false, 0, 123)

	if err := service.OutputSpent(t.Context(), &engine.OutputSpent{Topic: "tm_other"}); err != nil {
		t.Fatalf("wrong-topic spend was not ignored: %v", err)
	}
	spendingTxID := newLookupClaimTransaction(t, "dave", "spender.example", 0, 4).TxID()
	for range 2 {
		if err := service.OutputSpent(t.Context(), &engine.OutputSpent{
			Topic: TopicName, Outpoint: &confirmedOutpoint, SpendingTxid: spendingTxID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := store.QueryClaims(t.Context(), aliasStoreQuery("alice"), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("spent claim remained queryable: %+v", claims)
	}

	if err := service.OutputNoLongerRetainedInHistory(t.Context(), &confirmedOutpoint, "tm_other"); err != nil {
		t.Fatal(err)
	}
	assertPlacement(t, store, &confirmedOutpoint, true, 820_000, 42)
	for range 2 {
		if err := service.OutputNoLongerRetainedInHistory(t.Context(), &confirmedOutpoint, TopicName); err != nil {
			t.Fatal(err)
		}
	}
	assertMissingPlacement(t, store, &confirmedOutpoint)

	loader.outputs[unconfirmedOutpoint.String()] = lookupEngineOutput(t, unconfirmedTx, 0)
	if err := service.OutputEvicted(t.Context(), &unconfirmedOutpoint); err != nil {
		t.Fatal(err)
	}
	assertPlacement(t, store, &unconfirmedOutpoint, false, 0, 123)
	delete(loader.outputs, unconfirmedOutpoint.String())
	for range 2 {
		if err := service.OutputEvicted(t.Context(), &unconfirmedOutpoint); err != nil {
			t.Fatal(err)
		}
	}
	assertMissingPlacement(t, store, &unconfirmedOutpoint)

	for range 2 {
		if err := service.OutputNoLongerRetainedInHistory(t.Context(), &unreliableOutpoint, TopicName); err != nil {
			t.Fatal(err)
		}
	}
	assertMissingPlacement(t, store, &unreliableOutpoint)
}

func TestLookupAdmissionRejectsAtomicBEEFBoundsAndInvalidClaims(t *testing.T) {
	store, closeStore := openLookupClaimStore(t)
	defer closeStore()
	service := NewLookupService(store, &fakeOutputLoader{})
	valid := newLookupClaimTransaction(t, "alice", "example.com", 0, 1)

	tests := []struct {
		name    string
		payload *engine.OutputAdmittedByTopic
		want    string
	}{
		{
			name: "atomic-beef-required",
			payload: &engine.OutputAdmittedByTopic{
				Topic: TopicName, AtomicBEEF: []byte{1, 2, 3, 4},
			},
			want: "Atomic BEEF",
		},
		{
			name: "output-index-bounds",
			payload: &engine.OutputAdmittedByTopic{
				Topic: TopicName, OutputIndex: 1, AtomicBEEF: lookupAtomicBEEF(t, valid),
			},
			want: "out of range",
		},
	}
	invalid := transaction.NewTransaction()
	invalid.LockTime = 9
	invalid.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: &script.Script{script.OpTRUE}})
	tests = append(tests, struct {
		name    string
		payload *engine.OutputAdmittedByTopic
		want    string
	}{
		name: "shared-decode",
		payload: &engine.OutputAdmittedByTopic{
			Topic: TopicName, AtomicBEEF: lookupAtomicBEEF(t, invalid),
		},
		want: "failed to decode",
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := service.OutputAdmittedByTopic(t.Context(), tt.payload); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
	claims, err := store.QueryClaims(t.Context(), findAllStoreQuery(), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("invalid admissions were stored: %+v", claims)
	}
}

func TestLookupTransactionRolledBack(t *testing.T) {
	store, closeStore := openLookupClaimStore(t)
	defer closeStore()
	service := NewLookupService(store, &fakeOutputLoader{})
	originalTx := newLookupClaimTransaction(t, "alice", "example.com", 0, 1)
	rollbackTx := newLookupClaimTransaction(t, "bob", "example.net", 0, 2)
	original := lookupStoredClaim(originalTx, 0, "alice", "example.com", false, 0, 0)
	created := lookupStoredClaim(rollbackTx, 0, "bob", "example.net", false, 0, 0)
	if err := store.UpsertClaim(t.Context(), &original); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSpent(t.Context(), &original.Outpoint, rollbackTx.TxID()); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertClaim(t.Context(), &created); err != nil {
		t.Fatal(err)
	}

	if err := service.TransactionRolledBack(t.Context(), nil, "tm_other"); err != nil {
		t.Fatalf("wrong-topic rollback was not ignored: %v", err)
	}
	if err := service.TransactionRolledBack(t.Context(), nil, TopicName); err == nil {
		t.Fatal("nil exact-topic rollback transaction ID was accepted")
	}
	for range 2 {
		if err := service.TransactionRolledBack(t.Context(), rollbackTx.TxID(), TopicName); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := store.QueryClaims(t.Context(), findAllStoreQuery(), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := claimOutpointStrings(claims), []string{original.Outpoint.String()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rollback claims got %v want %v", got, want)
	}
}

func openLookupClaimStore(t *testing.T) (*SQLStore, func()) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return NewSQLStore(db, 0), func() { _ = db.Close() }
}

func newLookupClaimTransaction(t *testing.T, alias, domain string, vout, nonce uint32) *transaction.Transaction {
	t.Helper()
	certifier := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000001")
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002")
	var certifierKey [CertifierKeyLen]byte
	copy(certifierKey[:], certifier.PubKey().Compressed())
	digest := Digest(alias, domain, certifierKey)
	signature, err := certifier.Sign(digest[:])
	if err != nil {
		t.Fatal(err)
	}
	fields := [][]byte{
		[]byte(ProtocolName),
		[]byte(ProtocolVersion),
		[]byte(alias),
		[]byte(domain),
		certifierKey[:],
		signature.Serialize(),
	}

	tx := transaction.NewTransaction()
	tx.LockTime = nonce
	for uint32(len(tx.Outputs)) < vout {
		tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: &script.Script{script.OpTRUE}})
	}
	tx.AddOutput(&transaction.TransactionOutput{
		Satoshis:      1,
		LockingScript: decodeBuildLockAfter(t, fields, owner.PubKey().Compressed(), FieldCount/2),
	})
	return tx
}

func setLookupMerklePlacement(tx *transaction.Transaction, height uint32, blockIndex uint64, relevant bool) {
	txid := tx.TxID()
	duplicate := true
	leaf := &transaction.PathElement{Offset: blockIndex, Hash: txid}
	if relevant {
		leaf.Txid = &relevant
	}
	tx.MerklePath = transaction.NewMerklePath(height, [][]*transaction.PathElement{{
		leaf,
		{Offset: blockIndex ^ 1, Duplicate: &duplicate},
	}})
}

func lookupAtomicBEEF(t *testing.T, tx *transaction.Transaction) []byte {
	t.Helper()
	atomic, err := tx.AtomicBEEF(false)
	if err != nil {
		t.Fatal(err)
	}
	return atomic
}

func lookupBeef(t *testing.T, tx *transaction.Transaction) *transaction.Beef {
	t.Helper()
	beef, _, err := transaction.NewBeefFromAtomicBytes(lookupAtomicBEEF(t, tx))
	if err != nil {
		t.Fatal(err)
	}
	return beef
}

func lookupStoredClaim(tx *transaction.Transaction, vout uint32, alias, domain string, confirmed bool, height uint32, blockIndex uint64) StoredClaim {
	return StoredClaim{
		Outpoint:     transaction.Outpoint{Txid: *tx.TxID(), Index: vout},
		Alias:        alias,
		Domain:       domain,
		Confirmed:    confirmed,
		BlockHeight:  height,
		BlockIndex:   blockIndex,
		SpendingTxID: nil,
	}
}

func lookupEngineOutput(t *testing.T, tx *transaction.Transaction, vout uint32) *engine.Output {
	t.Helper()
	return &engine.Output{
		Outpoint: transaction.Outpoint{Txid: *tx.TxID(), Index: vout},
		Topic:    TopicName,
		Beef:     lookupBeef(t, tx),
	}
}

func lookupAncestry(t *testing.T, nonce uint32) (parent, subject *transaction.Transaction, shared, ancillary *transaction.Beef) {
	t.Helper()
	parent = transaction.NewTransaction()
	parent.LockTime = nonce
	parent.AddOutput(&transaction.TransactionOutput{Satoshis: 2, LockingScript: &script.Script{script.OpTRUE}})
	subject = newLookupClaimTransaction(t, "alice", "example.com", 0, nonce+1)
	subject.AddInput(&transaction.TransactionInput{
		SourceTXID:       parent.TxID(),
		SourceTxOutIndex: 0,
		UnlockingScript:  &script.Script{},
		SequenceNumber:   transaction.DefaultSequenceNumber,
	})

	shared = transaction.NewBeefV2()
	if _, err := shared.MergeRawTx(subject.Bytes(), nil); err != nil {
		t.Fatal(err)
	}
	ancillary = transaction.NewBeefV2()
	if _, err := ancillary.MergeRawTx(parent.Bytes(), nil); err != nil {
		t.Fatal(err)
	}
	return parent, subject, shared, ancillary
}

func assertSharedBEEFUnchanged(t *testing.T, shared *transaction.Beef, absentTxid *chainhash.Hash, wantTransactions int) {
	t.Helper()
	if len(shared.Transactions) != wantTransactions {
		t.Fatalf("shared BEEF transaction count changed to %d, want %d", len(shared.Transactions), wantTransactions)
	}
	if _, ok := shared.Transactions[*absentTxid]; ok {
		t.Fatalf("shared BEEF was mutated with ancillary transaction %s", absentTxid.String())
	}
}

func lookupQuestion(t *testing.T, service *LookupService, raw string) *lookup.LookupAnswer {
	t.Helper()
	answer, err := service.Lookup(t.Context(), &lookup.LookupQuestion{Service: LookupName, Query: json.RawMessage(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return answer
}

func assertLookupOutpoints(t *testing.T, answer *lookup.LookupAnswer, want []transaction.Outpoint) {
	t.Helper()
	got := make([]transaction.Outpoint, len(answer.Outputs))
	for i, item := range answer.Outputs {
		beef, txid, err := transaction.NewBeefFromAtomicBytes(item.Beef)
		if err != nil {
			t.Fatalf("output %d is not Atomic BEEF: %v", i, err)
		}
		tx := beef.FindTransactionByHash(txid)
		if tx == nil || uint64(item.OutputIndex) >= uint64(len(tx.Outputs)) {
			t.Fatalf("output %d cannot reconstruct subject outpoint", i)
		}
		got[i] = transaction.Outpoint{Txid: *txid, Index: item.OutputIndex}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("outpoints\n got %v\nwant %v", got, want)
	}
}

func assertPlacement(t *testing.T, store ClaimStore, outpoint *transaction.Outpoint, confirmed bool, height uint32, blockIndex uint64) {
	t.Helper()
	placement, err := store.PlacementForOutpoint(t.Context(), outpoint)
	if err != nil {
		t.Fatal(err)
	}
	if placement == nil || placement.Confirmed != confirmed || placement.BlockHeight != height || placement.BlockIndex != blockIndex {
		t.Fatalf("placement = %+v, want confirmed=%v height=%d index=%d", placement, confirmed, height, blockIndex)
	}
}

func assertMissingPlacement(t *testing.T, store ClaimStore, outpoint *transaction.Outpoint) {
	t.Helper()
	placement, err := store.PlacementForOutpoint(t.Context(), outpoint)
	if err != nil {
		t.Fatal(err)
	}
	if placement != nil {
		t.Fatalf("placement remains: %+v", placement)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

var _ OutputLoader = (*fakeOutputLoader)(nil)
