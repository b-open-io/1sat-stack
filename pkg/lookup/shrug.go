package lookup

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"sync"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/template/cosign"
	shrugtemplate "github.com/b-open-io/1sat-stack/pkg/template/shrug"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

// Shrug output kinds stored in the op column. Shrug has no operation field;
// the kind is derived from field presence.
const (
	ShrugOpDeploy = "deploy"
	ShrugOpAuth   = "auth"
	ShrugOpValue  = "value"
)

const shrugSchema = `
CREATE TABLE IF NOT EXISTS token_outputs (
    outpoint     BLOB PRIMARY KEY,
    token_id     TEXT NOT NULL,
    op           TEXT NOT NULL,
    lock_type    TEXT NOT NULL,
    address      TEXT NOT NULL,
    amount       TEXT NOT NULL,
    sym          TEXT,
    dec          INTEGER,
    icon         TEXT,
    spend_txid   BLOB,
    score        REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_token_utxos ON token_outputs(token_id, lock_type, address, score) WHERE spend_txid IS NULL;
CREATE INDEX IF NOT EXISTS idx_token_history ON token_outputs(token_id, lock_type, address, score);
CREATE INDEX IF NOT EXISTS idx_token_deploy ON token_outputs(token_id) WHERE op = 'deploy';
`

// ShrugLookup implements the LookupService interface for shrug tokens.
// Uses the overlay storage factory to resolve per-topic databases; each
// topic's database gets its own token_outputs table. Amounts are unbounded,
// stored as decimal TEXT and accumulated with big.Int.
type ShrugLookup struct {
	topicDB overlaystorage.Factory
	ready   sync.Map // tracks which topics have had schema created
}

// NewShrugLookup creates a new shrug lookup service backed by per-topic overlay storage.
func NewShrugLookup(topicDB overlaystorage.Factory) *ShrugLookup {
	return &ShrugLookup{topicDB: topicDB}
}

// db resolves the TopicStorage for a topic and ensures the schema exists.
func (l *ShrugLookup) db(topic string) (overlaystorage.TopicStorage, error) {
	ts, err := l.topicDB(topic)
	if err != nil {
		return nil, err
	}
	if _, ok := l.ready.Load(topic); !ok {
		if _, err := ts.DB().Exec(shrugSchema); err != nil {
			return nil, fmt.Errorf("failed to create token_outputs schema for %s: %w", topic, err)
		}
		l.ready.Store(topic, true)
	}
	return ts, nil
}

// tokenDB resolves a per-token topic database. Per-token shrug topics are
// expected to be registered as "tm_shrug_" + tokenId.
func (l *ShrugLookup) tokenDB(tokenId string) (overlaystorage.TopicStorage, error) {
	return l.db("tm_shrug_" + tokenId)
}

