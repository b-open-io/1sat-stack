package lookup

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/inscription"
	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const ordlockSchema = `
CREATE TABLE IF NOT EXISTS listings (
    outpoint      BLOB PRIMARY KEY,
    origin        TEXT,
    name          TEXT,
    content_type  TEXT,
    price         INTEGER NOT NULL,
    seller        TEXT NOT NULL,
    spend_txid    BLOB,
    spend_type    TEXT,
    spend_score   REAL,
    score         REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_listings_active ON listings(content_type, score) WHERE spend_txid IS NULL;
CREATE INDEX IF NOT EXISTS idx_listings_name ON listings(name COLLATE NOCASE) WHERE spend_txid IS NULL;
CREATE INDEX IF NOT EXISTS idx_listings_sales ON listings(spend_type, spend_score) WHERE spend_txid IS NOT NULL;
`

type OrdLockLookup struct {
	topicDB overlaystorage.Factory
	ready   sync.Map
}

func NewOrdLockLookup(topicDB overlaystorage.Factory) *OrdLockLookup {
	return &OrdLockLookup{topicDB: topicDB}
}

func (l *OrdLockLookup) db(topic string) (overlaystorage.TopicStorage, error) {
	ts, err := l.topicDB(topic)
	if err != nil {
		return nil, err
	}
	if _, ok := l.ready.Load(topic); !ok {
		if _, err := ts.DB().Exec(ordlockSchema); err != nil {
			return nil, fmt.Errorf("failed to create listings schema for %s: %w", topic, err)
		}
		l.ready.Store(topic, true)
	}
	return ts, nil
}

func (l *OrdLockLookup) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}

	if int(payload.OutputIndex) >= len(tx.Outputs) {
		return nil
	}

	output := tx.Outputs[int(payload.OutputIndex)]

	lock := ordlock.Decode(output.LockingScript)
	if lock == nil {
		return nil
	}

	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: payload.OutputIndex,
	}

	var contentType, name, origin string

	insc := inscription.Decode(output.LockingScript)
	if insc != nil {
		contentType = insc.File.Type

		if contentType == "application/op-ns" {
			name = string(insc.File.Content)
		}

		if insc.Parent != nil {
			origin = insc.Parent.OrdinalString()
		}
	}

	if origin == "" {
		origin = outpoint.OrdinalString()
	}

	if name == "" && insc != nil {
		if nameField, ok := insc.Fields["name"]; ok {
			name = string(nameField)
		}
	}

	ts, err := l.db(payload.Topic)
	if err != nil {
		return err
	}

	_, err = ts.DB().ExecContext(ctx,
		`INSERT OR REPLACE INTO listings (outpoint, origin, name, content_type, price, seller, score) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		outpoint.Bytes(), origin, name, contentType, lock.Price, lock.Seller.AddressString, types.ScoreFromTx(tx, txid),
	)
	return err
}

func (l *OrdLockLookup) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	_, tx, txid, err := transaction.ParseBeef(payload.SpendingAtomicBEEF)
	if err != nil {
		return err
	}

	spendType := classifySpend(payload.UnlockingScript)

	ts, err := l.db(payload.Topic)
	if err != nil {
		return err
	}

	_, err = ts.DB().ExecContext(ctx,
		`UPDATE listings SET spend_txid = ?, spend_type = ?, spend_score = ? WHERE outpoint = ?`,
		txid[:], spendType, types.ScoreFromTx(tx, txid), payload.Outpoint.Bytes(),
	)
	return err
}

// classifySpend determines whether a spend is a sale or cancel by examining
// the unlocking script. The OrdLock contract uses the last byte as a branch
// selector: OP_0 (0x00) for purchase, OP_1 (0x51) for cancel.
func classifySpend(unlockingScript *script.Script) string {
	if unlockingScript == nil {
		return "unknown"
	}
	raw := []byte(*unlockingScript)
	if len(raw) == 0 {
		return "unknown"
	}
	switch raw[len(raw)-1] {
	case 0x00:
		return "sale"
	case 0x51:
		return "cancel"
	default:
		return "unknown"
	}
}

func (l *OrdLockLookup) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

func (l *OrdLockLookup) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	return nil
}

func (l *OrdLockLookup) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

func (l *OrdLockLookup) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return nil, nil
}

func (l *OrdLockLookup) GetDocumentation() string {
	return "OrdLock Lookup Service"
}

func (l *OrdLockLookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "OrdLock",
	}
}

func (l *OrdLockLookup) SearchListings(ctx context.Context, topic, contentType, query string, limit int, from float64, rev bool) ([]*txo.IndexedOutput, error) {
	ts, err := l.db(topic)
	if err != nil {
		return nil, err
	}

	q := `SELECT outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score FROM listings WHERE spend_txid IS NULL`
	var args []any

	if contentType != "" {
		q += " AND content_type = ?"
		args = append(args, contentType)
	}

	if query != "" {
		q += " AND name LIKE ?"
		args = append(args, query+"%")
	}

	if from > 0 {
		if rev {
			q += " AND score < ?"
		} else {
			q += " AND score > ?"
		}
		args = append(args, from)
	}

	if rev {
		q += " ORDER BY score DESC"
	} else {
		q += " ORDER BY score ASC"
	}

	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := ts.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*txo.IndexedOutput
	for rows.Next() {
		out, err := scanListing(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, out)
	}
	return results, rows.Err()
}

func (l *OrdLockLookup) GetListing(ctx context.Context, topic string, outpoint []byte) (*txo.IndexedOutput, error) {
	ts, err := l.db(topic)
	if err != nil {
		return nil, err
	}

	rows, err := ts.DB().QueryContext(ctx,
		`SELECT outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score FROM listings WHERE outpoint = ?`,
		outpoint,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanListing(rows)
}

func scanListing(rows *sql.Rows) (*txo.IndexedOutput, error) {
	var opBytes, spendBytes []byte
	var origin, name, contentType, seller, spendType sql.NullString
	var price uint64
	var score float64

	if err := rows.Scan(&opBytes, &origin, &name, &contentType, &price, &seller, &spendBytes, &spendType, &score); err != nil {
		return nil, err
	}

	outpoint := transaction.NewOutpointFromBytes(opBytes)
	if outpoint == nil {
		return nil, fmt.Errorf("invalid outpoint bytes: %x", opBytes)
	}

	out := &txo.IndexedOutput{
		Outpoint: *outpoint,
		Score:    score,
	}

	if len(spendBytes) == 32 {
		out.SpendTxid = &chainhash.Hash{}
		copy(out.SpendTxid[:], spendBytes)
	}

	listing := map[string]any{
		"price":  price,
		"seller": seller.String,
	}
	if origin.Valid {
		listing["origin"] = origin.String
	}
	if name.Valid {
		listing["name"] = name.String
	}
	if contentType.Valid {
		listing["content_type"] = contentType.String
	}
	if spendType.Valid {
		listing["spend_type"] = spendType.String
	}

	out.Data = map[string]any{
		"ordlock": listing,
	}

	return out, nil
}
