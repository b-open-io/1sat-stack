package lookup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
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
    origin        BLOB,
    name          TEXT,
    content_type  TEXT,
    price         INTEGER NOT NULL,
    seller        TEXT NOT NULL,
    spend_txid    BLOB,
    spend_type    TEXT,
    spend_score   REAL,
    score         REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_listings_search ON listings(spend_type, content_type, name COLLATE NOCASE, score);
CREATE INDEX IF NOT EXISTS idx_listings_name ON listings(spend_type, name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_listings_origin ON listings(origin, spend_type);
CREATE INDEX IF NOT EXISTS idx_listings_sales ON listings(spend_type, spend_score);
`

type OrdLockLookup struct {
	topicDB overlaystorage.Factory
	ordfs   *ordfs.Ordfs
	ready   sync.Map
}

func NewOrdLockLookup(topicDB overlaystorage.Factory) *OrdLockLookup {
	return &OrdLockLookup{topicDB: topicDB}
}

func (l *OrdLockLookup) SetOrdfs(o *ordfs.Ordfs) {
	l.ordfs = o
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

	var contentType, name string
	var origin *transaction.Outpoint

	insc := inscription.Decode(output.LockingScript)
	if insc != nil {
		contentType = insc.File.Type

		if contentType == "application/op-ns" {
			name = string(insc.File.Content)
		}

		if insc.Parent != nil {
			origin = insc.Parent
		}

		if name == "" {
			if nameField, ok := insc.Fields["name"]; ok {
				name = string(nameField)
			}
		}
	}

	// For transferred ordinals without inscription data, resolve via ORDFS
	if insc == nil && l.ordfs != nil {
		seq := 0
		resp, err := l.ordfs.Load(ctx, &ordfs.Request{
			Outpoint: outpoint,
			Seq:      &seq,
			Content:  true,
			Map:      true,
		})
		if err == nil && resp != nil {
			if resp.Origin != nil {
				origin = resp.Origin
			}
			if resp.ContentType != "" {
				contentType = strings.Split(resp.ContentType, ";")[0]
				contentType = strings.TrimSpace(contentType)
			}
			if contentType == "application/op-ns" && len(resp.Content) > 0 {
				name = string(resp.Content)
			}
			if name == "" && resp.Map != nil {
				var mapData map[string]string
				if err := json.Unmarshal(resp.Map, &mapData); err == nil {
					if n, ok := mapData["name"]; ok && n != "" {
						name = n
					}
				}
			}
		}
	}

	if origin == nil {
		origin = outpoint
	}

	ts, err := l.db(payload.Topic)
	if err != nil {
		return err
	}

	_, err = ts.DB().ExecContext(ctx,
		`INSERT OR REPLACE INTO listings (outpoint, origin, name, content_type, price, seller, score) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		outpoint.Bytes(), origin.Bytes(), name, contentType, lock.Price, lock.Seller.AddressString, types.ScoreFromTx(tx, txid),
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

func (l *OrdLockLookup) SearchListings(ctx context.Context, topic, status, contentType, query string, limit int, from float64, rev bool) ([]*txo.IndexedOutput, error) {
	ts, err := l.db(topic)
	if err != nil {
		return nil, err
	}

	q := `SELECT outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score, spend_score FROM listings WHERE `
	var args []any

	scoreCol := "score"
	switch status {
	case "sale", "cancel":
		q += "spend_type = ?"
		args = append(args, status)
		scoreCol = "spend_score"
	default:
		q += "spend_type IS NULL"
	}

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
			q += fmt.Sprintf(" AND %s < ?", scoreCol)
		} else {
			q += fmt.Sprintf(" AND %s > ?", scoreCol)
		}
		args = append(args, from)
	}

	if rev {
		q += fmt.Sprintf(" ORDER BY %s DESC", scoreCol)
	} else {
		q += fmt.Sprintf(" ORDER BY %s ASC", scoreCol)
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
		`SELECT outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score, spend_score FROM listings WHERE outpoint = ?`,
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

func (l *OrdLockLookup) GetListingByOrigin(ctx context.Context, topic string, origin *transaction.Outpoint) (*txo.IndexedOutput, error) {
	ts, err := l.db(topic)
	if err != nil {
		return nil, err
	}

	rows, err := ts.DB().QueryContext(ctx,
		`SELECT outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score, spend_score FROM listings WHERE origin = ? AND spend_type IS NULL`,
		origin.Bytes(),
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

func (l *OrdLockLookup) GetListingsByOrigins(ctx context.Context, topic string, origins []*transaction.Outpoint) (map[string]*txo.IndexedOutput, error) {
	if len(origins) == 0 {
		return nil, nil
	}

	ts, err := l.db(topic)
	if err != nil {
		return nil, err
	}

	placeholders := strings.Repeat("?,", len(origins))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, len(origins))
	for i, o := range origins {
		args[i] = o.Bytes()
	}

	rows, err := ts.DB().QueryContext(ctx,
		fmt.Sprintf(`SELECT outpoint, origin, name, content_type, price, seller, spend_txid, spend_type, score, spend_score FROM listings WHERE origin IN (%s) AND spend_type IS NULL`, placeholders),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make(map[string]*txo.IndexedOutput)
	for rows.Next() {
		out, err := scanListing(rows)
		if err != nil {
			return nil, err
		}
		if listing, ok := out.Data["ordlock"].(map[string]any); ok {
			if origin, ok := listing["origin"].(string); ok {
				results[origin] = out
			}
		}
	}
	return results, rows.Err()
}

func scanListing(rows *sql.Rows) (*txo.IndexedOutput, error) {
	var opBytes, originBytes, spendBytes []byte
	var name, contentType, seller, spendType sql.NullString
	var price uint64
	var score, spendScore sql.NullFloat64

	if err := rows.Scan(&opBytes, &originBytes, &name, &contentType, &price, &seller, &spendBytes, &spendType, &score, &spendScore); err != nil {
		return nil, err
	}

	outpoint := transaction.NewOutpointFromBytes(opBytes)
	if outpoint == nil {
		return nil, fmt.Errorf("invalid outpoint bytes: %x", opBytes)
	}

	out := &txo.IndexedOutput{
		Outpoint: *outpoint,
		Score:    score.Float64,
	}

	if len(spendBytes) == 32 {
		out.SpendTxid = &chainhash.Hash{}
		copy(out.SpendTxid[:], spendBytes)
	}

	listing := map[string]any{
		"price":  price,
		"seller": seller.String,
	}
	if spendScore.Valid {
		listing["spend_score"] = spendScore.Float64
	}
	if origin := transaction.NewOutpointFromBytes(originBytes); origin != nil {
		listing["origin"] = origin.String()
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