// OutputAdmittedByTopic indexes a newly admitted shrug output into the
// per-topic token_outputs table. Outputs whose owner script is not a
// recognized lock type are still indexed, with empty lock_type and address —
// they simply never match owner-scoped queries.
func (l *ShrugLookup) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}

	if int(payload.OutputIndex) >= len(tx.Outputs) {
		return nil
	}

	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: payload.OutputIndex,
	}

	s := shrugtemplate.Decode(tx.Outputs[int(payload.OutputIndex)].LockingScript)
	if s == nil {
		return nil
	}

	var tokenId, op string
	switch {
	case s.Id == nil:
		tokenId = outpoint.OrdinalString()
		op = ShrugOpDeploy
	case s.Amount.Sign() == 0:
		tokenId = s.Id.OrdinalString()
		op = ShrugOpAuth
	default:
		tokenId = s.Id.OrdinalString()
		op = ShrugOpValue
	}

	// The owner script follows the inscription envelope when one is present.
	ownerScript := s.ScriptSuffix
	if s.Insc != nil {
		ownerScript = s.Insc.ScriptSuffix
	}
	var lockType, address string
	suffix := script.NewFromBytes(ownerScript)
	if p := p2pkh.Decode(suffix, true); p != nil {
		lockType = "p2pkh"
		address = p.AddressString
	} else if c := cosign.Decode(suffix); c != nil {
		lockType = "cos"
		address = c.Address
	}

	var sym, icon sql.NullString
	var dec sql.NullInt64
	if s.Metadata != nil {
		if s.Metadata.Symbol != nil {
			sym = sql.NullString{String: *s.Metadata.Symbol, Valid: true}
		}
		if s.Metadata.Icon != nil {
			icon = sql.NullString{String: s.Metadata.Icon.OrdinalString(), Valid: true}
		}
		if s.Metadata.Decimals != nil {
			dec = sql.NullInt64{Int64: int64(*s.Metadata.Decimals), Valid: true}
		}
	}

	ts, err := l.db(payload.Topic)
	if err != nil {
		return err
	}

	_, err = ts.DB().ExecContext(ctx,
		`INSERT OR REPLACE INTO token_outputs (outpoint, token_id, op, lock_type, address, amount, sym, dec, icon, score) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		outpoint.Bytes(), tokenId, op, lockType, address, s.Amount.String(), sym, dec, icon, types.ScoreFromTx(tx, txid),
	)
	return err
}

// OutputSpent marks a shrug output as spent in the per-topic table.
func (l *ShrugLookup) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	_, _, txid, err := transaction.ParseBeef(payload.SpendingAtomicBEEF)
	if err != nil {
		return err
	}

	ts, err := l.db(payload.Topic)
	if err != nil {
		return err
	}

	_, err = ts.DB().ExecContext(ctx,
		`UPDATE token_outputs SET spend_txid = ? WHERE outpoint = ?`,
		txid[:], payload.Outpoint.Bytes(),
	)
	return err
}

// OutputNoLongerRetainedInHistory is called when historical retention is no longer required.
func (l *ShrugLookup) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted permanently removes a shrug output from the index.
func (l *ShrugLookup) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	// No topic context — the engine cleans the outputs table before calling
	// lookup services.
	return nil
}

// OutputBlockHeightUpdated is called when a transaction's block height is updated.
func (l *ShrugLookup) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

// Lookup handles generic lookup queries.
func (l *ShrugLookup) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return &lookup.LookupAnswer{
		Type: lookup.AnswerTypeFormula,
	}, nil
}

// GetDocumentation returns documentation for this lookup service.
func (l *ShrugLookup) GetDocumentation() string {
	return "Shrug Lookup Service"
}

// GetMetaData returns metadata for this lookup service.
func (l *ShrugLookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "Shrug",
	}
}

// ShrugTokenInfo holds token identity and metadata from the discovery topic.
type ShrugTokenInfo struct {
	TokenID  string
	Amount   *big.Int // deploy amount: 0 = authority genesis, >0 = fixed supply
	Symbol   *string
	Decimals *uint8
	Icon     *string
}

// ListTokens returns all known tokens with their metadata from a discovery topic.
func (l *ShrugLookup) ListTokens(ctx context.Context, topic string) ([]*ShrugTokenInfo, error) {
	ts, err := l.db(topic)
	if err != nil {
		return nil, err
	}

	rows, err := ts.DB().QueryContext(ctx, `SELECT token_id, amount, sym, dec, icon FROM token_outputs WHERE op = 'deploy'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []*ShrugTokenInfo
	for rows.Next() {
		token, err := scanShrugToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// GetToken returns the deploy data for a specific token from a discovery topic.
func (l *ShrugLookup) GetToken(ctx context.Context, topic string, outpoint *transaction.Outpoint) (*ShrugTokenInfo, error) {
	ts, err := l.db(topic)
	if err != nil {
		return nil, err
	}

	rows, err := ts.DB().QueryContext(ctx,
		`SELECT token_id, amount, sym, dec, icon FROM token_outputs WHERE token_id = ? AND op = 'deploy' LIMIT 1`,
		outpoint.OrdinalString(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("token not found")
	}
	return scanShrugToken(rows)
}

// CountOutputs returns the count of unspent outputs in a topic's table.
func (l *ShrugLookup) CountOutputs(ctx context.Context, topic string) (int64, error) {
	ts, err := l.db(topic)
	if err != nil {
		return 0, err
	}

	var count int64
	err = ts.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM token_outputs WHERE spend_txid IS NULL`,
	).Scan(&count)
	return count, err
}

// GetBalance calculates the total unspent token amount for an address.
// Amounts are unbounded, so the total is a big.Int.
func (l *ShrugLookup) GetBalance(ctx context.Context, tokenId, lockType, address string) (*big.Int, int, error) {
	ts, err := l.tokenDB(tokenId)
	if err != nil {
		return nil, 0, err
	}

	rows, err := ts.DB().QueryContext(ctx,
		`SELECT amount FROM token_outputs WHERE token_id = ? AND lock_type = ? AND address = ? AND spend_txid IS NULL`,
		tokenId, lockType, address,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	total := new(big.Int)
	var count int
	for rows.Next() {
		var amtStr string
		if err := rows.Scan(&amtStr); err != nil {
			return nil, 0, err
		}
		amt, ok := new(big.Int).SetString(amtStr, 10)
		if !ok {
			return nil, 0, fmt.Errorf("invalid amount %q for %s", amtStr, tokenId)
		}
		total.Add(total, amt)
		count++
	}
	return total, count, rows.Err()
}

// SearchUTXOs searches for unspent token outputs.
func (l *ShrugLookup) SearchUTXOs(ctx context.Context, tokenId, lockType, address string, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	return l.searchShrugOutpoints(ctx, tokenId, lockType, address, true, cfg)
}

// SearchHistory searches for all outputs (including spent).
func (l *ShrugLookup) SearchHistory(ctx context.Context, tokenId, lockType, address string, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	return l.searchShrugOutpoints(ctx, tokenId, lockType, address, false, cfg)
}

// LoadOutputs loads full output data for a list of outpoints from a token's database.
func (l *ShrugLookup) LoadOutputs(ctx context.Context, tokenId string, outpoints []*transaction.Outpoint) ([]*txo.IndexedOutput, error) {
	if len(outpoints) == 0 {
		return nil, nil
	}

	ts, err := l.tokenDB(tokenId)
	if err != nil {
		return nil, err
	}

	query := `SELECT outpoint, token_id, op, lock_type, address, amount, sym, dec, icon, spend_txid, score FROM token_outputs WHERE outpoint IN (`
	args := make([]any, len(outpoints))
	for i, op := range outpoints {
		if i > 0 {
			query += ","
		}
		query += "?"
		args[i] = op.Bytes()
	}
	query += ")"

	rows, err := ts.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*txo.IndexedOutput
	for rows.Next() {
		out, err := scanShrugOutput(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, out)
	}
	return results, rows.Err()
}

// searchShrugOutpoints queries token_outputs for outpoints matching the criteria.
func (l *ShrugLookup) searchShrugOutpoints(ctx context.Context, tokenId, lockType, address string, unspentOnly bool, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	ts, err := l.tokenDB(tokenId)
	if err != nil {
		return nil, err
	}

	query := `SELECT outpoint FROM token_outputs WHERE token_id = ? AND lock_type = ? AND address = ?`
	args := []any{tokenId, lockType, address}

	if unspentOnly {
		query += " AND spend_txid IS NULL"
	}

	query, args = applySearchOpts(query, args, cfg)

	rows, err := ts.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanOutpoints(rows)
}

// scanShrugToken reads a deploy row into a ShrugTokenInfo.
func scanShrugToken(rows *sql.Rows) (*ShrugTokenInfo, error) {
	var id, amtStr string
	var sym, icon sql.NullString
	var dec sql.NullInt64

	if err := rows.Scan(&id, &amtStr, &sym, &dec, &icon); err != nil {
		return nil, err
	}

	amount, ok := new(big.Int).SetString(amtStr, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount %q for %s", amtStr, id)
	}

	token := &ShrugTokenInfo{TokenID: id, Amount: amount}
	if sym.Valid {
		token.Symbol = &sym.String
	}
	if dec.Valid {
		d := uint8(dec.Int64)
		token.Decimals = &d
	}
	if icon.Valid {
		token.Icon = &icon.String
	}
	return token, nil
}

// scanShrugOutput scans a single row into an IndexedOutput with
// Data["shrug"] populated for API compatibility.
func scanShrugOutput(rows *sql.Rows) (*txo.IndexedOutput, error) {
	var opBytes, spendBytes []byte
	var tokenId, op, lockType, address, amtStr string
	var sym, icon sql.NullString
	var dec sql.NullInt64
	var score float64

	if err := rows.Scan(&opBytes, &tokenId, &op, &lockType, &address, &amtStr, &sym, &dec, &icon, &spendBytes, &score); err != nil {
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

	shrugData := map[string]any{
		"id":  tokenId,
		"op":  op,
		"amt": amtStr,
	}
	if sym.Valid {
		shrugData["sym"] = sym.String
	}
	if dec.Valid {
		shrugData["dec"] = dec.Int64
	}
	if icon.Valid {
		shrugData["icon"] = icon.String
	}

	out.Data = map[string]any{
		"shrug": shrugData,
	}

	return out, nil
}
